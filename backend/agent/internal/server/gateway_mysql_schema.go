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
	errGatewayMySQLSchemaNotEmpty  = errors.New("mysql_schema_not_empty")
	errGatewayMySQLSchemaStatement = errors.New("mysql_schema_statement_failed")
	errGatewayMySQLSchemaInvalid   = errors.New("mysql_schema_invalid")
	errGatewayMySQLSchemaVerify    = errors.New("mysql_schema_verify_failed")

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
	statements, err := gatewayMySQLStatements(gatewayMySQLSchemaSQL)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.New("mysql_schema_connect_failed")
	}
	defer conn.Close()
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
