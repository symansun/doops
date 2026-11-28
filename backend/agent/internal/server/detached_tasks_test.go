package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func detachedSend(t *testing.T, client *websocket.Conn, id int, args map[string]interface{}) {
	t.Helper()
	lifecycleSend(t, client, id, "doops_bg", "detached-session", args)
}

// readUntilMethod reads client frames until the named notification arrives.
func readUntilMethod(t *testing.T, client *websocket.Conn, method string) map[string]interface{} {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer client.SetReadDeadline(time.Time{})
	for {
		var msg map[string]interface{}
		if err := client.ReadJSON(&msg); err != nil {
			t.Fatalf("waiting for %s: %v", method, err)
		}
		if msg["method"] == method {
			return msg
		}
	}
}

func detachedUserID(t *testing.T, f *executionLifecycleFixture, token CreatedToken) string {
	t.Helper()
	auth, err := f.store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return auth.UserID
}

func detachedNotificationAuditID(t *testing.T, notice map[string]interface{}) int64 {
	t.Helper()
	params, _ := notice["params"].(map[string]interface{})
	id, _ := numericID(params["audit_id"])
	if id <= 0 {
		t.Fatalf("background_task notification missing audit_id: %#v", notice)
	}
	if op, _ := params["operation_id"].(string); !strings.HasPrefix(op, "op-") {
		t.Fatalf("background_task notification missing operation_id: %#v", notice)
	}
	return id
}

func detachedDetail(t *testing.T, f *executionLifecycleFixture, token CreatedToken, auditID int64, want int) map[string]interface{} {
	t.Helper()
	body := f.http(t, token, http.MethodGet, fmt.Sprintf("/v1/background-tasks/%d", auditID), "", want)
	if want != http.StatusOK {
		return nil
	}
	var view map[string]interface{}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func detachedProgress(t *testing.T, agent *executionLifecycleAgent, call executionLifecycleCall, text string) {
	t.Helper()
	if err := agent.write(map[string]interface{}{
		"jsonrpc": "2.0", "method": "notifications/message",
		"params": map[string]interface{}{"request_id": call.id, "data": text},
	}); err != nil {
		t.Fatal(err)
	}
}

// Given a detached doops_bg call, when the client disconnects mid-run, the
// Agent relay, lease, and audit continue to the true terminal state.
func TestDetachedBackgroundSurvivesClientDisconnect(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "detached-agent")
	conn := dialGatewayRPCClient(t, f.server.URL, f.admin.Plaintext, "dev", "local")

	detachedSend(t, conn, 7, map[string]interface{}{"command": "sleep 5", "detach": true})
	notice := readUntilMethod(t, conn, "notifications/doops/background_task")
	auditID := detachedNotificationAuditID(t, notice)
	call := agent.call(t, "doops_bg", "detached-session")

	// When the client leaves, the Agent must not receive tools/cancel.
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case canceled := <-agent.cancels:
		t.Fatalf("detached call was canceled on client disconnect: %d", canceled)
	case <-time.After(500 * time.Millisecond):
	}

	detachedProgress(t, agent, call, "TaskID: task-42\nstill running\n")
	agent.complete(t, call, "")

	requireEventually(t, 3*time.Second, func() bool {
		task := f.hub.client.detached.get(auditID, detachedUserID(t, f, f.admin))
		return task != nil && task.Status == "success"
	})

	view := detachedDetail(t, f, f.admin, auditID, http.StatusOK)
	if view["status"] != "success" || view["task_id"] != "task-42" {
		t.Fatalf("detached view = %#v", view)
	}
	if out, _ := view["output"].(string); !strings.Contains(out, "still running") {
		t.Fatalf("detached output missing progress: %#v", view)
	}
	events, err := f.store.ListAuditFiltered(AuditFilter{Session: "detached-session"})
	if err != nil || len(events) != 1 || events[0].Status != "success" || events[0].EndedAt.IsZero() {
		t.Fatalf("detached audit = %#v, %v", events, err)
	}
}

// Given detach absent or false, disconnect keeps the existing cancellation.
func TestNonDetachedBackgroundStillCancelsOnDisconnect(t *testing.T) {
	for _, detach := range []interface{}{nil, false} {
		f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
		agent := f.agent(t, "attached-agent")
		conn := dialGatewayRPCClient(t, f.server.URL, f.admin.Plaintext, "dev", "local")
		args := map[string]interface{}{"command": "sleep 5"}
		if detach != nil {
			args["detach"] = detach
		}
		detachedSend(t, conn, 7, args)
		call := agent.call(t, "doops_bg", "detached-session")
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-agent.cancels:
		case <-time.After(3 * time.Second):
			t.Fatal("attached background call was not canceled on disconnect")
		}
		agent.complete(t, call, "operation canceled")
	}
}

