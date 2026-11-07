package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// dropStartupAuditIndexes recreates a pre-index database so maintenance
// failure paths can be exercised after startup already built the indexes.
func dropStartupAuditIndexes(t *testing.T, store *GatewayStore) {
	t.Helper()
	for _, name := range []string{"idx_audit_user_id", "idx_audit_user_session_id", "idx_audit_target_id"} {
		if _, err := store.db.Exec(`DROP INDEX IF EXISTS ` + name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuditIndexMaintenanceRollsBackPartialDDLAndRejectsWrongDefinitions(t *testing.T) {
	store := openCredentialTestStore(t)
	dropStartupAuditIndexes(t, store)
	if _, err := store.db.Exec(`CREATE TABLE idx_audit_target_id(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.optimizeAuditIndexes(t.Context()); err == nil {
		t.Fatal("conflicting third index was accepted")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='audit_events'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed maintenance retained partial DDL")
	}
	if _, err := store.db.Exec(`DROP TABLE idx_audit_target_id; CREATE INDEX idx_audit_user_session_id ON audit_events(session, id DESC)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.optimizeAuditIndexes(t.Context()); err == nil {
		t.Fatal("existing wrong index definition was accepted")
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='audit_events'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("definition mismatch retained new indexes")
	}
}

func TestAuditIndexMaintenanceCancellationReleasesTheStoreConnection(t *testing.T) {
	store := openCredentialTestStore(t)
	dropStartupAuditIndexes(t, store)
	rows, err := store.db.Query(`SELECT 1`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := store.optimizeAuditIndexes(ctx); err == nil {
		rows.Close()
		t.Fatal("blocked maintenance ignored cancellation")
	}
	rows.Close()
	available, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	var one int
	if err := store.db.QueryRowContext(available, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("store connection did not recover: %d %v", one, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='audit_events'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("canceled maintenance changed indexes")
	}
}

func TestGatewayStoreStartupCreatesAuditIndexes(t *testing.T) {
	path := t.TempDir() + "/gateway.db"
	for range 2 {
		store, err := OpenGatewayStore(path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='audit_events' AND name LIKE 'idx_audit_%'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("startup audit indexes = %d, want 3", count)
		}
		var id, parent, unused int
		var plan string
		if err := store.db.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND session=? ORDER BY id DESC LIMIT 500`, "u", "s").Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, "idx_audit_user_session_id") {
			t.Fatalf("session audit query does not use its index: %s", plan)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
