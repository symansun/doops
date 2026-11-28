package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Exit codes of the doops-gateway store mysql-copy command. gatewaycmd maps
// these values straight to the process exit status and the help text states the
// same contract.
const (
	GatewayMySQLCopyExitOK      = 0
	GatewayMySQLCopyExitRuntime = 1
	GatewayMySQLCopyExitUsage   = 2
)

// gatewayMySQLCopyDSNEnv is the only source of the destination DSN. A DSN given
// on the command line would land in shell history, the process table and any
// audit record of the invocation, so the command never accepts one.
const gatewayMySQLCopyDSNEnv = "DOOPS_GATEWAY_MYSQL_DSN"

// GatewayMySQLCopyOptions is the parsed input of the cutover command.
type GatewayMySQLCopyOptions struct {
	Source          string
	ConfirmDatabase string
	// SourceDataDir and TargetDataDir move the filesystem half of the store:
	// the managed artifacts/ and skills/ trees below DOOPS_GATEWAY_DATA_DIR.
	// Rows index those trees with data-dir-relative paths, so a database-only
	// cutover leaves every artifact and Skill package missing. Both must be
	// absolute and different; give both or neither.
	SourceDataDir string
	TargetDataDir string
	DryRun        bool
}

// RunGatewayMySQLCopy moves an offline SQLite snapshot into an empty, Gateway
// prepared MySQL database.
//
// It returns the process exit code: 0 success, 1 runtime failure, 2 usage or
// precondition failure. The destination is written only after every
// precondition held, and then only inside the copier's single transaction, so a
// failure leaves no row behind.
func RunGatewayMySQLCopy(ctx context.Context, opts GatewayMySQLCopyOptions, out io.Writer) (int, error) {
	print := &gatewayMySQLCopyPrinter{w: out}
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		return GatewayMySQLCopyExitUsage, errors.New("mysql_copy_source_required: --source <snapshot.db> is required (the file gateway-sqlite-snapshot.py produced)")
	}
	confirm := strings.TrimSpace(opts.ConfirmDatabase)
	if confirm == "" {
		return GatewayMySQLCopyExitUsage, errors.New("mysql_copy_confirm_database_required: --confirm-database <name> is required and must equal the database named by " + gatewayMySQLCopyDSNEnv)
	}
	info, err := os.Stat(source)
	if err != nil {
		return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_source_unreadable: --source %q cannot be read; it must be a snapshot taken by gateway-sqlite-snapshot.py", source)
	}
	if !info.Mode().IsRegular() {
		return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_source_invalid: --source %q is not a regular file", source)
	}
	cfg, err := gatewayMySQLCopyDestinationConfig(confirm)
	if err != nil {
		return GatewayMySQLCopyExitUsage, err
	}
	dirs, err := gatewayMySQLCopyDataDirOptions(opts.SourceDataDir, opts.TargetDataDir)
	if err != nil {
		return GatewayMySQLCopyExitUsage, err
	}

	src, err := gatewayMySQLCopyOpenSource(source)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	defer src.Close()
	if err := gatewayMySQLCopyInspectSource(ctx, src); err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	dst, collation, err := gatewayMySQLCopyOpenDestination(ctx, cfg)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	defer dst.Close()

	mode := "copy"
	if opts.DryRun {
		mode = "dry-run"
	}
	print.printf("mysql-copy mode=%s source=%s destination=%s collation=%s\n", mode, source, cfg.DBName, collation)
	print.printf("source: opened read-only (mode=ro, query_only=1); PRAGMA integrity_check = ok\n")

	state, err := gatewayMySQLCopyInspectDestination(ctx, dst)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	sourceTables, err := gatewayMySQLCopySourceTableSet(ctx, src)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_read_failed: the snapshot table list could not be read")
	}
	expected, _, err := gatewayMySQLExpectedObjects()
	if err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_preview_failed: the embedded Gateway schema could not be parsed")
	}
	if state.tables == 0 {
		// A dry run must not execute DDL, but it can still predict the real run:
		// the real run installs the native schema into this empty database and then
		// copies. Report that projection instead of "there is no schema to validate
		// against", so a rehearsal gate sees the same verdict the cutover would.
		if opts.DryRun {
			code, err := gatewayMySQLCopyPreviewPlan(ctx, src, print, sourceTables, expected)
			if err != nil {
				return code, err
			}
			if dirs != nil {
				if err := gatewayMySQLCopyPreviewDataDirs(ctx, src, sourceTables, print, dirs); err != nil {
					return GatewayMySQLCopyExitUsage, err
				}
			}
			return gatewayMySQLCopyPredictEmptyDestination(print.w, cfg.DBName, len(expected))
		}
		installed, err := installGatewayMySQLSchema(ctx, dst)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_schema_install_failed: %w", err)
		}
		print.printf("destination: installed the native Gateway MySQL schema into the empty database (%d statements, %d tables, version=%s)\n", installed.Statements, len(installed.Tables), installed.Version)
		if state, err = gatewayMySQLCopyInspectDestination(ctx, dst); err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
	}
	if err := gatewayMySQLCopyCheckPreconditions(state, cfg.DBName, sourceTables, expected); err != nil {
		return GatewayMySQLCopyExitUsage, err
	}
	if len(state.selfReferences) != 0 {
		print.printf("destination: self-referencing foreign keys are copied data-aware (NULL first, backfill in the same transaction): %s\n", strings.Join(state.selfReferences, ", "))
	}
	if dirs == nil {
		print.printf("data-dir: not supplied; artifacts and skills stay behind. A production cutover must pass -source-data-dir and -target-data-dir\n")
	}

	if opts.DryRun {
		code, err := gatewayMySQLCopyPrintPlan(ctx, src, dst, print)
		if err != nil {
			return code, err
		}
		if dirs != nil {
			if err := gatewayMySQLCopyPreviewDataDirs(ctx, src, sourceTables, print, dirs); err != nil {
				return GatewayMySQLCopyExitUsage, err
			}
		}
		print.printf("dry-run complete: no row was written, no file was copied and no DDL was executed on %s\n", cfg.DBName)
		return GatewayMySQLCopyExitOK, nil
	}

	var copyDirs *gatewayCopyDataDirs
	if dirs != nil {
		copyDirs = &gatewayCopyDataDirs{Source: dirs.Source, Target: dirs.Target}
	}
	report, err := gatewayCopyRun(ctx, src, dst, false, copyDirs)
	if err != nil {
		// A plan-level refusal is a precondition failure (exit 2), exactly as the
		// dry run reports it; only failures while moving rows are runtime errors.
		if errors.Is(err, errGatewayCopyStale) || errors.Is(err, errGatewayCopySchema) || errors.Is(err, errGatewayCopySourceFK) {
			if table := gatewayCopyTableName(err); table != "" {
				return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_failed: table %s: %w (the transaction was rolled back; no row from this attempt remains in the destination)", table, err)
			}
			return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_failed: %w (the transaction was rolled back; no row from this attempt remains in the destination)", err)
		}
		if table := gatewayCopyTableName(err); table != "" {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_failed: table %s: %w (the transaction was rolled back; no row from this attempt remains in the destination)", table, err)
		}
		return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_failed: %w (the transaction was rolled back; no row from this attempt remains in the destination)", err)
	}
	if err := gatewayMySQLCopyVerify(ctx, src, dst, report, print); err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	names := make([]string, 0, len(report.TableRows))
	destinationOnly := make(map[string]bool, len(report.DestinationOnlyTables))
	for _, table := range report.DestinationOnlyTables {
		destinationOnly[table.Name] = true
	}
	var total int64
	for name := range report.TableRows {
		names = append(names, name)
	}
	sort.Strings(names)
	copied := 0
	for _, name := range names {
		if destinationOnly[name] {
			continue
		}
		copied++
		print.printf("table=%s rows=%d\n", name, report.TableRows[name])
		total += report.TableRows[name]
	}
	gatewayMySQLCopyPrintDestinationOnly(print, report)
	if report.UTF8RepairedCells > 0 {
		repairedTables := make([]string, 0, len(report.UTF8RepairedTables))
		for name := range report.UTF8RepairedTables {
			repairedTables = append(repairedTables, name)
		}
		sort.Strings(repairedTables)
		parts := make([]string, 0, len(repairedTables))
		for _, name := range repairedTables {
			parts = append(parts, fmt.Sprintf("%s=%d", name, report.UTF8RepairedTables[name]))
		}
		print.printf("utf8-normalized: cells=%d tables=%s (invalid UTF-8 bytes in non-PK SQLite TEXT columns replaced with U+FFFD for MySQL utf8mb4)\n", report.UTF8RepairedCells, strings.Join(parts, ","))
	}
	if files := report.Files; files != nil {
		print.printf("data-dir: source=%s target=%s artifacts_files=%d skills_files=%d bytes=%d artifact_rows=%d skill_rows=%d paths_rewritten=%d verified=yes\n", dirs.Source, dirs.Target, files.ArtifactsCopied, files.SkillsCopied, files.Bytes, files.ArtifactRows, files.SkillRows, files.PathsRewritten)
	}
	print.printf("mysql-copy complete: tables=%d rows=%d sha256=%s ledger=%s verified=yes\n", copied, total, report.SHA256, gatewaySchemaBaselineVersion)
	for _, name := range report.LegacyTablesMissing {
		print.printf("skipped_table=%s reason=legacy_destination_only (left empty: the snapshot has no such table)\n", name)
	}
	for _, column := range report.LegacyColumnsSkipped {
		print.printf("skipped_column=%s.%s in_source=%t in_target=%t\n", column.Table, column.Column, column.InSource, column.InTarget)
	}
	if err := print.err; err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_output_failed")
	}
	return GatewayMySQLCopyExitOK, nil
}

