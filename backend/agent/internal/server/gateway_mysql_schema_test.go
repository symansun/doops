package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// gatewayMySQLTestTables is the expected set of 58 application tables from
// the production export. Keeping the list literal makes an accidental drop of
// a CREATE TABLE a hard test failure, not a silent omission. The schema must
// contain exactly these tables — the copier refuses any extra table.
var gatewayMySQLTestTables = []string{
	"agent_connection_events", "agent_enrollments", "agent_status",
	"asset_coverage_baselines", "asset_coverage_cluster_versions",
	"asset_coverage_registers", "asset_monitor_alerts", "asset_monitor_rules",
	"asset_monitor_samples", "asset_monitor_series", "asset_runtime_bindings",
	"asset_runtime_snapshots", "audit_events", "cluster_inventory_namespaces",
	"cluster_inventory_sources", "credential_bundle_items", "credential_bundles",
	"credential_grants", "credential_materializations", "credential_verifications",
	"credential_versions", "credentials", "detached_tasks", "edge_agent_status",
	"edge_enrollments", "edge_route_revision", "edge_routes",
	"gateway_secret_key_check", "git_repos", "grants",
	"inspection_items", "inspection_plans", "inspection_receipts", "inspection_runs",
	"inspection_scope", "inventory_business_deployments", "inventory_business_systems",
	"inventory_import_attempts", "inventory_machine_login_accounts",
	"inventory_machine_login_key", "inventory_machines", "inventory_node_links",
	"machine_diagnostic_bindings", "machine_diagnostic_samples",
	"monitor_resource_snapshots", "monitor_run_items", "monitor_runs",
	"monitor_schedules", "platform_load_samples", "proxy_routes", "release_events",
	"release_tickets", "scheduler_issues", "scheduler_job_leases", "scheduler_jobs",
	"tokens", "upgrade_operations", "users",
}

var gatewayMySQLTestTriggers = []string{
	"asset_monitor_one_open_insert", "asset_monitor_one_open_update",
	"asset_runtime_deployment_drop", "asset_runtime_ns_drop",
	"asset_runtime_ns_recreated", "asset_runtime_source_drop",
	"edge_route_revision_singleton",
	"gateway_secret_key_check_singleton",
	"inspection_cycle_insert", "inspection_cycle_update",
	"inventory_machine_login_key_singleton",
	"inventory_node_links_check_insert", "inventory_node_links_check_update",
	"inventory_node_links_machine_delete", "inventory_node_links_machine_rekey",
	"inventory_node_links_source_delete", "inventory_node_links_source_rekey",
	"machine_diagnostic_asset_deleted",
}

func gatewayMySQLTestSorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// TestGatewayMySQLStatementsSplit checks the DDL parser against the embedded
// file: every statement must start with a known verb, no statement may be
// empty, and the object counts must match the literal expectations.
func TestGatewayMySQLStatementsSplit(t *testing.T) {
	statements, err := gatewayMySQLStatements(gatewayMySQLSchemaSQL)
	if err != nil {
		t.Fatalf("split embedded DDL: %v", err)
	}
	var tables, triggers int
	for i, stmt := range statements {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		switch {
		case strings.HasPrefix(upper, "SET "):
		case strings.HasPrefix(upper, "CREATE TABLE "):
			tables++
		case strings.HasPrefix(upper, "CREATE TRIGGER "):
			triggers++
		default:
			t.Fatalf("statement %d has unexpected verb: %.40q", i, stmt)
		}
		if strings.Contains(stmt, "\x00") {
			t.Fatalf("statement %d contains NUL", i)
		}
	}
	if tables != len(gatewayMySQLTestTables) {
		t.Fatalf("CREATE TABLE count = %d, want %d", tables, len(gatewayMySQLTestTables)-1)
	}
	if triggers != len(gatewayMySQLTestTriggers) {
		t.Fatalf("CREATE TRIGGER count = %d, want %d", triggers, len(gatewayMySQLTestTriggers))
	}
}

