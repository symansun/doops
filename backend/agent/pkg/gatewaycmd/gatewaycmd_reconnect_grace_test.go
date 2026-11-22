package gatewaycmd

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/internal/server"
)

// TestGatewayServeTargetReconnectGraceFlag drives a real serve process so the
// -target-reconnect-grace flag reaches the hub: an RPC issued for an offline
// Agent that registers after a fixed delay must succeed only when the
// configured grace covers that delay.
func TestGatewayServeTargetReconnectGraceFlag(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "gateway.db")
	store, err := server.OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("grace-flag")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GrantUser(user.ID, server.ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateToken(server.CreateTokenRequest{Kind: server.TokenKindUser, UserID: user.ID, Name: "grace-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		grace string
	}{
		{"grace covers reconnect", "5s"},
		{"zero disables waiting", "0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := freeGatewayLockTestPort(t)
			child := startGatewayLockHelper(t,
				append(gatewayServeTestArgs(dbPath, port), "-target-reconnect-grace", tc.grace, "-allow-open-agent-registration")...)
			waitGatewayLockTestHealth(t, child, port)

			conn, _, err := websocket.DefaultDialer.Dial(
				fmt.Sprintf("ws://127.0.0.1:%d/v1/rpc?cluster=dev&instance=local", port),
				http.Header{"Authorization": []string{"Bearer " + token.Plaintext}})
			if err != nil {
				t.Fatalf("dial client: %v", err)
			}
			defer conn.Close()
			_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
			var init map[string]interface{}
			if err := conn.ReadJSON(&init); err != nil {
				t.Fatalf("read initialize: %v", err)
			}

			started := time.Now()
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": 2, "method": "tools/call",
				"params": map[string]interface{}{"name": "doops_web", "arguments": map[string]interface{}{"session_id": "flag", "operation": "site-list"}},
			})
			// The Agent registers only after the call is already waiting.
			go func() {
				select {
				case <-time.After(2 * time.Second):
				case <-t.Context().Done():
					return
				}
				dialFlagTestAgent(t, fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=local", port))
			}()
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			var response map[string]interface{}
			if err := conn.ReadJSON(&response); err != nil {
				t.Fatalf("read response: %v", err)
			}
			elapsed := time.Since(started)

			if tc.grace != "0s" {
				if rpcErr, ok := response["error"].(map[string]interface{}); ok {
					t.Fatalf("call failed although the grace covers the reconnect: %v after %s", rpcErr, elapsed)
				}
				if elapsed < 2*time.Second {
					t.Fatalf("call returned in %s before the Agent registered at 2s", elapsed)
				}
				return
			}
			rpcErr, _ := response["error"].(map[string]interface{})
			if code, _ := rpcErr["code"].(float64); code != -32004 {
				t.Fatalf("expected -32004, got %#v", response)
			}
			if message, _ := rpcErr["message"].(string); message != "target offline: dev/local" {
				t.Fatalf("offline text = %q", message)
			}
			if elapsed > time.Second {
				t.Fatalf("zero grace took %s; it must not wait for the Agent", elapsed)
			}
		})
	}
}

// dialFlagTestAgent registers a minimal Agent that answers every tools/call.
func dialFlagTestAgent(t *testing.T, url string) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Errorf("dial agent: %v", err)
		return
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-t.Context().Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"runtimeId":       "grace-flag-agent",
					"capabilities":    map[string]interface{}{"protocols": map[string]interface{}{}},
				},
			})
		case "tools/call":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": "ok"}}, "isError": false},
			})
		}
	}
}
