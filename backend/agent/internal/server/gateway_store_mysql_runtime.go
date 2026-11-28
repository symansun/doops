package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// This file is the runtime half of the MySQL store path. Offline tooling
// (gateway_mysql_copy.go) moved rows between two servers; this file lets the
// Gateway itself select a MySQL backend with DOOPS_GATEWAY_STORE=mysql.
//
// Fail-closed rules:
//   - an unknown backend name is a startup error, never a fallback to SQLite;
//   - a missing or unparsable DSN is a startup error, never a fallback;
//   - an existing schema without the D13 ledger row is a startup error, because
//     the Gateway cannot tell a Gateway schema from someone else's tables.
//
//   - a bootstrap row the shared list seeds cannot be written or read back is a
//     startup error, because the edge registry reads edge_route_revision while the
//     hub is built and a missing row used to be a bare panic (INT-33);
//   - a missing, relative, or unwritable DOOPS_GATEWAY_DATA_DIR is a startup
//     error, because a store with no file root would silently keep Skill
//     packages and inspection artifacts in database columns (D18).
//
// Rollback is an operator action: set DOOPS_GATEWAY_STORE=sqlite again. The
// MySQL schema is left untouched (no destructive step runs on shutdown).

// defaultGatewayMySQLDataDir is the fixed root for the files a MySQL-backed
// Gateway owns: Skill bundles unpacked on disk and File-First inspection
// artifacts. A SQLite store derives the root from its database file; MySQL has
// no such file, so the root is explicit and absolute.
const defaultGatewayMySQLDataDir = "/var/lib/doops-gateway"

// gatewayMySQLDataDir resolves DOOPS_GATEWAY_DATA_DIR. It is the MySQL half of
// D18: openGatewayMySQLStore used to build a store with no directory, so
// skillStorageRoot() and monitorArtifactRoot() returned "", Skill packages never
// unpacked, and File-First artifacts silently fell back to the database text
// column. That is exactly the silent downgrade AGENTS.md forbids, so a bad root
// is a startup error, never a degraded mode.
//
// The override is ignored on the SQLite backend: a SQLite store keeps its files
// next to its database file, so its behavior does not change.
func gatewayMySQLDataDir() (string, error) {
	dir, err := gatewayMySQLDataDirPath(os.Getenv("DOOPS_GATEWAY_DATA_DIR"))
	if err != nil {
		return "", err
	}
	if err := gatewayMySQLDataDirEnsureWritable(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// gatewayMySQLDataDirPath resolves the configured root without touching the
// filesystem. An empty value takes the default, and a relative value is a
// startup error instead of a silent dependency on the process working directory.
func gatewayMySQLDataDirPath(raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		dir = defaultGatewayMySQLDataDir
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("gateway_data_dir_invalid: DOOPS_GATEWAY_DATA_DIR=%q must be an absolute path", dir)
	}
	return filepath.Clean(dir), nil
}

// gatewayMySQLDataDirEnsureWritable proves the root can hold the Skill bundles
// and File-First artifacts this Gateway writes. MkdirAll alone is not enough:
// the directories can exist and still refuse a new file.
func gatewayMySQLDataDirEnsureWritable(dir string) error {
	for _, name := range []string{"skills", "artifacts"} {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0700); err != nil {
			return fmt.Errorf("gateway_data_dir_unwritable: %s: %w", sub, err)
		}
		probe, err := os.CreateTemp(sub, ".doops-gateway-write-probe-")
		if err != nil {
			return fmt.Errorf("gateway_data_dir_unwritable: %s: %w", sub, err)
		}
		probeName := probe.Name()
		closeErr := probe.Close()
		removeErr := os.Remove(probeName)
		if closeErr != nil {
			return fmt.Errorf("gateway_data_dir_unwritable: %s: %w", sub, closeErr)
		}
		if removeErr != nil {
			return fmt.Errorf("gateway_data_dir_unwritable: %s: %w", probeName, removeErr)
		}
	}
	return nil
}