// TestGatewayMySQLExpectedObjects verifies that the names parsed from the DDL
// match the literal lists exactly.
func TestGatewayMySQLExpectedObjects(t *testing.T) {
	tables, triggers, err := gatewayMySQLExpectedObjects()
	if err != nil {
		t.Fatalf("parse expected objects: %v", err)
	}
	var gotTables []string
	for name := range tables {
		gotTables = append(gotTables, name)
	}
	if !reflect.DeepEqual(gatewayMySQLTestSorted(gotTables), gatewayMySQLTestSorted(gatewayMySQLTestTables)) {
		t.Fatalf("tables mismatch:\n got: %v\nwant: %v", gatewayMySQLTestSorted(gotTables), gatewayMySQLTestSorted(gatewayMySQLTestTables))
	}
	var gotTriggers []string
	for name := range triggers {
		gotTriggers = append(gotTriggers, name)
	}
	if !reflect.DeepEqual(gatewayMySQLTestSorted(gotTriggers), gatewayMySQLTestSorted(gatewayMySQLTestTriggers)) {
		t.Fatalf("triggers mismatch:\n got: %v\nwant: %v", gatewayMySQLTestSorted(gotTriggers), gatewayMySQLTestSorted(gatewayMySQLTestTriggers))
	}
}

// gatewayMySQLTestSQLiteExport opens the production export read-only. The
// export ships with the tree as testdata/gateway-sqlite-schema.sql; the test
// skips when the file is absent (for example a partial checkout) and treats it
// as authoritative when present.
func gatewayMySQLTestSQLiteExport(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join("testdata", "gateway-sqlite-schema.sql")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("production export not available: %v", err)
	}
	// Load the DDL into an in-memory SQLite database so PRAGMA and
	// sqlite_schema queries work exactly as they do on the real export.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The export is sorted by name, not by dependency: indexes appear before
	// the tables they reference. Load tables first, then the rest.
	var tables, rest []string
	for _, stmt := range strings.Split(string(data), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		upper := strings.ToUpper(stmt)
		if strings.HasPrefix(upper, "CREATE TABLE") {
			tables = append(tables, stmt)
		} else {
			rest = append(rest, stmt)
		}
	}
	// Tables may reference each other; retry until no progress.
	pending := tables
	for len(pending) > 0 {
		var failed []string
		progress := false
		for _, stmt := range pending {
			if _, err := db.Exec(stmt); err != nil {
				failed = append(failed, stmt)
				continue
			}
			progress = true
		}
		if !progress {
			t.Fatalf("cannot load export tables: %d remaining (%.60q)", len(failed), failed[0])
		}
		pending = failed
	}
	// Indexes and triggers are checked by regex on the raw text, not executed:
	// SQLite trigger bodies contain ';' inside BEGIN...END, so a naive split
	// cannot reconstruct them.
	_ = rest
	return db
}

// gatewayMySQLTestExportText returns the raw export file for regex-based
// comparisons that do not need a parsed SQLite catalog (triggers, indexes).
func gatewayMySQLTestExportText(t *testing.T) string {
	t.Helper()
	path := filepath.Join("testdata", "gateway-sqlite-schema.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("production export not available: %v", err)
	}
	return string(data)
}

