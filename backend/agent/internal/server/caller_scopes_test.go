package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Scope metadata summarizes current target authority; it is not a second grant
// model. Overlapping target grants cannot grant privileged execution.
func TestUserScopeActions(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.CreateUser("scope-user")
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range []ScopeGrant{
		{Cluster: "dev", Instance: "local"},
		{Cluster: "dev", Instance: "*"},
		{Cluster: "prod", Instance: "local"},
		{Cluster: "dev", Instance: "local"},
	} {
		if err := store.GrantUser(user.ID, grant); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		cluster, instance string
		allowed           bool
	}{
		{"dev", "local", true},
		{"dev", "other", true},
		{"prod", "local", true},
		{"prod", "other", false},
	} {
		t.Run(tc.cluster+"/"+tc.instance, func(t *testing.T) {
			got, err := store.UserScopeActions(user.ID, tc.cluster, tc.instance)
			if tc.allowed {
				if err != nil || !reflect.DeepEqual(got, []string{"exec"}) {
					t.Fatalf("target scopes=%v err=%v, want only exec", got, err)
				}
			} else if !errors.Is(err, ErrForbidden) || got != nil {
				t.Fatalf("ungranted target scopes=%v err=%v, want forbidden", got, err)
			}
		})
	}
	for _, id := range []string{"", "missing-user"} {
		if got, err := store.UserScopeActions(id, "dev", "local"); err == nil || got != nil {
			t.Fatalf("unknown identity scopes=%v err=%v", got, err)
		}
	}
	if err := store.SetUserAdmin(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, err := store.UserScopeActions(user.ID, "ungranted", "node"); err != nil || !reflect.DeepEqual(got, []string{"admin", "exec"}) {
		t.Fatalf("current platform admin scopes=%v err=%v", got, err)
	}
	if err := store.SetUserDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, err := store.UserScopeActions(user.ID, "dev", "local"); !errors.Is(err, ErrForbidden) || got != nil {
		t.Fatalf("disabled admin scopes=%v err=%v", got, err)
	}
	if err := store.SetUserDisabled(user.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserAdmin(user.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, err := store.UserScopeActions(user.ID, "dev", "local"); err != nil || !reflect.DeepEqual(got, []string{"exec"}) {
		t.Fatalf("removed admin flag retained privilege: scopes=%v err=%v", got, err)
	}
	grants, err := store.ListGrants(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if err := store.DeleteGrant(grant.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := store.UserScopeActions(user.ID, "dev", "local"); !errors.Is(err, ErrForbidden) || got != nil {
		t.Fatalf("revoked grant scopes=%v err=%v", got, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.UserScopeActions(user.ID, "dev", "local"); err == nil || got != nil {
		t.Fatalf("unavailable authority store scopes=%v err=%v", got, err)
	}
}

func TestGatewayRPCInjectsCallerScopesForExec(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{MaxQueuedPerTarget: -1})
	token := f.user(t, "scope-wire-user", ScopeGrant{Cluster: "dev", Instance: "local"})
	auth, err := f.store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	captured := startCallerScopeCapturingAgent(t, f, false)
	client := f.client(t, token)
	for _, tc := range []struct {
		name  string
		admin bool
		want  []string
	}{
		{"target grant", false, []string{"exec"}},
		{"platform admin", true, []string{"admin", "exec"}},
		{"admin removed on existing connection", false, []string{"exec"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.store.SetUserAdmin(auth.UserID, tc.admin); err != nil {
				t.Fatal(err)
			}
			response := callToolResultWithScopes(t, client, "doops_shell", map[string]interface{}{
				"session_id": "scope-wire", "command": "hostname",
			}, nil)
			if response["error"] != nil || response["result"] == nil {
				t.Fatalf("expected forwarded result: %#v", response)
			}
			params := receiveCapturedParams(t, captured)
			requireCallerScopes(t, params, tc.want)

			response = callToolResultWithScopes(t, client, "doops_file_read", map[string]interface{}{
				"session_id": "scope-wire", "path": "x",
			}, []string{"admin", "exec:privileged"})
			if response["error"] != nil || response["result"] == nil {
				t.Fatalf("expected forwarded read result: %#v", response)
			}
			if params := receiveCapturedParams(t, captured); params["caller_scopes"] != nil {
				t.Fatalf("non-exec frame retained public scopes: %#v", params)
			}
		})
	}
}

func TestGatewayRPCOverridesForgedCallerScopes(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{MaxQueuedPerTarget: -1})
	token := f.user(t, "scope-forgery-user", ScopeGrant{Cluster: "dev", Instance: "local"})
	captured := startCallerScopeCapturingAgent(t, f, false)
	client := f.client(t, token)
	for _, scopes := range [][]string{{"admin"}, {"exec:privileged"}, {"exec", "admin", "exec:privileged"}} {
		t.Run(strings.Join(scopes, "+"), func(t *testing.T) {
			response := callToolResultWithScopes(t, client, "doops_shell", map[string]interface{}{
				"session_id": "scope-forgery", "command": "hostname",
				"caller_scopes": scopes, "_doops_action": "admin",
			}, scopes)
			if response["error"] != nil || response["result"] == nil {
				t.Fatalf("expected permitted target call: %#v", response)
			}
			requireCallerScopes(t, receiveCapturedParams(t, captured), []string{"exec"})
		})
	}
}

func TestGatewayCallerScopesRequireCurrentAuthorityBeforeForwarding(t *testing.T) {
	for _, failure := range []string{"revoked token", "disabled user", "revoked grant", "unavailable store"} {
		t.Run(failure, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{MaxQueuedPerTarget: -1})
			token := f.user(t, "scope-recheck-user", ScopeGrant{Cluster: "dev", Instance: "local"})
			auth, err := f.store.VerifyUserToken(token.Plaintext)
			if err != nil {
				t.Fatal(err)
			}
			captured := startCallerScopeCapturingAgent(t, f, failure == "unavailable store")
			client := f.client(t, token)
			wantCode, wantMessage := float64(-32001), "gateway_auth_revoked"
			switch failure {
			case "revoked token":
				err = f.store.RevokeToken(token.ID)
			case "disabled user":
				err = f.store.SetUserDisabled(auth.UserID, true)
				wantMessage = "gateway_auth_user_disabled"
			case "revoked grant":
				grants, listErr := f.store.ListGrants(auth.UserID)
				if listErr != nil || len(grants) != 1 {
					t.Fatalf("grants=%v err=%v", grants, listErr)
				}
				err = f.store.DeleteGrant(grants[0].ID)
				wantCode, wantMessage = -32003, "forbidden"
			case "unavailable store":
				err = f.store.Close()
				wantCode, wantMessage = -32603, "gateway_auth_unavailable"
			}
			if err != nil {
				t.Fatal(err)
			}
			response := callToolResultWithScopes(t, client, "doops_shell", map[string]interface{}{
				"session_id": "scope-recheck", "command": "hostname",
			}, []string{"admin", "exec:privileged"})
			rpcErr, _ := response["error"].(map[string]interface{})
			if rpcErr["code"] != wantCode || !strings.Contains(fmt.Sprint(rpcErr["message"]), wantMessage) {
				t.Fatalf("authority failure lost its terminal error: %#v", response)
			}
			select {
			case params := <-captured:
				t.Fatalf("failed current authority reached Agent: %#v", params)
			default:
			}
		})
	}
}

// The private-channel matrix retains both privileged values from the wire
// contract. The canary only writes a temporary file when execution is admitted.
func TestGuardrailBypassedByScopeViaWS(t *testing.T) {
	root, conn := setupCallerScopeGuardrailWS(t)
	for _, tc := range []struct {
		session string
		scopes  []string
	}{
		{"scope-admin", []string{"exec", "admin"}},
		{"scope-privileged", []string{"exec:privileged"}},
	} {
		t.Run(tc.session, func(t *testing.T) {
			marker, command := callerScopeCanary(t)
			response := callToolResultWithScopes(t, conn, "doops_shell", map[string]interface{}{
				"session_id": tc.session, "command": command,
			}, tc.scopes)
			requireCallerScopeExecuted(t, response, marker)
			audit := readCallerScopeAudit(t, root, tc.session, true)
			if !strings.Contains(audit, "bypassed_by_scope=1") || !strings.Contains(audit, command) || strings.Contains(audit, "blocked=1") {
				t.Fatalf("private scope bypass lacks exact command/audit evidence: %s", audit)
			}
			entries := parseAuditLog(audit, tc.session)
			if len(entries) != 1 || !entries[0].BypassedByScope || entries[0].Command != command {
				t.Fatalf("executed scope bypass is not queryable: %#v", entries)
			}
		})
	}
}

func TestGuardrailStillBlocksWithoutPrivilegedScopeViaWS(t *testing.T) {
	root, conn := setupCallerScopeGuardrailWS(t)
	for _, tc := range []struct {
		name   string
		scopes []string
	}{
		{"ordinary", []string{"exec"}},
		{"field-absent", nil},
		{"field-empty", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker, command := callerScopeCanary(t)
			session := "scope-" + tc.name
			response := callToolResultWithScopes(t, conn, "doops_shell", map[string]interface{}{
				"session_id": session, "command": command,
			}, tc.scopes)
			requireCallerScopeBlocked(t, response, marker)
			audit := readCallerScopeAudit(t, root, session, false)
			if !strings.Contains(audit, "blocked=1 rule=custom") || !strings.Contains(audit, command) || strings.Contains(audit, "bypassed_by_scope=1") {
				t.Fatalf("ordinary call lacks blocked audit or gained bypass: %s", audit)
			}
		})
	}
}

func TestGatewayRPCForgedScopesCannotBypassRealAgentGuardrail(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	t.Setenv("DOOPS_BLOCK_EXTRA", "caller-scope-canary")
	t.Setenv("DOOPS_BLOCK_OFF", "0")
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{MaxQueuedPerTarget: -1})
	token := f.user(t, "scope-real-user", ScopeGrant{Cluster: "dev", Instance: "local"})
	auth, err := f.store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	agentConn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	agent := NewGateway()
	agent.auditRoot = filepath.Join(root, "execution")
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		agent.ServeGatewayWebSocketConnWithReady(agentConn, "caller-scopes-functional", nil)
	}()
	t.Cleanup(func() {
		_ = agentConn.Close()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Error("real caller-scope Agent did not stop")
		}
		waitForCallerScopeAgentOffline(t, f, false)
	})
	waitForGatewayAgent(t, f.hub, "dev", "local")
	client := f.client(t, token)
	for i, scopes := range [][]string{{"admin"}, {"exec:privileged"}, {"admin", "exec:privileged"}} {
		marker, command := callerScopeCanary(t)
		session := fmt.Sprintf("scope-forged-%d", i)
		response := callToolResultWithScopes(t, client, "doops_shell", map[string]interface{}{
			"session_id": session, "command": command, "caller_scopes": scopes,
		}, scopes)
		requireCallerScopeBlocked(t, response, marker)
		audit := readCallerScopeAudit(t, root, session, false)
		if !strings.Contains(audit, "blocked=1 rule=custom") || strings.Contains(audit, "bypassed_by_scope=1") {
			t.Fatalf("forged public scopes bypassed real Agent audit: %s", audit)
		}
	}
	// Current authority is read for every call, even on this already-open client.
	for _, admin := range []bool{true, false} {
		if err := f.store.SetUserAdmin(auth.UserID, admin); err != nil {
			t.Fatal(err)
		}
		marker, command := callerScopeCanary(t)
		session := fmt.Sprintf("scope-current-admin-%t", admin)
		response := callToolResultWithScopes(t, client, "doops_shell", map[string]interface{}{
			"session_id": session, "command": command,
		}, []string{"admin"})
		audit := readCallerScopeAudit(t, root, session, admin)
		if admin {
			requireCallerScopeExecuted(t, response, marker)
			if !strings.Contains(audit, "bypassed_by_scope=1") || strings.Contains(audit, "blocked=1") {
				t.Fatalf("authenticated platform admin bypass is not audited: %s", audit)
			}
		} else {
			requireCallerScopeBlocked(t, response, marker)
			if !strings.Contains(audit, "blocked=1 rule=custom") || strings.Contains(audit, "bypassed_by_scope=1") {
				t.Fatalf("removed platform admin privilege stayed cached: %s", audit)
			}
		}
	}
}

