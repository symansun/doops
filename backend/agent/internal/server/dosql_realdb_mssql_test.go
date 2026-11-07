package server

import (
	"os"
	"strings"
	"testing"
)

// DOOPS_TEST_MSSQL_URI='sqlserver://sa:Pass@127.0.0.1:1433/testdb'
func mssqlTestTarget(t *testing.T) doSQLTarget {
	t.Helper()
	uri := os.Getenv("DOOPS_TEST_MSSQL_URI")
	if uri == "" {
		t.Skip("DOOPS_TEST_MSSQL_URI not set")
	}
	target, err := doSQLConnectionTarget("mssql", "testdb", map[string]string{"connection_uri": uri})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func mssqlTableExists(t *testing.T, target doSQLTarget, table string) bool {
	t.Helper()
	return realDoSQLScalar(t, target, "SELECT count(*) FROM information_schema.tables WHERE table_name = '"+table+"'") != "0"
}

func TestRealDoSQLMSSQLRollsBackDDLAndDMLTogether(t *testing.T) {
	target := mssqlTestTarget(t)
	t.Cleanup(func() {
		_, _ = realDoSQLRun(t, target, true, false, "", "DROP TABLE IF EXISTS zz_ms_a", "DROP TABLE IF EXISTS zz_ms_b")
	})
	t.Run("failed batch rolls back earlier DDL", func(t *testing.T) {
		_, err := realDoSQLRun(t, target, true, false, "",
			"CREATE TABLE zz_ms_a (id int PRIMARY KEY)", "INSERT INTO zz_ms_a VALUES (1)", "INSERT INTO zz_ms_a VALUES (1)")
		if err == nil || mssqlTableExists(t, target, "zz_ms_a") {
			t.Fatalf("DDL survived a failed batch: %v", err)
		}
	})
	t.Run("dry run rolls back DDL", func(t *testing.T) {
		result, err := realDoSQLRun(t, target, true, true, "SELECT count(*) FROM zz_ms_b",
			"CREATE TABLE zz_ms_b (id int)", "INSERT INTO zz_ms_b VALUES (1)")
		if err != nil || result.Status != "dry_run_rolled_back" || result.ImplicitCommits != 0 {
			t.Fatalf("dry run: %#v %v", result, err)
		}
		if mssqlTableExists(t, target, "zz_ms_b") {
			t.Fatal("dry run leaked the table")
		}
	})
	t.Run("real apply commits and failed verify rolls back", func(t *testing.T) {
		if _, err := realDoSQLRun(t, target, true, false, "SELECT 1/(count(*)-1) FROM zz_ms_b",
			"CREATE TABLE zz_ms_b (id int)", "INSERT INTO zz_ms_b VALUES (1)"); err == nil || mssqlTableExists(t, target, "zz_ms_b") {
			t.Fatalf("failed verification kept the table: %v", err)
		}
		if _, err := realDoSQLRun(t, target, true, false, "SELECT count(*) FROM zz_ms_b",
			"CREATE TABLE zz_ms_b (id int)", "INSERT INTO zz_ms_b VALUES (1)"); err != nil {
			t.Fatal(err)
		}
		if got := realDoSQLScalar(t, target, "SELECT count(*) FROM zz_ms_b"); got != "1" {
			t.Fatalf("rows = %s", got)
		}
	})
	t.Run("query refuses writes", func(t *testing.T) {
		if _, err := realDoSQLRun(t, target, false, false, "", "DELETE FROM zz_ms_b"); err == nil || !strings.Contains(err.Error(), "dosql") {
			t.Fatalf("write accepted in query mode: %v", err)
		}
	})
}
