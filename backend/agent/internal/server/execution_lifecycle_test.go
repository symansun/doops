package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
)

type executionLifecycleFixture struct {
	path   string
	store  *GatewayStore
	hub    *GatewayHub
	server *httptest.Server
	admin  CreatedToken
	opts   GatewayHubOptions
}

func newExecutionLifecycleFixture(t *testing.T, opts GatewayHubOptions) *executionLifecycleFixture {
	t.Helper()
	f := &executionLifecycleFixture{path: filepath.Join(t.TempDir(), "gateway.db"), opts: opts}
	f.open(t)
	f.admin = f.user(t, "lifecycle-admin", ScopeGrant{Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAll}})
	return f
}

func (f *executionLifecycleFixture) open(t *testing.T) {
	t.Helper()
	store, err := OpenGatewayStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f.store = store
	hub := NewGatewayHub(store, f.opts)
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		server.Close()
		requireEventually(t, 3*time.Second, func() bool { return len(hub.listActiveOperations()) == 0 })
	})
	f.hub, f.server = hub, server
}

func (f *executionLifecycleFixture) user(t *testing.T, name string, grant ScopeGrant) CreatedToken {
	t.Helper()
	user, err := f.store.CreateUser(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.GrantUser(user.ID, grant); err != nil {
		t.Fatal(err)
	}
	token, err := f.store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (f *executionLifecycleFixture) client(t *testing.T, token CreatedToken) *websocket.Conn {
	t.Helper()
	conn := dialGatewayRPCClient(t, f.server.URL, token.Plaintext, "dev", "local")
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (f *executionLifecycleFixture) http(t *testing.T, token CreatedToken, method, path, body string, want int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Plaintext)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	return data
}

func (f *executionLifecycleFixture) operation(t *testing.T, session, state string) GatewayActiveOperation {
	t.Helper()
	var found GatewayActiveOperation
	requireEventually(t, 3*time.Second, func() bool {
		for _, op := range f.hub.listActiveOperations() {
			if op.Session == session && op.State == state {
				found = op
				return true
			}
		}
		return false
	})
	return found
}

func TestContextMutexAcquireHonorsCancellation(t *testing.T) {
	mutex := newContextMutex()
	if err := mutex.Acquire(context.Background()); err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	defer mutex.Release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mutex.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire = %v, want context.Canceled", err)
	}
}

type executionLifecycleCall struct {
	id      int64
	name    string
	session string
}

type executionLifecycleAgent struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	calls   chan executionLifecycleCall
	cancels chan int64
}

func (f *executionLifecycleFixture) agent(t *testing.T, runtime string) *executionLifecycleAgent {
	t.Helper()
	return f.agentWithInitialize(t, gatewayTestInitializeResult(runtime))
}

func (f *executionLifecycleFixture) agentWithInitialize(t *testing.T, initialize map[string]interface{}) *executionLifecycleAgent {
	t.Helper()
	previous := f.hub.getAgent("dev", "local")
	agent := &executionLifecycleAgent{
		conn:    dialFakeGatewayAgent(t, f.server.URL, "dev", "local"),
		calls:   make(chan executionLifecycleCall, 16),
		cancels: make(chan int64, 16),
	}
	done := make(chan struct{})
	stop := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		_ = agent.conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("controlled agent did not stop")
		}
	})
	go func() {
		defer close(done)
		for {
			var msg map[string]interface{}
			if err := agent.conn.ReadJSON(&msg); err != nil {
				return
			}
			switch msg["method"] {
			case "initialize":
				if err := agent.write(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": initialize}); err != nil {
					return
				}
			case "tools/call":
				id, _ := numericID(msg["id"])
				params, _ := msg["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				name, _ := params["name"].(string)
				session, _ := args["session_id"].(string)
				select {
				case agent.calls <- executionLifecycleCall{id: id, name: name, session: session}:
				case <-stop:
					return
				}
			case "tools/cancel":
				params, _ := msg["params"].(map[string]interface{})
				id, _ := numericID(params["id"])
				if msg["id"] != fmt.Sprintf("cancel-%d", id) {
					return
				}
				if err := agent.write(map[string]interface{}{
					"jsonrpc": "2.0", "id": msg["id"], "result": map[string]interface{}{"canceled": true},
				}); err != nil {
					return
				}
				select {
				case agent.cancels <- id:
				case <-stop:
					return
				}
			}
		}
	}()
	requireEventually(t, 3*time.Second, func() bool {
		current := f.hub.getAgent("dev", "local")
		return current != nil && current != previous
	})
	return agent
}