// gatewayMySQLCopyDestinationConfig reads and validates the destination DSN.
// Every message here is fixed text or a database name: the DSN carries the
// password and is never echoed, not even through a driver parse error.
func gatewayMySQLCopyDestinationConfig(confirm string) (*mysql.Config, error) {
	dsn := strings.TrimSpace(os.Getenv(gatewayMySQLCopyDSNEnv))
	if dsn == "" {
		return nil, errors.New("gateway_mysql_dsn_missing: " + gatewayMySQLCopyDSNEnv + " is required; the DSN is read only from the environment and never echoed")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("gateway_mysql_dsn_invalid: " + gatewayMySQLCopyDSNEnv + " is not a valid go-sql-driver DSN (the value is never echoed)")
	}
	if cfg.Net != "tcp" || cfg.Addr == "" {
		return nil, errors.New("gateway_mysql_dsn_invalid: " + gatewayMySQLCopyDSNEnv + " must use tcp(host:port)")
	}
	if cfg.DBName == "" {
		return nil, errors.New("gateway_mysql_dsn_invalid: " + gatewayMySQLCopyDSNEnv + " must name a database")
	}
	switch cfg.DBName {
	case "mysql", "information_schema", "performance_schema", "sys":
		return nil, errors.New("gateway_mysql_database_reserved: " + gatewayMySQLCopyDSNEnv + " names a server schema")
	}
	if cfg.DBName != confirm {
		// The database name is not a credential: the operator typed it on both
		// sides and the mismatch must be obvious instead of silently ignored.
		return nil, fmt.Errorf("mysql_copy_confirm_database_mismatch: --confirm-database %q does not match the database named by %s (%q); refusing to write to a database the operator did not name", confirm, gatewayMySQLCopyDSNEnv, cfg.DBName)
	}
	return cfg, nil
}

