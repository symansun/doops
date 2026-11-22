package server

import (
	"github.com/gorilla/websocket"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireEventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func dialFakeGatewayAgent(t *testing.T, serverURL, cluster, instance string) *websocket.Conn {
	t.Helper()
	agentWS := "ws" + strings.TrimPrefix(serverURL, "http") + "/v1/agent/connect?cluster=" + cluster + "&instance=" + instance
	agentConn, _, err := websocket.DefaultDialer.Dial(agentWS, nil)
	if err != nil {
		t.Fatalf("dial agent websocket: %v", err)
	}
	return agentConn
}

func waitForGatewayAgent(t *testing.T, hub *GatewayHub, cluster, instance string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if agent := hub.getAgent(cluster, instance); agent != nil {
			// Tests model Agents that completed enrollment; token-less sessions
			// are covered explicitly by the impersonation tests.
			hub.registry.mu.Lock()
			if agent.TokenID == "" {
				agent.TokenID = "test-enrollment"
			}
			hub.registry.mu.Unlock()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("gateway agent did not become online: %s/%s", cluster, instance)
}

func dialGatewayRPCClient(t *testing.T, serverURL, token, cluster, instance string) *websocket.Conn {
	t.Helper()
	clientWS := "ws" + strings.TrimPrefix(serverURL, "http") + "/v1/rpc?cluster=" + cluster + "&instance=" + instance
	conn, _, err := websocket.DefaultDialer.Dial(clientWS, http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatalf("dial client websocket: %v", err)
	}
	writeGatewayJSON(t, conn, map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	readUntilID(t, conn, 1)
	return conn
}

func serveFakeAgent(t *testing.T, conn *websocket.Conn) {
	serveFakeAgentWithRuntimeID(t, conn, "")
}

func gatewayTestInitializeResult(runtimeID string) map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"runtimeId":       runtimeID,
		"capabilities":    map[string]interface{}{"protocols": map[string]interface{}{upgradeAuditProtocol: upgradeAuditCapability()}},
	}
}

func serveFakeAgentObservingPings(t *testing.T, conn *websocket.Conn, pings chan<- string) {
	conn.SetPingHandler(func(payload string) error {
		select {
		case pings <- payload:
		default:
		}
		return conn.WriteControl(
			websocket.PongMessage,
			[]byte(payload),
			time.Now().Add(time.Second),
		)
	})
	serveFakeAgent(t, conn)
}

func serveFakeAgentWithRuntimeID(t *testing.T, conn *websocket.Conn, runtimeID string) {
	t.Helper()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			initialize := gatewayTestInitializeResult(runtimeID)
			initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[executionLifecycleProtocol] = executionLifecycleCapability()
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  initialize,
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]interface{})
			if params["name"] == "doops_agent_upgrade" {
				args, _ := params["arguments"].(map[string]interface{})
				outcome := "accepted"
				if args["dry_run"] == true {
					outcome = "dry_run"
				}
				result := upgradeAuditTestResult(upgradeAuditCall{args: args}, outcome, "self-upgrade-"+outcome)
				_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": result})
				continue
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "notifications/message",
				"params":  map[string]interface{}{"request_id": msg["id"], "data": "fake-agent-output\n"},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "ok " + params["name"].(string)}},
				},
			})
		}
	}
}

func serveConcurrentBlockingFakeAgent(t *testing.T, conn *websocket.Conn, started chan<- int, release <-chan struct{}) {
	t.Helper()
	var writeMu sync.Mutex
	var pendingMu sync.Mutex
	pending := make(map[int64]chan struct{})
	done := make(chan struct{})
	defer close(done)
	writeJSON := func(msg map[string]interface{}) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteJSON(msg)
	}
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			writeJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  gatewayTestInitializeResult(""),
			})
		case "tools/call":
			reqID := msg["id"]
			id, _ := numericID(reqID)
			canceled := make(chan struct{})
			pendingMu.Lock()
			pending[id] = canceled
			pendingMu.Unlock()
			if started != nil {
				started <- 1
			}
			go func() {
				response := map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      reqID,
					"result":  map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "ok"}}},
				}
				select {
				case <-release:
				case <-canceled:
					delete(response, "result")
					response["error"] = map[string]interface{}{"code": -32800, "message": "operation canceled"}
				case <-done:
					return
				}
				pendingMu.Lock()
				delete(pending, id)
				pendingMu.Unlock()
				writeJSON(response)
			}()
		case "tools/cancel":
			params, _ := msg["params"].(map[string]interface{})
			id, _ := numericID(params["id"])
			writeJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]interface{}{"canceled": true},
			})
			pendingMu.Lock()
			if canceled := pending[id]; canceled != nil {
				delete(pending, id)
				close(canceled)
			}
			pendingMu.Unlock()
		}
	}
}

