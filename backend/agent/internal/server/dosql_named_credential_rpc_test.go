package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/symansun/doops/backend/agent/api"
)

// Given an application account and a migration account both bound to the
// target, When a dosql prompt names the migration account, Then the Gateway
// materializes only that credential and the final prompt carries one binding.
func TestGatewayDoSQLPromptBindsOnlyTheNamedCredential(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("n", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: 3 * time.Second})
	scope := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
	namedDatabaseCredential(t, f, f.admin, "xiaodi-app", scope)
	migrate, migrateGrant := namedDatabaseCredential(t, f, f.admin, "xiaodi-migrate", scope)
	user := f.user(t, "migration-operator", ScopeGrant{Cluster: "dev", Instance: "local"})
	agent, client := newCredentialTargetPermissionAgent(t, f), f.client(t, user)
	const session = "named-credential-apply"
	lifecycleSend(t, client, 2, "doops_agent_prompt", session, map[string]interface{}{
		"skill": "dosql", "operation": "apply", "instruction": "DoSql 直接执行 数据库变更",
		"credential_mode": "auto", "sql": "ALTER TABLE app_user ADD COLUMN username VARCHAR(64)",
		"sql_credential": "xiaodi-migrate",
	})

	materialize := agent.call(t, client, "doops_credential_materialize", session)
	var request CredentialMaterializeRequest
	if err := json.Unmarshal(materialize.arguments, &request); err != nil {
		t.Fatal(err)
	}
	if request.CredentialID != migrate.ID || request.GrantID != migrateGrant.ID {
		t.Fatalf("materialized %s/%s, want the migration credential %s/%s",
			request.CredentialID, request.GrantID, migrate.ID, migrateGrant.ID)
	}
	metadata, err := json.Marshal(CredentialMaterialization{
		CredentialID: migrate.ID, GrantID: migrateGrant.ID, VersionID: request.VersionID, Namespace: "apps",
		ResourceName: credentialSecretName(migrate.ID, migrateGrant.ID), SecretType: "Opaque",
		Keys: []string{"connection_uri"}, ResourceVersion: "7", Status: "verified",
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenLifecycleReplyText(t, agent.executionLifecycleAgent, materialize.executionLifecycleCall, string(metadata), false)

	final := agent.call(t, client, "doops_agent_prompt", session)
	var prompt api.AgentPromptParams
	if err := json.Unmarshal(final.arguments, &prompt); err != nil {
		t.Fatal(err)
	}
	if len(prompt.CredentialBindings) != 1 || prompt.CredentialBindings[0].CredentialID != migrate.ID ||
		prompt.CredentialBindings[0].Name != "xiaodi-migrate" || prompt.SQLCredential != "xiaodi-migrate" {
		t.Fatalf("final prompt bindings = %#v, want only xiaodi-migrate", prompt.CredentialBindings)
	}
	agent.complete(t, final.executionLifecycleCall, "")
	lifecycleResponse(t, client, 2, "")
	agent.noCalls(t)
	for _, output := range [][]byte{final.arguments, materializeArgumentsWithoutPayload(t, materialize.arguments)} {
		if strings.Contains(string(output), "synthetic-target-permission-payload") {
			t.Fatal("credential payload escaped into the public prompt")
		}
	}
}

// materializeArgumentsWithoutPayload drops the private payload field; only the
// remaining metadata may be compared against public output rules.
func materializeArgumentsWithoutPayload(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "payload")
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// Given an invalid sql_credential request, When it reaches the Gateway, Then it
// is rejected before any credential is resolved or any Agent call is made.
func TestGatewayRejectsInvalidSQLCredentialSelection(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]interface{}
		wantErr string
	}{
		{
			name:    "non-dosql skill",
			args:    map[string]interface{}{"skill": "doweb", "operation": "ask", "instruction": "x", "sql_credential": "xiaodi-migrate"},
			wantErr: "sql_credential requires skill dosql",
		},
		{
			name:    "dosql without automatic mode",
			args:    map[string]interface{}{"skill": "dosql", "operation": "ask", "instruction": "x", "sql_credential": "xiaodi-migrate"},
			wantErr: "sql_credential requires skill dosql",
		},
		{
			name: "invalid reference",
			args: map[string]interface{}{"skill": "dosql", "operation": "ask", "instruction": "x",
				"credential_mode": "auto", "sql_credential": "postgres://u:p@db/x"},
			wantErr: "credential reference name is invalid",
		},
		{
			name: "unknown credential",
			args: map[string]interface{}{"skill": "dosql", "operation": "apply", "instruction": "x",
				"credential_mode": "auto", "sql": "SELECT 1", "sql_credential": "missing-db"},
			wantErr: "dosql_credential_not_bound",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("v", 32))
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: 3 * time.Second})
			namedDatabaseCredential(t, f, f.admin, "xiaodi-app",
				CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase})
			agent, client := newCredentialTargetPermissionAgent(t, f), f.client(t, f.admin)
			lifecycleSend(t, client, 2, "doops_agent_prompt", "invalid-selection", tt.args)
			lifecycleResponse(t, client, 2, tt.wantErr)
			agent.noCalls(t)
		})
	}
}