// gatewayMySQLCopyOpenSource opens the snapshot read-only: mode=ro denies every
// write at the file layer and query_only makes SQLite itself reject DML.
func gatewayMySQLCopyOpenSource(path string) (*sql.DB, error) {
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, errors.New("mysql_copy_source_open_failed: the SQLite snapshot could not be opened read-only")
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// gatewayMySQLCopyInspectSource proves the read-only opening and the snapshot
// integrity before any plan is built. A damaged snapshot stops the run here.
func gatewayMySQLCopyInspectSource(ctx context.Context, db *sql.DB) error {
	var queryOnly int
	if err := db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
		return errors.New("mysql_copy_source_readonly_failed: the snapshot is not open in query_only mode")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return errors.New("mysql_copy_source_corrupt: PRAGMA integrity_check could not run")
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return errors.New("mysql_copy_source_corrupt: PRAGMA integrity_check could not run")
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return errors.New("mysql_copy_source_corrupt: PRAGMA integrity_check could not run")
	}
	if len(lines) != 1 || strings.TrimSpace(lines[0]) != "ok" {
		detail := truncateUTF8Bytes(strings.Join(lines, "; "), 200)
		return fmt.Errorf("mysql_copy_source_corrupt: PRAGMA integrity_check reported %q; refusing to copy from a damaged snapshot", detail)
	}
	return nil
}

// gatewayMySQLCopyOpenDestination applies the DSN, charset and collation
// contract the Gateway itself enforces at startup, minus the schema preparation
// (the command owns that step and reports it).
func gatewayMySQLCopyOpenDestination(ctx context.Context, cfg *mysql.Config) (*sql.DB, string, error) {
	charset, databaseCollation, sqlMode, err := gatewayMySQLDatabaseProbe(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	if charset != "utf8mb4" {
		return nil, "", fmt.Errorf("gateway_mysql_charset_unsupported: database %s defaults to character set %q; the Gateway schema requires utf8mb4", cfg.DBName, charset)
	}
	if cfg.Collation == "" {
		cfg.Collation = databaseCollation
	} else if cfg.Collation != databaseCollation {
		return nil, "", fmt.Errorf("gateway_mysql_collation_mismatch: %s sets collation=%s but database %s defaults to %s; JSON_TABLE comparisons take the session collation, so the DSN cannot override it", gatewayMySQLCopyDSNEnv, cfg.Collation, cfg.DBName, databaseCollation)
	}
	gatewayMySQLApplyRuntimeTransportDefaults(cfg, os.Getenv(gatewayMySQLCopyDSNEnv), sqlMode)
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, "", errors.New("mysql_connector_failed")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(2 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, mysqlPingTimeout())
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, "", gatewayMySQLConnectionError(err)
	}
	if _, err := gatewayMySQLQueryVersion(pingCtx, db); err != nil {
		db.Close()
		return nil, "", err
	}
	if err := gatewayMySQLAssertSessionCollation(pingCtx, db, databaseCollation); err != nil {
		db.Close()
		return nil, "", err
	}
	return db, databaseCollation, nil
}

// gatewayMySQLCopyDestinationState is the read-only inventory of the target:
// how many tables exist, whether the schema ledger is there, which tables
// already hold a row, and which foreign keys point at their own table.
type gatewayMySQLCopyDestinationState struct {
	tables         int
	ledger         bool
	nonEmpty       []gatewayMySQLCopyNonEmptyTable
	selfReferences []string
}

// gatewayMySQLCopyNonEmptyTable is one destination table holding rows. The
// count is carried so a refusal names both the table and the evidence.
type gatewayMySQLCopyNonEmptyTable struct {
	Name string
	Rows int64
}

