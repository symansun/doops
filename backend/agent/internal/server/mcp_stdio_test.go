package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServeMCPStdioListsStructuredK8STool(t *testing.T) {
	input := strings.NewReader(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n",
	)
	var output bytes.Buffer
	if err := ServeMCPStdio(context.Background(), input, &output); err != nil {
		t.Fatalf("serve MCP stdio: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("response lines = %d, want 2: %q", len(lines), output.String())
	}
	var listResponse map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &listResponse); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	result := listResponse["result"].(map[string]interface{})
	tools := result["tools"].([]interface{})
	tool := tools[0].(map[string]interface{})
	if tool["name"] != "k8s" {
		t.Fatalf("tool name = %v, want k8s", tool["name"])
	}
	schema := tool["inputSchema"].(map[string]interface{})
	if schema["additionalProperties"] != false {
		t.Fatalf("k8s schema must reject unknown fields: %#v", schema)
	}
}

func TestMCPStdioRejectsUnknownTool(t *testing.T) {
	response := handleMCPStdioRequest(context.Background(), mcpStdioRequest{
		ID:     1,
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"shell","arguments":{}}`),
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	if !strings.Contains(string(encoded), "unknown MCP tool") {
		t.Fatalf("unexpected response: %s", encoded)
	}
}
