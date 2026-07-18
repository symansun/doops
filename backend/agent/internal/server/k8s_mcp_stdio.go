package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/user/doops/agent/api"
)

const (
	k8sReadMCPToolName = "k8s_read"
	helmMCPToolName    = "helm"
)

var (
	k8sReadOperations = []string{
		"get", "describe", "logs", "top", "events", "rollout-status",
	}
	helmMutationOperations = []string{
		"upgrade-install", "rollback", "uninstall",
	}
)

type k8sMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type k8sMCPToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// RunK8SMCPStdio exposes the existing structured Kubernetes executor to
// doagent without exposing shell or the Agent's network control plane.
func RunK8SMCPStdio(input io.Reader, output io.Writer) error {
	return RunK8SMCPStdioContext(context.Background(), input, output)
}

func RunK8SMCPStdioContext(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	encoder := json.NewEncoder(output)

	for scanner.Scan() {
		var request k8sMCPRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if err := encoder.Encode(mcpErrorResponse(nil, -32700, "parse error")); err != nil {
				return err
			}
			continue
		}

		var response api.JSONRPCResponse
		switch request.Method {
		case "initialize":
			response = api.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result: map[string]interface{}{
					"protocolVersion": "2025-03-26",
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
					"serverInfo":      map[string]interface{}{"name": "doops-k8s", "version": "1"},
				},
			}
		case "notifications/initialized":
			continue
		case "tools/list":
			response = api.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result: map[string]interface{}{
					"tools": []interface{}{
						k8sMCPToolDefinition(k8sReadMCPToolName, k8sReadOperations, "Read Kubernetes state through the canonical doops_k8s contract."),
						helmMCPToolDefinition(),
					},
				},
			}
		case "tools/call":
			response = callK8SMCPTool(ctx, request)
		default:
			response = mcpErrorResponse(request.ID, -32601, "method not found")
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func callK8SMCPTool(ctx context.Context, request k8sMCPRequest) api.JSONRPCResponse {
	var call k8sMCPToolCall
	if err := json.Unmarshal(request.Params, &call); err != nil {
		return mcpErrorResponse(request.ID, -32602, "invalid tools/call params")
	}
	switch call.Name {
	case k8sReadMCPToolName:
		if !k8sMCPOperationAllowed(call.Arguments, k8sReadOperations) {
			return mcpErrorResponse(request.ID, -32602, "read tool requires a read-only operation")
		}
	case helmMCPToolName:
		if !k8sMCPOperationAllowed(call.Arguments, helmMutationOperations) {
			return mcpErrorResponse(request.ID, -32602, "helm tool requires a supported mutating operation")
		}
	default:
		return mcpErrorResponse(request.ID, -32601, "unknown tool")
	}

	var progress strings.Builder
	push := func(event notificationEvent) {
		if text := strings.TrimSpace(event.Data); text != "" {
			if progress.Len() != 0 {
				progress.WriteByte('\n')
			}
			progress.WriteString(text)
		}
	}
	var result string
	var err error
	if call.Name == helmMCPToolName {
		result, err = handleHelmTool(ctx, call.Arguments, push)
	} else {
		result, err = handleK8STool(ctx, call.Arguments, push)
	}
	if err != nil {
		return mcpErrorResponse(request.ID, -32000, err.Error())
	}
	if progress.Len() != 0 {
		progress.WriteByte('\n')
	}
	progress.WriteString(result)
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": progress.String()},
			},
			"isError": false,
		},
	}
}

func k8sMCPOperationAllowed(arguments json.RawMessage, allowed []string) bool {
	var input struct {
		Operation string `json:"operation"`
	}
	if json.Unmarshal(arguments, &input) != nil {
		return false
	}
	for _, operation := range allowed {
		if input.Operation == operation {
			return true
		}
	}
	return false
}

func k8sMCPToolDefinition(name string, operations []string, description string) map[string]interface{} {
	return map[string]interface{}{
		"name":        name,
		"description": description,
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"session_id", "operation"},
			"properties": map[string]interface{}{
				"session_id": map[string]interface{}{"type": "string"},
				"operation": map[string]interface{}{
					"type": "string",
					"enum": operations,
				},
				"kind":      map[string]interface{}{"type": "string"},
				"name":      map[string]interface{}{"type": "string"},
				"resource":  map[string]interface{}{"type": "string"},
				"namespace": map[string]interface{}{"type": "string"},
				"container": map[string]interface{}{"type": "string"},
				"output":    map[string]interface{}{"type": "string"},
				"timeout":   map[string]interface{}{"type": "string"},
				"tail":      map[string]interface{}{"type": "integer"},
			},
		},
	}
}

func helmMCPToolDefinition() map[string]interface{} {
	return map[string]interface{}{
		"name":        helmMCPToolName,
		"description": "Converge an explicitly named Helm release from deploy/helm assets in the bound workspace.",
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"session_id", "operation", "release", "namespace", "confirm"},
			"properties": map[string]interface{}{
				"session_id": map[string]interface{}{"type": "string"},
				"operation": map[string]interface{}{
					"type": "string",
					"enum": helmMutationOperations,
				},
				"release":   map[string]interface{}{"type": "string"},
				"chart":     map[string]interface{}{"type": "string"},
				"namespace": map[string]interface{}{"type": "string"},
				"values": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"type": "string"},
				},
				"set_strings": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "string"},
				},
				"revision": map[string]interface{}{"type": "integer", "minimum": 1},
				"timeout":  map[string]interface{}{"type": "string"},
				"confirm":  map[string]interface{}{"type": "boolean"},
			},
		},
	}
}

func mcpErrorResponse(id interface{}, code int, message string) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &api.RPCError{Code: code, Message: fmt.Sprintf("doops_k8s: %s", message)},
	}
}
