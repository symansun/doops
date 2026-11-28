package server

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The native MySQL 8.4 schema for the Gateway store. It is the explicit,
// reviewable conversion of the production SQLite export (58 tables, 48 named
// indexes, 12 triggers, 38 foreign keys). Install it only into an EMPTY
// schema: the installer fails closed when any user table already exists, so a
// typo in the target database name cannot mutate an unrelated database. The
// offline copier (gateway_mysql_copy.go) then validates the installed schema
// column-by-column against the SQLite source before it moves any row. The
// installed database therefore contains exactly the application tables — any
// extra table would make the copier fail closed (mysql_copy_schema_mismatch),
// which is the version/integrity gate: a destination installed from anything
// other than this exact DDL cannot be copied into.
//
//go:embed gateway_mysql_schema.sql
var gatewayMySQLSchemaSQL string

// gatewayMySQLSchemaVersion identifies this conversion and is recorded in the
// install report. The hard integrity gate is the copier itself: it refuses
// any destination whose objects differ from the SQLite source.
const gatewayMySQLSchemaVersion = "gateway-mysql-schema-1"

var (
	errGatewayMySQLSchemaNotEmpty    = errors.New("mysql_schema_not_empty")
	errGatewayMySQLSchemaHashWide    = errors.New("mysql_schema_hash_width_failed")
	errGatewayMySQLSchemaStatement   = errors.New("mysql_schema_statement_failed")
	errGatewayMySQLSchemaInvalid     = errors.New("mysql_schema_invalid")
	errGatewayMySQLSchemaVerify      = errors.New("mysql_schema_verify_failed")
	errGatewayMySQLSchemaHashInspect = errors.New("mysql_schema_hash_width_inspect_failed")
	// INT-91: the runtime column upgrade for execution_records.
	errGatewayMySQLExecutionRecordInspect = errors.New("mysql_execution_records_inspect_failed")
	errGatewayMySQLExecutionRecordUpgrade = errors.New("mysql_execution_records_upgrade_failed")
	// INT-108: runtime widening for audit/session/digest/receipt columns that
	// live SQLite deployments exceed at VARCHAR(64) or TEXT (64 KiB).
	errGatewayMySQLMigrationWidthInspect = errors.New("mysql_migration_width_inspect_failed")
	errGatewayMySQLMigrationWidthUpgrade = errors.New("mysql_migration_width_upgrade_failed")
	// AST-01: the runtime column upgrade for inventory_machines.
	errGatewayMySQLInventoryMachineInspect = errors.New("mysql_inventory_machines_inspect_failed")
	errGatewayMySQLInventoryMachineUpgrade = errors.New("mysql_inventory_machines_upgrade_failed")
	// PLG-01: runtime table upgrade for platform_plugin_config and platform_plugin_overrides.
	errGatewayMySQLPluginTableInspect = errors.New("mysql_plugin_tables_inspect_failed")
	errGatewayMySQLPluginTableUpgrade = errors.New("mysql_plugin_tables_upgrade_failed")
	// PLG-02: runtime column upgrade for platform_skills UI manifests and monitor_schedules inputs.
	errGatewayMySQLPluginPhase2Inspect = errors.New("mysql_plugin_phase2_inspect_failed")
	errGatewayMySQLPluginPhase2Upgrade = errors.New("mysql_plugin_phase2_upgrade_failed")

	gatewayMySQLIdentPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// gatewayMySQLSchemaReport summarizes one installation. It contains counts
// and names only — never data, never credentials.
type gatewayMySQLSchemaReport struct {
	Version    string
	Statements int
	Tables     []string
	Triggers   []string
}

// gatewayMySQLStatements splits the embedded DDL into statements. The file
// uses DELIMITER ;; only so trigger bodies can contain ';'; everything else
// ends at ';'. Comments and blank lines are stripped, so the result is the
// exact byte sequence the server executes, in file order.
func gatewayMySQLStatements(sqlText string) ([]string, error) {
	var statements []string
	var current strings.Builder
	delimiter := ";"
	flush := func() error {
		stmt := strings.TrimSpace(current.String())
		current.Reset()
		if stmt != "" {
			statements = append(statements, stmt)
		}
		return nil
	}
	for _, raw := range strings.Split(sqlText, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		if strings.HasPrefix(line, "DELIMITER ") {
			if err := flush(); err != nil {
				return nil, err
			}
			delimiter = strings.TrimSpace(strings.TrimPrefix(line, "DELIMITER "))
			if delimiter == "" {
				return nil, errGatewayMySQLSchemaInvalid
			}
			continue
		}
		// A line is complete when it ends with the current delimiter. Trigger
		// bodies are single lines ending in ";" so they never terminate a
		// statement early: the active delimiter is then ";;".
		current.WriteString(line)
		current.WriteByte('\n')
		trimmed := strings.TrimSpace(current.String())
		if strings.HasSuffix(trimmed, delimiter) && delimiter != "" {
			// Do not cut a CREATE TRIGGER at a body-internal ";" — that is
			// impossible here because the delimiter switches before triggers.
			stmt := strings.TrimSuffix(trimmed, delimiter)
			current.Reset()
			current.WriteString(stmt)
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(statements) == 0 {
		return nil, errGatewayMySQLSchemaInvalid
	}
	return statements, nil
}

// installGatewayMySQLSchema applies the embedded DDL to an empty MySQL schema
// and verifies the installed object set against information_schema. It is
// fail-closed: any existing table, any statement error, or any mismatch
// between the expected and installed object inventory aborts the install.
// DDL is not transactional in MySQL; a failed install leaves the partial
// objects behind and the error tells the operator to drop the schema. The
// copier independently refuses such a database (mysql_copy_schema_mismatch /
// mysql_copy_destination_stale), so a half-installed schema can never be
// copied into.
func installGatewayMySQLSchema(ctx context.Context, db *sql.DB) (*gatewayMySQLSchemaReport, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.New("mysql_schema_connect_failed")
	}
	defer conn.Close()
	serverVersion, err := gatewayMySQLQueryVersion(ctx, conn)
	if err != nil {
		return nil, err
	}
	ddlText, err := gatewayMySQLSchemaSQLForVersion(serverVersion)
	if err != nil {
		return nil, err
	}
	statements, err := gatewayMySQLStatements(ddlText)
	if err != nil {
		return nil, err
	}
	// Fail closed if the schema is not empty. Counting user tables (excluding
	// the metadata table created below) keeps the check deterministic.
	var existing int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'`).Scan(&existing); err != nil {
		return nil, errors.New("mysql_schema_inspect_failed")
	}
	if existing != 0 {
		return nil, errGatewayMySQLSchemaNotEmpty
	}
	if _, err := conn.ExecContext(ctx, `SET SESSION sql_mode =
		'STRICT_ALL_TABLES,NO_AUTO_VALUE_ON_ZERO,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'`); err != nil {
		return nil, errors.New("mysql_schema_session_failed")
	}
	report := &gatewayMySQLSchemaReport{Version: gatewayMySQLSchemaVersion}
	for i, stmt := range statements {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			// Do not echo the failing DDL or server text beyond a position;
			// the driver can include values in error messages.
			return nil, fmt.Errorf("%w at statement %d", errGatewayMySQLSchemaStatement, i)
		}
		report.Statements++
	}
	if err := gatewayMySQLVerifyInstalled(ctx, conn, report); err != nil {
		return nil, err
	}
	return report, nil
}

