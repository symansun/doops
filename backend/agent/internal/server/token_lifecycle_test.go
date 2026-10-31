package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
)

func TestGatewayStoreTokenRejectsDisabledUser(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "token-id"
		if legacy {
			name = "legacy-token"
		}
		t.Run(name, func(t *testing.T) {
			store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			user, err := store.CreateUser("token-owner")
			if err != nil {
				t.Fatal(err)
			}
			token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID})
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				token.Plaintext = "legacy-lifecycle-synthetic-canary"
				hash, err := bcrypt.GenerateFromPassword(tokenPassword(token.Plaintext), bcrypt.DefaultCost)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.db.Exec("UPDATE tokens SET hash = ? WHERE id = ?", string(hash), token.ID); err != nil {
					t.Fatal(err)
				}
			}
			auth, err := store.VerifyUserToken(token.Plaintext)
			if err != nil || auth.UserID != user.ID || auth.TokenID != token.ID {
				t.Fatalf("enabled token identity: %#v, %v", auth, err)
			}
			if err := store.SetUserDisabled(user.ID, true); err != nil {
				t.Fatal(err)
			}
			if _, err := store.VerifyUserToken(token.Plaintext); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("disabled user token was accepted: %v", err)
			}
			if err := store.SetUserDisabled(user.ID, false); err != nil {
				t.Fatal(err)
			}
			if restored, err := store.VerifyUserToken(token.Plaintext); err != nil || restored != auth {
				t.Fatalf("re-enabled token changed identity: %#v, %v", restored, err)
			}
			if _, err := store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", "invalid-expiry", token.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.VerifyUserToken(token.Plaintext); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("invalid token expiry was accepted: %v", err)
			}
		})
	}
}

func TestGatewayClientWSTokenLifecycleAdmission(t *testing.T) {
	for _, state := range []string{"enabled", "disabled", "revoked", "expired", "invalid-expiry", "reassigned"} {
		t.Run(state, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			token := f.user(t, "token-operator", ScopeGrant{Cluster: "dev", Instance: "local"})
			auth, err := f.store.VerifyUserToken(token.Plaintext)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", formatTime(time.Now().Add(time.Hour)), auth.TokenID); err != nil {
				t.Fatal(err)
			}
			agent, client := f.agent(t, "token-lifecycle"), f.client(t, token)
			before, forwarded := tokenLifecycleShellCall(t, client, agent, 2, "before-change")
			if !forwarded || before["error"] != nil || before["result"] == nil {
				t.Fatalf("valid initial call failed: %#v", before)
			}
			switch state {
			case "disabled":
				err = f.store.SetUserDisabled(auth.UserID, true)
			case "revoked":
				err = f.store.RevokeToken(auth.TokenID)
			case "expired":
				_, err = f.store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", formatTime(time.Now().Add(-time.Minute)), auth.TokenID)
			case "invalid-expiry":
				_, err = f.store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", "invalid-expiry", auth.TokenID)
			case "reassigned":
				other, createErr := f.store.CreateUser("other-token-owner")
				if createErr != nil {
					t.Fatal(createErr)
				}
				if err := f.store.GrantUser(other.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
					t.Fatal(err)
				}
				_, err = f.store.db.Exec("UPDATE tokens SET user_id = ? WHERE id = ?", other.ID, auth.TokenID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []int{3, 4} {
				response, forwarded := tokenLifecycleShellCall(t, client, agent, id, "after-change")
				if state == "enabled" {
					if !forwarded || response["error"] != nil || response["result"] == nil {
						t.Fatalf("enabled token stopped working: %#v", response)
					}
				} else {
					rpcError, ok := response["error"].(map[string]interface{})
					if forwarded || !ok || rpcError["code"] != float64(-32001) || rpcError["message"] != "gateway_auth_failed" {
						t.Fatalf("invalid original identity admitted: forwarded=%t, response=%#v", forwarded, response)
					}
				}
			}
			events, err := f.store.ListAudit(100)
			wantAudits := 1
			if state == "enabled" {
				wantAudits = 3
			}
			if err != nil || len(events) != wantAudits || len(f.hub.listActiveOperations()) != 0 {
				t.Fatalf("denial created an audit/operation: %#v, %v", events, err)
			}
			for _, event := range events {
				if event.UserID != auth.UserID || event.TokenID != auth.TokenID || event.Status != "success" {
					t.Fatalf("original actor identity changed: %#v", event)
				}
			}
			if state == "disabled" || state == "revoked" || state == "expired" || state == "invalid-expiry" {
				f.http(t, token, http.MethodGet, "/v1/targets", "", http.StatusUnauthorized)
			}
			if state == "disabled" {
				if err := f.store.SetUserDisabled(auth.UserID, false); err != nil {
					t.Fatal(err)
				}
				response, forwarded := tokenLifecycleShellCall(t, client, agent, 5, "re-enabled")
				if !forwarded || response["error"] != nil || response["result"] == nil {
					t.Fatalf("re-enabled original identity did not recover: %#v", response)
				}
				f.http(t, token, http.MethodGet, "/v1/targets", "", http.StatusOK)
			}
		})
	}
}