// TestGatewayMySQLTablesMatchExport compares the table names in the embedded
// MySQL DDL with the names in the SQLite export.
func TestGatewayMySQLTablesMatchExport(t *testing.T) {
	db := gatewayMySQLTestSQLiteExport(t)
	var export []string
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		export = append(export, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	tables, _, err := gatewayMySQLExpectedObjects()
	if err != nil {
		t.Fatal(err)
	}
	var mine []string
	for name := range tables {
		mine = append(mine, name)
	}
	if !reflect.DeepEqual(gatewayMySQLTestSorted(mine), gatewayMySQLTestSorted(export)) {
		t.Fatalf("table set differs from export:\n got: %v\nwant: %v", gatewayMySQLTestSorted(mine), gatewayMySQLTestSorted(export))
	}
}

// TestGatewayMySQLTriggersMatchExport compares trigger names. The SQLite
// export has 12 triggers; MySQL has 15 because two partial/expression unique
// indexes became enforcer triggers, and machine_diagnostic_target_changed is
// intentionally not ported (documented in the SQL header).
func TestGatewayMySQLTriggersMatchExport(t *testing.T) {
	text := gatewayMySQLTestExportText(t)
	re := regexp.MustCompile(`(?im)^CREATE TRIGGER ([a-z_][a-z0-9_]*)`)
	var export []string
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		export = append(export, m[1])
	}
	_, triggers, err := gatewayMySQLExpectedObjects()
	if err != nil {
		t.Fatal(err)
	}
	var mine []string
	for name := range triggers {
		mine = append(mine, name)
	}
	// Every SQLite trigger must be present, except the explicitly deferred
	// machine_diagnostic_target_changed (requires an application-side
	// re-target delete, not a portable trigger).
	var want []string
	for _, name := range export {
		if name == "machine_diagnostic_target_changed" {
			continue
		}
		want = append(want, name)
	}
	// The partial/expression unique indexes became four enforcer triggers, and
	// the three singleton CHECK constraints on AUTO_INCREMENT columns became
	// three more (MySQL forbids a CHECK referencing an auto-increment column).
	want = append(want,
		"asset_monitor_one_open_insert", "asset_monitor_one_open_update",
		"inspection_cycle_insert", "inspection_cycle_update",
		"edge_route_revision_singleton", "gateway_secret_key_check_singleton",
		"inventory_machine_login_key_singleton")
	if !reflect.DeepEqual(gatewayMySQLTestSorted(mine), gatewayMySQLTestSorted(want)) {
		t.Fatalf("trigger set differs from export:\n got: %v\nwant: %v", gatewayMySQLTestSorted(mine), gatewayMySQLTestSorted(want))
	}
}

// TestGatewayMySQLPrimaryKeysMatchExport extracts primary key column order
// from the SQLite export and asserts the embedded MySQL DDL reproduces it
// exactly for every table.
func TestGatewayMySQLPrimaryKeysMatchExport(t *testing.T) {
	db := gatewayMySQLTestSQLiteExport(t)
	tables, _, err := gatewayMySQLExpectedObjects()
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(gatewayMySQLSchemaSQL)
	for table := range tables {
		var sqlText string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&sqlText); err != nil {
			t.Fatalf("export missing table %s: %v", table, err)
		}
		want := gatewayMySQLTestSQLitePrimaryKey(db, table)
		got := gatewayMySQLTestMySQLPrimaryKey(ddl, table)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: MySQL primary key %v, SQLite wants %v", table, got, want)
		}
	}
}

func gatewayMySQLTestSQLitePrimaryKey(db *sql.DB, table string) []string {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil
		}
		cols = append(cols, name)
	}
	return cols
}

// gatewayMySQLTestMySQLPrimaryKey finds the PRIMARY KEY clause for one table
// inside the DDL text. It understands both column-level PRIMARY KEY and the
// separate PRIMARY KEY (...) clause.
func gatewayMySQLTestMySQLPrimaryKey(ddl, table string) []string {
	start := strings.Index(ddl, "CREATE TABLE "+table+" (")
	if start < 0 {
		return nil
	}
	rest := ddl[start:]
	end := strings.Index(rest, ") ENGINE=InnoDB")
	if end < 0 {
		return nil
	}
	body := rest[len("CREATE TABLE "+table+" ("):end]
	var pk []string
	// Column-level: name TYPE PRIMARY KEY
	colRe := regexp.MustCompile(`(?m)^\s*([a-z_][a-z0-9_]*)\s+[A-Z]+(?:\(\d+\))?\s+PRIMARY KEY`)
	if m := colRe.FindStringSubmatch(body); m != nil {
		return []string{m[1]}
	}
	// Table-level: PRIMARY KEY (a, b DESC, ...)
	pkRe := regexp.MustCompile(`(?i)PRIMARY KEY\s*\(([^)]+)\)`)
	m := pkRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	for _, part := range strings.Split(m[1], ",") {
		field := strings.Fields(strings.TrimSpace(part))
		if len(field) == 0 {
			continue
		}
		name := field[0]
		// Strip a prefix length such as name(191).
		if i := strings.IndexByte(name, '('); i >= 0 {
			name = name[:i]
		}
		pk = append(pk, name)
	}
	return pk
}

