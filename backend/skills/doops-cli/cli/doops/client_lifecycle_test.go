package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestAgentReleaseRequiresExactCommit(t *testing.T) {
	expected := strings.Repeat("a", 40)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var request map[string]interface{}
		if err := conn.ReadJSON(&request); err != nil {
			t.Error(err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]interface{}{
				"capabilities": map[string]interface{}{"releaseCommit": expected},
			},
		})
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	client := newGatewayTestClient(t, server.URL, "release-probe")
	defer client.Close()
	if err := client.RequireAgentRelease(expected); err != nil {
		t.Fatalf("exact release commit rejected: %v", err)
	}
	if err := client.RequireAgentRelease(strings.Repeat("b", 40)); err == nil ||
		!strings.Contains(err.Error(), "release commit mismatch") {
		t.Fatalf("mismatched release commit was accepted: %v", err)
	}
}

func TestLifecycleCapabilityHandshakeRejectsMalformedDeclarations(t *testing.T) {
	valid := map[string]interface{}{
		"schemaVersion": "doops.sh/execution-lifecycle/v1", "terminalResponse": true,
		"backgroundLifetime": true, "promptCancellation": true,
	}
	for _, test := range []struct {
		name       string
		capability interface{}
		accepted   bool
	}{
		{"valid", valid, true},
		{"missing", nil, false},
		{"boolean", false, false},
		{"wrong_schema", map[string]interface{}{"schemaVersion": "other", "terminalResponse": true, "backgroundLifetime": true, "promptCancellation": true}, false},
		{"no_terminal", map[string]interface{}{"schemaVersion": "doops.sh/execution-lifecycle/v1", "terminalResponse": false, "backgroundLifetime": true, "promptCancellation": true}, false},
		{"string_flag", map[string]interface{}{"schemaVersion": "doops.sh/execution-lifecycle/v1", "terminalResponse": "true", "backgroundLifetime": true, "promptCancellation": true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				var request map[string]interface{}
				if err := conn.ReadJSON(&request); err != nil {
					t.Error(err)
					return
				}
				if request["method"] != "initialize" {
					t.Error("capability probe executed a tool")
					return
				}
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "id": request["id"],
					"result": map[string]interface{}{"capabilities": map[string]interface{}{
						"protocols": map[string]interface{}{"executionLifecycle": test.capability},
					}},
				})
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			client := newGatewayTestClient(t, server.URL, "lifecycle-probe")
			defer client.Close()
			if err := client.RequireProtocol("executionLifecycle"); (err == nil) != test.accepted {
				t.Fatalf("capability accepted=%t, wanted=%t: %v", err == nil, test.accepted, err)
			}
		})
	}
}