// gatewayMySQLExpectedObjects parses the embedded DDL for the object names it
// must create. Names are restricted to a conservative identifier pattern so a
// hand-edited SQL file cannot smuggle a weird name past verification.
func gatewayMySQLExpectedObjects() (tables, triggers map[string]bool, err error) {
	tables = map[string]bool{}
	triggers = map[string]bool{}
	statements, err := gatewayMySQLStatements(gatewayMySQLSchemaSQL)
	if err != nil {
		return nil, nil, err
	}
	tableRe := regexp.MustCompile(`(?i)^CREATE TABLE ([a-z_][a-z0-9_]*) `)
	triggerRe := regexp.MustCompile(`(?i)^CREATE TRIGGER ([a-z_][a-z0-9_]*) `)
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if m := tableRe.FindStringSubmatch(stmt); m != nil {
			name := m[1]
			if !gatewayMySQLIdentPattern.MatchString(name) || tables[name] {
				return nil, nil, errGatewayMySQLSchemaInvalid
			}
			tables[name] = true
			continue
		}
		if m := triggerRe.FindStringSubmatch(stmt); m != nil {
			name := m[1]
			if !gatewayMySQLIdentPattern.MatchString(name) || triggers[name] {
				return nil, nil, errGatewayMySQLSchemaInvalid
			}
			triggers[name] = true
			continue
		}
		upper := strings.ToUpper(stmt)
		if strings.HasPrefix(upper, "SET ") {
			continue
		}
		return nil, nil, errGatewayMySQLSchemaInvalid
	}
	// The installed schema must contain exactly the application tables: the
	// copier (gateway_mysql_copy.go) refuses any destination whose table set
	// differs from the SQLite source, so no installer-owned metadata table can
	// exist. The schema revision travels in the install report instead.
	return tables, triggers, nil
}

