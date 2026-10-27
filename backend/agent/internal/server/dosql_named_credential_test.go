package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// namedDatabaseCredential creates an active credential with the given name and
// one grant on scope; the payload is a synthetic canary, never a real secret.
func namedDatabaseCredential(t *testing.T, f *executionLifecycleFixture, owner CreatedToken, name string, scope CredentialGrantScope) (CredentialResource, CredentialGrant) {
	t.Helper()
	var resource CredentialResource
	if err := json.Unmarshal(f.http(t, owner, http.MethodPost, "/v1/credentials",
		fmt.Sprintf(`{"name":%q,"scope":"personal","type":"opaque"}`, name), http.StatusOK), &resource); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: resource.ID, CreatedBy: resource.OwnerID, Activate: true,
		Payload: json.RawMessage(fmt.Sprintf(`{"data":{"connection_uri":%q}}`, credentialTargetPermissionCanary)),
	}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]interface{}{"scope": scope})
	if err != nil {
		t.Fatal(err)
	}
	var grant CredentialGrant
	if err := json.Unmarshal(f.http(t, owner, http.MethodPost, "/v1/credentials/"+resource.ID+"/grants",
		string(encoded), http.StatusOK), &grant); err != nil {
		t.Fatal(err)
	}
	return resource, grant
}