func (a *executionLifecycleAgent) write(msg map[string]interface{}) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.conn.WriteJSON(msg)
}

func (a *executionLifecycleAgent) call(t *testing.T, name, session string) executionLifecycleCall {
	t.Helper()
	select {
	case call := <-a.calls:
		if call.name != name || call.session != session {
			t.Fatalf("dispatched %s/%s, want %s/%s", call.name, call.session, name, session)
		}
		return call
	case <-time.After(3 * time.Second):
		t.Fatalf("%s/%s did not reach the agent", name, session)
		return executionLifecycleCall{}
	}
}

func (a *executionLifecycleAgent) complete(t *testing.T, call executionLifecycleCall, failure string) {
	t.Helper()
	msg := map[string]interface{}{"jsonrpc": "2.0", "id": call.id}
	if failure == "" {
		msg["result"] = map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "completed"}}}
	} else {
		msg["error"] = map[string]interface{}{"code": -32800, "message": failure}
	}
	if err := a.write(msg); err != nil {
		t.Fatal(err)
	}
}

func lifecycleSend(t *testing.T, client *websocket.Conn, id int, tool, session string, args map[string]interface{}) {
	t.Helper()
	args["session_id"] = session
	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]interface{}{"name": tool, "arguments": args},
	})
}

func lifecycleResponse(t *testing.T, client *websocket.Conn, id int, wantError string) map[string]interface{} {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer client.SetReadDeadline(time.Time{})
	msg := readUntilID(t, client, float64(id))
	if wantError == "" {
		if msg["error"] != nil || msg["result"] == nil {
			t.Fatalf("expected terminal success, got %#v", msg)
		}
	} else {
		rpcError, ok := msg["error"].(map[string]interface{})
		if !ok || !strings.Contains(fmt.Sprint(rpcError["message"]), wantError) {
			t.Fatalf("expected error %q, got %#v", wantError, msg)
		}
	}
	return msg
}

func TestExecutionLifecycleRequiresExactCapabilityBeforeDispatch(t *testing.T) {
	for _, change := range []string{"valid", "absent", "legacy string", "wrong version", "missing terminal", "no background lifetime", "no prompt cancellation", "unknown field"} {
		t.Run(change, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			initialize := gatewayTestInitializeResult("capability-agent")
			protocols := initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})
			capability := protocols["executionLifecycle"].(map[string]interface{})
			switch change {
			case "absent":
				delete(protocols, "executionLifecycle")
			case "legacy string":
				protocols["executionLifecycle"] = "doops.sh/execution-lifecycle/v1"
			case "wrong version":
				capability["schemaVersion"] = "doops.sh/execution-lifecycle/v0"
			case "missing terminal":
				delete(capability, "terminalResponse")
			case "no background lifetime":
				capability["backgroundLifetime"] = false
			case "no prompt cancellation":
				capability["promptCancellation"] = false
			case "unknown field":
				capability["extra"] = true
			}
			agent := f.agentWithInitialize(t, initialize)
			client := f.client(t, f.admin)
			lifecycleSend(t, client, 2, "doops_bg", "capability", map[string]interface{}{"command": "printf test"})
			if change == "valid" {
				agent.complete(t, agent.call(t, "doops_bg", "capability"), "")
				lifecycleResponse(t, client, 2, "")
			} else {
				lifecycleResponse(t, client, 2, "executionLifecycle")
				select {
				case call := <-agent.calls:
					t.Fatalf("incompatible capability reached dispatch: %#v", call)
				default:
				}
			}
		})
	}
}

func TestExecutionLifecycleForegroundCallsUseTerminalEvidenceWithoutCapability(t *testing.T) {
	for _, test := range []struct {
		name string
		tool string
		args map[string]interface{}
	}{
		{name: "shell", tool: "doops_shell", args: map[string]interface{}{"command": "printf test"}},
		{name: "prompt", tool: "doops_agent_prompt", args: map[string]interface{}{"instruction": "return []"}},
		{name: "apply prompt", tool: "doops_agent_prompt", args: map[string]interface{}{
			"operation": "apply", "instruction": "perform the verified synthetic mutation",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			initialize := gatewayTestInitializeResult("legacy-foreground-agent")
			protocols := initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})
			delete(protocols, "executionLifecycle")
			agent := f.agentWithInitialize(t, initialize)
			client := f.client(t, f.admin)
			lifecycleSend(t, client, 2, test.tool, "legacy-foreground", test.args)
			agent.complete(t, agent.call(t, test.tool, "legacy-foreground"), "")
			lifecycleResponse(t, client, 2, "")
		})
	}
}

