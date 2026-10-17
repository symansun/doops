package server

import (
	"strings"
	"testing"
)

func TestDoSQLAutoBindsEveryTargetDatabaseCredentialByID(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	scope := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
	first, _, _ := credentialTargetPermissionResource(t, f, f.admin, scope)
	// A second owner's credential reuses the fixture name; name-based
	// resolution would turn this into credential_reference_ambiguous.
	owner := f.user(t, "second-owner", ScopeGrant{Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAll}})
	second, _, _ := credentialTargetPermissionResource(t, f, owner, scope)
	if first.Name != second.Name || first.ID == second.ID {
		t.Fatal("fixture must create two same-named credentials")
	}
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	authorized, err := f.hub.resolveExplicitCredentials(auth, "dev", "local", "SELECT 1", nil, ActionAsk, "dosql", "auto")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	got := map[string]bool{}
	for _, item := range authorized {
		if item.Reference.Scope.Use != CredentialUseDatabase {
			t.Fatalf("binding use = %s, want database", item.Reference.Scope.Use)
		}
		got[item.Authorized.Resource.ID] = true
	}
	if len(authorized) != 2 || !got[first.ID] || !got[second.ID] {
		t.Fatalf("want both credentials bound once by ID, got %d: %v", len(authorized), got)
	}
}