func gatewayMySQLCopyInspectDestination(ctx context.Context, db *sql.DB) (gatewayMySQLCopyDestinationState, error) {
	state := gatewayMySQLCopyDestinationState{}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'").Scan(&state.tables); err != nil {
		return state, gatewayMySQLConnectionError(err)
	}
	if state.tables == 0 {
		return state, nil
	}
	var ledger int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'gateway_schema_migrations'").Scan(&ledger); err != nil {
		return state, gatewayMySQLConnectionError(err)
	}
	state.ledger = ledger != 0
	rows, err := db.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME")
	if err != nil {
		return state, gatewayMySQLConnectionError(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return state, gatewayMySQLConnectionError(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return state, gatewayMySQLConnectionError(err)
	}
	for _, name := range tables {
		var probe int
		switch err := db.QueryRowContext(ctx, "SELECT 1 FROM "+doSQLQuoteIdentifier("mysql", name)+" LIMIT 1").Scan(&probe); err {
		case nil:
			var rows int64
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+doSQLQuoteIdentifier("mysql", name)).Scan(&rows); err != nil {
				return state, gatewayMySQLConnectionError(err)
			}
			state.nonEmpty = append(state.nonEmpty, gatewayMySQLCopyNonEmptyTable{Name: name, Rows: rows})
		case sql.ErrNoRows:
		default:
			return state, gatewayMySQLConnectionError(err)
		}
	}
	fks, err := db.QueryContext(ctx, "SELECT TABLE_NAME, CONSTRAINT_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA = DATABASE() AND REFERENCED_TABLE_NAME = TABLE_NAME ORDER BY TABLE_NAME, CONSTRAINT_NAME")
	if err != nil {
		return state, gatewayMySQLConnectionError(err)
	}
	defer fks.Close()
	for fks.Next() {
		var table, constraint string
		if err := fks.Scan(&table, &constraint); err != nil {
			return state, gatewayMySQLConnectionError(err)
		}
		state.selfReferences = append(state.selfReferences, table+"."+constraint)
	}
	if err := fks.Err(); err != nil {
		return state, gatewayMySQLConnectionError(err)
	}
	return state, nil
}

// gatewayMySQLCopyCheckPreconditions turns the destination inventory into a
// usage failure (exit 2) before anything is written. Never overwrite, never
// merge, never clear: a database holding any row is refused outright. When the
// only rows belong to native tables the snapshot never had, the refusal is the
// plan's stale verdict rather than a generic non-empty database, so a dry run
// and the real run report the same code, table and row count.
func gatewayMySQLCopyCheckPreconditions(state gatewayMySQLCopyDestinationState, database string, sourceTables, nativeTables map[string]bool) error {
	if !state.ledger {
		return fmt.Errorf("mysql_copy_destination_schema_missing: database %s has %d tables but no gateway_schema_migrations; start the Gateway once with DOOPS_GATEWAY_STORE=mysql so it installs the native schema and writes the baseline row, or point the command at a fresh empty database (this command prepares that one itself)", database, state.tables)
	}
	if len(state.nonEmpty) != 0 {
		parts := make([]string, 0, len(state.nonEmpty))
		nativeOnly := true
		for _, table := range state.nonEmpty {
			parts = append(parts, fmt.Sprintf("%s rows=%d", table.Name, table.Rows))
			if sourceTables[table.Name] || !nativeTables[table.Name] {
				nativeOnly = false
			}
		}
		shown := parts
		if len(shown) > 5 {
			shown = shown[:5]
		}
		if nativeOnly {
			table := state.nonEmpty[0]
			return fmt.Errorf("%w: table %s holds %d row(s) but the snapshot has no such table; this native-schema table is only skipped while it is empty, and the copy never overwrites or clears rows", errGatewayCopyStale, table.Name, table.Rows)
		}
		return fmt.Errorf("mysql_copy_destination_not_empty: database %s already holds rows (first non-empty tables: %s); this command never overwrites, merges or clears existing rows, so use a fresh empty database", database, strings.Join(shown, ", "))
	}
	// Self-referencing foreign keys need no operator action: the copier inserts
	// those columns NULL and backfills them in the same transaction.
	return nil
}

