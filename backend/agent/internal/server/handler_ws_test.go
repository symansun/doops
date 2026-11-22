package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentReverseTunnelReadyOnlyAfterGatewayRegistration(t *testing.T) {
	gw := NewGateway()
	ready := make(chan struct{})
	var readyOnce sync.Once
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		gw.ServeGatewayWebSocketConnWithReady(conn, r.RemoteAddr, func() {
			readyOnce.Do(func() { close(ready) })
		})
	}))
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)
	select {
	case <-ready:
		t.Fatal("initialize must not mark reverse tunnel ready before registry acknowledgement")
	case <-time.After(50 * time.Millisecond):
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "gateway/registered",
	}); err != nil {
		t.Fatalf("send gateway registration acknowledgement: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("gateway registration acknowledgement did not mark reverse tunnel ready")
	}
}

func TestAgentWebSocketShellSnapshotNoOpDoesNotPanic(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	for _, command := range []string{
		": # kubectl apply -f -f x",
		"kubectl() { return 0; }; kubectl apply -f ",
		"printf 'independent command completed\\n'",
	} {
		result := callToolResult(t, conn, "doops_shell", map[string]interface{}{
			"session_id": "snapshot-noop", "command": command,
		})
		if result["error"] != nil || result["result"] == nil ||
			result["result"].(map[string]interface{})["isError"] == true {
			t.Fatalf("no-op or subsequent command failed: %q: %#v", command, result)
		}
	}
}

func TestAgentInitializeDoesNotExposeDedicatedCICDReconciliation(t *testing.T) {
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()

	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}

	var response map[string]interface{}
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("read initialize: %v", err)
	}
	result, _ := response["result"].(map[string]interface{})
	capabilities, _ := result["capabilities"].(map[string]interface{})
	for capability := range capabilities {
		if strings.Contains(strings.ToLower(capability), "deployment") {
			t.Fatalf("CI/CD must enter doagent through Ask, not a dedicated tool: %#v", capabilities)
		}
	}
}

func TestShellNotificationIncludesRawKind(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	rawArgs, _ := json.Marshal(map[string]interface{}{
		"session_id": "kind-shell",
		"command":    "printf 'raw-kind\\n'",
	})
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      43,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_shell",
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("call shell: %v", err)
	}

	msg := readNotification(t, conn)
	params, _ := msg["params"].(map[string]interface{})
	if params["kind"] != "raw" {
		t.Fatalf("expected raw notification kind, got %#v", params)
	}
	if params["data"] != "raw-kind\n" {
		t.Fatalf("expected shell output, got %#v", params["data"])
	}

	for {
		var response map[string]interface{}
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatalf("read shell response: %v", err)
		}
		if response["id"] != float64(43) {
			continue
		}
		if errObj, ok := response["error"]; ok {
			t.Fatalf("shell returned error: %#v", errObj)
		}
		break
	}
}

func TestAgentWebSocketCancelKillsShellCommand(t *testing.T) {
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	rawArgs, _ := json.Marshal(map[string]interface{}{
		"command":    "sleep 30",
		"session_id": "cancel-shell",
	})
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      float64(42),
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_shell",
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("send shell call: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      float64(43),
		"method":  "tools/cancel",
		"params":  map[string]interface{}{"id": float64(42)},
	}); err != nil {
		t.Fatalf("send shell cancel: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	var sawCancelAck bool
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read cancel response: %v", err)
		}
		if method, _ := msg["method"].(string); method != "" {
			continue
		}
		switch id, _ := msg["id"].(float64); id {
		case 43:
			result, _ := msg["result"].(map[string]interface{})
			if result["canceled"] != true {
				t.Fatalf("expected cancel ack, got %#v", msg)
			}
			sawCancelAck = true
		case 42:
			errObj, _ := msg["error"].(map[string]interface{})
			if !strings.Contains(fmt.Sprint(errObj["message"]), "operation canceled") {
				t.Fatalf("expected operation canceled, got %#v", msg)
			}
			if !sawCancelAck {
				t.Fatal("tool result arrived before cancel ack")
			}
			return
		}
	}
}

func TestAgentWebSocketRejectsBlockedShellCommand(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_shell", map[string]interface{}{
		"session_id": "blocked-shell",
		"command":    "rm -rf /",
	})
	if result["error"] == nil {
		t.Fatalf("expected blocked command error, got %#v", result)
	}
}

func TestAgentWebSocketReadLimitRejectsOversizedFrame(t *testing.T) {
	t.Setenv("DOOPS_MAX_WS_MESSAGE_BYTES", "128")
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	large := strings.Repeat("x", 512)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "doops_shell",
			"arguments": map[string]interface{}{
				"session_id": "large",
				"command":    large,
			},
		},
	}); err != nil {
		t.Fatalf("write oversized frame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	var msg map[string]interface{}
	if err := conn.ReadJSON(&msg); err == nil {
		t.Fatalf("expected oversized frame to close connection, got %#v", msg)
	}
}

func TestBackgroundTaskLogPathStaysInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	gw := NewGateway()
	if _, err := gw.submitBgTask("bg", "true", "/tmp/outside.log"); err == nil {
		t.Fatal("expected absolute background log path to be rejected")
	}
	task, err := gw.submitBgTask("bg", "printf ok", "logs/task.log")
	if err != nil {
		t.Fatalf("submit bg task: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(task.LogPath), filepath.Join(root, "bg")+string(os.PathSeparator)) {
		t.Fatalf("log path escaped workspace: %s", task.LogPath)
	}
}
