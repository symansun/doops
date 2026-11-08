package server

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/symansun/doops/backend/agent/api"
)

// Real-engine acceptance for rollback semantics. SQLite cannot show that MySQL
// commits DDL implicitly, so these run only when throwaway databases are given:
//
//	DOOPS_TEST_PG_DSN='postgres://u:p@127.0.0.1:5432/db?sslmode=disable'
//	DOOPS_TEST_MYSQL_DSN='u:p@tcp(127.0.0.1:3306)/db'
func realDoSQLTarget(t *testing.T, engine string) doSQLTarget {
	t.Helper()
	switch engine {
	case "postgres":
		dsn := os.Getenv("DOOPS_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("DOOPS_TEST_PG_DSN not set")
		}
		return doSQLTarget{Engine: "postgres", Driver: "pgx", DSN: dsn, Database: "testdb"}
	default:
		dsn := os.Getenv("DOOPS_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("DOOPS_TEST_MYSQL_DSN not set")
		}
		return doSQLTarget{Engine: "mysql", Driver: "mysql", DSN: dsn, Database: "testdb"}
	}
}

func realDoSQLRun(t *testing.T, target doSQLTarget, apply bool, dryRun bool, verify string, statements ...string) (doSQLDirectResult, error) {
	t.Helper()
	return executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target: target, Statements: statements, Verification: verify, ReadOnly: !apply, DryRun: dryRun,
	})
}

func realDoSQLScalar(t *testing.T, target doSQLTarget, query string) string {
	t.Helper()
	result, err := realDoSQLRun(t, target, false, false, "", query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return result.Statements[0].Rows[0][0]
}

func realDoSQLTableExists(t *testing.T, target doSQLTarget, table string) bool {
	t.Helper()
	query := "SELECT count(*) FROM information_schema.tables WHERE table_name = '" + table + "'"
	if target.Engine == "mysql" {
		query += " AND table_schema = database()"
	} else {
		query += " AND table_schema = current_schema()"
	}
	return realDoSQLScalar(t, target, query) != "0"
}

func realDoSQLCleanup(t *testing.T, target doSQLTarget, tables ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, table := range tables {
			_, _ = realDoSQLRun(t, target, true, false, "", "DROP TABLE IF EXISTS "+table)
		}
	})
}

func TestRealDoSQLPostgresRollsBackDDLAndDMLTogether(t *testing.T) {
	target := realDoSQLTarget(t, "postgres")
	realDoSQLCleanup(t, target, "zz_rb_a", "zz_rb_b")

	t.Run("failed batch rolls back earlier DDL", func(t *testing.T) {
		_, err := realDoSQLRun(t, target, true, false, "",
			"CREATE TABLE zz_rb_a (id int PRIMARY KEY)",
			"INSERT INTO zz_rb_a VALUES (1)",
			"INSERT INTO zz_rb_a VALUES (1)")
		if err == nil || !strings.Contains(err.Error(), "statement 3") {
			t.Fatalf("expected statement 3 failure, got %v", err)
		}
		if realDoSQLTableExists(t, target, "zz_rb_a") {
			t.Fatal("Postgres DDL survived a failed batch")
		}
		if strings.Contains(err.Error(), "dosql_ddl_already_committed") {
			t.Fatal("Postgres must not claim implicit commits")
		}
	})
	t.Run("failed verification rolls back DDL", func(t *testing.T) {
		_, err := realDoSQLRun(t, target, true, false, "SELECT 1/0",
			"CREATE TABLE zz_rb_a (id int)")
		if err == nil || !strings.Contains(err.Error(), "dosql_verification_failed") {
			t.Fatalf("expected verification failure, got %v", err)
		}
		if realDoSQLTableExists(t, target, "zz_rb_a") {
			t.Fatal("DDL survived a failed verification")
		}
	})
	t.Run("dry run DDL leaves no table and reports the effect", func(t *testing.T) {
		result, err := realDoSQLRun(t, target, true, true, "SELECT count(*) FROM zz_rb_b",
			"CREATE TABLE zz_rb_b (id int)", "INSERT INTO zz_rb_b VALUES (1),(2)")
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "dry_run_rolled_back" || result.Committed || result.Verification.Rows[0][0] != "2" {
			t.Fatalf("unexpected dry run result: %#v", result)
		}
		if realDoSQLTableExists(t, target, "zz_rb_b") {
			t.Fatal("dry run leaked a table")
		}
	})
	t.Run("committed DDL and DML persist", func(t *testing.T) {
		result, err := realDoSQLRun(t, target, true, false, "SELECT count(*) FROM zz_rb_a",
			"CREATE TABLE zz_rb_a (id int)", "INSERT INTO zz_rb_a VALUES (1),(2),(3)")
		if err != nil || !result.Committed || result.Status != "verified" || result.ImplicitCommits != 0 {
			t.Fatalf("apply failed: %#v %v", result, err)
		}
		if got := realDoSQLScalar(t, target, "SELECT count(*) FROM zz_rb_a"); got != "3" {
			t.Fatalf("rows = %s", got)
		}
	})
	t.Run("transaction control is refused", func(t *testing.T) {
		if _, err := splitDoSQLStatements("postgres", "INSERT INTO zz_rb_a VALUES (9); COMMIT"); err == nil {
			t.Fatal("caller COMMIT was accepted")
		}
	})
}

