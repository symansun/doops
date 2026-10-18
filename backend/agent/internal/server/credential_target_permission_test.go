package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
)

const credentialTargetPermissionCanary = "postgres://app:synthetic-target-permission-payload@db.invalid/app"

type credentialTargetPermissionCall struct {
	executionLifecycleCall
	arguments json.RawMessage
}

type credentialTargetPermissionAgent struct {
	*executionLifecycleAgent
	fixture *executionLifecycleFixture
	calls   chan credentialTargetPermissionCall
}

// The existing lifecycle Agent discards arguments; retain them here to inspect
// the private materialization payload and the metadata-only final prompt.
func newCredentialTargetPermissionAgent(t *testing.T, f *executionLifecycleFixture) *credentialTargetPermissionAgent {
	t.Helper()
	agent := &credentialTargetPermissionAgent{
		executionLifecycleAgent: &executionLifecycleAgent{conn: dialFakeGatewayAgent(t, f.server.URL, "dev", "local")},
		fixture:                 f,
		calls:                   make(chan credentialTargetPermissionCall, 16),
	}
	done, stop := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		_ = agent.conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("credential permission Agent did not stop")
		}
		requireEventually(t, 3*time.Second, func() bool { return f.hub.getAgent("dev", "local") == nil })
	})
	go func() {
		defer close(done)
		for {
			var message api.JSONRPCRequest
			if err := agent.conn.ReadJSON(&message); err != nil {
				return
			}
			switch message.Method {
			case "initialize":
				if err := agent.write(map[string]interface{}{
					"jsonrpc": "2.0", "id": message.ID, "result": gatewayTestInitializeResult("credential-target-permission"),
				}); err != nil {
					return
				}
			case "tools/call":
				var params api.ToolCallParams
				if json.Unmarshal(message.Params, &params) != nil {
					return
				}
				id, _ := numericID(message.ID)
				call := credentialTargetPermissionCall{
					executionLifecycleCall: executionLifecycleCall{id: id, name: params.Name, session: extractSession(params.Arguments)},
					arguments:              params.Arguments,
				}
				select {
				case agent.calls <- call:
				case <-stop:
					return
				}
			case "tools/cancel":
				if err := agent.write(map[string]interface{}{
					"jsonrpc": "2.0", "id": message.ID, "result": map[string]interface{}{"canceled": true},
				}); err != nil {
					return
				}
			}
		}
	}()
	waitForGatewayAgent(t, f.hub, "dev", "local")
	return agent
}

func (a *credentialTargetPermissionAgent) call(t *testing.T, client *websocket.Conn, name, session string) credentialTargetPermissionCall {
	t.Helper()
	select {
	case call := <-a.calls:
		if call.name != name || call.session != session {
			t.Fatalf("dispatched %s/%s, want %s/%s", call.name, call.session, name, session)
		}
		return call
	case <-time.After(3 * time.Second):
		if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		response := readUntilID(t, client, float64(2))
		events, err := a.fixture.store.ListAuditFiltered(AuditFilter{Session: session})
		t.Fatalf("%s/%s did not reach the Agent: root=%#v audits=%#v auditError=%v", name, session, response, events, err)
		return credentialTargetPermissionCall{}
	}
}

func (a *credentialTargetPermissionAgent) noCalls(t *testing.T) {
	t.Helper()
	select {
	case call := <-a.calls:
		t.Fatalf("unexpected Agent dispatch: %s/%s", call.name, call.session)
	default:
	}
}

func credentialTargetPermissionResource(t *testing.T, f *executionLifecycleFixture, owner CreatedToken, scope CredentialGrantScope) (CredentialResource, CredentialVersion, CredentialGrant) {
	t.Helper()
	var resource CredentialResource
	if err := json.Unmarshal(f.http(t, owner, http.MethodPost, "/v1/credentials",
		`{"name":"permission-db","scope":"personal","type":"opaque"}`, http.StatusOK), &resource); err != nil {
		t.Fatal(err)
	}
	// Seed the active version in the temporary fixture store; promotion is outside
	// this runtime test, and no real Agent or database is involved.
	version, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: resource.ID, CreatedBy: resource.OwnerID, Activate: true,
		Payload: json.RawMessage(fmt.Sprintf(`{"data":{"connection_uri":%q}}`, credentialTargetPermissionCanary)),
	})
	if err != nil {
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
	if resource.State != CredentialStateActive || version.State != CredentialVersionActive || grant.State != CredentialStateActive {
		t.Fatal("fixture must have an active credential, version and grant")
	}
	return resource, version, grant
}