// gatewayMySQLSchemaSelfReferences lists the tables in the embedded schema whose
// own CREATE TABLE carries a foreign key that points back at the same table.
// The copier refuses such a schema because row order then needs a data-aware
// migration, so the cutover command must be able to report the refusal before
// it installs anything. A dry run cannot read information_schema for a schema
// that does not exist yet, hence this static scan over the reviewed DDL.
func gatewayMySQLSchemaSelfReferences() ([]string, error) {
	statements, err := gatewayMySQLStatements(gatewayMySQLSchemaSQL)
	if err != nil {
		return nil, err
	}
	tableRe := regexp.MustCompile("(?i)^CREATE TABLE ([a-z_][a-z0-9_]*) ")
	referenceRe := regexp.MustCompile("(?i)REFERENCES\\s+([a-z_][a-z0-9_]*)")
	seen := map[string]bool{}
	var selfReferences []string
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		m := tableRe.FindStringSubmatch(stmt)
		if m == nil {
			continue
		}
		table := m[1]
		for _, ref := range referenceRe.FindAllStringSubmatch(stmt, -1) {
			if ref[1] == table && !seen[table] {
				seen[table] = true
				selfReferences = append(selfReferences, table)
			}
		}
	}
	sort.Strings(selfReferences)
	return selfReferences, nil
}

// gatewayMySQLVerifyInstalled compares the information_schema inventory to the
// expected object set. It checks counts and names only; the copier performs
// the column-level comparison against the SQLite source.
func gatewayMySQLVerifyInstalled(ctx context.Context, conn *sql.Conn, report *gatewayMySQLSchemaReport) error {
	expectedTables, expectedTriggers, err := gatewayMySQLExpectedObjects()
	if err != nil {
		return err
	}
	installedTables := map[string]bool{}
	rows, err := conn.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE' AND ENGINE = 'InnoDB'`)
	if err != nil {
		return errors.New("mysql_schema_inspect_failed")
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return errors.New("mysql_schema_inspect_failed")
		}
		installedTables[name] = true
	}
	if err := rows.Err(); err != nil {
		return errors.New("mysql_schema_inspect_failed")
	}
	if len(installedTables) != len(expectedTables) {
		return errGatewayMySQLSchemaVerify
	}
	for name := range expectedTables {
		if !installedTables[name] {
			return errGatewayMySQLSchemaVerify
		}
		report.Tables = append(report.Tables, name)
	}
	sort.Strings(report.Tables)

	installedTriggers := map[string]bool{}
	trows, err := conn.QueryContext(ctx, `SELECT TRIGGER_NAME FROM information_schema.TRIGGERS
		WHERE TRIGGER_SCHEMA = DATABASE()`)
	if err != nil {
		return errors.New("mysql_schema_inspect_failed")
	}
	defer trows.Close()
	for trows.Next() {
		var name string
		if err := trows.Scan(&name); err != nil {
			return errors.New("mysql_schema_inspect_failed")
		}
		installedTriggers[name] = true
	}
	if err := trows.Err(); err != nil {
		return errors.New("mysql_schema_inspect_failed")
	}
	if len(installedTriggers) != len(expectedTriggers) {
		return errGatewayMySQLSchemaVerify
	}
	for name := range expectedTriggers {
		if !installedTriggers[name] {
			return errGatewayMySQLSchemaVerify
		}
		report.Triggers = append(report.Triggers, name)
	}
	sort.Strings(report.Triggers)
	return nil
}

// gatewayMySQLExecutionRecordColumns are the columns INT-87 added to
// execution_records and INT-91 backfills on an already-installed database. The
// zero defaults are what an old row means: it was written before the marker
// existed, so nothing was truncated and no original size was recorded.
var gatewayMySQLExecutionRecordColumns = []struct {
	name       string
	definition string
}{
	{name: "terminal_truncated", definition: "TINYINT NOT NULL DEFAULT 0"},
	{name: "terminal_bytes", definition: "BIGINT NOT NULL DEFAULT 0"},
	// INT-82b: a row written before this column existed reads back with an empty
	// owner, i.e. "unbound", never "some other process".
	{name: "agent_boot_id", definition: "VARCHAR(64) NOT NULL DEFAULT ''"},
}

