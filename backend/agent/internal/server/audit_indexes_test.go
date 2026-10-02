package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAuditIndexesRequireExplicitAuditedPlatformMaintenance(t *testing.T) {
	store := openCredentialTestStore(t)
	_, adminToken := createCredentialHTTPUser(t, store, "index-admin", true)
	_, userToken := createCredentialHTTPUser(t, store, "index-user", false)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	countIndexes := func() int {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='audit_events'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if countIndexes() != 0 {
		t.Fatal("startup implicitly created audit maintenance indexes")
	}
	path := "/v1/admin/audit/optimize"
	denied := credentialHTTPRequest(t, mux, userToken, http.MethodPost, path, map[string]string{"session_id": "indexes-user"})
	if denied.Code != http.StatusForbidden || countIndexes() != 0 {
		t.Fatalf("unauthorized maintenance: %d", denied.Code)
	}
	missing := credentialHTTPRequest(t, mux, adminToken, http.MethodPost, path, map[string]string{})
	if missing.Code != http.StatusBadRequest || countIndexes() != 0 {
		t.Fatalf("missing session maintenance: %d", missing.Code)
	}
	unknown := credentialHTTPRequest(t, mux, adminToken, http.MethodPost, path, map[string]string{"session_id": "indexes-arbitrary", "sql": "DROP TABLE audit_events"})
	if unknown.Code != http.StatusBadRequest || countIndexes() != 0 {
		t.Fatalf("arbitrary SQL accepted: %d", unknown.Code)
	}
	result := credentialHTTPRequest(t, mux, adminToken, http.MethodPost, path, map[string]string{"session_id": "indexes-admin"})
	if result.Code != http.StatusOK || countIndexes() != 3 {
		t.Fatalf("maintenance did not create all indexes: %d %s", result.Code, result.Body.String())
	}
	repeated := credentialHTTPRequest(t, mux, adminToken, http.MethodPost, path, map[string]string{"session_id": "indexes-repeat"})
	if repeated.Code != http.StatusOK || countIndexes() != 3 {
		t.Fatal("index maintenance was not idempotent")
	}
	var id, parent, unused int
	var plan string
	if err := store.db.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND session=? ORDER BY id DESC LIMIT 500`, "user", "session").Scan(&id, &parent, &unused, &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_audit_user_session_id") {
		t.Fatalf("session query still scans history: %s", plan)
	}
	events, err := store.ListAuditFiltered(AuditFilter{Session: "indexes-admin"})
	if err != nil || len(events) != 1 || events[0].Status != "success" || events[0].TokenID == "" || events[0].UserID == "" {
		t.Fatalf("maintenance audit missing: %#v %v", events, err)
	}
}

func TestAuditIndexMaintenanceRollsBackPartialDDLAndRejectsWrongDefinitions(t *testing.T) {
	store := openCredentialTestStore(t)
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

func TestAuditIndexesAreNotCreatedWhenAuditUnavailable(t *testing.T) {
	store := openCredentialTestStore(t)
	_, token := createCredentialHTTPUser(t, store, "index-no-audit", true)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	if _, err := store.db.Exec(`CREATE TRIGGER deny_index_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	result := credentialHTTPRequest(t, mux, token, http.MethodPost, "/v1/admin/audit/optimize", map[string]string{"session_id": "indexes-denied"})
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("audit unavailable = %d", result.Code)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='audit_events'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("DDL executed without audit")
	}
}