func TestRealDoSQLMySQLRollbackBoundaries(t *testing.T) {
	target := realDoSQLTarget(t, "mysql")
	realDoSQLCleanup(t, target, "zz_rb_a", "zz_rb_b", "zz_rb_c")

	t.Run("dry run with DDL is rejected before anything runs", func(t *testing.T) {
		result, err := realDoSQLRun(t, target, true, true, "", "CREATE TABLE zz_rb_b (id int)")
		if err == nil || !strings.Contains(err.Error(), "dosql_dry_run_ddl_unsupported") {
			t.Fatalf("expected DDL dry-run rejection, got %v", err)
		}
		if result.Status != "rejected" || result.Committed {
			t.Fatalf("rejected dry run must not look executed: %#v", result)
		}
		if realDoSQLTableExists(t, target, "zz_rb_b") {
			t.Fatal("rejected dry run still created the table")
		}
	})
	t.Run("dry run rejects DDL anywhere in the batch, even after DML", func(t *testing.T) {
		_, err := realDoSQLRun(t, target, true, true, "", "SELECT 1", "ALTER TABLE zz_rb_nope ADD COLUMN x int")
		if err == nil || !strings.Contains(err.Error(), "statement 2") {
			t.Fatalf("expected statement 2 rejection, got %v", err)
		}
	})
	t.Run("dry run of DML rolls back and reports the effect", func(t *testing.T) {
		// MySQL refuses DDL and DML in one batch, so seed them as two operations.
		if _, err := realDoSQLRun(t, target, true, false, "", "CREATE TABLE zz_rb_a (id int PRIMARY KEY, v varchar(8))"); err != nil {
			t.Fatal(err)
		}
		if _, err := realDoSQLRun(t, target, true, false, "", "INSERT INTO zz_rb_a VALUES (1,'orig')"); err != nil {
			t.Fatal(err)
		}
		result, err := realDoSQLRun(t, target, true, true, "SELECT count(*) FROM zz_rb_a",
			"DELETE FROM zz_rb_a", "INSERT INTO zz_rb_a VALUES (2,'x')")
		if err != nil || result.Status != "dry_run_rolled_back" || result.Verification.Rows[0][0] != "1" {
			t.Fatalf("dry run: %#v %v", result, err)
		}
		if got := realDoSQLScalar(t, target, "SELECT v FROM zz_rb_a WHERE id = 1"); got != "orig" {
			t.Fatalf("dry run changed data: %s", got)
		}
	})
	t.Run("failed DML batch rolls back atomically", func(t *testing.T) {
		_, err := realDoSQLRun(t, target, true, false, "",
			"UPDATE zz_rb_a SET v = 'changed' WHERE id = 1",
			"INSERT INTO zz_rb_a VALUES (1,'dup')")
		if err == nil {
			t.Fatal("duplicate key accepted")
		}
		if strings.Contains(err.Error(), "dosql_ddl_already_committed") {
			t.Fatal("pure DML batch must not claim a DDL commit")
		}
		if got := realDoSQLScalar(t, target, "SELECT v FROM zz_rb_a WHERE id = 1"); got != "orig" {
			t.Fatalf("DML survived a failed batch: %s", got)
		}
	})
	t.Run("failed verification rolls back DML", func(t *testing.T) {
		_, err := realDoSQLRun(t, target, true, false, "SELECT 1/0 FROM zz_rb_nope",
			"UPDATE zz_rb_a SET v = 'changed' WHERE id = 1")
		if err == nil || !strings.Contains(err.Error(), "dosql_verification_failed") {
			t.Fatalf("expected verification failure, got %v", err)
		}
		if got := realDoSQLScalar(t, target, "SELECT v FROM zz_rb_a WHERE id = 1"); got != "orig" {
			t.Fatalf("DML survived a failed verification: %s", got)
		}
	})
	t.Run("mixed DDL and DML is refused before anything runs", func(t *testing.T) {
		result, err := realDoSQLRun(t, target, true, false, "",
			"CREATE TABLE zz_rb_c (id int PRIMARY KEY)",
			"INSERT INTO zz_rb_c VALUES (1)",
			"INSERT INTO zz_rb_c VALUES (1)")
		if err == nil || !strings.Contains(err.Error(), "dosql_mixed_ddl_dml_unsupported") {
			t.Fatalf("mixed batch must be refused, got %v", err)
		}
		if result.Status != "rejected" || result.Committed {
			t.Fatalf("refused batch must report nothing committed: %#v", result)
		}
		if realDoSQLTableExists(t, target, "zz_rb_c") {
			t.Fatal("refused batch still created the table")
		}
	})
	t.Run("DDL-only batch that fails discloses what already committed", func(t *testing.T) {
		result, err := realDoSQLRun(t, target, true, false, "",
			"CREATE TABLE zz_rb_c (id int PRIMARY KEY)",
			"CREATE TABLE zz_rb_c (id int PRIMARY KEY)")
		if err == nil || !strings.Contains(err.Error(), "dosql_ddl_already_committed: 1") {
			t.Fatalf("the unrolled-back DDL must be disclosed, got %v", err)
		}
		if result.ImplicitCommits != 1 || !result.Committed || result.Status != "partially_committed" {
			t.Fatalf("result hides the commit: %#v", result)
		}
		if !realDoSQLTableExists(t, target, "zz_rb_c") {
			t.Fatal("MySQL should have committed the first CREATE TABLE")
		}
	})
	t.Run("successful apply with DDL reports implicit commits", func(t *testing.T) {
		result, err := realDoSQLRun(t, target, true, false, "", "ALTER TABLE zz_rb_c ADD COLUMN note varchar(8)")
		if err != nil || !result.Committed || result.ImplicitCommits != 1 {
			t.Fatalf("result: %#v %v", result, err)
		}
	})
	t.Run("query mode stays read only on a real engine", func(t *testing.T) {
		if _, err := realDoSQLRun(t, target, false, false, "", "DELETE FROM zz_rb_a"); err == nil {
			// The executor itself relies on a read-only transaction.
			if got := realDoSQLScalar(t, target, "SELECT count(*) FROM zz_rb_a"); got != "1" {
				t.Fatalf("read-only request deleted rows: %s", got)
			}
			t.Fatal("DELETE ran inside a read-only request")
		}
	})
}