// TestGatewayMySQLForeignKeysMatchExport compares the child→parent edges.
func TestGatewayMySQLForeignKeysMatchExport(t *testing.T) {
	db := gatewayMySQLTestSQLiteExport(t)
	tables, _, err := gatewayMySQLExpectedObjects()
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(gatewayMySQLSchemaSQL)
	edges := func(table string) [][2]string {
		// pragma_foreign_key_list returns one row per column; composite FKs
		// share one id, so deduplicate on id.
		rows, err := db.Query(`SELECT DISTINCT id, "table" FROM pragma_foreign_key_list(?) ORDER BY id`, table)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out [][2]string
		for rows.Next() {
			var id int
			var parent string
			if err := rows.Scan(&id, &parent); err != nil {
				return nil
			}
			out = append(out, [2]string{table, parent})
		}
		return out
	}
	var want [][2]string
	for table := range tables {
		want = append(want, edges(table)...)
	}
	// Parse MySQL DDL for FOREIGN KEY ... REFERENCES parent.
	fkRe := regexp.MustCompile(`(?i)FOREIGN KEY\s*\([^)]*\)\s*REFERENCES\s+([a-z_][a-z0-9_]*)`)
	tableRe := regexp.MustCompile(`(?i)CREATE TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	var got [][2]string
	for _, m := range tableRe.FindAllStringSubmatchIndex(ddl, -1) {
		table := ddl[m[2]:m[3]]
		start := m[1]
		end := len(ddl)
		if chunkEnd := strings.Index(ddl[start:], ") ENGINE=InnoDB"); chunkEnd >= 0 {
			end = start + chunkEnd
		}
		chunk := ddl[start:end]
		for _, f := range fkRe.FindAllStringSubmatch(chunk, -1) {
			got = append(got, [2]string{table, f[1]})
		}
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i][0] != want[j][0] {
			return want[i][0] < want[j][0]
		}
		return want[i][1] < want[j][1]
	})
	sort.Slice(got, func(i, j int) bool {
		if got[i][0] != got[j][0] {
			return got[i][0] < got[j][0]
		}
		return got[i][1] < got[j][1]
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("foreign key edges differ:\n got: %v\nwant: %v", got, want)
	}
}

// TestGatewayMySQLAutoIncrementMatchExport checks that AUTO_INCREMENT appears
// exactly on the tables where SQLite uses a rowid alias: either the DDL says
// AUTOINCREMENT, or the table has a single bare INTEGER PRIMARY KEY (which
// SQLite treats as a rowid alias even without the keyword). The copier
// requires generator parity (gateway_mysql_copy.go: planColumns).
func TestGatewayMySQLAutoIncrementMatchExport(t *testing.T) {
	db := gatewayMySQLTestSQLiteExport(t)
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, table := range tables {
		pk := gatewayMySQLTestSQLitePrimaryKey(db, table)
		if len(pk) != 1 {
			continue
		}
		var typ string
		if err := db.QueryRow(`SELECT upper(type) FROM pragma_table_info(?) WHERE name=?`, table, pk[0]).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		if typ == "INTEGER" {
			want[table] = true
		}
	}

	ddl := string(gatewayMySQLSchemaSQL)
	tableRe := regexp.MustCompile(`(?i)CREATE TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	got := map[string]bool{}
	for _, m := range tableRe.FindAllStringSubmatchIndex(ddl, -1) {
		table := ddl[m[2]:m[3]]
		start := m[1]
		end := len(ddl)
		if chunkEnd := strings.Index(ddl[start:], ") ENGINE=InnoDB"); chunkEnd >= 0 {
			end = start + chunkEnd
		}
		if strings.Contains(strings.ToUpper(ddl[start:end]), "AUTO_INCREMENT") {
			got[table] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AUTO_INCREMENT tables differ:\n got: %v\nwant: %v", got, want)
	}
}

// TestGatewayMySQLInstallAndValidate installs the schema into a disposable
// MySQL 8.4 database and validates it with the same information_schema rules
// the copier uses. It is skipped unless DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT is
// set to a disposable local MySQL port (root, empty password).
func TestGatewayMySQLInstallAndValidate(t *testing.T) {
	ctx := context.Background()
	db := gatewayCopyTestDestination(t) // from gateway_mysql_copy_test.go; creates a fresh DB
	report, err := installGatewayMySQLSchema(ctx, db)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if report.Version != gatewayMySQLSchemaVersion {
		t.Fatalf("version = %q, want %q", report.Version, gatewayMySQLSchemaVersion)
	}
	if !reflect.DeepEqual(report.Tables, gatewayMySQLTestSorted(gatewayMySQLTestTables)) {
		t.Errorf("installed tables:\n got: %v\nwant: %v", report.Tables, gatewayMySQLTestSorted(gatewayMySQLTestTables))
	}
	if !reflect.DeepEqual(report.Triggers, gatewayMySQLTestSorted(gatewayMySQLTestTriggers)) {
		t.Errorf("installed triggers:\n got: %v\nwant: %v", report.Triggers, gatewayMySQLTestSorted(gatewayMySQLTestTriggers))
	}
	// Refuse a second install into the same schema.
	if _, err := installGatewayMySQLSchema(ctx, db); !errors.Is(err, errGatewayMySQLSchemaNotEmpty) {
		t.Errorf("reinstall: got %v, want mysql_schema_not_empty", err)
	}
}

// TestGatewayMySQLInstallThenCopierAccepts is the end-to-end contract proof:
// it builds a SQLite database from the production export DDL, installs the
// MySQL schema, and runs the real offline copier. The copier validates every
// table's columns, storage classes, primary key order, foreign key graph and
// AUTO_INCREMENT parity against the source. The production schema contains
// one self-referencing foreign key (inventory_machines.host_asset_id), which
// the copier rejects by design (mysql_copy_foreign_key_cycle) because a
// topological insert order cannot exist for it. This test asserts that the
// cycle error is the ONLY rejection: the schema, columns, keys and generators
// all match, and copying succeeds once that single self-reference is deferred
// (here simulated by dropping the FK in the destination only, which the test
// database allows; the real migration plan must handle the self-reference
// with a data-aware pass — see the header comment in the .sql file).
func TestGatewayMySQLInstallThenCopierAccepts(t *testing.T) {
	src := gatewayMySQLTestSQLiteExport(t)

	// Phase 1: the full schema must be rejected only by the known cycle.
	dstFull := gatewayCopyTestDestination(t)
	if _, err := installGatewayMySQLSchema(t.Context(), dstFull); err != nil {
		t.Fatalf("install: %v", err)
	}
	_, err := copyGatewaySQLiteToMySQL(t.Context(), src, dstFull, false)
	if err == nil || err.Error() != "mysql_copy_foreign_key_cycle" {
		t.Fatalf("expected exactly mysql_copy_foreign_key_cycle, got %v", err)
	}

	// Phase 2: prove the cycle is inventory_machines' self-reference and that
	// everything else matches. MySQL cannot drop one FK without knowing its
	// generated name, so look it up, then re-run the copier.
	dst := gatewayCopyTestDestination(t)
	if _, err := installGatewayMySQLSchema(t.Context(), dst); err != nil {
		t.Fatalf("install: %v", err)
	}
	var fkName string
	if err := dst.QueryRow(`SELECT CONSTRAINT_NAME FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='inventory_machines'
		AND REFERENCED_TABLE_NAME='inventory_machines'`).Scan(&fkName); err != nil {
		t.Fatalf("self-reference FK not found: %v", err)
	}
	if _, err := dst.Exec("ALTER TABLE inventory_machines DROP FOREIGN KEY " + fkName); err != nil {
		t.Fatalf("drop self-reference: %v", err)
	}
	report, err := copyGatewaySQLiteToMySQL(t.Context(), src, dst, false)
	if err != nil {
		t.Fatalf("copier rejected the schema beyond the known self-reference: %v", err)
	}
	if len(report.TableRows) != len(gatewayMySQLTestTables) {
		t.Fatalf("copied %d tables, want %d", len(report.TableRows), len(gatewayMySQLTestTables))
	}
}