func TestDoSQLNamedCredentialSelection(t *testing.T) {
	database := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
	otherTarget := CredentialGrantScope{Cluster: "dev", Instance: "other", Namespace: "apps", Use: CredentialUseDatabase}
	opaque := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret}

	type fixture struct {
		f        *executionLifecycleFixture
		app      CredentialResource
		migrate  CredentialResource
		appGrant CredentialGrant
	}
	tests := []struct {
		name string
		// Given
		setup func(t *testing.T, fx *fixture)
		// When
		selection func(fx *fixture) string
		user      bool // use a non-admin user with only the given target grant
		userGrant ScopeGrant
		// Then
		wantIDs      func(fx *fixture) []string
		wantErr      error
		wantCategory string
	}{
		{
			name:      "no selection binds every database credential",
			selection: func(*fixture) string { return "" },
			wantIDs:   func(fx *fixture) []string { return []string{fx.app.ID, fx.migrate.ID} },
		},
		{
			name:      "migration account by name binds only it",
			selection: func(*fixture) string { return "xiaodi-migrate" },
			wantIDs:   func(fx *fixture) []string { return []string{fx.migrate.ID} },
		},
		{
			name:      "application account by name binds only it",
			selection: func(*fixture) string { return "xiaodi-app" },
			wantIDs:   func(fx *fixture) []string { return []string{fx.app.ID} },
		},
		{
			name:      "selection by credential ID",
			selection: func(fx *fixture) string { return fx.migrate.ID },
			wantIDs:   func(fx *fixture) []string { return []string{fx.migrate.ID} },
		},
		{
			name:      "target operator without credential ownership can select",
			selection: func(*fixture) string { return "xiaodi-migrate" },
			user:      true, userGrant: ScopeGrant{Cluster: "dev", Instance: "local"},
			wantIDs: func(fx *fixture) []string { return []string{fx.migrate.ID} },
		},
		{
			name:         "unknown name is not bound",
			selection:    func(*fixture) string { return "missing-db" },
			wantErr:      ErrDoSQLCredentialNotBound,
			wantCategory: "dosql_credential_not_bound",
		},
		{
			name: "name bound only to another target",
			setup: func(t *testing.T, fx *fixture) {
				namedDatabaseCredential(t, fx.f, fx.f.admin, "elsewhere-db", otherTarget)
			},
			selection:    func(*fixture) string { return "elsewhere-db" },
			wantErr:      ErrDoSQLCredentialNotBound,
			wantCategory: "dosql_credential_not_bound",
		},
		{
			name: "non-database binding cannot be selected",
			setup: func(t *testing.T, fx *fixture) {
				namedDatabaseCredential(t, fx.f, fx.f.admin, "plain-secret", opaque)
			},
			selection:    func(*fixture) string { return "plain-secret" },
			wantErr:      ErrDoSQLCredentialNotBound,
			wantCategory: "dosql_credential_not_bound",
		},
		{
			name: "revoked grant is not bound",
			setup: func(t *testing.T, fx *fixture) {
				fx.f.http(t, fx.f.admin, http.MethodDelete,
					"/v1/credentials/"+fx.app.ID+"/grants", `{"grant_id":"`+fx.appGrant.ID+`"}`, http.StatusOK)
			},
			selection:    func(*fixture) string { return "xiaodi-app" },
			wantErr:      ErrDoSQLCredentialNotBound,
			wantCategory: "dosql_credential_not_bound",
		},
		{
			name: "revoked credential is not bound",
			setup: func(t *testing.T, fx *fixture) {
				fx.f.http(t, fx.f.admin, http.MethodPost, "/v1/credentials/"+fx.migrate.ID+"/revoke", "", http.StatusOK)
			},
			selection:    func(*fixture) string { return "xiaodi-migrate" },
			wantErr:      ErrDoSQLCredentialNotBound,
			wantCategory: "dosql_credential_not_bound",
		},
		{
			name: "same name from two owners is ambiguous",
			setup: func(t *testing.T, fx *fixture) {
				owner := fx.f.user(t, "second-owner", ScopeGrant{Cluster: "*", Instance: "*"})
				namedDatabaseCredential(t, fx.f, owner, "xiaodi-migrate", database)
			},
			selection:    func(*fixture) string { return "xiaodi-migrate" },
			wantErr:      ErrCredentialAmbiguous,
			wantCategory: "credential_reference_ambiguous",
		},
		{
			name: "same name from two owners resolves by ID",
			setup: func(t *testing.T, fx *fixture) {
				owner := fx.f.user(t, "second-owner", ScopeGrant{Cluster: "*", Instance: "*"})
				namedDatabaseCredential(t, fx.f, owner, "xiaodi-migrate", database)
			},
			selection: func(fx *fixture) string { return fx.migrate.ID },
			wantIDs:   func(fx *fixture) []string { return []string{fx.migrate.ID} },
		},
		{
			name:         "user without the target grant is denied",
			selection:    func(*fixture) string { return "xiaodi-migrate" },
			user:         true, userGrant: ScopeGrant{Cluster: "dev", Instance: "other"},
			wantErr:      ErrCredentialForbidden,
			wantCategory: "credential_forbidden",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("n", 32))
			fx := &fixture{f: newExecutionLifecycleFixture(t, GatewayHubOptions{})}
			fx.app, fx.appGrant = namedDatabaseCredential(t, fx.f, fx.f.admin, "xiaodi-app", database)
			fx.migrate, _ = namedDatabaseCredential(t, fx.f, fx.f.admin, "xiaodi-migrate", database)
			if tt.setup != nil {
				tt.setup(t, fx)
			}
			token := fx.f.admin
			if tt.user {
				token = fx.f.user(t, "operator", tt.userGrant)
			}
			auth, err := fx.f.store.VerifyUserToken(token.Plaintext)
			if err != nil {
				t.Fatal(err)
			}

			authorized, err := fx.f.hub.resolveExplicitCredentials(auth, "dev", "local", "ALTER TABLE t ADD c INT",
				nil, ActionExec, "dosql", "auto", tt.selection(fx))

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				if got := credentialErrorCategory(err); got != tt.wantCategory {
					t.Fatalf("category = %q, want %q", got, tt.wantCategory)
				}
				if strings.Contains(err.Error(), credentialTargetPermissionCanary) {
					t.Fatal("selection error exposed credential payload")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			got := map[string]bool{}
			for _, item := range authorized {
				if item.Reference.Scope.Use != CredentialUseDatabase {
					t.Fatalf("binding use = %s, want database", item.Reference.Scope.Use)
				}
				got[item.Authorized.Resource.ID] = true
			}
			want := tt.wantIDs(fx)
			if len(authorized) != len(want) {
				t.Fatalf("bound %d credentials, want %d: %v", len(authorized), len(want), got)
			}
			for _, id := range want {
				if !got[id] {
					t.Fatalf("credential %s not bound: %v", id, got)
				}
			}
		})
	}
}
