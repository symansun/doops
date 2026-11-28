package migrate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/user/doops/agent/internal/dosql/recorder"
)

func TestPackBaselineMigrateResumeAndTamperDetection(t *testing.T) {
	dir := t.TempDir()

	// 1. Pack V001 from auto-recorded session history (deduplicates consecutive identical statements & skips reads/failures).
	packed1, err := PackFromHistory(dir, "init orders", "mysql", []recorder.StatementRecord{
		{Kind: "read", Statement: "SELECT 1", Status: "succeeded", StmtHash: "h0"},
		{Kind: "ddl", Statement: "CREATE TABLE orders (id INT PRIMARY KEY)", Status: "succeeded", StmtHash: "h1"},
		{Kind: "ddl", Statement: "CREATE TABLE orders (id INT PRIMARY KEY)", Status: "succeeded", StmtHash: "h1"},
		{Kind: "dml", Statement: "INSERT INTO orders VALUES (1)", Status: "failed", StmtHash: "h2"},
	})
	if err != nil || packed1.FileName != "V001__init_orders.sql" {
		t.Fatalf("pack V001 failed: %+v err=%v", packed1, err)
	}

	// Pack V002 with 2 statements.
	packed2, err := PackFromHistory(dir, "add status", "mysql", []recorder.StatementRecord{
		{Kind: "ddl", Statement: "ALTER TABLE orders ADD COLUMN status TEXT", Status: "succeeded", StmtHash: "h3"},
		{Kind: "dml", Statement: "UPDATE orders SET status = 'ok' WHERE id = 1", Status: "succeeded", StmtHash: "h4"},
	})
	if err != nil || packed2.FileName != "V002__add_status.sql" {
		t.Fatalf("pack V002 failed: %+v err=%v", packed2, err)
	}

	// Verify dosql.sum was created and passes LoadDir.
	files, err := LoadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("LoadDir failed: len=%d err=%v", len(files), err)
	}

	// 2. Run migrations against a real SQLite DB with baseline=1 (simulating adopting an existing DB).
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &SQLHistoryStore{DB: db, Engine: "sqlite"}

	// First attempt on V002 fails after statement 1 of 2.
	failOnce := true
	_, err = Apply(t.Context(), store, files, 1, "v1.0.0", false, func(ctx context.Context, f MigrationFile, startIdx int) (int, int, error) {
		if f.Version != 2 || startIdx != 0 {
			t.Fatalf("unexpected exec call: ver=%d start=%d", f.Version, startIdx)
		}
		if failOnce {
			failOnce = false
			return 1, 2, errors.New("simulated mid-batch failure")
		}
		return 2, 2, nil
	})
	if err == nil || !strings.Contains(err.Error(), "dosql_migrate_failed") {
		t.Fatalf("expected failure on V002, got %v", err)
	}

	// Without --resume, dirty version blocks further migrations.
	if _, err := Apply(t.Context(), store, files, 0, "v1.0.0", false, nil); err == nil || !strings.Contains(err.Error(), "dosql_migrate_dirty_version") {
		t.Fatalf("expected dirty version block, got %v", err)
	}

	// With --resume, resumes from statement index 1 (never re-runs statement 0!).
	resumedFrom := -1
	res, err := Apply(t.Context(), store, files, 0, "v1.0.0", true, func(ctx context.Context, f MigrationFile, startIdx int) (int, int, error) {
		resumedFrom = startIdx
		return 1, 2, nil
	})
	if err != nil || resumedFrom != 1 || res.Current != 2 {
		t.Fatalf("resume failed: from=%d res=%+v err=%v", resumedFrom, res, err)
	}

	// Re-running Apply is 100% idempotent (0 executed, 2 skipped).
	again, err := Apply(t.Context(), store, files, 0, "v1.0.0", false, func(ctx context.Context, f MigrationFile, startIdx int) (int, int, error) {
		t.Fatal("should not re-execute already applied migrations")
		return 0, 0, nil
	})
	if err != nil || len(again.Executed) != 0 || again.Skipped != 2 {
		t.Fatalf("expected idempotent skip: %+v err=%v", again, err)
	}

	// Tampering with V002 breaks dosql.sum verification.
	if err := os.WriteFile(filepath.Join(dir, "V002__add_status.sql"), []byte("SELECT 999;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "dosql_migrate_sum_mismatch") {
		t.Fatalf("expected dosql.sum mismatch error, got %v", err)
	}
}