// gatewayMySQLEnsureExecutionRecordColumns brings an installed database up to the
// INT-87 read-back contract without touching a fresh one: it reads
// information_schema, adds only the columns that are missing, and returns the
// names it added so a restart can be asserted to add nothing. A database whose
// schema predates execution_records (the table is absent) is left alone -
// installGatewayMySQLSchema creates it with both columns.
func gatewayMySQLEnsureExecutionRecordColumns(ctx context.Context, db *sql.DB) ([]string, error) {
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_records' AND TABLE_TYPE = 'BASE TABLE'`).Scan(&tables); err != nil {
		return nil, fmt.Errorf("%w: %v", errGatewayMySQLExecutionRecordInspect, err)
	}
	if tables == 0 {
		return nil, nil
	}
	added := make([]string, 0, len(gatewayMySQLExecutionRecordColumns))
	for _, column := range gatewayMySQLExecutionRecordColumns {
		var present int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_records' AND COLUMN_NAME = ?`,
			column.name).Scan(&present); err != nil {
			return added, fmt.Errorf("%w: execution_records.%s: %v", errGatewayMySQLExecutionRecordInspect, column.name, err)
		}
		if present > 0 {
			continue
		}
		// The table and column names come from the fixed list above; the
		// definition is a compile-time constant.
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s",
			gatewayMySQLIdentifier("execution_records"), gatewayMySQLIdentifier(column.name), column.definition)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return added, fmt.Errorf("%w: execution_records.%s: %v", errGatewayMySQLExecutionRecordUpgrade, column.name, err)
		}
		added = append(added, column.name)
	}
	return added, nil
}

// gatewayMySQLInventoryMachineColumns are columns added to inventory_machines
// after the baseline schema that already-installed MySQL databases backfill in
// place on startup without altering fresh installs.
var gatewayMySQLInventoryMachineColumns = []struct {
	name       string
	definition string
}{
	{name: "created_at", definition: "VARCHAR(64) NOT NULL DEFAULT ''"},
}

func gatewayMySQLEnsureInventoryMachineColumns(ctx context.Context, db *sql.DB) ([]string, error) {
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'inventory_machines' AND TABLE_TYPE = 'BASE TABLE'`).Scan(&tables); err != nil {
		return nil, fmt.Errorf("%w: %v", errGatewayMySQLInventoryMachineInspect, err)
	}
	if tables == 0 {
		return nil, nil
	}
	added := make([]string, 0, len(gatewayMySQLInventoryMachineColumns))
	for _, column := range gatewayMySQLInventoryMachineColumns {
		var present int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'inventory_machines' AND COLUMN_NAME = ?`,
			column.name).Scan(&present); err != nil {
			return added, fmt.Errorf("%w: inventory_machines.%s: %v", errGatewayMySQLInventoryMachineInspect, column.name, err)
		}
		if present > 0 {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s",
			gatewayMySQLIdentifier("inventory_machines"), gatewayMySQLIdentifier(column.name), column.definition)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return added, fmt.Errorf("%w: inventory_machines.%s: %v", errGatewayMySQLInventoryMachineUpgrade, column.name, err)
		}
		added = append(added, column.name)
	}
	return added, nil
}

// gatewayMySQLEnsurePluginTables creates platform_plugin_config and
// platform_plugin_overrides on already-installed MySQL databases that predate
// PLG-01. Fresh installs create both tables directly from gateway_mysql_schema.sql.
func gatewayMySQLEnsurePluginTables(ctx context.Context, db *sql.DB) ([]string, error) {
	specs := []struct {
		table string
		ddl   string
	}{
		{
			table: "platform_plugin_config",
			ddl: `CREATE TABLE IF NOT EXISTS platform_plugin_config (
    id VARCHAR(16) PRIMARY KEY,
    scenario_id VARCHAR(64) NOT NULL DEFAULT 'all-in-one',
    revision BIGINT NOT NULL DEFAULT 0,
    updated_by VARCHAR(191) NOT NULL DEFAULT '',
    updated_at_ms BIGINT NOT NULL DEFAULT 0,
    CHECK (id = 'default')
) ENGINE=InnoDB`,
		},
		{
			table: "platform_plugin_overrides",
			ddl: `CREATE TABLE IF NOT EXISTS platform_plugin_overrides (
    plugin_id VARCHAR(128) PRIMARY KEY,
    enabled BIGINT NOT NULL DEFAULT 1,
    updated_by VARCHAR(191) NOT NULL DEFAULT '',
    updated_at_ms BIGINT NOT NULL DEFAULT 0,
    CHECK (enabled IN (0, 1))
) ENGINE=InnoDB`,
		},
	}
	created := make([]string, 0, len(specs))
	for _, spec := range specs {
		var present int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE'`,
			spec.table).Scan(&present); err != nil {
			return created, fmt.Errorf("%w: %s: %v", errGatewayMySQLPluginTableInspect, spec.table, err)
		}
		if present > 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, spec.ddl); err != nil {
			return created, fmt.Errorf("%w: %s: %v", errGatewayMySQLPluginTableUpgrade, spec.table, err)
		}
		created = append(created, spec.table)
	}
	return created, nil
}