func TestExecutionLifecycleDoesNotGateForegroundSSH(t *testing.T) {
	if requiresExecutionLifecycle("doops_ssh") {
		t.Fatal("foreground structured SSH must use terminal evidence, not executionLifecycle gating")
	}
}

func TestExecutionLifecycleInternalForegroundCallUsesTerminalEvidenceWithoutCapability(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	initialize := gatewayTestInitializeResult("legacy-internal-agent")
	protocols := initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})
	delete(protocols, "executionLifecycle")
	agent := f.agentWithInitialize(t, initialize)

	result := make(chan struct {
		output string
		err    error
	}, 1)
	go func() {
		output, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_agent_prompt", map[string]interface{}{
			"session_id":  "legacy-internal",
			"instruction": "return []",
		})
		result <- struct {
			output string
			err    error
		}{output: output, err: err}
	}()
	agent.complete(t, agent.call(t, "doops_agent_prompt", "legacy-internal"), "")
	select {
	case got := <-result:
		if got.err != nil || got.output != "completed" {
			t.Fatalf("internal foreground call failed: output=%q err=%v", got.output, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("internal foreground call did not finish")
	}
}

func TestExecutionLifecycleWaitingDoesNotConsumeUserOrGlobalQuota(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{
		MaxConcurrentOperations: 2, MaxConcurrentPerUser: 1,
		MaxQueuedPerTarget: 1, TargetQueueTimeout: 5 * time.Second,
	})
	agent := f.agent(t, "quota-agent")
	waiter := f.user(t, "waiter", ScopeGrant{Cluster: "dev", Instance: "local", Actions: []GatewayAction{ActionExec, ActionWrite}})
	holderClient, queuedClient, independentClient := f.client(t, f.admin), f.client(t, waiter), f.client(t, waiter)
	lifecycleSend(t, holderClient, 2, "doops_file_write", "occupied", map[string]interface{}{"path": "config.txt", "content": "first"})
	holderCall := agent.call(t, "doops_file_write", "occupied")
	holder := f.operation(t, "occupied", "running")
	lifecycleSend(t, queuedClient, 2, "doops_file_write", "occupied", map[string]interface{}{"path": "config.txt", "content": "second"})
	queued := f.operation(t, "occupied", "waiting")
	if len(queued.BlockedBy) != 1 || queued.BlockedBy[0] != holder.ID || queued.Resource != "workspace:occupied" {
		t.Fatalf("queued ownership is not visible: %#v", queued)
	}
	lifecycleSend(t, independentClient, 2, "doops_file_write", "independent", map[string]interface{}{"path": "config.txt", "content": "test"})
	independent := agent.call(t, "doops_file_write", "independent")
	agent.complete(t, independent, "")
	lifecycleResponse(t, independentClient, 2, "")
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 2 })
	agent.complete(t, holderCall, "")
	lifecycleResponse(t, holderClient, 2, "")
	queuedCall := agent.call(t, "doops_file_write", "occupied")
	agent.complete(t, queuedCall, "")
	lifecycleResponse(t, queuedClient, 2, "")
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
}

func TestExecutionLifecycleStructuredWorkspacesExcludeSameAndAllowDifferent(t *testing.T) {
	for _, contenderTool := range []string{"doops_file_read", "doops_file_write"} {
		t.Run(contenderTool, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			agent := f.agent(t, "workspace-agent")
			holderClient, contender := f.client(t, f.admin), f.client(t, f.admin)
			lifecycleSend(t, holderClient, 2, "doops_file_write", "workspace-a", map[string]interface{}{"path": "config.txt", "content": "a"})
			holder := agent.call(t, "doops_file_write", "workspace-a")
			lifecycleSend(t, contender, 2, contenderTool, "workspace-a", map[string]interface{}{"path": "config.txt", "content": "b"})
			lifecycleResponse(t, contender, 2, "target busy")
			lifecycleSend(t, contender, 3, contenderTool, "workspace-b", map[string]interface{}{"path": "config.txt", "content": "b"})
			parallel := agent.call(t, contenderTool, "workspace-b")
			if state := f.hub.getAgent("dev", "local").snapshot(); state.ActiveOps != 2 || len(state.Resources) != 2 {
				t.Fatalf("different workspaces did not own independent locks: %#v", state)
			}
			agent.complete(t, parallel, "")
			lifecycleResponse(t, contender, 3, "")
			agent.complete(t, holder, "")
			lifecycleResponse(t, holderClient, 2, "")
		})
	}
}