func TestPackFromHistoryFiltersDryRunBackfillMigrateAndSupportsMongo(t *testing.T) {
	dir := t.TempDir()
	records := []recorder.StatementRecord{
		{Mode: "query", Kind: "read", Statement: "SELECT 1", Status: "succeeded", StmtHash: "r1"},
		{Mode: "dry_run", Kind: "dml", Statement: "UPDATE orders SET status='dry' WHERE id=1", Status: "succeeded", StmtHash: "d1"},
		{Mode: "backfill", Kind: "backfill", Statement: "BACKFILL orders SET a=1 WHERE id>0", Status: "succeeded", StmtHash: "b1"},
		{Mode: "backfill_dry_run", Kind: "backfill", Statement: "BACKFILL orders SET a=2 WHERE id>0", Status: "succeeded", StmtHash: "b2"},
		{Mode: "migrate", Kind: "ddl", Statement: "ALTER TABLE orders ADD COLUMN already_migrated INT", Status: "succeeded", StmtHash: "m1"},
		{Mode: "apply", Kind: "ddl", Statement: "ALTER TABLE orders ADD COLUMN real_col INT", Status: "succeeded", StmtHash: "a1"},
	}
	packed, err := PackFromHistory(dir, "only real apply", "mysql", records)
	if err != nil {
		t.Fatalf("PackFromHistory failed: %v", err)
	}
	if !strings.Contains(packed.Content, "real_col") ||
		strings.Contains(packed.Content, "dry") ||
		strings.Contains(packed.Content, "BACKFILL") ||
		strings.Contains(packed.Content, "already_migrated") {
		t.Fatalf("PackFromHistory leaked non-apply statements:\n%s", packed.Content)
	}

	// Verify no atomic temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file in migration dir: %s", e.Name())
		}
	}

	// MongoDB pack generates .mongo.json and passes LoadDir + dosql.sum verification.
	mongoDir := t.TempDir()
	mongoPacked, err := PackFromHistory(mongoDir, "add index", "mongodb", []recorder.StatementRecord{
		{Mode: "apply", Kind: "ddl", Statement: `{"collection":"orders","op":"createIndex","keys":{"code":1}}`, Status: "succeeded", StmtHash: "mg1"},
	})
	if err != nil || mongoPacked.FileName != "V001__add_index.mongo.json" || mongoPacked.EngineHint != "mongodb" {
		t.Fatalf("mongo pack failed: %+v err=%v", mongoPacked, err)
	}
	loaded, err := LoadDir(mongoDir)
	if err != nil || len(loaded) != 1 || loaded[0].EngineHint != "mongodb" {
		t.Fatalf("mongo LoadDir failed: %+v err=%v", loaded, err)
	}
}

