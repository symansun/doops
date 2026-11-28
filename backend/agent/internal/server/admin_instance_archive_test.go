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
	if list.Code != http.StatusOK || containsArchiveInstance(t, list.Body.Bytes(), true) {
		t.Fatalf("archived instance must be hidden from default list: HTTP %d: %s", list.Code, list.Body.String())
	}
	history := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/instances?include=history", nil)
	if history.Code != http.StatusOK || !containsArchiveInstance(t, history.Body.Bytes(), true) {
		t.Fatalf("archived instance missing from history list: HTTP %d: %s", history.Code, history.Body.String())
	}

	response = credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/unarchive", body)
	if response.Code != http.StatusOK {
		t.Fatalf("unarchive: HTTP %d: %s", response.Code, response.Body.String())
	}
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
	listAfterUnarchive := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/instances", nil)
	if listAfterUnarchive.Code != http.StatusOK || !containsArchiveInstance(t, listAfterUnarchive.Body.Bytes(), false) {
		t.Fatalf("unarchived recent instance missing from default list: HTTP %d: %s", listAfterUnarchive.Code, listAfterUnarchive.Body.String())
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

func TestAdminInstanceDeleteDecommissionsOfflineAndReportsRemaining(t *testing.T) {
	f := newAdminAuditFixture(t)
	seed := seedOfflineAdminInstance(t, f.store)
	if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: seed.Cluster, Instance: seed.Instance}); err != nil {
		t.Fatalf("grant user: %v", err)
	}
	if _, err := f.store.UpsertProxyRoute(ProxyRouteInput{
		Host: "retired.example.test", Cluster: seed.Cluster, Instance: seed.Instance,
		Upstream: "http://127.0.0.1:9000", Enabled: true,
	}); err != nil {
		t.Fatalf("proxy route: %v", err)
	}
	credActive, err := f.store.CreateCredential(CredentialCreateRequest{
		Name: "retired-active-cred", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
		OwnerID: f.adminAuth.UserID, CreatedBy: f.adminAuth.UserID,
	})
	if err != nil {
		t.Fatalf("create active cred: %v", err)
	}
	if _, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: credActive.ID, CreatedBy: f.adminAuth.UserID,
		Scope: CredentialGrantScope{Cluster: seed.Cluster, Instance: seed.Instance, Namespace: "default", Use: CredentialUseOpaqueSecret},
	}); err != nil {
		t.Fatalf("create active cred grant: %v", err)
	}
	credRevoking, err := f.store.CreateCredential(CredentialCreateRequest{
		Name: "retired-revoking-cred", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
		OwnerID: f.adminAuth.UserID, CreatedBy: f.adminAuth.UserID,
	})
	if err != nil {
		t.Fatalf("create revoking cred: %v", err)
	}
	revGrant, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: credRevoking.ID, CreatedBy: f.adminAuth.UserID,
		Scope: CredentialGrantScope{Cluster: seed.Cluster, Instance: seed.Instance, Namespace: "default", Use: CredentialUseOpaqueSecret},
	})
	if err != nil {
		t.Fatalf("create revoking cred grant: %v", err)
	}
	if _, err := f.store.BeginCredentialGrantRevocation(credRevoking.ID, revGrant.ID, f.adminAuth.UserID); err != nil {
		t.Fatalf("begin grant revocation: %v", err)
	}

	body := map[string]string{"cluster": seed.Cluster, "instance": seed.Instance}
	response := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/delete", body)
	if response.Code != http.StatusOK {
		t.Fatalf("delete offline instance: HTTP %d: %s", response.Code, response.Body.String())
	}
	var result AgentDecommissionResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Deleted || result.Cluster != seed.Cluster || result.Instance != seed.Instance {
		t.Fatalf("delete result = %#v", result)
	}
	if result.Remaining.ActiveCredentialGrants != 1 || result.Remaining.RevokingCredentialGrants != 1 ||
		result.Remaining.ProxyRoutes != 1 || result.Remaining.UserGrants != 1 {
		t.Fatalf("remaining counts = %+v, want all 1", result.Remaining)
	}
	assertAdminAudit(t, f.store, f.adminAuth, "/v1/admin/instances/delete", http.MethodPost, "success", http.StatusOK)

	statuses, err := f.store.ListAgentStatus()
	if err != nil || len(statuses) != 0 {
		t.Fatalf("agent_status after delete = %#v, %v", statuses, err)
	}
	// Repeating delete returns 404 now that the row is gone.
	again := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodPost, "/v1/admin/instances/delete", body)
	if again.Code != http.StatusNotFound {
		t.Fatalf("repeat delete: HTTP %d: %s", again.Code, again.Body.String())
	}
	// Authority records stay intact.
	grants, err := f.store.ListGrants(f.user.ID)
	if err != nil {
		t.Fatalf("list user grants after delete: %v", err)
	}
	foundGrant := false
	for _, g := range grants {
		if g.Cluster == seed.Cluster && g.Instance == seed.Instance {
			foundGrant = true
			break
		}
	}
	if !foundGrant {
		t.Fatalf("user grant for %s/%s did not survive delete: %#v", seed.Cluster, seed.Instance, grants)
	}
	routes, err := f.store.ListProxyRoutes()
	if err != nil {
		t.Fatalf("list proxy routes after delete: %v", err)
	}
	foundRoute := false
	for _, r := range routes {
		if r.Cluster == seed.Cluster && r.Instance == seed.Instance {
			foundRoute = true
			break
		}
	}
	if !foundRoute {
		t.Fatalf("proxy route for %s/%s did not survive delete: %#v", seed.Cluster, seed.Instance, routes)
	}
}

func TestAdminInstanceDeleteRejectsOnlineAndUnauthorized(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		body        any
		online      bool
		want        int
	}{
		{"unauthenticated", "", map[string]string{"cluster": "dev", "instance": "retired"}, false, http.StatusUnauthorized},
		{"ordinary user", "user", map[string]string{"cluster": "dev", "instance": "retired"}, false, http.StatusForbidden},
		{"missing instance", "admin", map[string]string{"cluster": "dev"}, false, http.StatusBadRequest},
		{"not found", "admin", map[string]string{"cluster": "dev", "instance": "missing"}, false, http.StatusNotFound},
		{"online instance", "admin", map[string]string{"cluster": "dev", "instance": "retired"}, true, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdminAuditFixture(t)
			seedOfflineAdminInstance(t, f.store)
			if tc.online {
				if err := f.store.MarkAgentOnline(AgentStatus{Cluster: "dev", Instance: "retired", Generation: 2, LastSeen: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			token := f.adminToken
			if tc.token == "user" {
				token = f.userToken
			} else if tc.token == "" {
				token = ""
			}
			response := credentialHTTPRequest(t, f.mux, token, http.MethodPost, "/v1/admin/instances/delete", tc.body)
			if response.Code != tc.want {
				t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
			}
		})
	}
}
