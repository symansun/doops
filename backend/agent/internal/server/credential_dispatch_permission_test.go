package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTargetCredentialRechecksOperationAuthorityAfterMaterialization(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	user := f.user(t, "operator", ScopeGrant{Cluster: "dev", Instance: "local", Actions: []GatewayAction{ActionAsk}})
	scope := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
	resource, version, grant := credentialTargetPermissionResource(t, f, f.admin, scope)
	agent, client := newCredentialTargetPermissionAgent(t, f), f.client(t, user)
	const session = "permission-during-materialization"
	lifecycleSend(t, client, 2, "doops_agent_prompt", session, map[string]interface{}{
		"skill": "dosql", "operation": "ask", "instruction": "SELECT 1", "credential_refs": []string{resource.Name},
	})
	materialization := agent.call(t, client, "doops_credential_materialize", session)
	auth, err := f.store.VerifyUserToken(user.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := f.store.ListGrants(auth.UserID)
	if err != nil || len(authority) != 1 {
		t.Fatal("operator authority fixture is invalid")
	}
	if err := f.store.DeleteGrant(authority[0].ID); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(CredentialMaterialization{
		CredentialID: resource.ID, GrantID: grant.ID, VersionID: version.ID,
		ResourceName: credentialSecretName(resource.ID, grant.ID), Namespace: scope.Namespace,
		SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "7", Status: "verified",
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenLifecycleReplyText(t, agent.executionLifecycleAgent, materialization.executionLifecycleCall, string(metadata), false)
	cleanup := agent.call(t, client, "doops_credential_cleanup", session)
	tokenLifecycleReplyText(t, agent.executionLifecycleAgent, cleanup.executionLifecycleCall, "cleaned", false)
	lifecycleResponse(t, client, 2, "credential_authorization_changed")
	agent.noCalls(t)
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
}
