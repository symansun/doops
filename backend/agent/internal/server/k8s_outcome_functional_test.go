package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/internal/dispatcher"
)

type k8sFunctionalAgent struct {
	fixture *executionLifecycleFixture
	conn    *websocket.Conn
	started string
	release string
}

func newK8SFunctionalAgent(t *testing.T, mode string) *k8sFunctionalAgent {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(root, "kubectl.started")
	release := filepath.Join(root, "kubectl.release")
	originalPath := os.Getenv("PATH")
	if mode == "missing" {
		t.Setenv("PATH", bin)
	} else {
		t.Setenv("PATH", bin+string(os.PathListSeparator)+originalPath)
		if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(`#!/bin/sh
set -eu
touch "$K8S_STARTED"
case "$K8S_MODE" in
success)
  printf '%s\n' 'synthetic kubectl success'
  exit 0
  ;;
failure)
  printf '%s\n' 'synthetic kubectl failure' >&2
  exit 7
  ;;
cancel)
  while [ ! -f "$K8S_RELEASE" ]; do sleep 0.01; done
  exit 0
  ;;
*)
  exit 9
  ;;
esac
`), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("K8S_MODE", mode)
	t.Setenv("K8S_STARTED", started)
	t.Setenv("K8S_RELEASE", release)

	fixture := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agentGateway := &Gateway{
		Dispatcher: dispatcher.NewDispatcher(),
		tasks:      make(map[string]*bgTask),
		sessionMap: make(map[string]*sessionEntry),
	}
	conn := dialFakeGatewayAgent(t, fixture.server.URL, "dev", "local")
	done := make(chan struct{})
	go func() {
		defer close(done)
		agentGateway.ServeGatewayWebSocketConnWithReady(conn, "k8s-functional", nil)
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("real Agent did not stop")
		}
	})
	waitForGatewayAgent(t, fixture.hub, "dev", "local")
	return &k8sFunctionalAgent{
		fixture: fixture,
		conn:    conn,
		started: started,
		release: release,
	}
}

func TestK8SMutationOutcomeThroughRealAgentGateway(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newK8SFunctionalAgent(t, "success")
		client := f.fixture.client(t, f.fixture.admin)
		lifecycleSend(t, client, 2, "doops_k8s", "k8s-success", map[string]interface{}{
			"operation": "scale", "resource": "deployment/app", "replicas": 2, "confirm": true,
		})
		response := readK8SFunctionalResponse(t, client, 2)
		result := requireK8SToolResult(t, response)
		if result["isError"] == true {
			t.Fatalf("successful mutation returned tool error: %#v", result)
		}
	})

	t.Run("kubectl nonzero after start is unknown", func(t *testing.T) {
		f := newK8SFunctionalAgent(t, "failure")
		client := f.fixture.client(t, f.fixture.admin)
		lifecycleSend(t, client, 2, "doops_k8s", "k8s-nonzero", map[string]interface{}{
			"operation": "scale", "resource": "deployment/app", "replicas": 2, "confirm": true,
		})
		waitForK8SFunctionalFile(t, f.started)
		response := readK8SFunctionalResponse(t, client, 2)
		assertK8SUnknownResponse(t, response)
	})

	t.Run("cancel after start is unknown", func(t *testing.T) {
		f := newK8SFunctionalAgent(t, "cancel")
		client := f.fixture.client(t, f.fixture.admin)
		lifecycleSend(t, client, 2, "doops_k8s", "k8s-cancel", map[string]interface{}{
			"operation": "scale", "resource": "deployment/app", "replicas": 2, "confirm": true,
		})
		waitForK8SFunctionalFile(t, f.started)
		op := f.fixture.operation(t, "k8s-cancel", "running")
		f.fixture.cancel(t, op.ID)
		assertK8SUnknownResponse(t, readK8SFunctionalResponse(t, client, 2))
		_ = os.WriteFile(f.release, []byte("release\n"), 0o600)
	})
}

func TestK8SAdmissionAndStartFailuresRemainDefiniteThroughRealAgentGateway(t *testing.T) {
	t.Run("parameter rejection", func(t *testing.T) {
		f := newK8SFunctionalAgent(t, "missing")
		client := f.fixture.client(t, f.fixture.admin)
		lifecycleSend(t, client, 2, "doops_k8s", "k8s-parameter-rejected", map[string]interface{}{
			"operation": "scale", "resource": "deployment/app", "replicas": 2,
		})
		result := requireK8SToolResult(t, readK8SFunctionalResponse(t, client, 2))
		if result["isError"] != true || !strings.Contains(fmt.Sprint(result), "confirm=true") {
			t.Fatalf("parameter rejection was not explicit: %#v", result)
		}
		if strings.Contains(fmt.Sprint(result), errExecutionOutcomeUnknown.Error()) {
			t.Fatalf("parameter rejection became unknown: %#v", result)
		}
	})

	t.Run("kubectl start failure", func(t *testing.T) {
		f := newK8SFunctionalAgent(t, "missing")
		client := f.fixture.client(t, f.fixture.admin)
		lifecycleSend(t, client, 2, "doops_k8s", "k8s-start-failed", map[string]interface{}{
			"operation": "scale", "resource": "deployment/app", "replicas": 2, "confirm": true,
		})
		result := requireK8SToolResult(t, readK8SFunctionalResponse(t, client, 2))
		if result["isError"] != true || !strings.Contains(fmt.Sprint(result), "start kubectl") {
			t.Fatalf("kubectl start failure was not explicit: %#v", result)
		}
		if strings.Contains(fmt.Sprint(result), errExecutionOutcomeUnknown.Error()) {
			t.Fatalf("kubectl start failure became unknown: %#v", result)
		}
	})
}

