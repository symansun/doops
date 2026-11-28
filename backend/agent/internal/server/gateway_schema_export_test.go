package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGatewaySchemaExportMatchesLiveStore is the first segment of the schema
// guard chain. TestGatewayMySQLColumnsMatchExport only compares the frozen
// export with the MySQL DDL, so a column that migrate() adds to a live store
// but that nobody re-captured into the export stays invisible: the export and
// the DDL agree with each other and both disagree with the database a Gateway
// actually opens.
//
// The guard compares every live table with the same table in the export. A
// live table the export lacks fails outright. Columns are compared as sets in
// both directions, because each direction breaks a different path: a column
// only in the live store is one the copier would drop (or the MySQL INSERT
// would not know), and a column only in the export is one the runtime never
// writes.
//
// The export legitimately holds tables a fresh store does not create any more
// (tables a production database still carries from earlier releases), so the
// guard only requires live tables to be a subset of the export, not equality.
func TestGatewaySchemaExportMatchesLiveStore(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open a live store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	live := store.readDB()
	export := gatewayMySQLTestSQLiteExport(t)

	exportTables := make(map[string]bool)
	for _, table := range gatewayMySQLExportTables(t, export) {
		exportTables[table] = true
	}
	liveTables := gatewaySchemaLiveTables(t, live)
	if len(liveTables) == 0 {
		t.Fatalf("the live store created no tables")
	}
	for _, table := range liveTables {
		if !exportTables[table] {
			t.Errorf("live table %s has no definition in the frozen export; re-capture testdata/gateway-sqlite-schema.sql from a live store", table)
			continue
		}
		want := gatewayMySQLSQLiteColumnNames(t, export, table)
		got := gatewayMySQLSQLiteColumnNames(t, live, table)
		if liveOnly := gatewayMySQLColumnDifference(got, want); len(liveOnly) > 0 {
			t.Errorf("%s columns exist in the live store but not in the frozen export: %v; re-capture testdata/gateway-sqlite-schema.sql from a live store",
				table, liveOnly)
			continue
		}
		legacy := gatewayMySQLColumnDifference(want, got)
		if len(legacy) == 0 {
			continue
		}
		// A column the export carries but a fresh store never creates is a
		// column earlier releases wrote; a production database keeps it. It is
		// tolerated only while the MySQL DDL lets an insert omit it, otherwise
		// copying a live row fails with error 1364.
		specs := map[string]gatewayMySQLColumnSpec{}
		for _, spec := range gatewayMySQLDDLColumns(t, string(gatewayMySQLSchemaSQL), table) {
			specs[spec.name] = spec
		}
		for _, column := range legacy {
			spec, ok := specs[column]
			if !ok {
				t.Errorf("%s column %s is absent from the live store and from the MySQL DDL; production databases carry it, so the DDL must keep it", table, column)
				continue
			}
			if spec.notNull && !spec.hasDefault {
				t.Errorf("%s column %s is absent from the live store and is NOT NULL without a default in the MySQL DDL, so copying a live row fails with error 1364", table, column)
			}
		}
	}
}

// gatewaySchemaLiveTables lists the tables a store built by migrate() holds.
// The sqlite_ internal tables are excluded, exactly as the export helper does.
func gatewaySchemaLiveTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("list the live store tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan live table: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list the live store tables: %v", err)
	}
	return tables
}