func credentialTargetPermissionAudit(t *testing.T, f *executionLifecycleFixture, auth TokenAuth, session string, action GatewayAction, status string, count int, public interface{}) {
	t.Helper()
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	events, err := f.store.ListAuditFiltered(AuditFilter{Session: session})
	if err != nil || len(events) != count {
		t.Fatalf("runtime audit count = %d, want %d: %v", len(events), count, err)
	}
	rootCount := 0
	for _, event := range events {
		if event.UserID != auth.UserID || event.TokenID != auth.TokenID || event.Cluster != "dev" ||
			event.Instance != "local" || event.Status != status || event.EndedAt.IsZero() {
			t.Fatalf("runtime audit lost request identity or terminal state: %#v", event)
		}
		if event.Action == action {
			rootCount++
		} else if event.Action != ActionCredentialUse {
			t.Fatalf("unexpected runtime audit action: %s", event.Action)
		}
	}
	if rootCount != 1 {
		t.Fatalf("root audit count = %d, want 1 (scope planning must not run)", rootCount)
	}
	allEvents, err := f.store.ListAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []interface{}{public, allEvents} {
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{credentialTargetPermissionCanary, "synthetic-target-permission-payload", `"payload"`, "ciphertext"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("public output or audit exposed credential material: %s", forbidden)
			}
		}
	}
}

func TestTargetCredentialPromptUsesOperationAction(t *testing.T) {
	for _, operation := range []struct {
		name   string
		action GatewayAction
	}{{"ask", ActionAsk}, {"apply", ActionExec}} {
		t.Run(operation.name, func(t *testing.T) {
			t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("p", 32))
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: 3 * time.Second})
			user := f.user(t, "database-operator", ScopeGrant{Cluster: "dev", Instance: "local"})
			auth, err := f.store.VerifyUserToken(user.Plaintext)
			if err != nil {
				t.Fatal(err)
			}
			grants, err := f.store.ListGrants(auth.UserID)
			if err != nil || len(grants) != 1 || grants[0].Cluster != "dev" || grants[0].Instance != "local" {
				t.Fatalf("request user must have only the dev/local grant: %#v, %v", grants, err)
			}
			scope := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
			resource, version, grant := credentialTargetPermissionResource(t, f, f.admin, scope)
			if resource.OwnerID == auth.UserID {
				t.Fatal("runtime fixture must use another user's credential")
			}
			agent, client := newCredentialTargetPermissionAgent(t, f), f.client(t, user)
			session := "database-" + operation.name
			instruction := "query: SELECT 1 AS permission_probe"
			if operation.name == "apply" {
				instruction = "apply the authorized synthetic database change"
			}
			lifecycleSend(t, client, 2, "doops_agent_prompt", session, map[string]interface{}{
				"skill": "dosql", "operation": operation.name, "instruction": instruction, "credential_refs": []string{resource.Name},
			})
			first := agent.call(t, client, "doops_credential_materialize", session)
			foundRoot := false
			for _, op := range f.hub.listActiveOperations() {
				if op.Session == session && op.Kind == "rpc" {
					foundRoot = true
					if op.Action != operation.action || op.Resource != "target-mutation:dev/local" || op.UserID != auth.UserID {
						t.Fatalf("materialization changed the root action or target lease: %#v", op)
					}
				}
			}
			if !foundRoot {
				t.Fatal("materialization must run under the active ask/exec root request")
			}
			var request CredentialMaterializeRequest
			if err := json.Unmarshal(first.arguments, &request); err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal(request.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if request.Cluster != "dev" || request.Instance != "local" || request.CredentialID != resource.ID ||
				request.GrantID != grant.ID || request.VersionID != version.ID || request.CredentialType != CredentialTypeOpaque ||
				!reflect.DeepEqual(request.Scope, scope) || !reflect.DeepEqual(request.RequiredKeys, []string{"connection_uri"}) ||
				len(payload.Data) != 1 || payload.Data["connection_uri"] != credentialTargetPermissionCanary {
				t.Fatal("private materialization did not bind the active database credential and exact target")
			}
			metadata, err := json.Marshal(CredentialMaterialization{
				CredentialID: resource.ID, GrantID: grant.ID, VersionID: version.ID, Namespace: "apps",
				ResourceName: credentialSecretName(resource.ID, grant.ID), SecretType: "Opaque",
				Keys: []string{"connection_uri"}, ResourceVersion: "7", Status: "verified",
			})
			if err != nil {
				t.Fatal(err)
			}
			tokenLifecycleReplyText(t, agent.executionLifecycleAgent, first.executionLifecycleCall, string(metadata), false)
			final := agent.call(t, client, "doops_agent_prompt", session)
			var prompt api.AgentPromptParams
			if err := json.Unmarshal(final.arguments, &prompt); err != nil {
				t.Fatal(err)
			}
			wantBinding := api.AgentCredentialBinding{
				Name: resource.Name, Use: string(CredentialUseDatabase), Namespace: "apps",
				ResourceName: credentialSecretName(resource.ID, grant.ID), SecretType: "Opaque",
				Keys: []string{"connection_uri"}, ResourceVersion: "7", CredentialID: resource.ID, GrantID: grant.ID, VersionID: version.ID,
			}
			if prompt.Skill != "dosql" || prompt.Operation != operation.name || prompt.SessionID != session ||
				prompt.Instruction != instruction || len(prompt.CredentialRefs) != 0 ||
				!reflect.DeepEqual(prompt.CredentialBindings, []api.AgentCredentialBinding{wantBinding}) {
				t.Fatalf("final prompt did not carry the verified database binding: %#v", prompt)
			}
			agent.complete(t, final.executionLifecycleCall, "")
			response := lifecycleResponse(t, client, 2, "")
			if resultContentText(response["result"].(map[string]interface{})) != "completed" {
				t.Fatalf("root request lost the final Agent response: %#v", response)
			}
			agent.noCalls(t)
			credentialTargetPermissionAudit(t, f, auth, session, operation.action, "success", 3,
				[]interface{}{json.RawMessage(final.arguments), response})
		})
	}
}