func startCallerScopeCapturingAgent(t *testing.T, f *executionLifecycleFixture, storeUnavailable bool) <-chan map[string]interface{} {
	t.Helper()
	conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	t.Cleanup(func() {
		_ = conn.Close()
		waitForCallerScopeAgentOffline(t, f, storeUnavailable)
	})
	captured := make(chan map[string]interface{}, 8)
	go serveCapturingFakeAgent(t, conn, captured)
	waitForGatewayAgent(t, f.hub, "dev", "local")
	return captured
}

func waitForCallerScopeAgentOffline(t *testing.T, f *executionLifecycleFixture, storeUnavailable bool) {
	t.Helper()
	requireEventually(t, time.Second, func() bool {
		if f.hub.getAgent("dev", "local") != nil {
			return false
		}
		if storeUnavailable {
			return true // This case deliberately closed the authority store.
		}
		agents, err := f.store.ListAgentStatus()
		return err == nil && len(agents) == 1 && agents[0].Status == "offline"
	})
}

func serveCapturingFakeAgent(t *testing.T, conn *websocket.Conn, captured chan<- map[string]interface{}) {
	t.Helper()
	for {
		var msg map[string]interface{}
		if conn.ReadJSON(&msg) != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			result := gatewayTestInitializeResult("")
			result["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[executionLifecycleProtocol] = executionLifecycleCapability()
			_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": result})
		case "tools/call":
			params, _ := msg["params"].(map[string]interface{})
			captured <- params
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "ok"}}},
			})
		}
	}
}

