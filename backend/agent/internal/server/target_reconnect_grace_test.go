package server

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// rpcResult is the terminal result of one client tools/call, including the raw
// text the CLI receives and how long the call took end to end.
type rpcResult struct {
	err     map[string]interface{}
	text    string
	elapsed time.Duration
}

func rpcCallInline(t *testing.T, client *websocket.Conn, id int, tool string, args map[string]interface{}) rpcResult {
	t.Helper()
	if args == nil {
		args = map[string]interface{}{}
	}
	started := time.Now()
	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]interface{}{"name": tool, "arguments": args},
	})
	msg := readUntilID(t, client, float64(id))
	out := rpcResult{elapsed: time.Since(started)}
	if rpcError, ok := msg["error"].(map[string]interface{}); ok {
		out.err = rpcError
		return out
	}
	var text strings.Builder
	if result, ok := msg["result"].(map[string]interface{}); ok {
		content, _ := result["content"].([]interface{})
		for _, item := range content {
			block, _ := item.(map[string]interface{})
			if block["type"] == "text" {
				if s, ok := block["text"].(string); ok {
					text.WriteString(s)
				}
			}
		}
	}
	out.text = text.String()
	return out
}

// requireTargetOffline asserts the CLI-facing offline contract: code -32004 and
// exactly "target offline: dev/local", which probesGatewayTarget matches.
func requireTargetOffline(t *testing.T, got rpcResult) {
	t.Helper()
	if got.err == nil {
		t.Fatalf("expected target offline, got success %q", got.text)
	}
	if code, _ := got.err["code"].(float64); code != -32004 {
		t.Fatalf("expected -32004, got %#v", got.err)
	}
	if message, _ := got.err["message"].(string); message != "target offline: dev/local" {
		t.Fatalf("expected exact offline text, got %q", message)
	}
}

// rpcClientForTarget connects a user RPC client for a plain user (not admin)
// with an optional target grant.
func rpcClientForTarget(t *testing.T, f *executionLifecycleFixture, name string, grant *ScopeGrant) *websocket.Conn {
	t.Helper()
	user, err := f.store.CreateUser(name)
	if err != nil {
		t.Fatal(err)
	}
	if grant != nil {
		if err := f.store.GrantUser(user.ID, *grant); err != nil {
			t.Fatal(err)
		}
	}
	token, err := f.store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	uri := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/v1/rpc?cluster=dev&instance=local"
	conn, _, err := websocket.DefaultDialer.Dial(uri, http.Header{"Authorization": []string{"Bearer " + token.Plaintext}})
	if err != nil {
		t.Fatalf("dial client websocket for %s: %v", name, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestGatewayToolCallWaitsForReconnectingAgent(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: 10 * time.Second, AllowOpenAgentRegistration: true})
	client := rpcClientForTarget(t, f, "waiter", &ScopeGrant{Cluster: "dev", Instance: "local"})

	// The Agent registers only after the call has started waiting.
	registeredAt := make(chan time.Time, 1)
	go func() {
		time.Sleep(2 * time.Second)
		conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
		t.Cleanup(func() { _ = conn.Close() })
		go serveFakeAgent(t, conn)
		registeredAt <- time.Now()
	}()

	got := rpcCallInline(t, client, 2, "doops_bg", map[string]interface{}{"session_id": "reconnect", "command": "true"})
	if got.err != nil {
		t.Fatalf("call during reconnect failed instead of waiting: %#v", got.err)
	}
	if got.elapsed < 1500*time.Millisecond {
		t.Fatalf("call returned in %s, before the Agent registered at +2s", got.elapsed)
	}
	<-registeredAt

	var audit AuditEvent
	requireEventually(t, 3*time.Second, func() bool {
		events, err := f.store.ListAuditFiltered(AuditFilter{Session: "reconnect", Limit: 10})
		if err != nil {
			return false
		}
		for _, e := range events {
			if e.Status == "success" {
				audit = e
				return true
			}
		}
		return false
	})
	if audit.Cluster != "dev" || audit.Instance != "local" {
		t.Fatalf("unexpected success audit target: %#v", audit)
	}
}

func TestGatewayToolCallGraceExpiresToTargetOffline(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: time.Second})
	client := rpcClientForTarget(t, f, "grace-expired-user", &ScopeGrant{Cluster: "dev", Instance: "local"})

	got := rpcCallInline(t, client, 3, "doops_bg", map[string]interface{}{"session_id": "grace-expired", "command": "true"})
	requireTargetOffline(t, got)
	if got.elapsed < 800*time.Millisecond || got.elapsed > 2500*time.Millisecond {
		t.Fatalf("grace wait took %s, want about 1s", got.elapsed)
	}
}