// openGatewayMySQLStore opens the MySQL backend and prepares its schema.
func openGatewayMySQLStore(ctx context.Context) (*GatewayStore, error) {
	dsn := strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_MYSQL_DSN"))
	if dsn == "" {
		return nil, errors.New("gateway_mysql_dsn_missing: DOOPS_GATEWAY_MYSQL_DSN is required when DOOPS_GATEWAY_STORE=mysql")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		// Do not echo the parse error: it can contain the DSN.
		return nil, errors.New("gateway_mysql_dsn_invalid: DOOPS_GATEWAY_MYSQL_DSN is not a valid go-sql-driver DSN")
	}
	if cfg.Net != "tcp" || cfg.Addr == "" {
		return nil, errors.New("gateway_mysql_dsn_invalid: DOOPS_GATEWAY_MYSQL_DSN must use tcp(host:port)")
	}
	if cfg.DBName == "" {
		return nil, errors.New("gateway_mysql_dsn_invalid: DOOPS_GATEWAY_MYSQL_DSN must name a database")
	}
	switch cfg.DBName {
	case "mysql", "information_schema", "performance_schema", "sys":
		return nil, errors.New("gateway_mysql_database_reserved: DOOPS_GATEWAY_MYSQL_DSN names a server schema")
	}
	// D18: the rows live in MySQL, but Skill bundles and File-First inspection
	// artifacts are files. Resolve their root before connecting, so a broken data
	// directory fails at startup instead of silently keeping both in database
	// columns (see gatewayMySQLDataDir).
	dataDir, err := gatewayMySQLDataDir()
	if err != nil {
		return nil, err
	}
	// MySQL renders the json_each/json_group_array family as JSON_TABLE ... COLUMNS(...).
	// Those derived columns take the *session* collation, while every table column
	// inherits the database default collation. go-sql-driver negotiates
	// utf8mb4_general_ci when the DSN names none, so a database created with the
	// MySQL 8 default (utf8mb4_0900_ai_ci) raised
	// "Error 1267: Illegal mix of collations (utf8mb4_0900_ai_ci,IMPLICIT) and
	// (utf8mb4_general_ci,IMPLICIT) for operation '='" the first time the Gateway
	// reconciled agent resources. Align the session with the database default and
	// refuse a database whose tables disagree with it; both failures are startup
	// errors, never a silent fallback.
	charset, databaseCollation, sqlMode, err := gatewayMySQLDatabaseProbe(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if charset != "utf8mb4" {
		return nil, fmt.Errorf("gateway_mysql_charset_unsupported: database %s defaults to character set %q; the Gateway schema requires utf8mb4", cfg.DBName, charset)
	}
	if cfg.Collation == "" {
		cfg.Collation = databaseCollation
	} else if cfg.Collation != databaseCollation {
		return nil, fmt.Errorf("gateway_mysql_collation_mismatch: DOOPS_GATEWAY_MYSQL_DSN sets collation=%s but database %s defaults to %s; JSON_TABLE comparisons take the session collation, so the DSN cannot override it", cfg.Collation, cfg.DBName, databaseCollation)
	}
	gatewayMySQLApplyRuntimeTransportDefaults(cfg, dsn, sqlMode)
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, errors.New("mysql_connector_failed")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(24)
	db.SetMaxIdleConns(8)
	db.SetConnMaxIdleTime(2 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, mysqlPingTimeout())
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, gatewayMySQLConnectionError(err)
	}
	serverVersion, err := gatewayMySQLQueryVersion(pingCtx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := gatewayMySQLAssertTableCollations(ctx, db, databaseCollation); err != nil {
		db.Close()
		return nil, err
	}
	// The schema preparation seeds the same startup defaults SQLite's migrate()
	// seeds, so the store must exist (and the process dialect must already be
	// MySQL) before it runs.
	store := &GatewayStore{
		db:            db,
		dialect:       gatewayDialectMySQL,
		backend:       "mysql",
		mysqlAddr:     cfg.Addr,
		mysqlDatabase: cfg.DBName,
		mysql57:       serverVersion.isMySQL57(),
		dbDir:         dataDir,
	}
	previousDialect := gatewayDialect()
	previousMySQL57 := gatewayIsMySQL57()
	// Remember what was current so Close can put it back: the ambient dialect is
	// a property of the live store, not of the process lifetime.
	store.previousDialect = previousDialect
	store.previousMySQL57 = previousMySQL57
	gatewaySetDialect(gatewayDialectMySQL)
	gatewaySetMySQL57(store.mysql57)
	// The installer runs DDL on a pooled connection. A schema statement that
	// changes the session collation (the pre-2026-11-24 SET NAMES) would otherwise
	// go back into the pool and raise Error 1267 intermittently, long after the
	// startup checks passed. Refusing idle connections while the schema is
	// prepared keeps that session out of the pool; the assertions below then run
	// on sessions the DSN alone configured.
	db.SetMaxIdleConns(0)
	prepareErr := gatewayMySQLPrepareRuntimeSchema(ctx, store)
	db.SetMaxIdleConns(8)
	if prepareErr != nil {
		gatewaySetDialect(previousDialect)
		gatewaySetMySQL57(previousMySQL57)
		db.Close()
		return nil, prepareErr
	}
	// Table and DSN collations are asserted before the schema exists, so they
	// cannot see a session or a column the installation just changed.
	if err := gatewayMySQLAssertSessionCollation(ctx, db, databaseCollation); err != nil {
		gatewaySetDialect(previousDialect)
		gatewaySetMySQL57(previousMySQL57)
		db.Close()
		return nil, err
	}
	if err := gatewayMySQLAssertColumnCollations(ctx, db, databaseCollation); err != nil {
		gatewaySetDialect(previousDialect)
		gatewaySetMySQL57(previousMySQL57)
		db.Close()
		return nil, err
	}
	// One backend per process; every renderer reads this value. The address is
	// logged without user or password because cfg.Addr holds only host:port.
	log.Printf("gateway store: backend=mysql address=%s database=%s data_dir=%s version=%s", cfg.Addr, cfg.DBName, dataDir, serverVersion.Raw)
	return store, nil
}

// gatewayMySQLPrepareRuntimeSchema installs the empty-schema DDL on first
// startup, runs the shared startup seeds, and always ends with exactly one D13
// baseline ledger row.
func gatewayMySQLPrepareRuntimeSchema(ctx context.Context, store *GatewayStore) error {
	db := store.db
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'").Scan(&tables); err != nil {
		return errors.New("mysql_schema_inspect_failed")
	}
	if tables == 0 {
		if _, err := installGatewayMySQLSchema(ctx, db); err != nil {
			return err
		}
	}
	// Reading the ledger proves the schema is a Gateway schema rather than
	// someone else's tables; the row count itself is checked after the seeds.
	var existingLedgerRows int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gateway_schema_migrations WHERE version = ?", gatewaySchemaBaselineVersion).Scan(&existingLedgerRows); err != nil {
		return errors.New("mysql_schema_ledger_missing: the database has tables but no gateway_schema_migrations ledger; run the offline copier instead of pointing the Gateway at a foreign schema")
	}
	// INT-74: the DDL installs the digest columns wide enough for today's hash,
	// but a database installed by an older release keeps VARCHAR(64) and would
	// reject every issued token with error 1406. Widen in place before any
	// request can write; the width is read from information_schema first, so
	// this is a no-op on a current schema and on every restart after the first.
	if err := gatewayMySQLWidenHashColumns(ctx, db); err != nil {
		return err
	}
	// INT-91: the same shape for the INT-87 truncation marker. A database
	// installed before execution_records gained terminal_truncated/terminal_bytes
	// would otherwise read every terminal back without a marker and hand a cut
	// payload to the caller as if it were whole. Missing columns are added in
	// place before the hub restores any record; a current schema issues no DDL.
	if _, err := gatewayMySQLEnsureExecutionRecordColumns(ctx, db); err != nil {
		return err
	}
	// INT-108: widen session, sha256: digest and import receipt columns on
	// already-installed MySQL databases so strict mode never rejects values
	// existing SQLite deployments already hold (or runtime writes produce).
	if _, err := gatewayMySQLWidenMigrationColumns(ctx, db); err != nil {
		return err
	}
	if _, err := gatewayMySQLEnsureInventoryMachineColumns(ctx, db); err != nil {
		return err
	}
	if _, err := gatewayMySQLEnsurePluginTables(ctx, db); err != nil {
		return err
	}
	if _, err := gatewayMySQLEnsurePluginPhase2Columns(ctx, db); err != nil {
		return err
	}
	// INT-33: run the same startup seeds SQLite's migrate() runs. The MySQL path
	// used to install DDL and write one ledger row, so edge_route_revision stayed
	// empty (the edge registry panicked on the first read) and the built-in skill
	// and MCP catalogues stayed empty. Every seed is a do-nothing upsert, so a
	// restart or a racing startup is safe.
	if err := store.seedRuntimeDefaults(ctx); err != nil {
		return err
	}
	var rows int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gateway_schema_migrations WHERE version = ?", gatewaySchemaBaselineVersion).Scan(&rows); err != nil {
		return errors.New("mysql_schema_ledger_inspect_failed")
	}
	if rows != 1 {
		return fmt.Errorf("mysql_schema_ledger_invalid: found %d baseline rows, want exactly one", rows)
	}
	// Read the seeded rows back with the statements the runtime uses first, so a
	// store that lost a seed fails with the table name instead of panicking while
	// the hub builds the edge registry (tunnel_hub.go:127).
	if err := verifyGatewayStoreBootstrapRows(ctx, db); err != nil {
		return err
	}
	return nil
}

