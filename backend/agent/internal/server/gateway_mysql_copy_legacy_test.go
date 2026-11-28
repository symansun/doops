package server

import (
	"database/sql"
	"slices"
	"testing"
)

// TestGatewayCopyLegacyListIsExact pins the frozen legacy list: exact names
// only, no prefix, no inference, and no entry that the repository cannot
// explain. A prefix match is the failure mode this guards against, because it
// would quietly accept any future table whose name starts the same way.
func TestGatewayCopyLegacyListIsExact(t *testing.T) {
	for _, name := range gatewayCopyLegacyTables {
		if !gatewayCopyLegacyTable(name) {
			t.Errorf("legacy table %q is listed but not matched", name)
		}
	}
	for _, name := range []string{"release_events_x", "release", "scheduler_job", "", "Git_Repos"} {
		if gatewayCopyLegacyTable(name) {
			t.Errorf("legacy table lookup accepted %q; the list matches exact names only", name)
		}
	}
	for table, columns := range gatewayCopyLegacyColumns {
		for _, column := range columns {
			if !gatewayCopyLegacyColumn(table, column) {
				t.Errorf("legacy column %s.%s is listed but not matched", table, column)
			}
		}
	}
	for _, probe := range [][2]string{{"audit_events", "environment_x"}, {"audit_events", "id"}, {"users", "id"}, {"users", "Account_Type"}, {"tokens", "account_type"}} {
		if gatewayCopyLegacyColumn(probe[0], probe[1]) {
			t.Errorf("legacy column lookup accepted %s.%s; the list matches exact names per table only", probe[0], probe[1])
		}
	}
	// The list is frozen and explained in gateway_mysql_copy_legacy.go; a size
	// change must come with a deleted-feature commit, not with a failing copy.
	if len(gatewayCopyLegacyTables) != 7 {
		t.Errorf("legacy table list has %d entries, want the 7 deleted-feature tables", len(gatewayCopyLegacyTables))
	}
	if len(gatewayCopyLegacyColumns["audit_events"]) != 4 || len(gatewayCopyLegacyColumns["users"]) != 1 {
		t.Errorf("legacy column list = %v, want 4 audit_events columns and 1 users column", gatewayCopyLegacyColumns)
	}
}

// gatewayCopyLegacyDestination installs the reviewed MySQL DDL and defers the
// one self-referencing foreign key the copier rejects by design, exactly as
// the other full-schema copy tests do.
func gatewayCopyLegacyDestination(t *testing.T) *sql.DB {
	t.Helper()
	dst := gatewayCopyTestDestination(t)
	if _, err := installGatewayMySQLSchema(t.Context(), dst); err != nil {
		t.Fatalf("install the reviewed DDL: %v", err)
	}
	var fkName string
	if err := dst.QueryRow("SELECT CONSTRAINT_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='inventory_machines' AND REFERENCED_TABLE_NAME='inventory_machines'").Scan(&fkName); err != nil {
		t.Fatalf("self-reference FK not found: %v", err)
	}
	if _, err := dst.Exec("ALTER TABLE inventory_machines DROP FOREIGN KEY " + fkName); err != nil {
		t.Fatalf("drop the self-reference: %v", err)
	}
	return dst
}