// The owner cancel endpoint ACKs immediately; the Agent tools/cancel drives
// the true terminal state.
func TestDetachedBackgroundOwnerCancel(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "owner-cancel-agent")
	conn := dialGatewayRPCClient(t, f.server.URL, f.admin.Plaintext, "dev", "local")
	detachedSend(t, conn, 7, map[string]interface{}{"command": "sleep 5", "detach": true})
	auditID := detachedNotificationAuditID(t, readUntilMethod(t, conn, "notifications/doops/background_task"))
	call := agent.call(t, "doops_bg", "detached-session")

	body := f.http(t, f.admin, http.MethodPost,
		fmt.Sprintf("/v1/background-tasks/%d/cancel", auditID), "", http.StatusAccepted)
	var ack map[string]interface{}
	if err := json.Unmarshal(body, &ack); err != nil || ack["cancel_requested"] != true {
		t.Fatalf("cancel ack = %s, %v", body, err)
	}
	select {
	case <-agent.cancels:
	case <-time.After(3 * time.Second):
		t.Fatal("owner cancel did not reach the Agent")
	}
	agent.complete(t, call, "operation canceled")
	// A detached task cannot be proven stopped by an ack alone: the terminal is
	// either the Agent's canceled reply or outcome_unknown under existing rules.
	requireEventually(t, 3*time.Second, func() bool {
		task := f.hub.client.detached.get(auditID, detachedUserID(t, f, f.admin))
		return task != nil && (task.Status == "canceled" || task.Status == "outcome_unknown")
	})
	f.http(t, f.admin, http.MethodPost,
		fmt.Sprintf("/v1/background-tasks/%d/cancel", auditID), "", http.StatusConflict)
}

// Another user must not see or stop the task; losing the target grant hides it.
func TestDetachedBackgroundOwnerIsolation(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "isolation-agent")
	owner := f.user(t, "owner", ScopeGrant{Cluster: "dev", Instance: "local"})
	other := f.user(t, "other", ScopeGrant{Cluster: "dev", Instance: "local"})
	conn := dialGatewayRPCClient(t, f.server.URL, owner.Plaintext, "dev", "local")
	detachedSend(t, conn, 7, map[string]interface{}{"command": "sleep 5", "detach": true})
	auditID := detachedNotificationAuditID(t, readUntilMethod(t, conn, "notifications/doops/background_task"))
	call := agent.call(t, "doops_bg", "detached-session")

	detachedDetail(t, f, other, auditID, http.StatusNotFound)
	f.http(t, other, http.MethodPost, fmt.Sprintf("/v1/background-tasks/%d/cancel", auditID), "", http.StatusNotFound)
	list := f.http(t, other, http.MethodGet, "/v1/background-tasks?cluster=dev&instance=local", "", http.StatusOK)
	if strings.Contains(string(list), fmt.Sprintf("%d", auditID)) {
		t.Fatalf("other user's list contains the task: %s", list)
	}
	// The platform flag grants no visibility into another user's tasks either.
	detachedDetail(t, f, f.admin, auditID, http.StatusNotFound)

	// Revoking the owner's grant hides the task from the owner too.
	grants, err := f.store.ListGrants(detachedUserID(t, f, owner))
	if err != nil || len(grants) != 1 {
		t.Fatalf("owner grants = %#v, %v", grants, err)
	}
	if err := f.store.DeleteGrant(grants[0].ID); err != nil {
		t.Fatal(err)
	}
	detachedDetail(t, f, owner, auditID, http.StatusNotFound)

	agent.complete(t, call, "")
}

func TestDetachedArgumentValidation(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	f.agent(t, "validate-agent")
	conn := f.client(t, f.admin)
	lifecycleSend(t, conn, 7, "doops_shell", "detached-session", map[string]interface{}{
		"command": "true", "detach": true,
	})
	lifecycleResponse(t, conn, 7, "detach is only supported by doops_bg")
	lifecycleSend(t, conn, 8, "doops_bg", "detached-session", map[string]interface{}{
		"command": "true", "detach": "yes",
	})
	lifecycleResponse(t, conn, 8, "detach must be a boolean")
}

// The Agent schema is unchanged: detach is stripped before forwarding.
func TestDetachedArgumentStrippedBeforeAgentDispatch(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "strip-agent")
	conn := f.client(t, f.admin)
	detachedSend(t, conn, 7, map[string]interface{}{"command": "sleep 1", "detach": true})
	call := agent.call(t, "doops_bg", "detached-session")
	if call.args["detach"] != nil {
		t.Fatalf("detach leaked to the Agent: %#v", call.args)
	}
	agent.complete(t, call, "")
	lifecycleResponse(t, conn, 7, "")
}