func receiveCapturedParams(t *testing.T, captured <-chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	select {
	case params := <-captured:
		return params
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for relayed caller-scope frame")
		return nil
	}
}

func requireCallerScopes(t *testing.T, params map[string]interface{}, want []string) {
	t.Helper()
	raw, ok := params["caller_scopes"].([]interface{})
	if !ok {
		t.Fatalf("exec frame lacks caller_scopes: %#v", params)
	}
	got := make([]string, len(raw))
	for i, value := range raw {
		got[i], ok = value.(string)
		if !ok {
			t.Fatalf("non-string caller scope: %#v", value)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("caller_scopes=%v, want %v", got, want)
	}
}

func setupCallerScopeGuardrailWS(t *testing.T) (string, *websocket.Conn) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	t.Setenv("DOOPS_BLOCK_EXTRA", "caller-scope-canary")
	t.Setenv("DOOPS_BLOCK_OFF", "0")
	agent := NewGateway()
	agent.auditRoot = filepath.Join(root, "execution")
	server := newGatewayAuthorizedAgentServer(t, agent)
	t.Cleanup(server.Close)
	conn := dialAgentTestWS(t, server.URL)
	t.Cleanup(func() { _ = conn.Close() })
	initializeAgentTestWS(t, conn)
	return root, conn
}