func serveSessionAwareBlockingFakeAgent(t *testing.T, conn *websocket.Conn, started chan<- string, release <-chan struct{}) {
	t.Helper()
	var writeMu sync.Mutex
	writeJSON := func(msg map[string]interface{}) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteJSON(msg)
	}
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			writeJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  gatewayTestInitializeResult(""),
			})
		case "tools/call":
			reqID := msg["id"]
			params, _ := msg["params"].(map[string]interface{})
			args, _ := params["arguments"].(map[string]interface{})
			sessionID, _ := args["session_id"].(string)
			started <- sessionID
			go func() {
				<-release
				writeJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "notifications/message",
					"params":  map[string]interface{}{"request_id": reqID, "sessionID": sessionID, "data": "output " + sessionID + "\n"},
				})
				writeJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      reqID,
					"result":  map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "ok " + sessionID}}},
				})
			}()
		}
	}
}

func serveSessionOrphanNotificationFakeAgent(t *testing.T, conn *websocket.Conn, started chan<- string, release <-chan struct{}) {
	t.Helper()
	var writeMu sync.Mutex
	writeJSON := func(msg map[string]interface{}) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteJSON(msg)
	}
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			writeJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  gatewayTestInitializeResult(""),
			})
		case "tools/call":
			reqID := msg["id"]
			params, _ := msg["params"].(map[string]interface{})
			args, _ := params["arguments"].(map[string]interface{})
			sessionID, _ := args["session_id"].(string)
			started <- sessionID
			go func() {
				<-release
				writeJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "notifications/message",
					"params":  map[string]interface{}{"request_id": reqID.(float64) + 1000, "data": "orphan output " + sessionID + "\n"},
				})
				time.Sleep(50 * time.Millisecond)
				writeJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      reqID,
					"result":  map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "ok " + sessionID}}},
				})
			}()
		}
	}
}

func serveTimeoutFakeAgent(t *testing.T, conn *websocket.Conn, cancelSeen chan<- struct{}) {
	t.Helper()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  gatewayTestInitializeResult(""),
			})
		case "tools/cancel":
			params, _ := msg["params"].(map[string]interface{})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]interface{}{"canceled": true},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": params["id"],
				"error": map[string]interface{}{"code": -32800, "message": "operation canceled"},
			})
			select {
			case cancelSeen <- struct{}{}:
			default:
			}
		}
	}
}

func serveBlockingFakeAgent(t *testing.T, conn *websocket.Conn, release <-chan struct{}) {
	t.Helper()
	serveConcurrentBlockingFakeAgent(t, conn, nil, release)
}

func writeGatewayJSON(t *testing.T, conn *websocket.Conn, msg map[string]interface{}) {
	t.Helper()
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

func readUntilID(t *testing.T, conn *websocket.Conn, id float64) map[string]interface{} {
	t.Helper()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read json: %v", err)
		}
		if msgID, _ := msg["id"].(float64); msgID == id {
			return msg
		}
	}
}

func readGatewayNotification(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read notification: %v", err)
		}
		if method, _ := msg["method"].(string); method == "notifications/message" {
			return msg
		}
	}
	t.Fatal("timed out waiting for notification")
	return nil
}

func readGatewayMessage(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})
	var msg map[string]interface{}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read gateway message: %v", err)
	}
	return msg
}

func notificationData(msg map[string]interface{}) string {
	params, _ := msg["params"].(map[string]interface{})
	data, _ := params["data"].(string)
	return data
}
