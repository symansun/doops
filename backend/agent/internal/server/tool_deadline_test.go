package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func TestToolDeadlinePrefersTheShorterLimit(t *testing.T) {
	t.Setenv(ToolTimeoutEnv, "")
	_, cancel, limit := toolDeadline(context.Background(), 7000)
	cancel()
	if limit != 2*time.Second {
		t.Fatalf("gateway 7s limit = %s, want 2s", limit)
	}

	t.Setenv(ToolTimeoutEnv, "1h")
	_, cancel, limit = toolDeadline(context.Background(), 7000)
	cancel()
	if limit != 2*time.Second {
		t.Fatalf("explicit 1h with gateway 7s = %s, want 2s", limit)
	}

	t.Setenv(ToolTimeoutEnv, "1s")
	_, cancel, limit = toolDeadline(context.Background(), 0)
	cancel()
	if limit != time.Second {
		t.Fatalf("explicit 1s without gateway limit = %s, want 1s", limit)
	}

	t.Setenv(ToolTimeoutEnv, "")
	_, cancel, limit = toolDeadline(context.Background(), 0)
	cancel()
	if limit != 30*time.Minute {
		t.Fatalf("default limit = %s, want 30m", limit)
	}
}

func TestToolCallParamsCarryDeadlineOnlyForCapableAgents(t *testing.T) {
	params := api.ToolCallParams{Name: "doops_shell", Arguments: json.RawMessage(`{"command":"true"}`)}
	capable := &GatewayAgent{ToolDeadline: true}
	got := toolCallParamsFor(capable, params, 90*time.Second)
	if got["timeout_ms"] != int64(90000) {
		t.Fatalf("timeout_ms = %#v, want 90000", got["timeout_ms"])
	}
	if _, ok := toolCallParamsFor(&GatewayAgent{}, params, 90*time.Second)["timeout_ms"]; ok {
		t.Fatal("an Agent without toolDeadline must not receive timeout_ms")
	}
	if _, ok := toolCallParamsFor(capable, params, 0)["timeout_ms"]; ok {
		t.Fatal("a zero Gateway timeout must not be sent")
	}
}

func TestAgentToolTimeoutReplyIsClassifiedAsTimeout(t *testing.T) {
	response := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    float64(-32603),
			"message": AgentToolTimeoutCategory + ": command exceeded 2s and its process group was stopped",
			"data":    map[string]interface{}{"category": AgentToolTimeoutCategory},
		},
	}
	if !agentToolTimeoutResponse(response) {
		t.Fatal("agent_tool_timeout reply was not recognized")
	}
	if got := relayErrorAuditText("doops_shell", response["error"]); got == "" || got[:len(AgentToolTimeoutCategory)] != AgentToolTimeoutCategory {
		t.Fatalf("audit text = %q", got)
	}
	other := map[string]interface{}{"error": map[string]interface{}{"code": float64(-32007), "message": "operation canceled"}}
	if agentToolTimeoutResponse(other) {
		t.Fatal("a cancellation must not be classified as an Agent timeout")
	}
}

func TestAgentToolTimeoutEndsTheCommandWithStableCategory(t *testing.T) {
	t.Setenv(ToolTimeoutEnv, "1s")
	response, _ := runShellThroughWS(t, 0, "sleep 30")
	errObject, _ := response["error"].(map[string]interface{})
	if errObject == nil {
		t.Fatalf("timeout response = %#v, want an error", response)
	}
	if code, _ := errObject["code"].(float64); code != -32603 {
		t.Fatalf("code = %v, want -32603", errObject["code"])
	}
	message, _ := errObject["message"].(string)
	if len(message) < len(AgentToolTimeoutCategory) || message[:len(AgentToolTimeoutCategory)] != AgentToolTimeoutCategory {
		t.Fatalf("message = %q, want the %s prefix", message, AgentToolTimeoutCategory)
	}
	data, _ := errObject["data"].(map[string]interface{})
	if data["category"] != AgentToolTimeoutCategory {
		t.Fatalf("error.data = %#v", data)
	}
}