func callToolResultWithScopes(t *testing.T, conn *websocket.Conn, tool string, args map[string]interface{}, scopes []string) map[string]interface{} {
	t.Helper()
	params := map[string]interface{}{"name": tool, "arguments": args}
	if scopes != nil {
		params["caller_scopes"] = scopes
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer conn.SetReadDeadline(time.Time{})
	writeGatewayJSON(t, conn, map[string]interface{}{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": params})
	return readUntilID(t, conn, 2)
}

func callerScopeCanary(t *testing.T) (string, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "executed")
	return marker, "printf caller-scope-canary > " + shellQuote(marker)
}

func requireCallerScopeExecuted(t *testing.T, response map[string]interface{}, marker string) {
	t.Helper()
	result, _ := response["result"].(map[string]interface{})
	if response["error"] != nil || result == nil || result["isError"] == true {
		t.Fatalf("authorized canary did not execute successfully: %#v", response)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "caller-scope-canary" {
		t.Fatalf("authorized canary marker=%q err=%v", data, err)
	}
}

func requireCallerScopeBlocked(t *testing.T, response map[string]interface{}, marker string) {
	t.Helper()
	rpcErr, _ := response["error"].(map[string]interface{})
	if rpcErr["code"] != float64(-32602) || !strings.Contains(fmt.Sprint(rpcErr["message"]), "blocked_command(custom)") {
		t.Fatalf("ordinary/forged scopes did not enforce Agent guardrail: %#v", response)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked canary executed: stat err=%v", err)
	}
}

func readCallerScopeAudit(t *testing.T, root, session string, executed bool) string {
	t.Helper()
	path := filepath.Join(root, session, ".doops-audit-log")
	if executed {
		path = filepath.Join(root, "execution", session+".log")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read caller-scope audit: %v", err)
	}
	return string(data)
}
