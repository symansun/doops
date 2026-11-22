package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentMetadataModeReturnsModelStatusWithoutPrompt(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	previousSettingsFile := doagentSettingsFile
	doagentSettingsFile = settingsPath
	t.Cleanup(func() { doagentSettingsFile = previousSettingsFile })
	if err := os.WriteFile(settingsPath, []byte(`{
  "model": "minimax/MiniMax-M3",
  "provider": {
    "minimax": {
      "options": {"apiKey": "real-key", "baseURL": "https://api.minimaxi.com/anthropic", "kind": "anthropic"},
      "models": {"MiniMax-M3": {"name": "MiniMax-M3"}}
    }
  }
}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" {
			http.NotFound(w, r)
			return
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		if req["method"] != "initialize" {
			t.Fatalf("metadata mode should only initialize doagent, got method %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"_meta": map[string]interface{}{"model": "unconfigured"},
			},
		})
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	got := callTool(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id": "metadata-smoke",
		"mode":       "metadata",
	})
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(got), &meta); err != nil {
		t.Fatalf("metadata should be JSON, got %q: %v", got, err)
	}
	if meta["status"] != "connected" || meta["provider"] != "minimax" || meta["model"] != "minimax/MiniMax-M3" {
		t.Fatalf("metadata mismatch: %+v", meta)
	}
	if meta["metadata"].(map[string]interface{})["model"] != meta["model"] {
		t.Fatal("ACP startup placeholder overrode the delivered model")
	}
}

func TestAgentMetadataAwaitingModelStillAdvertisesACP(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	previousSettingsFile := doagentSettingsFile
	doagentSettingsFile = settingsPath
	t.Cleanup(func() { doagentSettingsFile = previousSettingsFile })
	if err := os.WriteFile(settingsPath, []byte(`{"mcp_servers":[],"permissions":{}}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")

	got, err := NewGateway().handleAgentMetadataWS(context.Background(), "awaiting-model")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(got), &meta); err != nil {
		t.Fatalf("metadata should be JSON, got %q: %v", got, err)
	}
	formats, _ := meta["event_formats"].([]interface{})
	if meta["status"] != "awaiting_model_provider" || meta["event_schema"] != "doops.sh/acp-events/v1" || len(formats) != 1 || formats[0] != "acp" {
		t.Fatalf("awaiting model must still advertise ACP: %+v", meta)
	}
	if meta["model"] != nil && meta["model"] != "" {
		t.Fatalf("awaiting model leaked a model: %+v", meta["model"])
	}
}

func TestAgentHistoryModeWithoutSessionReturnsEmptyHistory(t *testing.T) {
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("history mode without a bound session should not call doagent: %s", r.URL.Path)
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	got := callTool(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id": "empty-history",
		"mode":       "history",
	})
	var history map[string]interface{}
	if err := json.Unmarshal([]byte(got), &history); err != nil {
		t.Fatalf("history should be JSON, got %q: %v", got, err)
	}
	messages, _ := history["messages"].([]interface{})
	if history["source"] != "doagent" || history["sessionId"] != "empty-history" || len(messages) != 0 {
		t.Fatalf("history mismatch: %+v", history)
	}
}

func TestConfiguredDoagentURLUsesEntrypointPort(t *testing.T) {
	t.Setenv("DO_AGENT_URL", "")
	t.Setenv("DO_AGENT_PORT", "7749")
	if got := configuredDoagentURL(); got != "http://127.0.0.1:7749" {
		t.Fatalf("configured doagent URL = %q", got)
	}

	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:9010")
	if got := configuredDoagentURL(); got != "http://127.0.0.1:9010" {
		t.Fatalf("explicit doagent URL = %q", got)
	}
}

func TestMarshalDoagentHistorySanitizesNestedMessages(t *testing.T) {
	history, err := marshalDoagentHistory("history-session", []interface{}{
		map[string]interface{}{
			"role":  "assistant",
			"token": "opaque-history-canary",
			"content": []interface{}{
				map[string]interface{}{"text": "https://storage.example.test/object?signature=history-canary"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	if strings.Contains(history, "history-canary") ||
		strings.Contains(history, "opaque-history-canary") ||
		!strings.Contains(history, "REDACTED") {
		t.Fatalf("history was not sanitized: %s", history)
	}
}