// gatewayMySQLCopyVerify re-reads both sides after the commit and compares them
// table by table. A count that disagrees is a failure, never a warning.
func gatewayMySQLCopyVerify(ctx context.Context, src, dst *sql.DB, report *gatewayMySQLCopyReport, print *gatewayMySQLCopyPrinter) error {
	var mismatches []string
	// A table the copier skipped silently would never appear in the report, so
	// the source inventory is compared with what the copier claims to have moved.
	sourceTables, err := gatewayMySQLCopyTableNames(ctx, src)
	if err != nil {
		return errors.New("mysql_copy_verification_failed: the snapshot table list could not be read after the copy")
	}
	for _, name := range sourceTables {
		if gatewayMySQLCopySQLiteInternal(name) {
			continue
		}
		if _, ok := report.TableRows[name]; !ok {
			mismatches = append(mismatches, name+" was not copied at all")
		}
	}
	destinationOnly := make(map[string]bool, len(report.DestinationOnlyTables)+len(report.LegacyTablesMissing))
	for _, table := range report.DestinationOnlyTables {
		destinationOnly[table.Name] = true
	}
	// Frozen legacy tables are destination-only too: the snapshot does not have
	// them, they were accepted empty, and they must still be empty after the copy.
	for _, name := range report.LegacyTablesMissing {
		destinationOnly[name] = true
	}
	names := make([]string, 0, len(report.TableRows))
	for name := range report.TableRows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if destinationOnly[name] {
			got, err := gatewayMySQLCopyCount(ctx, dst, "mysql", name)
			if err != nil {
				return fmt.Errorf("mysql_copy_verification_failed: table %s: destination row count unreadable", name)
			}
			if got != 0 {
				mismatches = append(mismatches, fmt.Sprintf("%s is native-schema-only in the snapshot but holds %d row(s) at the destination", name, got))
			}
			continue
		}
		want, err := gatewayMySQLCopyCount(ctx, src, "sqlite", name)
		if err != nil {
			return fmt.Errorf("mysql_copy_verification_failed: table %s: source row count unreadable", name)
		}
		if want != report.TableRows[name] {
			mismatches = append(mismatches, fmt.Sprintf("%s source=%d copied=%d", name, want, report.TableRows[name]))
			continue
		}
		got, err := gatewayMySQLCopyCount(ctx, dst, "mysql", name)
		if err != nil {
			return fmt.Errorf("mysql_copy_verification_failed: table %s: destination row count unreadable", name)
		}
		if got != want {
			mismatches = append(mismatches, fmt.Sprintf("%s source=%d destination=%d", name, want, got))
		}
	}
	if len(mismatches) != 0 {
		shown := mismatches
		if len(shown) > 5 {
			shown = shown[:5]
		}
		return fmt.Errorf("mysql_copy_verification_failed: per-table row counts disagree (%s); %d table(s) differ, the copy is not accepted", strings.Join(shown, "; "), len(mismatches))
	}
	var rows int
	var version string
	if err := dst.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(MIN(version), '') FROM gateway_schema_migrations").Scan(&rows, &version); err != nil {
		return errors.New("mysql_copy_verification_failed: the destination schema ledger is unreadable")
	}
	if rows != 1 || version != gatewaySchemaBaselineVersion {
		return fmt.Errorf("mysql_copy_verification_failed: destination gateway_schema_migrations holds %d row(s) with version %q, want exactly one %q", rows, version, gatewaySchemaBaselineVersion)
	}
	print.printf("verified: every table matches the source row count and the schema ledger holds exactly one %s row\n", gatewaySchemaBaselineVersion)
	return nil
}

func gatewayMySQLCopyTableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_list WHERE schema = 'main' AND type = 'table' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// gatewayMySQLCopySQLiteInternal reports whether name is one of SQLite's own
// bookkeeping tables. They exist in every database file and the copier never
// moves them, so the post-copy comparison must not treat them as data.
func gatewayMySQLCopySQLiteInternal(name string) bool {
	switch name {
	case "sqlite_schema", "sqlite_temp_schema", "sqlite_sequence",
		"sqlite_stat1", "sqlite_stat2", "sqlite_stat3", "sqlite_stat4":
		return true
	}
	return false
}

// gatewayMySQLCopySourceTableSet is the snapshot inventory the plan rules are
// applied to: SQLite's own bookkeeping tables are excluded, everything else is
// a table the destination must be able to receive (or a schema mismatch).
func gatewayMySQLCopySourceTableSet(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	names, err := gatewayMySQLCopyTableNames(ctx, db)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(names))
	for _, name := range names {
		if !gatewayMySQLCopySQLiteInternal(name) {
			set[name] = true
		}
	}
	return set, nil
}

