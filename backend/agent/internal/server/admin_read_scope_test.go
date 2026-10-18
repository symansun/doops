package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func newAdminReadScopeFixture(t *testing.T) *adminAuditFixture {
	t.Helper()
	f := newAdminAuditFixture(t)
	if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node", Actions: []GatewayAction{ActionAdmin}}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "sibling", Actions: []GatewayAction{ActionRead}}); err != nil {
		t.Fatal(err)
	}
	if f.store.UserIsPlatformAdmin(f.user.ID) {
		t.Fatal("fixture must be a target admin")
	}
	for _, target := range []struct{ cluster, instance string }{{"dev", "node"}, {"dev", "sibling"}, {"prod", "node"}} {
		if err := f.store.MarkAgentOnline(AgentStatus{Cluster: target.cluster, Instance: target.instance, Remote: target.cluster + "/" + target.instance}); err != nil {
			t.Fatal(err)
		}
		if target.cluster == "dev" && target.instance == "node" {
			continue
		}
		if _, err := f.store.UpsertProxyRoute(ProxyRouteInput{
			Host: target.instance + "." + target.cluster + ".example.test", Cluster: target.cluster, Instance: target.instance,
			Upstream: "http://localhost:8080", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		_, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		f.hub.client.registerActiveOperation(GatewayActiveOperation{
			Cluster: target.cluster, Instance: target.instance, Action: ActionExec,
		}, cancel)
	}
	return f
}

func assertAdminReadScopeNoAudit(t *testing.T, store *GatewayStore) {
	t.Helper()
	events, err := store.ListAudit(500)
	if err != nil || len(events) != 0 {
		t.Fatalf("read requests created mutation audit events: %v, %#v", err, events)
	}
}

func TestAdminReadScopeGlobalResources(t *testing.T) {
	f := newAdminReadScopeFixture(t)
	for _, path := range []string{
		"/v1/admin/users", "/v1/admin/grants", "/v1/admin/tokens",
	} {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				response := credentialHTTPRequest(t, f.mux, f.userToken, method, path, nil)
				if response.Code != http.StatusForbidden {
					t.Fatalf("target admin read global resources: %s HTTP %d: %s", method, response.Code, response.Body.String())
				}
			}
			response := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, path, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("platform admin lost global read: HTTP %d: %s", response.Code, response.Body.String())
			}
			response = credentialHTTPRequest(t, f.mux, "", http.MethodGet, path, nil)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated read = HTTP %d", response.Code)
			}
		})
	}
	assertAdminReadScopeNoAudit(t, f.store)
}

func TestAdminReadScopeOperationsExactTarget(t *testing.T) {
	f := newAdminReadScopeFixture(t)
	for _, query := range []string{
		"cluster=dev&instance=sibling", "cluster=prod&instance=node", "cluster=dev&instance=*",
		"", "cluster=dev", "instance=node", "cluster=dev&instance=sibling&id=" + f.operationID,
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := credentialHTTPRequest(t, f.mux, f.userToken, method, "/v1/admin/operations?"+query, nil)
			if response.Code != http.StatusForbidden {
				t.Errorf("target admin read unauthorized operations: %s %s HTTP %d: %s", method, query, response.Code, response.Body.String())
			}
		}
	}
	for _, tc := range []struct{ token, cluster, instance string }{
		{f.userToken, "dev", "node"}, {f.adminToken, "dev", "node"},
		{f.adminToken, "dev", "sibling"}, {f.adminToken, "prod", "node"},
	} {
		response := credentialHTTPRequest(t, f.mux, tc.token, http.MethodGet,
			"/v1/admin/operations?cluster="+tc.cluster+"&instance="+tc.instance, nil)
		var result struct {
			Operations []GatewayActiveOperation `json:"operations"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Operations) != 1 {
			t.Fatalf("authorized operations read failed: HTTP %d: %s", response.Code, response.Body.String())
		}
		if op := result.Operations[0]; op.Cluster != tc.cluster || op.Instance != tc.instance {
			t.Fatalf("operations escaped query target: %#v", op)
		}
	}
	assertAdminReadScopeNoAudit(t, f.store)
}

func TestAdminReadScopeCollectionsFilterResources(t *testing.T) {
	f := newAdminReadScopeFixture(t)
	for _, tc := range []struct{ path, key string }{
		{"/v1/admin/instances", "instances"}, {"/v1/admin/proxy-routes", "routes"},
	} {
		for _, role := range []struct {
			name, token string
			count       int
		}{{"target", f.userToken, 1}, {"platform", f.adminToken, 3}} {
			t.Run(tc.key+"/"+role.name, func(t *testing.T) {
				response := credentialHTTPRequest(t, f.mux, role.token, http.MethodGet, tc.path, nil)
				var result map[string][]struct {
					Cluster  string `json:"cluster"`
					Instance string `json:"instance"`
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result[tc.key]) != role.count {
					t.Fatalf("collection scope mismatch: HTTP %d: %s", response.Code, response.Body.String())
				}
				for _, resource := range result[tc.key] {
					if role.name == "target" && (resource.Cluster != "dev" || resource.Instance != "node") {
						t.Fatalf("sibling resource leaked: %#v", resource)
					}
				}
			})
		}
	}
	assertAdminReadScopeNoAudit(t, f.store)
}

func TestAdminReadScopeWrapperHEADAndUnsupportedMethods(t *testing.T) {
	f := newAdminReadScopeFixture(t)
	for _, tc := range []struct {
		name, route, query, token string
		want                      int
	}{
		{"target-global", "/v1/admin/users", "", f.userToken, http.StatusForbidden},
		{"platform-global", "/v1/admin/users", "", f.adminToken, http.StatusOK},
		{"allowed-target", "/v1/admin/operations", "?cluster=dev&instance=node", f.userToken, http.StatusOK},
		{"sibling-target", "/v1/admin/operations", "?cluster=dev&instance=sibling", f.userToken, http.StatusForbidden},
		{"platform-target", "/v1/admin/operations", "?cluster=dev&instance=sibling", f.adminToken, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := f.hub.auditAdminMutation(tc.route, func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			response := credentialHTTPRequest(t, handler, tc.token, http.MethodHead, tc.route+tc.query, nil)
			if response.Code != tc.want || called != (tc.want == http.StatusOK) {
				t.Fatalf("HEAD bypassed authorization: HTTP %d, handler called=%t", response.Code, called)
			}
		})
	}
	for _, method := range []string{http.MethodOptions, http.MethodTrace, http.MethodConnect} {
		for _, path := range []string{"/v1/admin/instances", "/v1/admin/proxy-routes"} {
			for _, token := range []string{f.userToken, f.adminToken} {
				response := credentialHTTPRequest(t, f.mux, token, method, path, nil)
				if response.Code != http.StatusMethodNotAllowed {
					t.Errorf("unsupported method reached read handler: %s %s HTTP %d", method, path, response.Code)
				}
			}
		}
	}
	assertAdminReadScopeNoAudit(t, f.store)
}