// useDialect runs fn with this store's dialect and restores the process value
// afterwards. Production opens one store per process, so the global default is
// enough there; the scope exists so tests and offline tools can host both.
func (s *GatewayStore) useDialect(fn func()) {
	dialect := s.dialect
	if dialect == "" {
		dialect = gatewayDialectSQLite
	}
	gatewayUseDialect(dialect, func() {
		previous57 := gatewayIsMySQL57()
		gatewaySetMySQL57(dialect == gatewayDialectMySQL && s.mysql57)
		defer gatewaySetMySQL57(previous57)
		fn()
	})
}

// gatewayMySQLRetry retries a single statement whose only failure was InnoDB
// deadlock (1213) or lock wait timeout (1205). Retrying is safe only because
// the failed statement rolled back completely; a transaction that already
// committed earlier statements must not be retried this way.
func gatewayMySQLRetry(ctx context.Context, op func() error) error {
	delay := 50 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := op()
		if err == nil || !gatewayMySQLRetryable(err) || attempt == 2 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func gatewayMySQLRetryable(err error) bool {
	var serverError *mysql.MySQLError
	if !errors.As(err, &serverError) {
		return false
	}
	return serverError.Number == 1213 || serverError.Number == 1205
}

// gatewayMySQLStartAudit is the one write path this slice retries: an INSERT
// that is a single implicit transaction, so a 1213/1205 rollback leaves no
// partial work. Reads, multi-statement transactions and upserts inside an
// explicit tx do not use it.
func (s *GatewayStore) gatewayMySQLStartAudit(query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := gatewayMySQLRetry(context.Background(), func() error {
		var execErr error
		result, execErr = s.db.Exec(query, args...)
		return execErr
	})
	return result, err
}

// gatewayRetryExecer lets finishAuditWith reuse the retry policy without
// knowing about MySQL: only the store-level (non-transactional) caller wraps it.
type gatewayRetryExecer struct{ execer auditExecer }

func (r gatewayRetryExecer) Exec(query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := gatewayMySQLRetry(context.Background(), func() error {
		var execErr error
		result, execErr = r.execer.Exec(query, args...)
		return execErr
	})
	return result, err
}

// QueryRow forwards to the wrapped execer so finishAuditWith can still read the
// terminal status when an UPDATE matched no rows. Without this the retry wrapper
// would answer every zero-row update with sql.ErrNoRows and turn an immutable
// terminal audit row into a permanent "persistence pending" retry.
func (r gatewayRetryExecer) QueryRow(query string, args ...any) *sql.Row {
	if querier, ok := r.execer.(interface {
		QueryRow(string, ...any) *sql.Row
	}); ok {
		return querier.QueryRow(query, args...)
	}
	return nil
}

const (
	gatewayMySQLDefaultReadTimeout  = 60 * time.Second
	gatewayMySQLDefaultWriteTimeout = 60 * time.Second
)

// gatewayMySQLShouldInterpolateParams reports whether client-side parameter
// interpolation is safe and permitted for this DSN/server pair. Under utf8mb4
// (already required by openGatewayMySQLStore) without NO_BACKSLASH_ESCAPES,
// InterpolateParams=true cuts every one-shot SQL statement from 3 WAN
// round-trips (COM_STMT_PREPARE + EXECUTE + CLOSE) down to 1. An explicit
// interpolateParams= query option in the raw DSN always takes precedence.
func gatewayMySQLShouldInterpolateParams(rawDSN, sqlMode string) bool {
	if strings.Contains(strings.ToLower(rawDSN), "interpolateparams=") {
		return false
	}
	return !strings.Contains(strings.ToUpper(sqlMode), "NO_BACKSLASH_ESCAPES")
}

// gatewayMySQLApplyRuntimeTransportDefaults configures WAN-safe driver defaults
// without overriding explicit operator DSN parameters.
func gatewayMySQLApplyRuntimeTransportDefaults(cfg *mysql.Config, rawDSN, sqlMode string) {
	cfg.Logger = gatewayMySQLQuietLogger{}
	if cfg.Timeout == 0 {
		cfg.Timeout = timeouts.MySQLConnectTimeout
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = gatewayMySQLDefaultReadTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = gatewayMySQLDefaultWriteTimeout
	}
	cfg.CheckConnLiveness = true
	if gatewayMySQLShouldInterpolateParams(rawDSN, sqlMode) {
		cfg.InterpolateParams = true
	}
}

// gatewayMySQLDatabaseProbe reads the target database's default character set,
// collation, and server sql_mode in a single startup round-trip. The probe
// connector deliberately leaves Collation empty so the server's real default is
// observed before negotiating the production pool.
func gatewayMySQLDatabaseProbe(ctx context.Context, cfg *mysql.Config) (string, string, string, error) {
	probe := *cfg
	probe.Collation = ""
	probe.Logger = gatewayMySQLQuietLogger{}
	if probe.Timeout == 0 {
		probe.Timeout = timeouts.MySQLConnectTimeout
	}
	connector, err := mysql.NewConnector(&probe)
	if err != nil {
		return "", "", "", errors.New("mysql_connector_failed")
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	probeCtx, cancel := context.WithTimeout(ctx, mysqlPingTimeout())
	defer cancel()
	var charset, collation, sqlMode string
	if err := db.QueryRowContext(probeCtx, "SELECT @@character_set_database, @@collation_database, @@sql_mode").Scan(&charset, &collation, &sqlMode); err != nil {
		return "", "", "", gatewayMySQLConnectionError(err)
	}
	return charset, collation, sqlMode, nil
}

// gatewayMySQLDatabaseCharset reads the default character set and collation of
// the target database.
func gatewayMySQLDatabaseCharset(ctx context.Context, cfg *mysql.Config) (string, string, error) {
	charset, collation, _, err := gatewayMySQLDatabaseProbe(ctx, cfg)
	return charset, collation, err
}

// gatewayMySQLAssertTableCollations fails closed when a table does not use the
// database default collation. The session collation is aligned with that
// default (gatewayMySQLDatabaseCharset), so a table carrying another collation
// still turns a JSON_TABLE comparison into Error 1267 at run time; naming the
// offending tables at startup is the only place that can be fixed cheaply.
// gatewayMySQLAssertSessionCollation reads the session the pool hands out after
// schema preparation. Table collations and the DSN are asserted before the DDL
// runs, so this is the only check that catches a session changed by an
// installation statement; without it the pre-2026-11-24 SET NAMES could return
// to the pool and fail later, at whichever JSON_TABLE comparison came next.
func gatewayMySQLAssertSessionCollation(ctx context.Context, db *sql.DB, collation string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return gatewayMySQLConnectionError(err)
	}
	defer conn.Close()
	var got string
	if err := conn.QueryRowContext(ctx, "SELECT @@collation_connection").Scan(&got); err != nil {
		return gatewayMySQLConnectionError(err)
	}
	if got != collation {
		// go-sql-driver falls back to utf8mb4_general_ci for any collation it does
		// not know (utf8mb4_0900_bin is one), so a database whose default collation
		// the driver cannot set is unusable rather than merely misconfigured.
		return fmt.Errorf("gateway_mysql_collation_mismatch: post-schema session collation is %s but the database default is %s; go-sql-driver falls back to utf8mb4_general_ci for a collation it does not know, so the database cannot host the Gateway schema - use the MySQL 8 default utf8mb4_0900_ai_ci; JSON_TABLE comparisons take the session collation and would fail with Error 1267", got, collation)
	}
	return nil
}

// gatewayMySQLAssertColumnCollations covers the granularity a table cannot show:
// ALTER TABLE ... MODIFY <col> ... COLLATE foreign leaves table_collation at the
// database default, so the table-level check passes while every comparison on
// that one column raises Error 1267.
func gatewayMySQLAssertColumnCollations(ctx context.Context, db *sql.DB, collation string) error {
	rows, err := db.QueryContext(ctx, "SELECT table_name, column_name, collation_name FROM information_schema.columns WHERE table_schema = DATABASE() AND collation_name IS NOT NULL AND collation_name <> '' ORDER BY table_name, ordinal_position")
	if err != nil {
		return gatewayMySQLConnectionError(err)
	}
	defer rows.Close()
	var offenders []string
	for rows.Next() {
		var table, column, got string
		if err := rows.Scan(&table, &column, &got); err != nil {
			return gatewayMySQLConnectionError(err)
		}
		if got != collation {
			offenders = append(offenders, table+"."+column+"="+got)
		}
	}
	if err := rows.Err(); err != nil {
		return gatewayMySQLConnectionError(err)
	}
	if len(offenders) > 0 {
		return fmt.Errorf("gateway_mysql_collation_mismatch: columns %s do not use the database collation %s; comparisons against JSON_TABLE derived columns would fail with Error 1267", strings.Join(offenders, ", "), collation)
	}
	return nil
}

func gatewayMySQLAssertTableCollations(ctx context.Context, db *sql.DB, collation string) error {
	rows, err := db.QueryContext(ctx, "SELECT table_name, table_collation FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_name")
	if err != nil {
		return gatewayMySQLConnectionError(err)
	}
	defer rows.Close()
	var offenders []string
	for rows.Next() {
		var name, got string
		if err := rows.Scan(&name, &got); err != nil {
			return gatewayMySQLConnectionError(err)
		}
		if got != collation {
			offenders = append(offenders, name+"="+got)
		}
	}
	if err := rows.Err(); err != nil {
		return gatewayMySQLConnectionError(err)
	}
	if len(offenders) > 0 {
		return fmt.Errorf("gateway_mysql_collation_mismatch: tables %s do not use the database collation %s; JSON_TABLE comparisons take the session collation and would fail with Error 1267", strings.Join(offenders, ", "), collation)
	}
	return nil
}