// gatewayMySQLEnsurePluginPhase2Columns adds the PLG-02 declarative UI manifest
// and schedule input columns on already-installed MySQL databases without
// touching fresh installs (which create them directly from gateway_mysql_schema.sql).
func gatewayMySQLEnsurePluginPhase2Columns(ctx context.Context, db *sql.DB) ([]string, error) {
	specs := []struct {
		table      string
		column     string
		definition string
	}{
		{table: "platform_skills", column: "ui_manifest_json", definition: "MEDIUMTEXT NOT NULL"},
		{table: "platform_skills", column: "ui_manifest_error", definition: "MEDIUMTEXT NOT NULL"},
		{table: "monitor_schedules", column: "inputs_json", definition: "MEDIUMTEXT NOT NULL"},
	}
	added := make([]string, 0, len(specs))
	for _, spec := range specs {
		var tablePresent int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE'`,
			spec.table).Scan(&tablePresent); err != nil {
			return added, fmt.Errorf("%w: %s: %v", errGatewayMySQLPluginPhase2Inspect, spec.table, err)
		}
		if tablePresent == 0 {
			continue
		}
		var present int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			spec.table, spec.column).Scan(&present); err != nil {
			return added, fmt.Errorf("%w: %s.%s: %v", errGatewayMySQLPluginPhase2Inspect, spec.table, spec.column, err)
		}
		if present > 0 {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s",
			gatewayMySQLIdentifier(spec.table), gatewayMySQLIdentifier(spec.column), spec.definition)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return added, fmt.Errorf("%w: %s.%s: %v", errGatewayMySQLPluginPhase2Upgrade, spec.table, spec.column, err)
		}
		added = append(added, spec.table+"."+spec.column)
	}
	return added, nil
}

// gatewayMySQLHashColumnWidth is the declared width of every issued-token hash
// column in gateway_mysql_schema.sql: tokens.hash, agent_enrollments.hash and
// edge_enrollments.hash. The stored form is tokenFastHashPrefix plus a hex
// digest (tokenFastHashMaxLength = 71), and the pre-INT-64 bcrypt form needed
// only 64; 191 is what the schema's other hash columns already use, is
// utf8mb4 index-safe if a hash ever gains an index, and leaves room for a
// longer digest generation without a second widen. The guard test fails if this
// ever drops below tokenFastHashMaxLength or disagrees with the DDL.
const gatewayMySQLHashColumnWidth = 191

type gatewayMySQLHashColumn struct{ table, column string }

// gatewayMySQLHashColumns is every column the runtime writes tokenFastHash
// into. installGatewayMySQLSchema creates them at gatewayMySQLHashColumnWidth
// and gatewayMySQLWidenHashColumns brings an older install up to it.
var gatewayMySQLHashColumns = []gatewayMySQLHashColumn{
	{table: "tokens", column: "hash"},
	{table: "agent_enrollments", column: "hash"},
	{table: "edge_enrollments", column: "hash"},
}