func TestGatewayClientWSStoreFailureDoesNotForward(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	token := f.user(t, "store-failure-operator", ScopeGrant{Cluster: "dev", Instance: "local"})
	agent, client := f.agent(t, "store-failure"), f.client(t, token)
	response, forwarded := tokenLifecycleShellCall(t, client, agent, 2, "store-failure-before")
	if !forwarded || response["error"] != nil || response["result"] == nil {
		t.Fatalf("valid call before store failure failed: %#v", response)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	response, forwarded = tokenLifecycleShellCall(t, client, agent, 3, "store-failure-after")
	rpcError, ok := response["error"].(map[string]interface{})
	if forwarded || !ok || rpcError["code"] != float64(-32603) || rpcError["message"] != "gateway_auth_unavailable" {
		t.Fatalf("store failure was exposed or forwarded: forwarded=%t response=%#v", forwarded, response)
	}
	if len(f.hub.listActiveOperations()) != 0 {
		t.Fatalf("store failure created an active operation: %#v", f.hub.listActiveOperations())
	}
}

// Answer an incorrectly forwarded call too, so an admission bug fails without a timeout.
func tokenLifecycleShellCall(t *testing.T, client *websocket.Conn, agent *executionLifecycleAgent, id int, session string) (map[string]interface{}, bool) {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer client.SetReadDeadline(time.Time{})
	lifecycleSend(t, client, id, "doops_shell", session, map[string]interface{}{"command": "true"})
	type reply struct {
		message map[string]interface{}
		err     error
	}
	replies := make(chan reply, 1)
	go func() {
		for {
			var message map[string]interface{}
			err := client.ReadJSON(&message)
			if err != nil || message["id"] == float64(id) {
				replies <- reply{message: message, err: err}
				return
			}
		}
	}()
	forwarded := false
	for {
		select {
		case call := <-agent.calls:
			forwarded = true
			if call.name != "doops_shell" || call.session != session {
				t.Fatalf("unexpected dispatch: %#v", call)
			}
			agent.complete(t, call, "")
		case response := <-replies:
			if response.err != nil {
				t.Fatal(response.err)
			}
			return response.message, forwarded
		}
	}
}

func TestGatewayCredentialRecoveryOutcome(t *testing.T) {
	for _, recoveryFails := range []bool{false, true} {
		name := "recovered"
		if recoveryFails {
			name = "outcome-unknown"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("r", 32))
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
			if err != nil {
				t.Fatal(err)
			}
			resource, err := f.store.CreateCredential(CredentialCreateRequest{
				Name: "lifecycle-credential", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
				OwnerID: auth.UserID, CreatedBy: auth.UserID,
			})
			if err != nil {
				t.Fatal(err)
			}
			version, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
				CredentialID: resource.ID, CreatedBy: auth.UserID, Activate: true,
				Payload: json.RawMessage(`{"data":{"dsn":"synthetic-lifecycle-payload-canary"}}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			scope := CredentialGrantScope{
				Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret,
				Workload: CredentialPlanWorkload{Kind: "deployment", Name: "api"}, RequiredKeys: []string{"dsn"},
			}
			grant, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
				CredentialID: resource.ID, Scope: scope, CreatedBy: auth.UserID,
			})
			if err != nil {
				t.Fatal(err)
			}
			agent, client := f.agent(t, "credential-recovery"), f.client(t, f.admin)
			const session = "credential-recovery"
			lifecycleSend(t, client, 2, "doops_agent_prompt", session, map[string]interface{}{
				"operation": "apply", "instruction": "synthetic credential-bound apply", "credential_refs": []string{resource.Name},
			})
			materialization, err := json.Marshal(CredentialMaterialization{
				CredentialID: resource.ID, GrantID: grant.ID, VersionID: version.ID,
				ResourceName: credentialSecretName(resource.ID, grant.ID), Namespace: scope.Namespace,
				SecretType: "Opaque", Keys: scope.RequiredKeys, ResourceVersion: "1", Status: "verified",
			})
			if err != nil {
				t.Fatal(err)
			}
			tokenLifecycleReplyText(t, agent, agent.call(t, "doops_credential_materialize", session), string(materialization), false)
			tokenLifecycleReplyText(t, agent, agent.call(t, "doops_agent_prompt", session), "synthetic_apply_failed", true)
			tokenLifecycleReplyText(t, agent, agent.call(t, "doops_credential_cleanup", session), "credential_cleanup_failed", recoveryFails)
			if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			response := readUntilID(t, client, float64(2))
			if recoveryFails {
				rpcError, ok := response["error"].(map[string]interface{})
				if !ok || rpcError["code"] != float64(-32008) || rpcError["message"] != errExecutionOutcomeUnknown.Error()+": credential_apply_outcome_unknown" {
					t.Fatalf("credential recovery uncertainty lost: %#v", response)
				}
			} else if result, ok := response["result"].(map[string]interface{}); !ok || result["isError"] != true {
				t.Fatalf("known apply failure changed after successful recovery: %#v", response)
			}
			events, err := f.store.ListAuditFiltered(AuditFilter{Session: session, Action: ActionExec})
			wantStatus, wantError := "error", "synthetic_apply_failed"
			if recoveryFails {
				wantStatus, wantError = "outcome_unknown", errExecutionOutcomeUnknown.Error()+": credential_apply_outcome_unknown"
			}
			if err != nil || len(events) != 1 || events[0].Status != wantStatus || events[0].Error != wantError ||
				events[0].UserID != auth.UserID || events[0].TokenID != auth.TokenID {
				t.Fatalf("root recovery audit disagrees with response: %#v, %v", events, err)
			}
			unresolved, err := f.store.UnresolvedExecutions("dev", "local")
			wantUnresolved := 0
			if recoveryFails {
				wantUnresolved = 1
			}
			if err != nil || len(unresolved) != wantUnresolved {
				t.Fatalf("recovery uncertainty was not persisted: %#v, %v", unresolved, err)
			}
			audits, err := f.store.ListAudit(100)
			if err != nil {
				t.Fatal(err)
			}
			auditJSON, err := json.Marshal(audits)
			if err != nil || strings.Contains(string(auditJSON), "synthetic-lifecycle-payload-canary") ||
				strings.Contains(string(auditJSON), f.admin.Plaintext) || len(f.hub.listActiveOperations()) != 0 {
				t.Fatalf("recovery leaked payload or retained an operation: %v", err)
			}
			followup, forwarded := tokenLifecycleShellCall(t, client, agent, 3, session)
			if !forwarded || followup["error"] != nil || followup["result"] == nil {
				t.Fatalf("historical unknown blocked the next task: %#v", followup)
			}
			if retained := executionAuditTestEvent(t, f.store, events[0].ID); retained.Status != wantStatus || retained.Error != wantError {
				t.Fatalf("follow-up changed the original outcome: %#v", retained)
			}
		})
	}
}

func tokenLifecycleReplyText(t *testing.T, agent *executionLifecycleAgent, call executionLifecycleCall, text string, isError bool) {
	t.Helper()
	if err := agent.write(map[string]interface{}{
		"jsonrpc": "2.0", "id": call.id,
		"result": map[string]interface{}{
			"isError": isError, "content": []map[string]interface{}{{"type": "text", "text": text}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}