func gatewayMySQLCopyCount(ctx context.Context, db *sql.DB, engine, table string) (int64, error) {
	var n int64
	qualified := doSQLQuoteIdentifier(engine, table)
	if engine == "sqlite" {
		qualified = "main." + qualified
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+qualified).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// gatewayMySQLCopyPrintPlan is the review path: it builds the copy plan with the
// copier's own schema validation inside two transactions and rolls both back,
// so not one row and not one DDL statement is executed.
func gatewayMySQLCopyPrintPlan(ctx context.Context, src, dst *sql.DB, print *gatewayMySQLCopyPrinter) (int, error) {
	srcTx, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelSerializable})
	if err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_read_failed: the snapshot could not be read")
	}
	defer srcTx.Rollback()
	dstConn, err := dst.Conn(ctx)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_connect_failed")
	}
	defer dstConn.Close()
	serverVersion, err := gatewayMySQLQueryVersion(ctx, dstConn)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	// The plan asserts the same session guards the copier relies on. Apply them on
	// one private connection so the assertion cannot depend on server defaults.
	sessionInitSQL := `SET SESSION information_schema_stats_expiry=0,
		sql_mode=CONCAT_WS(',',NULLIF(@@SESSION.sql_mode,''),'STRICT_ALL_TABLES','NO_AUTO_VALUE_ON_ZERO')`
	if serverVersion.isMySQL57() {
		sessionInitSQL = `SET SESSION sql_mode=CONCAT_WS(',',NULLIF(@@SESSION.sql_mode,''),'STRICT_ALL_TABLES','NO_AUTO_VALUE_ON_ZERO')`
	}
	if _, err := dstConn.ExecContext(ctx, sessionInitSQL); err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_unsafe_session")
	}
	dstTx, err := dstConn.BeginTx(ctx, nil)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_begin_failed")
	}
	defer dstTx.Rollback()
	report := &gatewayMySQLCopyReport{TableRows: make(map[string]int64)}
	tables, err := gatewayCopyPlan(ctx, srcTx, dstTx, report, serverVersion.isMySQL57())
	if err != nil {
		if errors.Is(err, errGatewayCopyStale) {
			return GatewayMySQLCopyExitUsage, fmt.Errorf("%w (nothing was written; the destination is left exactly as it was)", err)
		}
		if errors.Is(err, errGatewayCopySourceFK) {
			if table := gatewayCopyTableName(err); table != "" {
				return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_failed: table %s: %w (nothing was written; fix the source foreign-key violation and retake the snapshot)", table, err)
			}
			return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_failed: %w (nothing was written; fix the source foreign-key violation and retake the snapshot)", err)
		}
		if errors.Is(err, errGatewayCopySchema) {
			if table := gatewayCopyTableName(err); table != "" {
				return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_schema_mismatch: table %s: the snapshot and the destination schema differ (tables and columns must match exactly); nothing was written", table)
			}
			return GatewayMySQLCopyExitUsage, errors.New("mysql_copy_schema_mismatch: the snapshot and the destination schema differ (tables and columns must match exactly); nothing was written")
		}
		return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_plan_failed: %w (nothing was written)", err)
	}
	var total int64
	skippedColumns := map[string]int{}
	for _, column := range report.LegacyColumnsSkipped {
		skippedColumns[column.Table]++
	}
	for _, t := range tables {
		var rows int64
		if err := srcTx.QueryRowContext(ctx, "SELECT COUNT(*) FROM main."+doSQLQuoteIdentifier("sqlite", t.name)).Scan(&rows); err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_read_failed: table %s could not be counted in the snapshot", t.name)
		}
		var probe int
		target := "empty"
		switch err := dstTx.QueryRowContext(ctx, "SELECT 1 FROM "+doSQLQuoteIdentifier("mysql", t.name)+" LIMIT 1").Scan(&probe); err {
		case nil:
			target = "not-empty"
		case sql.ErrNoRows:
		default:
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_read_failed: table %s could not be probed in the destination", t.name)
		}
		skipped := ""
		if n := skippedColumns[t.name]; n != 0 {
			skipped = fmt.Sprintf(" skipped_columns=%d", n)
		}
		print.printf("table=%s source_rows=%d target=%s copy=yes%s\n", t.name, rows, target, skipped)
		total += rows
	}
	for _, name := range report.LegacyTablesMissing {
		print.printf("table=%s source_rows=0 target=empty copy=no reason=legacy_destination_only\n", name)
	}
	gatewayMySQLCopyPrintDestinationOnly(print, report)
	for _, column := range report.LegacyColumnsSkipped {
		print.printf("skipped_column=%s.%s in_source=%t in_target=%t\n", column.Table, column.Column, column.InSource, column.InTarget)
	}
	print.printf("plan: tables=%d source_rows=%d\n", len(tables), total)
	if err := srcTx.Rollback(); err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_rollback_failed")
	}
	if err := dstTx.Rollback(); err != nil {
		return GatewayMySQLCopyExitRuntime, errors.New("mysql_copy_rollback_failed")
	}
	return GatewayMySQLCopyExitOK, nil
}

// gatewayMySQLCopyPrintDestinationOnly names every native-schema table the
// snapshot never had. The warning states why leaving them empty is safe, and
// the per-table line is the evidence an operator reviews before the cutover.
func gatewayMySQLCopyPrintDestinationOnly(print *gatewayMySQLCopyPrinter, report *gatewayMySQLCopyReport) {
	nativeOnly := make([]string, 0, len(report.DestinationOnlyTables))
	for _, table := range report.DestinationOnlyTables {
		nativeOnly = append(nativeOnly, table.Name)
	}
	destinationOnly := len(nativeOnly) + len(report.LegacyTablesMissing)
	if destinationOnly == 0 {
		return
	}
	sample := append(append([]string{}, nativeOnly...), report.LegacyTablesMissing...)
	print.printf("warning: %d table(s) exist in this release's native schema but not in the snapshot (%s): %d are this version's feature modules this SQLite deployment never created (native_schema_only) and %d are retired historical tables kept for compatibility (legacy_destination_only); their destination tables are empty, so they are left empty and not copied\n", destinationOnly, strings.Join(sample[:min(5, len(sample))], ", "), len(nativeOnly), len(report.LegacyTablesMissing))
	for _, table := range report.DestinationOnlyTables {
		print.printf("table=%s source=absent destination=empty reason=native_schema_only\n", table.Name)
	}
	print.printf("destination_only: total=%d (native_schema_only=%d legacy_destination_only=%d) left empty\n", destinationOnly, len(nativeOnly), len(report.LegacyTablesMissing))
}

