package backfill

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRunIntKeyBackfillChunkedAndResumable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT, status_v2 TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 55; i++ {
		if _, err := db.Exec(`INSERT INTO orders (id, status) VALUES (?, ?)`, i, "paid"); err != nil {
			t.Fatal(err)
		}
	}

	// Dry-run updates nothing permanently.
	dry, err := RunIntKeyBackfill(t.Context(), db, Options{
		Engine: "sqlite", Table: "orders", KeyColumn: "id", SetClause: "status_v2 = status", WhereClause: "status_v2 IS NULL", BatchSize: 20, DryRun: true,
	})
	if err != nil || dry.Batches != 3 || dry.TotalAffected != 55 {
		t.Fatalf("dry-run backfill failed: %+v err=%v", dry, err)
	}
	var nullCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM orders WHERE status_v2 IS NULL`).Scan(&nullCount)
	if nullCount != 55 {
		t.Fatalf("dry-run leaked writes: nullCount=%d", nullCount)
	}

	// Real chunked backfill updates all 55 rows in 3 batches.
	res, err := RunIntKeyBackfill(t.Context(), db, Options{
		Engine: "sqlite", Table: "orders", KeyColumn: "id", SetClause: "status_v2 = status", WhereClause: "status_v2 IS NULL", BatchSize: 20,
	})
	if err != nil || res.Batches != 3 || res.TotalAffected != 55 || res.LastKey != 55 {
		t.Fatalf("real backfill failed: %+v err=%v", res, err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM orders WHERE status_v2 = 'paid'`).Scan(&nullCount)
	if nullCount != 55 {
		t.Fatalf("expected 55 updated rows, got %d", nullCount)
	}
}

func TestRunIntKeyBackfillResumeThrottleEmptyAndValidation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Missing required options fail closed.
	if _, err := RunIntKeyBackfill(t.Context(), db, Options{Engine: "sqlite"}); err == nil || !strings.Contains(err.Error(), "dosql_backfill_invalid") {
		t.Fatalf("expected dosql_backfill_invalid, got %v", err)
	}

	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, flag INT DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	// Empty table returns 0 batches, 0 affected.
	emptyRes, err := RunIntKeyBackfill(t.Context(), db, Options{
		Engine: "sqlite", Table: "items", KeyColumn: "id", SetClause: "flag = 1",
	})
	if err != nil || emptyRes.Batches != 0 || emptyRes.TotalAffected != 0 {
		t.Fatalf("empty table failed: %+v err=%v", emptyRes, err)
	}

	for i := 1; i <= 10; i++ {
		if _, err := db.Exec(`INSERT INTO items (id, flag) VALUES (?, 0)`, i); err != nil {
			t.Fatal(err)
		}
	}
	// StartAfterID skips IDs <= 6 and updates only 7..10 with throttle.
	resumed, err := RunIntKeyBackfill(t.Context(), db, Options{
		Engine: "sqlite", Table: "items", KeyColumn: "id", SetClause: "flag = 1",
		BatchSize: 2, Throttle: time.Millisecond, StartAfterID: 6,
	})
	if err != nil || resumed.Batches != 2 || resumed.TotalAffected != 4 || resumed.LastKey != 10 {
		t.Fatalf("resumed backfill failed: %+v err=%v", resumed, err)
	}

	// Pre-canceled context stops immediately without modifying rows.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := RunIntKeyBackfill(ctx, db, Options{
		Engine: "sqlite", Table: "items", KeyColumn: "id", SetClause: "flag = 2",
	}); err == nil {
		t.Fatal("expected canceled context error")
	}

	if quoteID("mysql", "a`b") != "`a``b`" || quoteID("postgres", `a"b`) != `"a""b"` {
		t.Fatal("quoteID escaping failed")
	}
}

func TestRunIntKeyBackfillSparseSnowflakeKeys(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, flag INT DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	ids := []int64{100, 1_000_000_000, 9_000_000_000_000}
	for _, id := range ids {
		if _, err := db.Exec(`INSERT INTO events (id, flag) VALUES (?, 0)`, id); err != nil {
			t.Fatal(err)
		}
	}
	res, err := RunIntKeyBackfill(t.Context(), db, Options{
		Engine: "sqlite", Table: "events", KeyColumn: "id", SetClause: "flag = 1", BatchSize: 2,
	})
	if err != nil {
		t.Fatalf("sparse backfill failed: %v", err)
	}
	if res.Batches != 2 || res.TotalAffected != 3 || res.LastKey != 9_000_000_000_000 {
		t.Fatalf("sparse backfill = %+v, want Batches=2 TotalAffected=3 LastKey=9000000000000", res)
	}
}
