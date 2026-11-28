package server

import (
	"errors"
	"strings"
	"testing"
)

func TestDoSQLAutoDiscoveryReportsWhyNoDatabaseBindingIsUsable(t *testing.T) {
	tests := []struct {
		name         string
		scope        *CredentialGrantScope
		unpromote    bool
		wantErr      error
		wantCategory string
	}{
		{
			name:         "never bound",
			wantErr:      ErrDoSQLDatabaseBindingMissing,
			wantCategory: "dosql_database_binding_missing",
		},
		{
			name:         "bound to another target",
			scope:        &CredentialGrantScope{Cluster: "other", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase},
			wantErr:      ErrDoSQLDatabaseBindingMissing,
			wantCategory: "dosql_database_binding_missing",
		},
		{
			name:         "bound but version not promoted",
			scope:        &CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase},
			unpromote:    true,
			wantErr:      ErrCredentialVersionNotFound,
			wantCategory: "credential_version_not_found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("c", 32))
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			if tt.scope != nil {
				_, version, _ := credentialTargetPermissionResource(t, f, f.admin, *tt.scope)
				if tt.unpromote {
					if _, err := f.store.db.Exec("UPDATE credential_versions SET state = ? WHERE id = ?",
						CredentialVersionStaged, version.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
			if err != nil {
				t.Fatal(err)
			}

			_, err = f.hub.resolveExplicitCredentials(auth, "dev", "local", "SELECT 1", nil, ActionAsk, "dosql", "auto", "")

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("resolve error = %v, want %v", err, tt.wantErr)
			}
			if got := credentialOperationErrorCategory(err); got != tt.wantCategory {
				t.Fatalf("category = %q, want %q", got, tt.wantCategory)
			}
		})
	}
}

func TestCredentialGrantDenialCategoryKeepsGrantDeniedIdentity(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrCredentialBindingTargetMismatch, "credential_binding_target_mismatch"},
		{ErrCredentialBindingScopeInvalid, "credential_binding_scope_invalid"},
		{ErrDoSQLDatabaseBindingMissing, "dosql_database_binding_missing"},
		{ErrCredentialGrantDenied, "credential_grant_denied"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if !errors.Is(tt.err, ErrCredentialGrantDenied) {
				t.Fatalf("%v must still match ErrCredentialGrantDenied so callers keep the 403 path", tt.err)
			}
			if got := credentialErrorCategory(tt.err); got != tt.want {
				t.Fatalf("HTTP category = %q, want %q", got, tt.want)
			}
			if got := credentialOperationErrorCategory(tt.err); got != tt.want {
				t.Fatalf("operation category = %q, want %q", got, tt.want)
			}
		})
	}
	if _, ok := credentialGrantDenialCategory(errors.New("unrelated")); ok {
		t.Fatal("unrelated errors must not be categorized as grant denials")
	}
}