// The handler records one version per real apply, before and after it runs.
func TestDirectDoSQLHandlerRecordsVersions(t *testing.T) {
	target := realDoSQLTarget(t, "postgres")
	realDoSQLCleanup(t, target, "zz_ver_a")

	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "ws"))
	if err := os.MkdirAll(filepath.Join(root, "ws", "sess"), 0700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	uri := os.Getenv("DOOPS_TEST_PG_DSN")
	secret := `{"metadata":{"resourceVersion":"1","labels":{"doops.sh/credential-id":"cred_a","doops.sh/credential-grant-id":"grant_a"},"annotations":{"doops.sh/credential-version":"ver_a"}},"data":{"connection_uri":"` +
		base64.StdEncoding.EncodeToString([]byte(uri)) + `"}}`
	writeExecutable(t, filepath.Join(bin, "kubectl"), "#!/bin/sh\nprintf '%s' '"+secret+"'\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	binding := api.AgentCredentialBinding{
		Name: "ver-db", Use: string(CredentialUseDatabase), Namespace: "apps", ResourceName: "sec",
		SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "1",
		CredentialID: "cred_a", GrantID: "grant_a", VersionID: "ver_a",
	}
	run := func(operation, sqlText string, dry bool) (string, error) {
		return handleDirectDoSQLPrompt(context.Background(), "sess", api.AgentPromptParams{
			Skill: "dosql", Operation: operation, SQL: sqlText, SQLDryRun: dry,
			CredentialBindings: []api.AgentCredentialBinding{binding},
		})
	}
	path := doSQLVersionJournalPath("postgres", "testdb")
	_ = os.Remove(path)

	if _, err := run("ask", "SELECT 1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := run("apply", "CREATE TABLE zz_ver_a (id int)", true); err != nil {
		t.Fatal(err)
	}
	if versions, _ := listDoSQLVersions(path); len(versions) != 0 {
		t.Fatalf("queries and dry runs must not take versions: %#v", versions)
	}

	first, err := run("apply", "CREATE TABLE zz_ver_a (id int)", false)
	if err != nil || !strings.Contains(first, `"version": 1`) {
		t.Fatalf("first apply: %v\n%s", err, first)
	}
	if _, err := run("apply", "INSERT INTO zz_ver_a VALUES (1)", false); err != nil {
		t.Fatal(err)
	}
	if _, err := run("apply", "INSERT INTO nope VALUES (1)", false); err == nil || !strings.Contains(err.Error(), "version=3") {
		t.Fatalf("a failed apply must still name its version, got %v", err)
	}
	versions, err := listDoSQLVersions(path)
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions: %#v %v", versions, err)
	}
	wantStatus := []string{"verified", "verified", "failed"}
	for i, summary := range versions {
		if summary.Version != int64(i+1) || summary.Status != wantStatus[i] {
			t.Fatalf("version %d = %#v, want status %s", i+1, summary, wantStatus[i])
		}
	}
	if unfinished := doSQLVersionUnfinished(versions); len(unfinished) != 0 {
		t.Fatalf("unfinished versions: %v", unfinished)
	}

	// An unwritable journal must stop the apply before it reaches the database.
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		_, err = run("apply", "INSERT INTO zz_ver_a VALUES (2)", false)
		if err == nil || !strings.Contains(err.Error(), "nothing was executed") {
			t.Fatalf("apply ran without a recorded version: %v", err)
		}
		_ = os.Chmod(path, 0600)
		if got := realDoSQLScalar(t, target, "SELECT count(*) FROM zz_ver_a"); got != "1" {
			t.Fatalf("an unrecorded change reached the database: %s rows", got)
		}
	}
}