func TestGatewayLivenessProbesDoNotWaitForReconnect(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: 10 * time.Second})
	client := rpcClientForTarget(t, f, "prober", &ScopeGrant{Cluster: "dev", Instance: "local"})

	for i, tool := range []string{"doops_node_info", "doops_task_status"} {
		got := rpcCallInline(t, client, 10+i, tool, map[string]interface{}{"task_id": "probe"})
		requireTargetOffline(t, got)
		if got.elapsed > 500*time.Millisecond {
			t.Fatalf("%s waited %s for a reconnecting Agent; liveness probes must stay immediate", tool, got.elapsed)
		}
	}
}

// agentRegistryWaitPending reports whether any goroutine is currently blocked in
// AgentRegistry.Wait, read from runtime stacks. Only this test issues a call.
func agentRegistryWaitPending() bool {
	buf := make([]byte, 1<<20)
	for _, frame := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if strings.Contains(frame, "AgentRegistry).Wait(") {
			return true
		}
	}
	return false
}

func TestGatewayToolCallWaitEndsOnClientDisconnect(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: 30 * time.Second})
	client := rpcClientForTarget(t, f, "disconnecter", &ScopeGrant{Cluster: "dev", Instance: "local"})

	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_bg", "arguments": map[string]interface{}{"session_id": "client-gone", "command": "true"}},
	})
	// The call is inside the registry wait while the Agent stays offline.
	requireEventually(t, 2*time.Second, func() bool { return agentRegistryWaitPending() })

	// Closing the client ends the wait promptly instead of running out the 30s grace.
	_ = client.Close()
	requireEventually(t, 2*time.Second, func() bool { return !agentRegistryWaitPending() })
	// The detached call must be gone, not merely missing the next poll.
	time.Sleep(500 * time.Millisecond)
	if agentRegistryWaitPending() {
		t.Fatal("grace wait survived the client disconnect")
	}
}

func TestGatewayToolCallOnlineTargetHasNoGraceDelay(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: 10 * time.Second})
	agent := f.agent(t, "online-agent")
	client := f.client(t, f.admin)

	go func() {
		agent.complete(t, <-agent.calls, "")
	}()
	got := rpcCallInline(t, client, 6, "doops_bg", map[string]interface{}{"session_id": "online", "command": "true"})
	if got.err != nil {
		t.Fatalf("online target failed: %#v", got.err)
	}
	if got.elapsed > time.Second {
		t.Fatalf("online target call took %s; grace must not delay an online Agent", got.elapsed)
	}
}