// TestGatewaySchemaLiveStoreCopiesToMySQL runs the offline copier with the
// database a Gateway actually opens as its source, not with the frozen export.
// The frozen export is only a capture; migrate() keeps changing a live store
// (ensureColumn appends columns), so a copier that is proven only against the
// export can still reject the real database. This test therefore starts from
// OpenGatewayStore, adds the operator and legacy tables a production database
// carries from the export (a fresh store never creates them), and copies the
// result into the reviewed MySQL DDL.
//
// The source keeps the live definitions of the tables it creates: the point is
// that the live columns reach MySQL, including the eight artifact columns of
// monitor_run_items that migrate() appends.
func TestGatewaySchemaLiveStoreCopiesToMySQL(t *testing.T) {
	if os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT") == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to a disposable MySQL port")
	}
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open a live store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	src := store.db

	data, err := os.ReadFile(filepath.Join("testdata", "gateway-sqlite-schema.sql"))
	if err != nil {
		t.Fatalf("read the frozen export: %v", err)
	}
	var tables []string
	for _, stmt := range strings.Split(string(data), ";") {
		stmt = strings.TrimSpace(stmt)
		if strings.HasPrefix(strings.ToUpper(stmt), "CREATE TABLE") {
			tables = append(tables, stmt)
		}
	}
	apply := func(stmts []string, ignoreExisting bool) {
		for _, stmt := range stmts {
			if _, err := src.Exec(stmt); err != nil {
				if ignoreExisting && strings.Contains(err.Error(), "already exists") {
					continue
				}
				t.Fatalf("extend the live store: %v", err)
			}
		}
	}
	// Only the table definitions: the export also carries indexes and triggers,
	// which the live store owns and the copier derives from pragma instead.
	apply(tables, true)

	// Five columns earlier releases wrote are still present in the export and
	// in the MySQL DDL (NOT NULL with defaults), but a fresh store no longer
	// creates them. A production database carries them, so the source here does
	// too. This is a shape fixture for the export guard, not a copy allowance:
	// the copier's frozen list (gateway_mysql_copy_legacy.go) is what accepts
	// them, and the copy tests cover both the old and the fresh shape.
	for _, statement := range []string{
		"ALTER TABLE audit_events ADD COLUMN project TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN environment TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN template TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN namespace TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN account_type TEXT NOT NULL DEFAULT 'person'",
	} {
		if _, err := src.Exec(statement); err != nil {
			t.Fatalf("add a legacy column: %v", err)
		}
	}
	if got := len(gatewaySchemaLiveTables(t, src)); got != len(gatewayMySQLTestTables) {
		t.Fatalf("source tables = %d, want %d (the live store plus the operator tables)", got, len(gatewayMySQLTestTables))
	}

	dst := gatewayCopyTestDestination(t)
	if _, err := installGatewayMySQLSchema(t.Context(), dst); err != nil {
		t.Fatalf("install: %v", err)
	}
	var fkName string
	if err := dst.QueryRow("SELECT CONSTRAINT_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='inventory_machines' AND REFERENCED_TABLE_NAME='inventory_machines'").Scan(&fkName); err != nil {
		t.Fatalf("self-reference FK not found: %v", err)
	}
	if _, err := dst.Exec("ALTER TABLE inventory_machines DROP FOREIGN KEY " + fkName); err != nil {
		t.Fatalf("drop self-reference: %v", err)
	}
	report, err := copyGatewaySQLiteToMySQL(t.Context(), src, dst, false)
	if err != nil {
		t.Fatalf("copier rejected the live store: %v", err)
	}
	if len(report.TableRows) != len(gatewayMySQLTestTables) {
		t.Fatalf("copied %d tables, want %d", len(report.TableRows), len(gatewayMySQLTestTables))
	}
	columns := []string{"artifact_format", "artifact_markdown", "artifact_json", "raw_output", "artifact_dir", "artifact_bytes", "artifact_sha256", "artifact_truncated"}
	rows, err := dst.Query("SELECT " + strings.Join(columns, ", ") + " FROM monitor_run_items")
	if err != nil {
		t.Fatalf("the copied monitor_run_items cannot serve its artifact columns: %v", err)
	}
	rows.Close()
	for _, prompt := range []string{"prompt", "command", "skills_json"} {
		for _, table := range []string{"monitor_runs", "monitor_schedules"} {
			var got string
			if err := dst.QueryRow("SELECT " + prompt + " FROM " + table + " LIMIT 0").Scan(&got); err != nil && err != sql.ErrNoRows {
				t.Fatalf("%s.%s is missing after the copy: %v", table, prompt, err)
			}
		}
	}
}
