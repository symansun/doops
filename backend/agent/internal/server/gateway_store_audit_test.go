package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"
)

// completedOnlyIndexes is what deleteCompletedAuditSQL removes from the shared
// audit purge fixture: expired ordinary rows plus upgrade rows whose terminal
// evidence exists in the same snapshot. Row 14 (accepted, no terminal row) is
// left for the batch statement, so it must survive this path.
var completedOnlyIndexes = []int{0, 4, 5, 8, 10, 13}

func openAuditDeleteParityStores(t *testing.T) map[string]*GatewayStore {
	t.Helper()
	return map[string]*GatewayStore{
		"sqlite": openAuditPurgeParitySQLiteStore(t),
		"mysql":  openAuditPurgeParityMySQLStore(t),
	}
}

func auditDeleteSurvivors(t *testing.T, store *GatewayStore, ids []int64, indexes []int) {
	t.Helper()
	for _, index := range indexes {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE id = ?", ids[index]).Scan(&count); err != nil {
			t.Fatalf("count audit row %d: %v", ids[index], err)
		}
		if count != 1 {
			t.Fatalf("audit row %d (index %d) must survive, remaining=%d", ids[index], index, count)
		}
	}
}

// TestDeleteAuditBeforeParityAcrossDialects is the store-level guard: the same
// fixture deleted through DeleteAuditBefore must remove the same rows on SQLite
// and on MySQL, and the active-status rows must survive on both.
func TestDeleteAuditBeforeParityAcrossDialects(t *testing.T) {
	stores := openAuditDeleteParityStores(t)
	cutoff := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	seeds, _ := auditPurgeParitySeeds(cutoff)

	ids := map[string][]int64{}
	removed := map[string][]int64{}
	counts := map[string]int64{}
	for name, store := range stores {
		ids[name] = auditPurgeInsertSeeds(t, store.db, seeds, "int41-delete")
		count, err := store.DeleteAuditBefore(cutoff)
		if err != nil {
			t.Fatalf("%s DeleteAuditBefore: %v", name, err)
		}
		counts[name] = count
		removed[name] = auditPurgeSorted(auditPurgeRemoved(ids[name], auditPurgeAllIDs(t, store.db)))
	}

	want := make([]int64, 0, len(completedOnlyIndexes))
	for _, index := range completedOnlyIndexes {
		want = append(want, ids["sqlite"][index])
	}
	want = auditPurgeSorted(want)

	if !reflect.DeepEqual(removed["sqlite"], removed["mysql"]) {
		t.Fatalf("one scan deleted different rows: sqlite=%v mysql=%v", removed["sqlite"], removed["mysql"])
	}
	if !reflect.DeepEqual(removed["sqlite"], want) {
		t.Fatalf("DeleteAuditBefore deleted %v, want %v", removed["sqlite"], want)
	}
	if counts["sqlite"] != int64(len(want)) || counts["mysql"] != int64(len(want)) {
		t.Fatalf("affected rows sqlite=%d mysql=%d, want %d", counts["sqlite"], counts["mysql"], len(want))
	}
	for name, store := range stores {
		// 1 running, 2 outcome_unknown, 3 executing are live or unresolved.
		auditDeleteSurvivors(t, store, ids[name], []int{1, 2, 3, 6, 7, 9, 11, 12, 14})
	}
}

// TestAuditPurgeHTTPParityAcrossDialects drives the real HTTP entry point: an
// admin DELETE /v1/audit?before=... through the registered mux. Both backends
// must delete the same rows, keep the protected statuses, and report the same
// deleted count.
func TestAuditPurgeHTTPParityAcrossDialects(t *testing.T) {
	previousPause := auditPurgePause
	auditPurgePause = func() {}
	t.Cleanup(func() { auditPurgePause = previousPause })

	stores := openAuditDeleteParityStores(t)
	cutoff := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	seeds, purgeIndexes := auditPurgeParitySeeds(cutoff)

	type purgeRun struct {
		ids     []int64
		removed []int64
		deleted int64
	}
	runs := map[string]purgeRun{}
	for name, store := range stores {
		// The handler needs only the store and the auth options. The full hub
		// constructor also builds the edge registry, which panics on a fresh
		// MySQL database because its bootstrap row is still owned by another
		// in-flight change; registering the real route on a bare hub keeps this
		// test on the HTTP path without depending on that file.
		hub := &GatewayHub{store: store, opts: GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true}}
		mux := http.NewServeMux()
		hub.RegisterRoutes(mux)
		server := httptest.NewServer(mux)

		ids := auditPurgeInsertSeeds(t, store.db, seeds, "int41-http")
		admin, err := store.CreateUser("admin")
		if err != nil {
			t.Fatalf("%s create admin: %v", name, err)
		}
		if err := store.SetUserAdmin(admin.ID, true); err != nil {
			t.Fatalf("%s grant admin: %v", name, err)
		}
		token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: admin.ID, Name: "admin"})
		if err != nil {
			t.Fatalf("%s create token: %v", name, err)
		}

		request, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/audit?before="+url.QueryEscape(cutoff.Format(time.RFC3339Nano)), nil)
		if err != nil {
			t.Fatalf("%s build request: %v", name, err)
		}
		request.Header.Set("Authorization", "Bearer "+token.Plaintext)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s HTTP purge: %v", name, err)
		}
		var payload auditPurgeResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		server.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s HTTP purge status=%d, want 200", name, response.StatusCode)
		}
		if decodeErr != nil {
			t.Fatalf("%s decode purge response: %v", name, decodeErr)
		}

		runs[name] = purgeRun{
			ids:     ids,
			removed: auditPurgeSorted(auditPurgeRemoved(ids, auditPurgeAllIDs(t, store.db))),
			deleted: payload.Deleted,
		}
	}

	want := make([]int64, 0, len(purgeIndexes))
	for _, index := range purgeIndexes {
		want = append(want, runs["sqlite"].ids[index])
	}
	want = auditPurgeSorted(want)

	if !reflect.DeepEqual(runs["sqlite"].removed, runs["mysql"].removed) {
		t.Fatalf("one batch deleted different rows: sqlite=%v mysql=%v", runs["sqlite"].removed, runs["mysql"].removed)
	}
	if !reflect.DeepEqual(runs["sqlite"].removed, want) {
		t.Fatalf("HTTP purge deleted %v, want %v", runs["sqlite"].removed, want)
	}
	if runs["sqlite"].deleted != runs["mysql"].deleted || runs["mysql"].deleted != int64(len(want)) {
		t.Fatalf("HTTP purge counts sqlite=%d mysql=%d, want %d", runs["sqlite"].deleted, runs["mysql"].deleted, len(want))
	}
	for name, store := range stores {
		auditDeleteSurvivors(t, store, runs[name].ids, []int{1, 2, 3, 6, 7, 9, 11, 12})
	}
}