func TestK8SReadOnlyNonzeroRemainsDefiniteThroughRealAgentGateway(t *testing.T) {
	f := newK8SFunctionalAgent(t, "failure")
	client := f.fixture.client(t, f.fixture.admin)
	lifecycleSend(t, client, 2, "doops_k8s", "k8s-read-failed", map[string]interface{}{
		"operation": "get", "kind": "pods",
	})
	waitForK8SFunctionalFile(t, f.started)
	result := requireK8SToolResult(t, readK8SFunctionalResponse(t, client, 2))
	if result["isError"] != true || !strings.Contains(fmt.Sprint(result), "kubectl get pods failed") {
		t.Fatalf("read-only nonzero was not an explicit tool error: %#v", result)
	}
	if strings.Contains(fmt.Sprint(result), errExecutionOutcomeUnknown.Error()) {
		t.Fatalf("read-only nonzero became unknown: %#v", result)
	}
}

func TestGatewayRelayDrainsOutputBeforeTerminalOnAgentDisconnect(t *testing.T) {
	serverConn := make(chan *websocket.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConn <- conn
	}))
	defer ts.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial agent websocket: %v", err)
	}
	defer clientConn.Close()

	agent := &GatewayAgent{
		conn:           <-serverConn,
		pending:        make(map[int64]*gatewayMessageQueue),
		activeSessions: make(map[string]int),
		messageBudget:  newGatewayMessageBudget(64 << 10),
		closed:         make(chan struct{}),
	}
	dispatchSeen := make(chan struct{})
	go func() {
		for {
			_, data, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]interface{}
			if json.Unmarshal(data, &message) == nil && message["method"] == "tools/call" {
				close(dispatchSeen)
				return
			}
		}
	}()

	forwarded := make(chan gatewayWSMessage, 2)
	done := make(chan error, 1)
	go func() {
		done <- agent.relayToolCall(context.Background(), api.ToolCallParams{
			Name:      "doops_shell",
			Arguments: json.RawMessage(`{"session_id":"relay-order","command":"printf ok"}`),
		}, time.Second, func(message gatewayWSMessage) error {
			forwarded <- message
			return nil
		})
	}()
	select {
	case <-dispatchSeen:
	case <-time.After(time.Second):
		t.Fatal("Gateway did not dispatch the tool call")
	}

	requireEventually(t, time.Second, func() bool {
		agent.pendingMu.Lock()
		defer agent.pendingMu.Unlock()
		return agent.pending[1] != nil
	})
	agent.pendingMu.Lock()
	queue := agent.pending[1]
	agent.pendingMu.Unlock()
	if err := queue.enqueue(gatewayWSMessage{
		Parsed: map[string]interface{}{
			"jsonrpc": "2.0", "method": "notifications/message",
			"params": map[string]interface{}{"data": "output-before-terminal\n"},
		},
	}); err != nil {
		t.Fatalf("enqueue output: %v", err)
	}
	terminal := gatewayWSMessage{
		Parsed: map[string]interface{}{
			"jsonrpc": "2.0", "id": float64(1),
			"result": map[string]interface{}{"content": []interface{}{}},
		},
	}
	queue.observeTerminal(terminal)
	if err := queue.enqueue(terminal); err != nil {
		t.Fatalf("enqueue terminal: %v", err)
	}
	close(agent.closed)

	if err := <-done; err != nil {
		t.Fatalf("relay returned error: %v", err)
	}
	first := <-forwarded
	second := <-forwarded
	if first.Parsed["method"] != "notifications/message" {
		t.Fatalf("terminal bypassed queued output: first=%#v second=%#v", first.Parsed, second.Parsed)
	}
	if _, ok := second.Parsed["id"]; !ok {
		t.Fatalf("terminal response was not forwarded after output: %#v", second.Parsed)
	}
}

func readK8SFunctionalResponse(t *testing.T, client *websocket.Conn, id int) map[string]interface{} {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer client.SetReadDeadline(time.Time{})
	return readUntilID(t, client, float64(id))
}

func requireK8SToolResult(t *testing.T, response map[string]interface{}) map[string]interface{} {
	t.Helper()
	if response["error"] != nil {
		t.Fatalf("expected a terminal tool result, got RPC error: %#v", response)
	}
	result, ok := response["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing terminal tool result: %#v", response)
	}
	return result
}

func assertK8SUnknownResponse(t *testing.T, response map[string]interface{}) {
	t.Helper()
	rpcError, ok := response["error"].(map[string]interface{})
	if !ok || !strings.Contains(fmt.Sprint(rpcError["message"]), errExecutionOutcomeUnknown.Error()) {
		t.Fatalf("expected unknown RPC error, got %#v", response)
	}
	if response["result"] != nil {
		t.Fatalf("unknown outcome was converted to a tool result: %#v", response)
	}
}

func waitForK8SFunctionalFile(t *testing.T, path string) {
	t.Helper()
	requireEventually(t, 3*time.Second, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
}