func TestDetachedTimeoutTerminatesTask(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{DetachedTaskTimeout: 300 * time.Millisecond})
	agent := f.agent(t, "timeout-agent")
	conn := f.client(t, f.admin)
	detachedSend(t, conn, 7, map[string]interface{}{"command": "sleep 60", "detach": true})
	auditID := detachedNotificationAuditID(t, readUntilMethod(t, conn, "notifications/doops/background_task"))
	call := agent.call(t, "doops_bg", "detached-session")
	// Reaching the detached lifetime fires tools/cancel; the Agent's terminal
	// reply then settles the audit under the existing outcome rules.
	select {
	case <-agent.cancels:
	case <-time.After(3 * time.Second):
		t.Fatal("detached timeout did not send tools/cancel to the Agent")
	}
	agent.complete(t, call, "operation canceled")
	requireEventually(t, 3*time.Second, func() bool {
		task := f.hub.client.detached.get(auditID, detachedUserID(t, f, f.admin))
		return task != nil && task.Status != "running"
	})
	view := detachedDetail(t, f, f.admin, auditID, http.StatusOK)
	if status, _ := view["status"].(string); status == "" || status == "running" {
		t.Fatalf("detached timeout view = %#v", view)
	}
}

func TestDetachedCapabilityAdvertised(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{DetachedTaskTimeout: 2 * time.Hour})
	f.agent(t, "capability-agent")
	conn := f.client(t, f.admin)
	writeGatewayJSON(t, conn, map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	msg := readUntilID(t, conn, 1)
	result, _ := msg["result"].(map[string]interface{})
	caps, _ := result["capabilities"].(map[string]interface{})
	gatewayProtocols, _ := caps["gatewayProtocols"].(map[string]interface{})
	proto, _ := gatewayProtocols[detachedBackgroundProtocol].(map[string]interface{})
	if proto["schemaVersion"] != detachedBackgroundSchema || proto["maxDuration"] != "2h0m0s" {
		t.Fatalf("detachedBackground capability = %#v", proto)
	}
}

// While a detached task holds the target-mutation lease, a second mutation on
// the same session still waits.
func TestDetachedLeaseBlocksSameSessionMutation(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "lease-agent")
	conn := f.client(t, f.admin)
	detachedSend(t, conn, 7, map[string]interface{}{"command": "sleep 5", "detach": true})
	call := agent.call(t, "doops_bg", "detached-session")
	lifecycleSend(t, conn, 8, "doops_file_write", "detached-session", map[string]interface{}{
		"path": "config.txt", "content": "x",
	})
	lifecycleResponse(t, conn, 8, "target busy")
	agent.complete(t, call, "")
	lifecycleResponse(t, conn, 7, "")
	lifecycleSend(t, conn, 9, "doops_file_write", "detached-session", map[string]interface{}{
		"path": "config.txt", "content": "x",
	})
	agent.complete(t, agent.call(t, "doops_file_write", "detached-session"), "")
	lifecycleResponse(t, conn, 9, "")
}

// Given a detached call whose terminal audit write fails, the registry entry
// still records the true execution status instead of leaking "running".
func TestDetachedBackgroundAuditFailureStillTerminatesTask(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "detached-agent")
	conn := dialGatewayRPCClient(t, f.server.URL, f.admin.Plaintext, "dev", "local")
	defer conn.Close()

	detachedSend(t, conn, 11, map[string]interface{}{"command": "sleep 5", "detach": true})
	notice := readUntilMethod(t, conn, "notifications/doops/background_task")
	auditID := detachedNotificationAuditID(t, notice)
	call := agent.call(t, "doops_bg", "detached-session")

	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_detached_finalization
		BEFORE UPDATE OF status ON audit_events
		WHEN OLD.session = 'detached-session' AND OLD.status = 'executing'
		BEGIN SELECT RAISE(FAIL, 'synthetic detached audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	defer f.store.db.Exec("DROP TRIGGER IF EXISTS fail_detached_finalization")

	agent.complete(t, call, "")
	lifecycleResponse(t, conn, 11, "audit_completion_failed")

	userID := detachedUserID(t, f, f.admin)
	requireEventually(t, 3*time.Second, func() bool {
		task := f.hub.client.detached.get(auditID, userID)
		return task != nil && task.Status == "success"
	})
	view := detachedDetail(t, f, f.admin, auditID, http.StatusOK)
	if view["status"] != "success" {
		t.Fatalf("detached view = %#v", view)
	}
	f.http(t, f.admin, http.MethodPost, fmt.Sprintf("/v1/background-tasks/%d/cancel", auditID), "", http.StatusConflict)
}