// gatewayMySQLCopyPredictEmptyDestination reports what the real run does to an
// empty destination — install the native schema, then copy — without executing
// any DDL. It returns the same exit code the real run would end with, so a
// dry run cannot tell an operator "review somewhere else" while the real run
// accepts the very same database.
func gatewayMySQLCopyPredictEmptyDestination(w io.Writer, dbName string, tables int) (int, error) {
	print := &gatewayMySQLCopyPrinter{w: w}
	print.printf("destination: empty; the real run installs the native Gateway MySQL schema here first (%d tables, version=%s) and then copies\n", tables, gatewayMySQLSchemaVersion)
	print.printf("dry-run complete: the real run would install the schema and copy; no row was written and no DDL was executed on %s\n", dbName)
	return GatewayMySQLCopyExitOK, print.err
}

// gatewayMySQLCopyPreviewPlan prints the plan an empty destination can support:
// the tables the native schema will install and the rows the snapshot would
// carry. It applies the same acceptance rules as the real plan — a native table
// the snapshot lacks is accepted only because the install leaves it empty, and
// a snapshot table the native schema cannot create is a schema mismatch — so a
// dry run and the real run cannot disagree about the same pair.
func gatewayMySQLCopyPreviewPlan(ctx context.Context, src *sql.DB, print *gatewayMySQLCopyPrinter, present, expected map[string]bool) (int, error) {
	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)
	var unknown []string
	for name := range present {
		if !gatewayMySQLCopySQLiteInternal(name) && !expected[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	var total int64
	var nativeOnly, legacyOnly []string
	for _, name := range names {
		if !present[name] {
			bucket, err := gatewayCopyClassifyDestinationOnly(name, false, 0)
			if err != nil {
				return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_preview_failed: table %s: %w", name, err)
			}
			switch bucket {
			case gatewayCopyBucketNativeSchemaOnly:
				nativeOnly = append(nativeOnly, name)
			case gatewayCopyBucketLegacyDestinationOnly:
				legacyOnly = append(legacyOnly, name)
			default:
				return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_preview_failed: table %s has no destination-only bucket", name)
			}
			print.printf("table=%s source=absent destination=empty copy=no reason=%s\n", name, bucket)
			continue
		}
		var count int64
		if err := src.QueryRowContext(ctx, "SELECT COUNT(*) FROM main."+doSQLQuoteIdentifier("sqlite", name)).Scan(&count); err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("mysql_copy_read_failed: table %s could not be counted in the snapshot", name)
		}
		print.printf("table=%s source_rows=%d target=empty copy=yes\n", name, count)
		total += count
	}
	destinationOnly := len(nativeOnly) + len(legacyOnly)
	print.printf("plan(preview): tables=%d destination_only=%d (native_schema_only=%d legacy_destination_only=%d) source_rows=%d\n", len(names)-destinationOnly, destinationOnly, len(nativeOnly), len(legacyOnly), total)
	if destinationOnly != 0 {
		sample := append(append([]string{}, nativeOnly...), legacyOnly...)
		print.printf("warning: %d table(s) exist in this release's native schema but not in the snapshot (%s): %d are this version's feature modules this SQLite deployment never created (native_schema_only) and %d are retired historical tables kept for compatibility (legacy_destination_only); their destination tables are empty, so they are left empty instead of copied\n", destinationOnly, strings.Join(sample[:min(5, len(sample))], ", "), len(nativeOnly), len(legacyOnly))
	}
	if len(unknown) != 0 {
		return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_schema_mismatch: the snapshot holds table(s) the embedded native schema does not create (%s); the real run installs that schema and would reject them, so this dry run refuses too (nothing was written)", strings.Join(unknown[:min(5, len(unknown))], ", "))
	}
	if err := gatewayCopyCheckSourceForeignKeys(ctx, src); err != nil {
		if table := gatewayCopyTableName(err); table != "" {
			return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_failed: table %s: %w (nothing was written; fix the source foreign-key violation and retake the snapshot)", table, err)
		}
		return GatewayMySQLCopyExitUsage, fmt.Errorf("mysql_copy_failed: %w (nothing was written; fix the source foreign-key violation and retake the snapshot)", err)
	}
	return GatewayMySQLCopyExitOK, nil
}

type gatewayMySQLCopyPrinter struct {
	w   io.Writer
	err error
}

