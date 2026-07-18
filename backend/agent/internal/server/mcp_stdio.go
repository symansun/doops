package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type mcpStdioRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func ServeMCPStdio(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	encoder := json.NewEncoder(output)

	for scanner.Scan() {
		var req mcpStdioRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if err := encoder.Encode(buildErrorResponse(nil, -32700, "invalid JSON-RPC request")); err != nil {
				return err
			}
			continue
		}

		response := handleMCPStdioRequest(ctx, req)
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handleMCPStdioRequest(ctx context.Context, req mcpStdioRequest) interface{} {
	switch req.Method {
	case "initialize":
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]string{
					"name":    "doops-local-tools",
					"version": "1.0",
				},
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
			},
		}
	case "tools/list":
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]interface{}{
				"tools": []interface{}{mcpK8SToolDefinition()},
			},
		}
	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return buildErrorResponse(req.ID, -32602, "invalid tools/call params")
		}
		if params.Name != "k8s" {
			return buildErrorResponse(req.ID, -32601, "unknown MCP tool: "+params.Name)
		}
		if params.Arguments == nil {
			params.Arguments = make(map[string]interface{})
		}
		if _, ok := params.Arguments["session_id"]; !ok {
			params.Arguments["session_id"] = "doagent-mcp"
		}
		raw, err := json.Marshal(params.Arguments)
		if err != nil {
			return buildErrorResponse(req.ID, -32602, "invalid k8s arguments")
		}
		result, err := handleK8STool(ctx, raw, func(notificationEvent) {})
		if err != nil {
			return buildErrorResponse(req.ID, -32000, err.Error())
		}
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]interface{}{
				"content": []map[string]string{{
					"type": "text",
					"text": result,
				}},
			},
		}
	default:
		return buildErrorResponse(req.ID, -32601, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

func mcpK8SToolDefinition() map[string]interface{} {
	return map[string]interface{}{
		"name":        "k8s",
		"description": "Execute constrained Kubernetes reads and confirmed mutations. Always provide namespace for namespaced resources.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"operation": map[string]interface{}{
					"type": "string",
					"enum": []string{
						"get", "describe", "logs", "top", "events", "rollout-status",
						"rollout-restart", "scale", "cordon", "uncordon", "delete-pod", "set-image",
					},
				},
				"kind":      map[string]string{"type": "string"},
				"name":      map[string]string{"type": "string"},
				"resource":  map[string]string{"type": "string"},
				"namespace": map[string]string{"type": "string"},
				"container": map[string]string{"type": "string"},
				"image":     map[string]string{"type": "string"},
				"revision":  map[string]string{"type": "string"},
				"output":    map[string]string{"type": "string"},
				"timeout":   map[string]string{"type": "string"},
				"tail":      map[string]string{"type": "integer"},
				"replicas":  map[string]string{"type": "integer"},
				"confirm":   map[string]string{"type": "boolean"},
			},
			"required":             []string{"operation"},
			"additionalProperties": false,
		},
	}
}
