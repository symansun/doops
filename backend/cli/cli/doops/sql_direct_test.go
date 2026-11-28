package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSQLFixture(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLDirectInlineAndFileProduceSameArguments(t *testing.T) {
	statement := "UPDATE orders SET status = 'paid' WHERE id = 1;\nINSERT INTO audit VALUES (1)"
	file := writeSQLFixture(t, "change.sql", "\n"+statement+"\n")
	verifyFile := writeSQLFixture(t, "verify.sql", "SELECT status FROM orders WHERE id = 1")

	inline, err := buildSQLDirectInput(sqlModeApply, statement, "", "SELECT status FROM orders WHERE id = 1", "", "orders", true)
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := buildSQLDirectInput(sqlModeApply, "", file, "", verifyFile, "orders", true)
	if err != nil {
		t.Fatal(err)
	}
	a, b := inline.promptArguments(), fromFile.promptArguments()
	for _, key := range []string{"sql", "sql_verification", "sql_database", "sql_dry_run"} {
		if a[key] != b[key] {
			t.Fatalf("%s differs: inline=%v file=%v", key, a[key], b[key])
		}
	}
}

func TestSQLDirectQueryAcceptsInlineAndFile(t *testing.T) {
	file := writeSQLFixture(t, "report.sql", "SELECT count(*) FROM users")
	for _, input := range [][2]string{{"SELECT count(*) FROM users", ""}, {"", file}} {
		direct, err := buildSQLDirectInput(sqlModeQuery, input[0], input[1], "", "", "", false)
		if err != nil || direct == nil || direct.SQL != "SELECT count(*) FROM users" {
			t.Fatalf("query input %v: %#v, %v", input, direct, err)
		}
	}
}

func TestSQLDirectRejectsAmbiguousOrInvalidInput(t *testing.T) {
	file := writeSQLFixture(t, "x.sql", "SELECT 1")
	empty := writeSQLFixture(t, "empty.sql", "  \n")
	for name, call := range map[string]func() error{
		"sql and file": func() error {
			_, err := buildSQLDirectInput(sqlModeQuery, "SELECT 1", file, "", "", "", false)
			return err
		},
		"verify and file": func() error {
			_, err := buildSQLDirectInput(sqlModeApply, "UPDATE t SET a=1", "", "SELECT 1", file, "", false)
			return err
		},
		"empty file": func() error { _, err := buildSQLDirectInput(sqlModeQuery, "", empty, "", "", "", false); return err },
		"missing file": func() error {
			_, err := buildSQLDirectInput(sqlModeQuery, "", file+".missing", "", "", "", false)
			return err
		},
		"verify on query": func() error {
			_, err := buildSQLDirectInput(sqlModeQuery, "SELECT 1", "", "SELECT 1", "", "", false)
			return err
		},
		"dry-run on query": func() error {
			_, err := buildSQLDirectInput(sqlModeQuery, "SELECT 1", "", "", "", "", true)
			return err
		},
		"plan mode": func() error {
			_, err := buildSQLDirectInput(sqlModePlan, "SELECT 1", "", "", "", "", false)
			return err
		},
		"verify without sql": func() error {
			_, err := buildSQLDirectInput(sqlModeApply, "", "", "SELECT 1", "", "", false)
			return err
		},
		"dry-run without sql": func() error { _, err := buildSQLDirectInput(sqlModeApply, "", "", "", "", "", true); return err },
	} {
		if err := call(); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestSQLDirectAbsentKeepsNaturalLanguagePath(t *testing.T) {
	direct, err := buildSQLDirectInput(sqlModeApply, "", "", "", "", "orders", false)
	if err != nil || direct != nil {
		t.Fatalf("no SQL must keep the --request path: %#v, %v", direct, err)
	}
	if !strings.Contains(sqlDirectAuditDescription(sqlModeApply, &sqlDirectInput{DryRun: true}), "回滚") {
		t.Fatal("dry-run audit description must mention rollback")
	}
}