func TestTargetCredentialPromptRejectsUnauthorizedUse(t *testing.T) {
	for _, operation := range []struct {
		name   string
		action GatewayAction
	}{{"ask", ActionAsk}, {"apply", ActionExec}} {
		for _, scenario := range []string{"no_grant", "wrong_user_target", "wrong_grant_cluster", "wrong_grant_instance", "no_active_version", "revoked_version", "revoked_grant"} {
			t.Run(operation.name+"/"+scenario, func(t *testing.T) {
				t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("p", 32))
				f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: 3 * time.Second})
				targetGrant := ScopeGrant{Cluster: "dev", Instance: "local"}
				if scenario == "wrong_user_target" {
					targetGrant.Cluster = "other"
				}
				user := f.user(t, "database-denied", targetGrant)
				auth, err := f.store.VerifyUserToken(user.Plaintext)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "no_grant" {
					if _, err := f.store.db.Exec("DELETE FROM grants WHERE user_id = ?", auth.UserID); err != nil {
						t.Fatal(err)
					}
				}
				scope := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
				if scenario == "wrong_grant_cluster" {
					scope.Cluster = "other"
				} else if scenario == "wrong_grant_instance" {
					scope.Instance = "other"
				}
				resource, version, grant := credentialTargetPermissionResource(t, f, f.admin, scope)
				wantError, wantStatus := "credential_grant_denied", "blocked"
				switch scenario {
				case "no_active_version":
					_, err = f.store.db.Exec("UPDATE credential_versions SET state = ? WHERE id = ?", CredentialVersionStaged, version.ID)
					wantError = "credential_version_not_found"
				case "revoked_version":
					_, err = f.store.db.Exec("UPDATE credential_versions SET state = ? WHERE id = ?", CredentialVersionRevoked, version.ID)
					wantError = "credential_version_not_found"
				case "revoked_grant":
					body, marshalErr := json.Marshal(map[string]string{"grant_id": grant.ID})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					f.http(t, f.admin, http.MethodDelete, "/v1/credentials/"+resource.ID+"/grants", string(body), http.StatusOK)
				case "wrong_grant_cluster", "wrong_grant_instance":
					wantError = "credential_binding_target_mismatch"
				}
				if err != nil {
					t.Fatal(err)
				}
				agent := newCredentialTargetPermissionAgent(t, f)
				if scenario == "no_grant" || scenario == "wrong_user_target" {
					conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.server.URL, "http")+
						"/v1/rpc?cluster=dev&instance=local", http.Header{"Authorization": []string{"Bearer " + user.Plaintext}})
					if conn != nil {
						defer conn.Close()
					}
					if response != nil {
						defer response.Body.Close()
					}
					if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
						t.Fatalf("target access accepted without target ask/exec authority: response=%v err=%v", response, err)
					}
					agent.noCalls(t)
					return
				}
				client := f.client(t, user)
				session := "denied-" + operation.name + "-" + scenario
				lifecycleSend(t, client, 2, "doops_agent_prompt", session, map[string]interface{}{
					"skill": "dosql", "operation": operation.name, "instruction": "perform the synthetic database operation", "credential_refs": []string{resource.Name},
				})
				response := lifecycleResponse(t, client, 2, wantError)
				if response["error"].(map[string]interface{})["code"] != float64(-32003) {
					t.Fatalf("unexpected permission error code: %#v", response)
				}
				agent.noCalls(t)
				credentialTargetPermissionAudit(t, f, auth, session, operation.action, wantStatus, 1, response)
			})
		}
	}
}