// gatewayMySQLWidenHashColumns makes an already-installed schema accept the
// hash form the code writes today. Before INT-74 the three digest columns were
// VARCHAR(64) while the code writes 71 characters, so every CreateToken and
// every enrollment issuance failed with MySQL error 1406 inside strict mode.
// Changing only the embedded DDL would fix fresh installs and leave every
// existing database broken, so the runtime upgrade reads the installed width
// from information_schema and issues MODIFY COLUMN only when it is narrower.
// Widening is a metadata-only change in MySQL 8.4 and repeating it is a no-op,
// which is what makes running this on every startup safe; rows are untouched
// and a legacy bcrypt row keeps working until its next successful verification
// rewrites it in the fast form.
func gatewayMySQLWidenHashColumns(ctx context.Context, db *sql.DB) error {
	if gatewayMySQLHashColumnWidth < tokenFastHashMaxLength {
		return errGatewayMySQLSchemaHashWide
	}
	for _, target := range gatewayMySQLHashColumns {
		var declared sql.NullInt64
		err := db.QueryRowContext(ctx, `SELECT CHARACTER_MAXIMUM_LENGTH FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			target.table, target.column).Scan(&declared)
		if err != nil || !declared.Valid {
			return fmt.Errorf("%w: %s.%s", errGatewayMySQLSchemaHashInspect, target.table, target.column)
		}
		if declared.Int64 >= int64(gatewayMySQLHashColumnWidth) {
			continue
		}
		// Identifiers come from the fixed list above, never from input; the
		// width is a compile-time constant.
		statement := fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s VARCHAR(%d) NOT NULL",
			gatewayMySQLIdentifier(target.table), gatewayMySQLIdentifier(target.column), gatewayMySQLHashColumnWidth)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("%w: %s.%s", errGatewayMySQLSchemaHashWide, target.table, target.column)
		}
	}
	return nil
}

// gatewayMySQLMigrationWideColumn describes one column that INT-108 widened in
// gateway_mysql_schema.sql after production SQLite audits showed real values
// exceeding VARCHAR(64) (audit_events.session up to 104 chars, sha256: digests
// at 71 chars) or approaching MySQL's 64 KiB TEXT cap (receipt at 45 KiB).
type gatewayMySQLMigrationWideColumn struct {
	table      string
	column     string
	minChars   int64
	definition string
}

const (
	gatewayMySQLWideVarcharWidth = 191
	gatewayMySQLMediumTextChars  = 16777215
)

var gatewayMySQLMigrationWideColumns = []gatewayMySQLMigrationWideColumn{
	{table: "audit_events", column: "session", minChars: gatewayMySQLWideVarcharWidth, definition: "VARCHAR(191) NOT NULL DEFAULT ''"},
	{table: "audit_events", column: "digest", minChars: gatewayMySQLWideVarcharWidth, definition: "VARCHAR(191) NOT NULL DEFAULT ''"},
	{table: "audit_events", column: "correlation_id", minChars: gatewayMySQLWideVarcharWidth, definition: "VARCHAR(191) NOT NULL DEFAULT ''"},
	{table: "detached_tasks", column: "session", minChars: gatewayMySQLWideVarcharWidth, definition: "VARCHAR(191) NOT NULL"},
	{table: "release_tickets", column: "plan_digest", minChars: gatewayMySQLWideVarcharWidth, definition: "VARCHAR(191) NOT NULL DEFAULT ''"},
	{table: "inspection_receipts", column: "digest", minChars: gatewayMySQLWideVarcharWidth, definition: "VARCHAR(191) NOT NULL"},
	{table: "inventory_import_attempts", column: "receipt", minChars: gatewayMySQLMediumTextChars, definition: "MEDIUMTEXT NOT NULL"},
}

// gatewayMySQLWidenMigrationColumns upgrades already-installed MySQL schemas to
// the INT-108 column widths without touching fresh databases or rewriting rows
// when a column is already wide enough.
func gatewayMySQLWidenMigrationColumns(ctx context.Context, db *sql.DB) ([]string, error) {
	widened := make([]string, 0, len(gatewayMySQLMigrationWideColumns))
	for _, target := range gatewayMySQLMigrationWideColumns {
		var dataType string
		var declaredChars, declaredOctets sql.NullInt64
		err := db.QueryRowContext(ctx, `SELECT DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, CHARACTER_OCTET_LENGTH FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			target.table, target.column).Scan(&dataType, &declaredChars, &declaredOctets)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil || !declaredChars.Valid {
			return widened, fmt.Errorf("%w: %s.%s", errGatewayMySQLMigrationWidthInspect, target.table, target.column)
		}
		if target.minChars == gatewayMySQLMediumTextChars {
			if strings.EqualFold(dataType, "mediumtext") || strings.EqualFold(dataType, "longtext") || (declaredOctets.Valid && declaredOctets.Int64 >= target.minChars) {
				continue
			}
		} else if declaredChars.Int64 >= target.minChars {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s %s",
			gatewayMySQLIdentifier(target.table), gatewayMySQLIdentifier(target.column), target.definition)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return widened, fmt.Errorf("%w: %s.%s", errGatewayMySQLMigrationWidthUpgrade, target.table, target.column)
		}
		widened = append(widened, target.table+"."+target.column)
	}
	return widened, nil
}