func TestExecutionLifecycleTargetMutationAllowsQueries(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "exclusive-agent")
	holderClient, other := f.client(t, f.admin), f.client(t, f.admin)
	lifecycleSend(t, holderClient, 2, "doops_shell", "mutation", map[string]interface{}{"command": "kubectl apply -f test.yaml"})
	mutation := agent.call(t, "doops_shell", "mutation")
	lifecycleSend(t, other, 2, "doops_shell", "other-mutation", map[string]interface{}{"command": "kubectl apply -f other.yaml"})
	lifecycleResponse(t, other, 2, "target busy")
	lifecycleSend(t, other, 3, "doops_node_info", "query", map[string]interface{}{})
	query := agent.call(t, "doops_node_info", "query")
	agent.complete(t, query, "")
	lifecycleResponse(t, other, 3, "")
	agent.complete(t, mutation, "")
	lifecycleResponse(t, holderClient, 2, "")
}

func TestExecutionLifecycleAgentUpgradeIsExclusive(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "upgrade-agent")
	holderClient, other := f.client(t, f.admin), f.client(t, f.admin)
	lifecycleSend(t, holderClient, 2, "doops_file_write", "workspace", map[string]interface{}{"path": "config.txt", "content": "test"})
	write := agent.call(t, "doops_file_write", "workspace")
	upgradeArgs := func() map[string]interface{} {
		return map[string]interface{}{"image": "registry.example/doops@sha256:" + strings.Repeat("a", 64), "mode": "k8s", "dry_run": true}
	}
	lifecycleSend(t, other, 4, "doops_agent_upgrade", "upgrade", upgradeArgs())
	lifecycleResponse(t, other, 4, "target busy")
	agent.complete(t, write, "")
	lifecycleResponse(t, holderClient, 2, "")
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	lifecycleSend(t, holderClient, 3, "doops_agent_upgrade", "upgrade", upgradeArgs())
	upgrade := agent.call(t, "doops_agent_upgrade", "upgrade")
	lifecycleSend(t, other, 5, "doops_node_info", "query", map[string]interface{}{})
	lifecycleResponse(t, other, 5, "target busy")
	lifecycleSend(t, other, 6, "doops_file_write", "independent", map[string]interface{}{"path": "config.txt", "content": "test"})
	lifecycleResponse(t, other, 6, "target busy")
	agent.complete(t, upgrade, "")
	lifecycleResponse(t, holderClient, 3, "")
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	lifecycleSend(t, other, 7, "doops_node_info", "query", map[string]interface{}{})
	agent.complete(t, agent.call(t, "doops_node_info", "query"), "")
	lifecycleResponse(t, other, 7, "")
}