func (p *gatewayMySQLCopyPrinter) printf(format string, args ...any) {
	if p == nil || p.err != nil {
		return
	}
	if p.w == nil {
		p.w = io.Discard
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

// gatewayMySQLCopyDataDirOptions validates the filesystem half. Both roots are
// required together, must be absolute, and must name different directories: a
// cutover never merges into a data directory that already holds files.
func gatewayMySQLCopyDataDirOptions(source, target string) (*gatewayCopyDataDirs, error) {
	source, target = strings.TrimSpace(source), strings.TrimSpace(target)
	if source == "" && target == "" {
		return nil, nil
	}
	if source == "" || target == "" {
		return nil, errors.New("mysql_copy_data_dir_required: -source-data-dir and -target-data-dir must be given together")
	}
	for _, path := range []string{source, target} {
		if !filepath.IsAbs(path) {
			return nil, errors.New("mysql_copy_data_dir_invalid: data directories must be absolute paths, got " + path)
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return nil, errors.New("mysql_copy_data_dir_invalid: data directory " + path + " is not a readable directory")
		}
	}
	source, target = filepath.Clean(source), filepath.Clean(target)
	if source == target {
		return nil, errors.New("mysql_copy_data_dir_not_distinct: -source-data-dir and -target-data-dir are the same directory; a cutover never copies a data directory onto itself")
	}
	for _, name := range []string{"artifacts", "skills"} {
		if _, err := os.Stat(filepath.Join(target, name)); err == nil {
			return nil, fmt.Errorf("mysql_copy_data_dir_not_empty: %s already exists under %s; a cutover never merges files", name, target)
		}
	}
	return &gatewayCopyDataDirs{Source: source, Target: target}, nil
}

// gatewayMySQLCopyPreviewDataDirs proves the dry-run filesystem verdict without
// copying a byte: every indexed artifact and Skill package must exist under the
// source data dir (with the same legacy absolute-path containment and digest
// rules the real copy applies), and the target trees must still be free.
func gatewayMySQLCopyPreviewDataDirs(ctx context.Context, src *sql.DB, sourceTables map[string]bool, print *gatewayMySQLCopyPrinter, dirs *gatewayCopyDataDirs) error {
	count := func(name string) (int64, error) {
		var files int64
		root := filepath.Join(dirs.Source, name)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if entry.Type().IsRegular() {
				files++
			}
			return nil
		})
		return files, err
	}
	artifacts, err := count("artifacts")
	if err != nil {
		return errors.New("mysql_copy_files_failed: the source artifacts tree cannot be read")
	}
	skills, err := count("skills")
	if err != nil {
		return errors.New("mysql_copy_files_failed: the source skills tree cannot be read")
	}
	var artifactRows, skillRows, rewritten int64
	if sourceTables["monitor_run_items"] {
		rows, err := src.QueryContext(ctx, "SELECT id, run_id, artifact_dir, artifact_bytes, artifact_sha256 FROM monitor_run_items WHERE artifact_dir != ''")
		if err != nil {
			return errors.New("mysql_copy_read_failed: the snapshot artifact index could not be read")
		}
		for rows.Next() {
			var id, size int64
			var runID, dir, wantSHA string
			if err := rows.Scan(&id, &runID, &dir, &size, &wantSHA); err != nil {
				rows.Close()
				return errors.New("mysql_copy_read_failed: the snapshot artifact index could not be read")
			}
			rel, needsRewrite, err := gatewayCopyResolveArtifactDir(dirs.Source, runID, id, dir)
			if err != nil {
				rows.Close()
				return err
			}
			if needsRewrite {
				rewritten++
			}
			sourceDir := filepath.Join(dirs.Source, "artifacts", filepath.FromSlash(rel))
			gotBytes, gotSHA, digestErr := gatewayCopyArtifactDigest(sourceDir)
			if digestErr != nil || gotBytes == 0 {
				rows.Close()
				return fmt.Errorf("mysql_copy_artifact_missing: run_id=%s item_id=%d artifact_dir=%s has no files under %s", runID, id, rel, sourceDir)
			}
			if wantSHA != "" && (gotSHA != wantSHA || gotBytes != size) {
				rows.Close()
				return fmt.Errorf("mysql_copy_artifact_mismatch: run_id=%s item_id=%d artifact_dir=%s records %d bytes sha256=%s but the files are %d bytes sha256=%s", runID, id, rel, size, wantSHA, gotBytes, gotSHA)
			}
			artifactRows++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return errors.New("mysql_copy_read_failed: the snapshot artifact index could not be read")
		}
	}
	if sourceTables["platform_skills"] {
		skillQuery, err := src.QueryContext(ctx, "SELECT slug, storage_dir FROM platform_skills WHERE storage_dir != ''")
		if err != nil {
			return errors.New("mysql_copy_read_failed: the snapshot skill index could not be read")
		}
		for skillQuery.Next() {
			var slug, dir string
			if err := skillQuery.Scan(&slug, &dir); err != nil {
				skillQuery.Close()
				return errors.New("mysql_copy_read_failed: the snapshot skill index could not be read")
			}
			rel, needsRewrite, err := gatewayCopyResolveSkillDir(dirs.Source, slug, dir)
			if err != nil {
				skillQuery.Close()
				return err
			}
			if needsRewrite {
				rewritten++
			}
			if info, err := os.Stat(filepath.Join(dirs.Source, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
				skillQuery.Close()
				return fmt.Errorf("mysql_copy_skill_missing: platform_skills slug=%s storage_dir=%s does not exist under %s", slug, rel, dirs.Source)
			}
			skillRows++
		}
		skillQuery.Close()
		if err := skillQuery.Err(); err != nil {
			return errors.New("mysql_copy_read_failed: the snapshot skill index could not be read")
		}
	}
	print.printf("data-dir: source=%s target=%s artifacts_files=%d skills_files=%d artifact_rows=%d skill_rows=%d paths_rewritten=%d (dry-run: nothing copied)\n", dirs.Source, dirs.Target, artifacts, skills, artifactRows, skillRows, rewritten)
	return nil
}
