package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const verifyStoredScopeCanary = "synthetic-verify-canary-value"

// Verify only names the target and version; each binding is checked with the
// namespace, use and keys stored when it was granted.
func TestCredentialVerifyUsesStoredGrantScope(t *testing.T) {
	type verifyRequest struct {
		cluster, instance string
		extraScope        map[string]any
	}
	cases := []struct {
		name       string
		grantScope CredentialGrantScope
		request    verifyRequest
		asUser     bool
		wantStatus int
		wantBody   string
	}{
		{
			name:       "opaque binding without use or keys in the request",
			grantScope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps"},
			request:    verifyRequest{cluster: "dev", instance: "local"},
			wantStatus: http.StatusOK,
			wantBody:   `"status":"verified"`,
		},
		{
			name: "explicit opaque use and keys stored on the binding",
			grantScope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps",
				Use: CredentialUseOpaqueSecret, RequiredKeys: []string{"token"}},
			request:    verifyRequest{cluster: "dev", instance: "local"},
			wantStatus: http.StatusOK,
			wantBody:   `"status":"verified"`,
		},
		{
			name:       "stale request fields are ignored, not used to override the binding",
			grantScope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps"},
			request: verifyRequest{cluster: "dev", instance: "local", extraScope: map[string]any{
				"namespace": "elsewhere", "use": "imagePull", "required_keys": []string{"wrong"},
			}},
			wantStatus: http.StatusOK,
			wantBody:   `"status":"verified"`,
		},
		{
			name:       "target without a binding is denied",
			grantScope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps"},
			request:    verifyRequest{cluster: "dev", instance: "other"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "missing target is rejected",
			grantScope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps"},
			request:    verifyRequest{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "user without target or ownership is denied",
			grantScope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps"},
			request:    verifyRequest{cluster: "dev", instance: "local"},
			asUser:     true,
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := credentialImportFixture(t)
			var resource CredentialResource
			json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
				`{"name":"verify-stored","scope":"personal","type":"opaque"}`, http.StatusOK), &resource)
			base := "/v1/credentials/" + resource.ID
			var version CredentialVersion
			json.Unmarshal(f.http(t, f.admin, http.MethodPut, base+"/payload",
				`{"data":{"token":"`+verifyStoredScopeCanary+`"}}`, http.StatusOK), &version)
			grantBody, _ := json.Marshal(map[string]any{"scope": tc.grantScope})
			f.http(t, f.admin, http.MethodPost, base+"/grants", string(grantBody), http.StatusOK)

			scope := map[string]any{"cluster": tc.request.cluster, "instance": tc.request.instance}
			for key, value := range tc.request.extraScope {
				scope[key] = value
			}
			body, _ := json.Marshal(map[string]any{
				"session_id": "verify-stored", "version_id": version.ID, "scope": scope,
			})
			token := f.admin
			if tc.asUser {
				token = f.user(t, "outsider", ScopeGrant{Cluster: "elsewhere", Instance: "other"})
			}
			response := string(f.http(t, token, http.MethodPost, base+"/verify", string(body), tc.wantStatus))
			if tc.wantBody != "" && !strings.Contains(response, tc.wantBody) {
				t.Fatalf("verify response = %s, want %s", response, tc.wantBody)
			}
			if strings.Contains(response, verifyStoredScopeCanary) {
				t.Fatalf("verify response leaked payload: %s", response)
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			verifications, err := f.store.CredentialVerifications(resource.ID, version.ID)
			if err != nil || len(verifications) != 1 {
				t.Fatalf("recorded verifications = %#v, %v", verifications, err)
			}
			if verifications[0].Use != CredentialUseOpaqueSecret {
				t.Fatalf("verification use = %q, want stored/default opaqueSecret", verifications[0].Use)
			}
			f.http(t, f.admin, http.MethodPost, base+"/promote",
				`{"version_id":"`+version.ID+`"}`, http.StatusOK)
		})
	}
}