func TestExecutionLifecycleCancelAckRetainsOwnershipUntilOriginalTerminal(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "cancel-agent")
	client, probe := f.client(t, f.admin), f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_file_write", "cancel-owner", map[string]interface{}{"path": "config.txt", "content": "first"})
	call := agent.call(t, "doops_file_write", "cancel-owner")
	op := f.operation(t, "cancel-owner", "running")
	data := f.http(t, f.admin, http.MethodDelete, "/v1/admin/operations?id="+op.ID, "", http.StatusAccepted)
	var accepted map[string]interface{}
	if err := json.Unmarshal(data, &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted["state"] != "cancel_requested" || accepted["cancel_requested"] != true || accepted["id"] != op.ID {
		t.Fatalf("cancel endpoint claimed completion: %#v", accepted)
	}
	select {
	case id := <-agent.cancels:
		if id != call.id {
			t.Fatalf("canceled %d, want original request %d", id, call.id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received cancellation")
	}
	// A subsequent reply on the same agent connection proves its ACK was consumed.
	lifecycleSend(t, probe, 2, "doops_node_info", "barrier", map[string]interface{}{})
	agent.complete(t, agent.call(t, "doops_node_info", "barrier"), "")
	lifecycleResponse(t, probe, 2, "")
	owned := f.operation(t, "cancel-owner", "cancel_requested")
	if owned.ID != op.ID || owned.AuditID != op.AuditID {
		t.Fatalf("ACK replaced original ownership: %#v", owned)
	}
	events, err := f.store.UnresolvedExecutions("dev", "local")
	if err != nil || len(events) != 1 || events[0].ID != op.AuditID || events[0].Status != "executing" {
		t.Fatalf("ACK retired execution audit: %#v, %v", events, err)
	}
	lifecycleSend(t, probe, 3, "doops_file_write", "cancel-owner", map[string]interface{}{"path": "config.txt", "content": "blocked"})
	lifecycleResponse(t, probe, 3, "target busy")
	f.http(t, f.admin, http.MethodPost, "/v1/targets/unlock?cluster=dev&instance=local", "", http.StatusConflict)
	f.http(t, f.admin, http.MethodPost, "/v1/admin/operations",
		fmt.Sprintf(`{"id":"audit-%d","confirm_stopped":true,"reason":"external check","evidence":"operator report"}`, op.AuditID),
		http.StatusConflict)
	agent.complete(t, call, "operation canceled")
	lifecycleResponse(t, client, 2, "operation canceled")
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	lifecycleSend(t, probe, 4, "doops_file_write", "cancel-owner", map[string]interface{}{"path": "config.txt", "content": "released"})
	agent.complete(t, agent.call(t, "doops_file_write", "cancel-owner"), "")
	lifecycleResponse(t, probe, 4, "")
}

func TestExecutionLifecycleDisconnectPersistsAcrossReconnectAndRestart(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "lost-agent")
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_shell", "lost-owner", map[string]interface{}{"command": "sleep 1"})
	agent.call(t, "doops_shell", "lost-owner")
	op := f.operation(t, "lost-owner", "running")
	// No terminal reply is sent; closing the connection avoids the 30-second grace.
	_ = agent.conn.Close()
	lifecycleResponse(t, client, 2, "execution_outcome_unknown")
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	replacement := f.agent(t, "replacement-agent")
	reconnected := f.client(t, f.admin)
	lifecycleSend(t, reconnected, 2, "doops_file_write", "different-workspace", map[string]interface{}{"path": "config.txt", "content": "blocked"})
	lifecycleResponse(t, reconnected, 2, "execution_outcome_unknown")
	f.http(t, f.admin, http.MethodPost, "/v1/targets/unlock?cluster=dev&instance=local", "", http.StatusConflict)
	lifecycleSend(t, reconnected, 3, "doops_node_info", "safe-query", map[string]interface{}{})
	replacement.complete(t, replacement.call(t, "doops_node_info", "safe-query"), "")
	lifecycleResponse(t, reconnected, 3, "")
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	_ = client.Close()
	_ = reconnected.Close()
	_ = replacement.conn.Close()
	requireEventually(t, 3*time.Second, func() bool { return f.hub.getAgent("dev", "local") == nil })
	f.server.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.open(t)
	restarted := f.agent(t, "restart-agent")
	afterRestart := f.client(t, f.admin)
	lifecycleSend(t, afterRestart, 2, "doops_shell", "new-workspace", map[string]interface{}{"command": "printf blocked"})
	lifecycleResponse(t, afterRestart, 2, "execution_outcome_unknown")
	events, err := f.store.UnresolvedExecutions("dev", "local")
	if err != nil || len(events) != 1 || events[0].ID != op.AuditID || events[0].Status != "outcome_unknown" || events[0].EndedAt.IsZero() {
		t.Fatalf("unknown outcome did not survive restart: %#v, %v", events, err)
	}
	var listed struct {
		Operations []GatewayActiveOperation `json:"operations"`
	}
	if err := json.Unmarshal(f.http(t, f.admin, http.MethodGet, "/v1/admin/operations", "", http.StatusOK), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Operations) != 1 || listed.Operations[0].ID != fmt.Sprintf("audit-%d", op.AuditID) ||
		listed.Operations[0].State != "outcome_unknown" || listed.Operations[0].Session != "lost-owner" {
		t.Fatalf("persistent blocker missing from admin display: %#v", listed)
	}
	scoped := f.user(t, "target-admin", ScopeGrant{Cluster: "dev", Instance: "local", Actions: []GatewayAction{ActionAdmin}})
	body := fmt.Sprintf(`{"id":"audit-%d","confirm_stopped":true,"reason":"operator reconciled externally","evidence":"external process inspection"}`, op.AuditID)
	f.http(t, scoped, http.MethodPost, "/v1/admin/operations", body, http.StatusForbidden)
	f.http(t, f.admin, http.MethodPost, "/v1/admin/operations", strings.Replace(body, `"confirm_stopped":true`, `"confirm_stopped":false`, 1), http.StatusBadRequest)
	f.http(t, f.admin, http.MethodPost, "/v1/admin/operations", body+` {}`, http.StatusBadRequest)
	f.http(t, f.admin, http.MethodPost, "/v1/admin/operations", body+` trailing`, http.StatusBadRequest)
	lifecycleSend(t, afterRestart, 3, "doops_shell", "new-workspace", map[string]interface{}{"command": "printf still-blocked"})
	lifecycleResponse(t, afterRestart, 3, "execution_outcome_unknown")
	f.http(t, f.admin, http.MethodPost, "/v1/admin/operations", body, http.StatusOK)
	lifecycleSend(t, afterRestart, 4, "doops_shell", "new-workspace", map[string]interface{}{"command": "printf reconciled"})
	restarted.complete(t, restarted.call(t, "doops_shell", "new-workspace"), "")
	lifecycleResponse(t, afterRestart, 4, "")
	resolutions, err := f.store.ListAuditFiltered(AuditFilter{Action: GatewayAction("execution:resolve"), Limit: 10})
	if err != nil || len(resolutions) != 1 {
		t.Fatalf("manual reconciliation audit missing: %#v, %v", resolutions, err)
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(resolutions[0].CommandSummary), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["machine_verified"] != false || metadata["attestation"] != "administrator_external_reconciliation" {
		t.Fatalf("manual reconciliation claimed machine verification: %#v", metadata)
	}
}

func TestExecutionLifecycleReplacementCannotInheritStillActiveOldGeneration(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	old := f.agent(t, "old-generation")
	oldGeneration := f.hub.getAgent("dev", "local").Generation
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	responseReached := make(chan []byte, 1)
	releaseResponse := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseResponse) })
	forward := func(raw []byte) error {
		responseReached <- append([]byte(nil), raw...)
		<-releaseResponse
		return nil
	}
	done := make(chan error, 1)
	// Stall delivery of the old call's failure so its active owner and executing
	// audit remain present after registry replacement, not just after cleanup.
	go func() {
		done <- f.hub.client.handleGatewayToolCall(context.Background(), auth, "dev", "local", api.JSONRPCRequest{
			JSONRPC: "2.0", ID: 2, Method: "tools/call",
			Params: json.RawMessage(`{"name":"doops_shell","arguments":{"session_id":"old-owner","command":"sleep 1"}}`),
		}, forward, func(value interface{}) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			return forward(raw)
		})
	}()
	old.call(t, "doops_shell", "old-owner")
	owner := f.operation(t, "old-owner", "running")
	replacement := f.agent(t, "new-generation")
	if generation := f.hub.getAgent("dev", "local").Generation; generation <= oldGeneration {
		t.Fatalf("agent generation did not advance: %d -> %d", oldGeneration, generation)
	}
	select {
	case raw := <-responseReached:
		if !strings.Contains(string(raw), "execution_outcome_unknown") {
			t.Fatalf("old generation produced a false terminal outcome: %s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replacement did not interrupt the old transport")
	}
	stillActive := f.operation(t, "old-owner", "running")
	if stillActive.ID != owner.ID {
		t.Fatal("test lost the original active owner before checking replacement")
	}
	events, err := f.store.UnresolvedExecutions("dev", "local")
	if err != nil || len(events) != 1 || events[0].ID != owner.AuditID || events[0].Status != "executing" {
		t.Fatalf("test must retain old executing audit: %#v, %v", events, err)
	}
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_file_write", "new-owner", map[string]interface{}{"path": "config.txt", "content": "blocked"})
	lifecycleResponse(t, client, 2, "execution_outcome_unknown")
	select {
	case call := <-replacement.calls:
		t.Fatalf("new generation bypassed old active ownership: %#v", call)
	default:
	}
	releaseOnce.Do(func() { close(releaseResponse) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old operation failed to leave after response was released")
	}
	requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	lifecycleSend(t, client, 3, "doops_file_write", "new-owner", map[string]interface{}{"path": "config.txt", "content": "still-blocked"})
	lifecycleResponse(t, client, 3, "execution_outcome_unknown")
}
