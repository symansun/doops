package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

func TestTargetManifestAndDirectoryAuthorization(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			var store *GatewayStore
			if engine == "mysql" {
				store = openGatewayMySQLResourceTestStore(t)
				t.Cleanup(func() { store.Close() })
			} else {
				t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
				store = openCredentialTestStore(t)
			}
			hub := NewGatewayHub(store, GatewayHubOptions{})
			t.Cleanup(hub.CloseTunnels)
			mux := http.NewServeMux()
			hub.RegisterRoutes(mux)

			first := workspaceTarget{Cluster: "lab", Instance: "alpha"}
			second := workspaceTarget{Cluster: "lab", Instance: "beta"}
			third := workspaceTarget{Cluster: "west", Instance: "alpha"}
			targets := []workspaceTarget{first, second, third}
			for _, target := range targets {
				if err := store.MarkAgentOnline(AgentStatus{Cluster: target.Cluster, Instance: target.Instance, Generation: 1}); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkAgentOffline(target.Cluster, target.Instance, 1, time.Now()); err != nil {
					t.Fatal(err)
				}
			}

			for _, tc := range []struct {
				name    string
				grants  []ScopeGrant
				admin   bool
				visible []workspaceTarget
			}{
				{name: "no_grant"},
				{name: "exact", grants: []ScopeGrant{{Cluster: "lab", Instance: "alpha"}}, visible: []workspaceTarget{first}},
				{name: "cluster_case", grants: []ScopeGrant{{Cluster: "Lab", Instance: "*"}}},
				{name: "instance_case", grants: []ScopeGrant{{Cluster: "*", Instance: "Alpha"}}},
				{name: "cluster_wildcard", grants: []ScopeGrant{{Cluster: "*", Instance: "alpha"}}, visible: []workspaceTarget{first, third}},
				{name: "instance_wildcard", grants: []ScopeGrant{{Cluster: "lab", Instance: "*"}}, visible: []workspaceTarget{first, second}},
				{name: "two_grants", grants: []ScopeGrant{{Cluster: "west", Instance: "alpha"}, {Cluster: "lab", Instance: "beta"}}, visible: []workspaceTarget{second, third}},
				{name: "all_targets", grants: []ScopeGrant{{Cluster: "*", Instance: "*"}}, visible: targets},
				{name: "admin", admin: true, visible: targets},
			} {
				t.Run(tc.name, func(t *testing.T) {
					user, token := createCredentialHTTPUser(t, store, tc.name, tc.admin)
					for _, grant := range tc.grants {
						if err := store.GrantUser(user.ID, grant); err != nil {
							t.Fatal(err)
						}
					}
					for _, view := range []string{"directory", "targets"} {
						response := credentialHTTPRequest(t, mux, token, http.MethodGet, "/v1/assets/workspace?view="+view, nil)
						if view == "targets" && !tc.admin {
							if response.Code != http.StatusForbidden {
								t.Errorf("%s: HTTP %d, want 403", view, response.Code)
							}
							continue
						}
						var directory struct{ Targets []workspaceTarget }
						if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &directory) != nil || !slices.Equal(directory.Targets, tc.visible) {
							t.Errorf("%s: HTTP %d, targets %v, want %v", view, response.Code, directory.Targets, tc.visible)
						}
					}
					checkManifest := func(target workspaceTarget, want int) {
						t.Helper()
						query := url.Values{"cluster": {target.Cluster}, "instance": {target.Instance}}
						response := credentialHTTPRequest(t, mux, token, http.MethodGet, "/v1/assets/target-manifest?"+query.Encode(), nil)
						if response.Code != want {
							t.Errorf("manifest %v: HTTP %d, want %d", target, response.Code, want)
						}
					}
					for _, target := range targets {
						want := http.StatusForbidden
						if slices.Contains(tc.visible, target) {
							want = http.StatusOK
						}
						checkManifest(target, want)
					}
					if tc.admin || tc.name == "all_targets" {
						checkManifest(workspaceTarget{Cluster: "Lab", Instance: "alpha"}, http.StatusNotFound)
						checkManifest(workspaceTarget{Cluster: "lab", Instance: "Alpha"}, http.StatusNotFound)
					}
					refresh := credentialHTTPRequest(t, mux, token, http.MethodPost, "/v1/assets/target-manifest", map[string]string{
						"action": "refresh", "cluster": first.Cluster, "instance": first.Instance,
					})
					want := http.StatusForbidden
					if slices.Contains(tc.visible, first) {
						want = http.StatusConflict // The registered target is offline.
					}
					if refresh.Code != want {
						t.Errorf("refresh: HTTP %d, want %d", refresh.Code, want)
					}
				})
			}
		})
	}
}