func TestGatewayToolCallWithoutGrantFailsBeforeWaiting(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: 10 * time.Second})
	// No grant: the Gateway refuses the RPC handshake itself, before any Agent
	// lookup or grace wait.
	uri := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/v1/rpc?cluster=dev&instance=local"
	user, err := f.store.CreateUser("no-grant")
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "no-grant"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	conn, resp, err := websocket.DefaultDialer.Dial(uri, http.Header{"Authorization": []string{"Bearer " + token.Plaintext}})
	if conn != nil {
		_ = conn.Close()
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("ungranted dial took %s; authorization must run before any wait", elapsed)
	}
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("expected a forbidden dial, got err=%v status=%v", err, resp)
	}

	// The same connect with the grant in place returns immediately; only a
	// granted user can reach the RPC loop where a call may wait.
	started = time.Now()
	granted := rpcClientForTarget(t, f, "granted-before-wait", &ScopeGrant{Cluster: "dev", Instance: "local"})
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("granted dial took %s", elapsed)
	}
	writeGatewayJSON(t, granted, map[string]interface{}{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_bg", "arguments": map[string]interface{}{"session_id": "granted", "command": "true"}},
	})
	// The call is parked in the grace wait, past authorization.
	requireEventually(t, 2*time.Second, func() bool { return agentRegistryWaitPending() })
}

func TestInternalToolCallWaitsForReconnectingAgent(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: 10 * time.Second})
	go func() {
		time.Sleep(700 * time.Millisecond)
		conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
		t.Cleanup(func() { _ = conn.Close() })
		go serveFakeAgent(t, conn)
	}()

	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_bg", map[string]interface{}{"session_id": "internal", "command": "true"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("internal call during reconnect failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("internal call did not wait for the reconnecting Agent")
	}
	if elapsed := time.Since(started); elapsed < 500*time.Millisecond {
		t.Fatalf("internal call returned in %s; it should have waited for registration", elapsed)
	}
}

func TestInternalToolCallWaitIsBoundedByCallerContext(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{TargetReconnectGrace: 30 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := f.hub.RunInternalToolCall(ctx, "dev", "local", "doops_bg", map[string]interface{}{"session_id": "internal", "command": "true"})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the internal call to fail when the Agent never registered")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("internal call ignored the caller context and kept waiting for the 30s grace")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("internal call took %s; it must be bounded by the caller context", elapsed)
	}
}

func TestGatewayHubReconnectGraceNormalization(t *testing.T) {
	hubWith := func(opts GatewayHubOptions) *GatewayHub {
		store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return NewGatewayHub(store, opts)
	}
	if got := hubWith(GatewayHubOptions{}).opts.TargetReconnectGrace; got != 10*time.Second {
		t.Fatalf("unset grace normalized to %s, want 10s", got)
	}
	if got := hubWith(GatewayHubOptions{TargetReconnectGrace: time.Second}).opts.TargetReconnectGrace; got != time.Second {
		t.Fatalf("explicit grace changed to %s", got)
	}
	// ReconnectGraceOption maps a configured 0 to "no wait"; the hub keeps it.
	hub := hubWith(GatewayHubOptions{TargetReconnectGrace: ReconnectGraceOption(0)})
	if hub.opts.TargetReconnectGrace != 0 {
		t.Fatalf("configured zero grace became %s, want no wait", hub.opts.TargetReconnectGrace)
	}
	started := time.Now()
	if agent := hub.registry.Wait(context.Background(), "dev", "local", hub.opts.TargetReconnectGrace); agent != nil {
		t.Fatal("zero grace returned an agent for an offline target")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("zero grace waited %s", elapsed)
	}
	// A negative value is "unset" and keeps the 10s default.
	if got := hubWith(GatewayHubOptions{TargetReconnectGrace: -time.Second}).opts.TargetReconnectGrace; got != 10*time.Second {
		t.Fatalf("negative grace normalized to %s, want 10s", got)
	}
}

func TestGatewayToolReconnectGraceExemptsLivenessProbes(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewGatewayHub(store, GatewayHubOptions{TargetReconnectGrace: 10 * time.Second}).client
	for tool, want := range map[string]time.Duration{
		"doops_node_info":    0,
		"doops_task_status":  0,
		"doops_bg":           10 * time.Second,
		"doops_agent_prompt": 10 * time.Second,
	} {
		if got := service.targetReconnectGrace(tool); got != want {
			t.Fatalf("%s grace = %s, want %s", tool, got, want)
		}
	}
}
