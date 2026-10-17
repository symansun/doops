package server

import (
	"strings"
	"testing"
)

func TestDoSQLExplicitCredentialIsNotDuplicatedByAutoDiscovery(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	scope := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
	resource, _, _ := credentialTargetPermissionResource(t, f, f.admin, scope)
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	for name, refs := range map[string][]string{
		"explicit name":       {resource.Name},
		"explicit id":         {resource.ID},
		"repeated explicit":   {resource.Name, " " + resource.Name + " "},
		"auto discovery only": nil,
	} {
		t.Run(name, func(t *testing.T) {
			authorized, err := f.hub.resolveExplicitCredentials(
				auth, "dev", "local", "SELECT 1", refs, ActionAsk, "dosql", "auto",
			)
			if err != nil {
				t.Fatalf("resolve credentials: %v", err)
			}
			if len(authorized) != 1 || authorized[0].Authorized.Resource.ID != resource.ID {
				t.Fatalf("want exactly one binding for %s, got %d", resource.ID, len(authorized))
			}
		})
	}
}