func TestTargetCredentialPromptRechecksQueuedAuthority(t *testing.T) {
	for _, operation := range []struct {
		name   string
		action GatewayAction
	}{{"ask", ActionAsk}, {"apply", ActionExec}} {
		for _, revoke := range []string{"token", "operation_action", "same_name_credential", "grant_id", "revoked_grant", "revoked_version"} {
			t.Run(operation.name+"/"+revoke, func(t *testing.T) {
				t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("p", 32))
				f := newExecutionLifecycleFixture(t, GatewayHubOptions{
					OperationTimeout: 3 * time.Second, MaxQueuedPerTarget: 1, TargetQueueTimeout: 5 * time.Second,
				})
				user := f.user(t, "database-queued", ScopeGrant{Cluster: "dev", Instance: "local"})
				auth, err := f.store.VerifyUserToken(user.Plaintext)
				if err != nil {
					t.Fatal(err)
				}
				scope := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
				resource, version, grant := credentialTargetPermissionResource(t, f, f.admin, scope)
				agent, client := newCredentialTargetPermissionAgent(t, f), f.client(t, user)
				session := "queued-" + operation.name + "-" + revoke
				var target *GatewayAgent
				requireEventually(t, 3*time.Second, func() bool {
					target = f.hub.client.registry.Get("dev", "local")
					return target != nil
				})
				// Park the prompt behind its session workspace lease so authority
				// can change between admission and dispatch.
				held, err := target.acquireActionLeaseForSession(context.Background(), ActionWrite,
					"workspace:"+session, session, 1, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				unlock := held.Release
				defer func() {
					if unlock != nil {
						unlock()
					}
				}()
				lifecycleSend(t, client, 2, "doops_agent_prompt", session, map[string]interface{}{
					"skill": "dosql", "operation": operation.name, "instruction": "perform the synthetic database operation", "credential_refs": []string{resource.Name},
				})
				f.operation(t, session, "waiting")
				agent.noCalls(t)
				switch revoke {
				case "token":
					err = f.store.RevokeToken(auth.TokenID)
				case "operation_action":
					grants, listErr := f.store.ListGrants(auth.UserID)
					if listErr != nil || len(grants) != 1 {
						t.Fatalf("queued user's operation grant: %#v, %v", grants, listErr)
					}
					// Keep credential-only authority to prove it cannot replace the
					// revoked operation action at dispatch time.
					err = f.store.GrantUser(auth.UserID, ScopeGrant{Cluster: "dev", Instance: "local"})
					if err == nil {
						err = f.store.DeleteGrant(grants[0].ID)
					}
				case "same_name_credential", "grant_id", "revoked_grant":
					// Inject the concurrent lifecycle change into the temporary store;
					// the queued request still holds its original credential lock.
					if _, err = f.store.RevokeCredentialGrant(resource.ID, grant.ID, resource.OwnerID); err != nil {
						t.Fatal(err)
					}
					if revoke == "same_name_credential" {
						owner := f.adminUser(t, "replacement-owner")
						replacement, _, _ := credentialTargetPermissionResource(t, f, owner, scope)
						resolved, resolveErr := f.store.AuthorizeCredentialUse(CredentialUseRequest{
							ActorID: auth.UserID, Credential: resource.Name, Scope: scope,
						})
						if resolveErr != nil || replacement.ID == resource.ID || resolved.Resource.ID != replacement.ID {
							t.Fatalf("same-name replacement was not runtime-authorized: %#v, %v", resolved, resolveErr)
						}
					} else if revoke == "grant_id" {
						replacementScope := scope
						replacementScope.Namespace = "new-namespace"
						replacement, createErr := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
							CredentialID: resource.ID, CreatedBy: resource.OwnerID, Scope: replacementScope,
						})
						if createErr != nil || replacement.ID == grant.ID || replacement.Scope.Namespace != "new-namespace" {
							t.Fatalf("grant replacement did not change identity: %#v, %v", replacement, createErr)
						}
					}
				case "revoked_version":
					_, err = f.store.db.Exec("UPDATE credential_versions SET state = ? WHERE id = ?", CredentialVersionRevoked, version.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				unlock()
				unlock = nil
				response := lifecycleResponse(t, client, 2, "credential_authorization_changed")
				if response["error"].(map[string]interface{})["code"] != float64(-32003) {
					t.Fatalf("queued authority revocation returned the wrong error code: %#v", response)
				}
				agent.noCalls(t)
				credentialTargetPermissionAudit(t, f, auth, session, operation.action, "blocked", 1, response)
			})
		}
	}
}
