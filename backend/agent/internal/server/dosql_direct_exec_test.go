package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// SQLite exercises the executor's real database/sql transaction handling
// without a database server; engine-specific session setup is skipped for it.
func newDoSQLDirectTestTarget(t *testing.T) doSQLTarget {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orders.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO orders (id, status) VALUES (1, 'new'), (2, 'new')"); err != nil {
		t.Fatal(err)
	}
	return doSQLTarget{Engine: "sqlite", Driver: "sqlite", DSN: path, Database: "orders"}
}

func countDoSQLRows(t *testing.T, target doSQLTarget, where string) string {
	t.Helper()
	result, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target: target, Statements: []string{"SELECT count(*) FROM orders WHERE " + where}, ReadOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.Statements[0].Rows[0][0]
}

func TestDoSQLDirectCommitsBatchAndReturnsVerification(t *testing.T) {
	target := newDoSQLDirectTestTarget(t)
	result, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target: target,
		Statements: []string{
			"UPDATE orders SET status = 'paid' WHERE id = 1",
			"INSERT INTO orders (id, status) VALUES (3, 'paid')",
		},
		Verification: "SELECT count(*) FROM orders WHERE status = 'paid'",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || result.Status != "verified" {
		t.Fatalf("batch was not committed: %#v", result)
	}
	if result.Statements[0].AffectedRows != 1 || result.Verification.Rows[0][0] != "2" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if got := countDoSQLRows(t, target, "status = 'paid'"); got != "2" {
		t.Fatalf("committed rows = %s", got)
	}
}

func TestDoSQLDirectRollsBackWholeBatchOnFailure(t *testing.T) {
	target := newDoSQLDirectTestTarget(t)
	_, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target: target,
		Statements: []string{
			"UPDATE orders SET status = 'paid' WHERE id = 1",
			"INSERT INTO orders (id, status) VALUES (1, 'duplicate')",
		},
		Verification: "SELECT 1",
	})
	if err == nil || !strings.Contains(err.Error(), "dosql_statement_failed: statement 2") {
		t.Fatalf("expected statement 2 failure, got %v", err)
	}
	if got := countDoSQLRows(t, target, "status = 'paid'"); got != "0" {
		t.Fatalf("first statement survived a failed batch: %s rows", got)
	}
}

func TestDoSQLDirectRollsBackWhenVerificationFails(t *testing.T) {
	target := newDoSQLDirectTestTarget(t)
	_, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target:       target,
		Statements:   []string{"UPDATE orders SET status = 'paid'"},
		Verification: "SELECT missing_column FROM orders",
	})
	if err == nil || !strings.Contains(err.Error(), "dosql_verification_failed") {
		t.Fatalf("expected verification failure, got %v", err)
	}
	if got := countDoSQLRows(t, target, "status = 'paid'"); got != "0" {
		t.Fatalf("mutation survived a failed verification: %s rows", got)
	}
}

func TestDoSQLDirectDryRunAlwaysRollsBack(t *testing.T) {
	target := newDoSQLDirectTestTarget(t)
	result, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target:       target,
		Statements:   []string{"DELETE FROM orders"},
		Verification: "SELECT count(*) FROM orders",
		DryRun:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed || result.Status != "dry_run_rolled_back" || result.Verification.Rows[0][0] != "0" {
		t.Fatalf("dry run did not report the in-transaction effect: %#v", result)
	}
	if got := countDoSQLRows(t, target, "1 = 1"); got != "2" {
		t.Fatalf("dry run changed data: %s rows left", got)
	}
}

func TestDoSQLDirectBoundsRows(t *testing.T) {
	target := newDoSQLDirectTestTarget(t)
	result, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target: target, ReadOnly: true,
		Statements: []string{"WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n WHERE x < 500) SELECT x FROM n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Statements[0].Truncated || len(result.Statements[0].Rows) != doSQLDirectRowLimit {
		t.Fatalf("rows were not bounded: truncated=%t rows=%d", result.Statements[0].Truncated, len(result.Statements[0].Rows))
	}
}
