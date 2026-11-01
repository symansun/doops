package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func seedOfflineAdminInstance(t *testing.T, store *GatewayStore) AgentStatus {
	t.Helper()
	lastSeen := time.Now().UTC().Add(-time.Hour)
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster: "dev", Instance: "retired", TokenID: "tok_agent", Remote: "127.0.0.1:1234",
		Generation: 1, ConnectedAt: lastSeen.Add(-time.Minute), LastSeen: lastSeen,
	}); err != nil {
		t.Fatalf("mark instance online: %v", err)
	}
	if err := store.MarkAgentOffline("dev", "retired", 1, lastSeen); err != nil {
		t.Fatalf("mark instance offline: %v", err)
	}
	statuses, err := store.ListAgentStatus()
	if err != nil || len(statuses) != 1 || statuses[0].Status != "offline" {
		t.Fatalf("offline seed = %#v, %v", statuses, err)
	}
	return statuses[0]
}

func TestAdminInstanceArchiveLifecycle(t *testing.T) {
	f := newAdminAuditFixture(t)
	seed := seedOfflineAdminInstance(t, f.store)
	body := map[string]string{"cluster": seed.Cluster, "instance": seed.Instance}

	response := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/archive", body)
	if response.Code != http.StatusOK {
		t.Fatalf("archive: HTTP %d: %s", response.Code, response.Body.String())
	}
	var archived adminInstanceInfo
	if err := json.NewDecoder(response.Body).Decode(&archived); err != nil {
		t.Fatal(err)
	}
	if !archived.Archived || archived.ArchivedAt.IsZero() || archived.ArchivedBy != f.adminAuth.UserID {
		t.Fatalf("archive response = %#v", archived)
	}
	assertAdminAudit(t, f.store, f.adminAuth, "/v1/admin/instances/archive", http.MethodPost, "success", http.StatusOK)

	firstArchivedAt := archived.ArchivedAt
	response = credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/archive", body)
	if response.Code != http.StatusOK {
		t.Fatalf("repeat archive: HTTP %d: %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&archived); err != nil {
		t.Fatal(err)
	}
	if !archived.ArchivedAt.Equal(firstArchivedAt) || archived.ArchivedBy != f.adminAuth.UserID {
		t.Fatalf("repeat archive changed metadata: %#v", archived)
	}

	list := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/instances", nil)
	if list.Code != http.StatusOK || !containsArchiveInstance(t, list.Body.Bytes(), true) {
		t.Fatalf("archive missing from list: HTTP %d: %s", list.Code, list.Body.String())
	}

	response = credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/unarchive", body)
	if response.Code != http.StatusOK {
		t.Fatalf("unarchive: HTTP %d: %s", response.Code, response.Body.String())
	}
	archived = adminInstanceInfo{}
	archived = adminInstanceInfo{}
	if err := json.NewDecoder(response.Body).Decode(&archived); err != nil {
		t.Fatal(err)
	}
	if archived.Archived || !archived.ArchivedAt.IsZero() || archived.ArchivedBy != "" {
		t.Fatalf("unarchive response = %#v", archived)
	}
	statuses, err := f.store.ListAgentStatus()
	if err != nil || len(statuses) != 1 || !statuses[0].ArchivedAt.IsZero() || statuses[0].ArchivedBy != "" {
		t.Fatalf("unarchive persistence = %#v, %v", statuses, err)
	}
	assertAdminAudit(t, f.store, f.adminAuth, "/v1/admin/instances/unarchive", http.MethodPost, "success", http.StatusOK)
}

func containsArchiveInstance(t *testing.T, data []byte, archived bool) bool {
	t.Helper()
	var response struct {
		Instances []adminInstanceInfo `json:"instances"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	for _, instance := range response.Instances {
		if instance.Cluster == "dev" && instance.Instance == "retired" && instance.Archived == archived {
			return true
		}
	}
	return false
}

func TestAdminInstanceArchiveRejectsUnauthorizedOrInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, token, path string
		body              any
		want              int
	}{
		{"unauthenticated", "", "/v1/admin/instances/archive", map[string]string{"cluster": "dev", "instance": "retired"}, http.StatusUnauthorized},
		{"ordinary user", "user", "/v1/admin/instances/archive", map[string]string{"cluster": "dev", "instance": "retired"}, http.StatusForbidden},
		{"missing instance", "admin", "/v1/admin/instances/archive", map[string]string{"cluster": "dev"}, http.StatusBadRequest},
		{"not found", "admin", "/v1/admin/instances/archive", map[string]string{"cluster": "dev", "instance": "missing"}, http.StatusNotFound},
		{"unarchive not found", "admin", "/v1/admin/instances/unarchive", map[string]string{"cluster": "dev", "instance": "missing"}, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdminAuditFixture(t)
			seedOfflineAdminInstance(t, f.store)
			token := f.adminToken
			if tc.token == "user" {
				token = f.userToken
			} else if tc.token == "" {
				token = ""
			}
			response := credentialHTTPRequest(t, f.mux, token, http.MethodPost, tc.path, tc.body)
			if response.Code != tc.want {
				t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
			}
			if tc.token == "admin" {
				assertAdminAudit(t, f.store, f.adminAuth, tc.path, http.MethodPost, "error", tc.want)
			}
			if tc.token == "user" {
				auth, err := f.store.VerifyUserToken(f.userToken)
				if err != nil {
					t.Fatal(err)
				}
				assertAdminAudit(t, f.store, auth, tc.path, http.MethodPost, "forbidden", tc.want)
			}
		})
	}
}

func TestAdminInstanceArchiveRejectsOnlineAndReconnectClearsArchive(t *testing.T) {
	f := newAdminAuditFixture(t)
	seedOfflineAdminInstance(t, f.store)
	body := map[string]string{"cluster": "dev", "instance": "retired"}
	if response := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/archive", body); response.Code != http.StatusOK {
		t.Fatalf("archive: HTTP %d: %s", response.Code, response.Body.String())
	}
	if err := f.store.MarkAgentOnline(AgentStatus{Cluster: "dev", Instance: "retired", Generation: 2, LastSeen: time.Now().UTC()}); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	statuses, err := f.store.ListAgentStatus()
	if err != nil || len(statuses) != 1 || !statuses[0].ArchivedAt.IsZero() || statuses[0].ArchivedBy != "" {
		t.Fatalf("reconnect did not unarchive: %#v, %v", statuses, err)
	}
	response := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/archive", body)
	if response.Code != http.StatusConflict {
		t.Fatalf("online archive: HTTP %d: %s", response.Code, response.Body.String())
	}
}