// TestGatewayMySQLCopyAcceptsAbsentLegacySchema copies the schema a current
// release creates: the frozen export minus the features that were deleted.
// The copier must complete, leave the legacy destination tables empty, and
// report them by name instead of failing with a bare schema mismatch.
func TestGatewayMySQLCopyAcceptsAbsentLegacySchema(t *testing.T) {
	src := gatewayMySQLTestSQLiteExport(t)
	gatewayCopyTestSQL(t, src, "PRAGMA foreign_keys=OFF")
	for i := len(gatewayCopyLegacyTables) - 1; i >= 0; i-- {
		gatewayCopyTestSQL(t, src, "DROP TABLE "+gatewayCopyLegacyTables[i])
	}
	for _, column := range gatewayCopyLegacyColumns["audit_events"] {
		gatewayCopyTestSQL(t, src, "ALTER TABLE audit_events DROP COLUMN "+column)
	}
	for _, column := range gatewayCopyLegacyColumns["users"] {
		gatewayCopyTestSQL(t, src, "ALTER TABLE users DROP COLUMN "+column)
	}

	dst := gatewayCopyLegacyDestination(t)
	report, err := copyGatewaySQLiteToMySQL(t.Context(), src, dst, false)
	if err != nil {
		t.Fatalf("copier rejected a schema without the deleted features: %v", err)
	}
	want := append([]string(nil), gatewayCopyLegacyTables...)
	slices.Sort(want)
	if !slices.Equal(report.LegacyTablesMissing, want) {
		t.Fatalf("legacy tables missing = %v, want the frozen list %v", report.LegacyTablesMissing, want)
	}
	if len(report.TableRows) != len(gatewayMySQLTestTables) {
		t.Fatalf("copied %d tables, want %d (every reviewed table, including the empty legacy ones)", len(report.TableRows), len(gatewayMySQLTestTables))
	}
	for _, name := range want {
		if rows, ok := report.TableRows[name]; !ok || rows != 0 {
			t.Fatalf("legacy table %s reports rows=%d present=%v, want an explicit empty entry", name, rows, ok)
		}
		var count int
		if err := dst.QueryRow("SELECT COUNT(*) FROM " + name).Scan(&count); err != nil || count != 0 {
			t.Fatalf("destination legacy table %s count = %d, want 0 (read error: %v)", name, count, err)
		}
	}
	if len(report.LegacyColumnsSkipped) != 5 {
		t.Fatalf("skipped legacy columns = %v, want five entries", report.LegacyColumnsSkipped)
	}
	for _, skipped := range report.LegacyColumnsSkipped {
		if skipped.InSource || !skipped.InTarget {
			t.Fatalf("skipped column %s.%s = source:%v target:%v, want target-only", skipped.Table, skipped.Column, skipped.InSource, skipped.InTarget)
		}
	}
}

// TestGatewayMySQLCopySkipsLegacyColumns copies the schema an old production
// database still carries. The historical columns are neither compared nor
// carried: the destination keeps its DDL default and the report names every
// column that lost its value.
func TestGatewayMySQLCopySkipsLegacyColumns(t *testing.T) {
	src := gatewayMySQLTestSQLiteExport(t)
	gatewayCopyTestSQL(t, src,
		"INSERT INTO users(id,name,disabled,created_at,account_type) VALUES('u1','operator',0,'2026-01-01T00:00:00Z','admin')",
		"INSERT INTO audit_events(user_id,started_at,environment,namespace,project,template) VALUES('u1','2026-01-01T00:00:00Z','prod','ops','billing','legacy')")

	dst := gatewayCopyLegacyDestination(t)
	report, err := copyGatewaySQLiteToMySQL(t.Context(), src, dst, false)
	if err != nil {
		t.Fatalf("copier rejected an old production schema: %v", err)
	}
	if len(report.LegacyTablesMissing) != 0 {
		t.Fatalf("legacy tables missing = %v, want none for an old production database", report.LegacyTablesMissing)
	}
	want := map[string][2]bool{
		"audit_events.environment": {true, true},
		"audit_events.namespace":   {true, true},
		"audit_events.project":     {true, true},
		"audit_events.template":    {true, true},
		"users.account_type":       {true, true},
	}
	for _, skipped := range report.LegacyColumnsSkipped {
		key := skipped.Table + "." + skipped.Column
		direction, ok := want[key]
		if !ok || direction != [2]bool{skipped.InSource, skipped.InTarget} {
			t.Fatalf("skipped column entry %s = source:%v target:%v, want one of %v", key, skipped.InSource, skipped.InTarget, want)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("legacy columns not reported: %v", want)
	}
	var account, namespace string
	if err := dst.QueryRow("SELECT account_type FROM users WHERE id='u1'").Scan(&account); err != nil {
		t.Fatalf("copied user row unreadable: %v", err)
	}
	if err := dst.QueryRow("SELECT namespace FROM audit_events WHERE user_id='u1'").Scan(&namespace); err != nil {
		t.Fatalf("copied audit row unreadable: %v", err)
	}
	if account != "person" || namespace != "" {
		t.Fatalf("legacy values reached the destination: account_type=%q namespace=%q, want the DDL defaults", account, namespace)
	}
}