func TestBaselineRemainsAbsorbedAfterSubsequentMigrations(t *testing.T) {
	files := []MigrationFile{
		{Version: 1, VersionTag: "V001", Description: "legacy_one", FileName: "V001__legacy_one.sql", Checksum: "sha256:1"},
		{Version: 2, VersionTag: "V002", Description: "legacy_two", FileName: "V002__legacy_two.sql", Checksum: "sha256:2"},
		{Version: 3, VersionTag: "V003", Description: "post_base_one", FileName: "V003__post_base_one.sql", Checksum: "sha256:3"},
		{Version: 4, VersionTag: "V004", Description: "post_base_two", FileName: "V004__post_base_two.sql", Checksum: "sha256:4"},
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &SQLHistoryStore{DB: db, Engine: "sqlite"}

	// Adopt an existing DB at baseline=2 while applying V003.
	var ran []int64
	res, err := Apply(t.Context(), store, files[:3], 2, "v1.0.0", false, func(ctx context.Context, f MigrationFile, startIdx int) (int, int, error) {
		ran = append(ran, f.Version)
		return 1, 1, nil
	})
	if err != nil {
		t.Fatalf("initial baseline+V003 apply failed: %v", err)
	}
	if res.Current != 3 || len(ran) != 1 || ran[0] != 3 {
		t.Fatalf("unexpected initial run: res=%+v ran=%v", res, ran)
	}

	// Now V004 arrives: V001 must remain absorbed by baseline=2, NOT re-queued!
	applied, err := store.ListRevisions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	status, err := ComputeStatus(applied, files)
	if err != nil {
		t.Fatalf("ComputeStatus after post-baseline migration failed: %v", err)
	}
	if len(status.Pending) != 1 || status.Pending[0].Version != 4 {
		t.Fatalf("pending = %+v, want only V004 (V001 must stay absorbed by baseline=2)", status.Pending)
	}
	ran = nil
	res2, err := Apply(t.Context(), store, files, 0, "v1.1.0", false, func(ctx context.Context, f MigrationFile, startIdx int) (int, int, error) {
		ran = append(ran, f.Version)
		return 1, 1, nil
	})
	if err != nil {
		t.Fatalf("V004 apply failed: %v", err)
	}
	if res2.Current != 4 || len(ran) != 1 || ran[0] != 4 {
		t.Fatalf("unexpected second run: res=%+v ran=%v", res2, ran)
	}
}

func TestListRevisionsIsStrictlyReadOnlyAndWorksOnReadOnlyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "readonly_check.db")
	rwDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rwDB.Close()
	if _, err := rwDB.Exec(`CREATE TABLE orders (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	// Open a strictly read-only handle; any CREATE TABLE will fail with readonly error.
	roDB, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer roDB.Close()

	roStore := &SQLHistoryStore{DB: roDB, Engine: "sqlite"}
	exists, err := roStore.TableExists(t.Context())
	if err != nil || exists {
		t.Fatalf("expected TableExists=false on fresh DB, got exists=%v err=%v", exists, err)
	}
	revs, err := roStore.ListRevisions(t.Context())
	if err != nil || len(revs) != 0 {
		t.Fatalf("ListRevisions on read-only DB without history table must succeed with 0 rows, got revs=%+v err=%v", revs, err)
	}

	// Even on a writable handle, ListRevisions MUST NOT silently create _dosql_schema_history.
	rwStore := &SQLHistoryStore{DB: rwDB, Engine: "sqlite"}
	revs, err = rwStore.ListRevisions(t.Context())
	if err != nil || len(revs) != 0 {
		t.Fatalf("ListRevisions on writable fresh DB failed: revs=%+v err=%v", revs, err)
	}
	exists, err = rwStore.TableExists(t.Context())
	if err != nil || exists {
		t.Fatalf("ListRevisions must NOT create _dosql_schema_history as a side effect; exists=%v err=%v", exists, err)
	}

	// After real Apply creates the table, read-only ListRevisions reads it cleanly.
	files := []MigrationFile{{Version: 1, VersionTag: "V001", Description: "init", FileName: "V001__init.sql", Checksum: "sha256:1"}}
	if _, err := Apply(t.Context(), rwStore, files, 0, "v1.0.0", false, func(context.Context, MigrationFile, int) (int, int, error) {
		return 1, 1, nil
	}); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	revs, err = roStore.ListRevisions(t.Context())
	if err != nil || len(revs) != 1 || revs[0].Version != 1 || revs[0].Status != "succeeded" {
		t.Fatalf("read-only ListRevisions after Apply = %+v err=%v", revs, err)
	}
}
