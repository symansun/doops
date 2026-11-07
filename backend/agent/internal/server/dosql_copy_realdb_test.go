package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Copy acceptance on real engines. It needs the same throwaway servers as the
// rollback tests (DOOPS_TEST_PG_DSN / DOOPS_TEST_MYSQL_DSN) and creates two
// scratch databases of its own on them.
func copyScratchTargets(t *testing.T, engine string) (doSQLTarget, doSQLTarget) {
	t.Helper()
	base := realDoSQLTarget(t, engine)
	admin, err := sql.Open(base.Driver, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	names := []string{"zz_copy_src", "zz_copy_dst"}
	var targets []doSQLTarget
	for _, name := range names {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name)
		if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		target := base
		target.Database = name
		target.DSN = strings.Replace(base.DSN, "/testdb", "/"+name, 1)
		if target.DSN == base.DSN {
			t.Fatalf("cannot derive DSN for %s", name)
		}
		targets = append(targets, target)
	}
	t.Cleanup(func() {
		for _, name := range names {
			_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name)
		}
	})
	return targets[0], targets[1]
}

func TestRealDoSQLCopyBetweenDatabases(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			src, dst := copyScratchTargets(t, engine)
			ctx := context.Background()
			ddl := "CREATE TABLE items (id int PRIMARY KEY, name varchar(40), note text, created timestamp NULL)"
			for _, target := range []doSQLTarget{src, dst} {
				for _, table := range []string{"items", "other"} {
					statement := strings.Replace(ddl, "items", table, 1)
					if _, err := realDoSQLRun(t, target, true, false, "", statement); err != nil {
						t.Fatalf("%s ddl: %v", table, err)
					}
				}
			}
			var rows []string
			for i := 1; i <= 1300; i++ {
				note := "NULL"
				if i%3 == 0 {
					note = fmt.Sprintf("'it''s row %d'", i)
				}
				rows = append(rows, fmt.Sprintf("(%d,'n%d',%s,'2026-01-02 03:04:05')", i, i, note))
			}
			if _, err := realDoSQLRun(t, src, true, false, "",
				"INSERT INTO items (id,name,note,created) VALUES "+strings.Join(rows, ",")); err != nil {
				t.Fatal(err)
			}
			if _, err := realDoSQLRun(t, src, true, false, "",
				"INSERT INTO other (id,name) VALUES (1,'a'),(2,'b')"); err != nil {
				t.Fatal(err)
			}

			t.Run("dry run copies inside a transaction and leaves the destination empty", func(t *testing.T) {
				result, err := executeDoSQLCopy(ctx, src, dst, []string{"items"}, true)
				if err != nil || result.Status != "dry_run_rolled_back" || result.Statements[0].AffectedRows != 1300 {
					t.Fatalf("dry run: %#v %v", result, err)
				}
				if got := realDoSQLScalar(t, dst, "SELECT count(*) FROM items"); got != "0" {
					t.Fatalf("dry run left %s rows", got)
				}
			})
			t.Run("a failing later table rolls back the earlier one", func(t *testing.T) {
				_, err := executeDoSQLCopy(ctx, src, dst, []string{"items", "missing_table"}, false)
				if err == nil || !strings.Contains(err.Error(), "dosql_copy_failed") {
					t.Fatalf("expected copy failure, got %v", err)
				}
				if got := realDoSQLScalar(t, dst, "SELECT count(*) FROM items"); got != "0" {
					t.Fatalf("partial copy survived: %s rows", got)
				}
			})
			t.Run("a conflict in a later batch rolls back every batch", func(t *testing.T) {
				if _, err := realDoSQLRun(t, dst, true, false, "", "INSERT INTO items (id,name) VALUES (1250,'existing')"); err != nil {
					t.Fatal(err)
				}
				_, err := executeDoSQLCopy(ctx, src, dst, []string{"items"}, false)
				if err == nil {
					t.Fatal("duplicate key accepted")
				}
				if got := realDoSQLScalar(t, dst, "SELECT count(*) FROM items"); got != "1" {
					t.Fatalf("batches before the conflict survived: %s rows", got)
				}
				if _, err := realDoSQLRun(t, dst, true, false, "", "DELETE FROM items"); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("a real copy commits every row, NULLs and quotes included", func(t *testing.T) {
				result, err := executeDoSQLCopy(ctx, src, dst, []string{"items", "other"}, false)
				if err != nil || !result.Committed || result.Status != "verified" {
					t.Fatalf("copy: %#v %v", result, err)
				}
				if result.Statements[0].AffectedRows != 1300 || result.Statements[1].AffectedRows != 2 {
					t.Fatalf("row counts: %#v", result.Statements)
				}
				if got := realDoSQLScalar(t, dst, "SELECT count(*) FROM items WHERE note IS NULL"); got != "867" {
					t.Fatalf("NULL notes = %s, want 867", got)
				}
				if got := realDoSQLScalar(t, dst, "SELECT note FROM items WHERE id = 1299"); got != "it's row 1299" {
					t.Fatalf("quoted note = %q", got)
				}
				if got := realDoSQLScalar(t, dst, "SELECT sum(id) FROM items"); got != "845650" {
					t.Fatalf("sum(id) = %s, want 845650", got)
				}
			})
			t.Run("the source is never modified", func(t *testing.T) {
				if got := realDoSQLScalar(t, src, "SELECT count(*) FROM items"); got != "1300" {
					t.Fatalf("source rows = %s", got)
				}
			})
		})
	}
}

func TestDoSQLCopyValidation(t *testing.T) {
	// Only a name that cannot be a table at all is refused.
	for name, tables := range map[string][]string{
		"empty list":    nil,
		"blank":         {" "},
		"control chars": {"it\nems"},
	} {
		if _, err := validateDoSQLCopyTables(tables); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// Anything else is a table name, and quoting keeps it one identifier.
	for _, c := range []struct{ engine, name, want string }{
		{"mysql", "items; DROP TABLE x", "`items; DROP TABLE x`"},
		{"mysql", "it`ems", "`it``ems`"},
		{"postgres", "items\" ; DROP TABLE x", `"items"" ; DROP TABLE x"`},
		{"postgres", "other.items", `"other.items"`},
	} {
		if got := doSQLQuoteIdentifier(c.engine, c.name); got != c.want {
			t.Errorf("%s quote(%q) = %s, want %s", c.engine, c.name, got, c.want)
		}
	}
	got, err := validateDoSQLCopyTables([]string{"a", " a ", "b"})
	if err != nil || len(got) != 2 {
		t.Fatalf("dedupe: %v %v", got, err)
	}
}
