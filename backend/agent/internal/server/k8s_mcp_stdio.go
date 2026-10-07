package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/doops/agent/api"
)

const (
	k8sReadMCPToolName = "k8s_read"
	helmMCPToolName    = "helm"
	webReadMCPToolName = "web_read"
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
	return runK8SMCPStdioContext(ctx, input, output, boundSessionFromWorkingDirectory)
}

func runK8SMCPStdioContext(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	resolveSession func() (string, error),
) error {
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
						k8sMCPToolDefinition(k8sReadMCPToolName, k8sReadOperations, "Read Kubernetes state through the canonical doops_k8s contract. For operation=events, omit resource for namespace-wide events; an object filter must be kind/name. Never use Bash or kubectl as a fallback."),
						helmMCPToolDefinition(),
						webMCPToolDefinition(webReadMCPToolName, webReadOperations, "Read a target-local DoWeb site, Nginx or health state without mutation."),
						webMCPToolDefinition(webMCPToolName, webMutationOperations, "Mutate a target-local DoWeb site through SiteSpec, validation, health and rollback evidence. Requires confirm=true."),
					},
				},
			}
		case "tools/call":
			sessionID, err := resolveSession()
			if err != nil {
				response = mcpErrorResponse(request.ID, -32000, err.Error())
				break
			}
			response = callK8SMCPTool(ctx, request, sessionID)
		default:
			response = mcpErrorResponse(request.ID, -32601, "method not found")
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func callK8SMCPTool(
	ctx context.Context,
	request k8sMCPRequest,
	boundSessionID string,
) api.JSONRPCResponse {
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
	case webMCPToolName:
		if !k8sMCPOperationAllowed(call.Arguments, webMutationOperations) {
			return webMCPErrorResponse(request.ID, -32602, "web tool requires a supported mutation")
		}
	case webReadMCPToolName:
		if !k8sMCPOperationAllowed(call.Arguments, webReadOperations) {
			return webMCPErrorResponse(request.ID, -32602, "web_read tool requires a supported read operation")
		}
	default:
		return mcpErrorResponse(request.ID, -32601, "unknown tool")
	}
	boundArguments, err := bindK8SMCPSession(call.Arguments, boundSessionID)
	if err != nil {
		return mcpErrorResponse(request.ID, -32602, err.Error())
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
	if call.Name == helmMCPToolName {
		result, err = handleHelmTool(ctx, boundArguments, push)
	} else if call.Name == webMCPToolName || call.Name == webReadMCPToolName {
		result, err = handleWebTool(ctx, boundArguments, push)
	} else {
		result, err = handleK8STool(ctx, boundArguments, push)
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, errExecutionOutcomeUnknown) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return mcpErrorResponse(request.ID, -32008, errExecutionOutcomeUnknown.Error())
		}
		return buildToolErrorResponse(request.ID, sanitizeSecretText(err.Error()))
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

func webMCPToolDefinition(name string, operations []string, description string) map[string]interface{} {
	return map[string]interface{}{
		"name":        name,
		"description": description,
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"operation"},
			"properties": map[string]interface{}{
				"operation":    map[string]interface{}{"type": "string", "enum": operations},
				"confirm":      map[string]interface{}{"type": "boolean"},
				"runtime_mode": map[string]interface{}{"type": "string", "enum": []string{"host", "docker"}},
				"site_id":      map[string]interface{}{"type": "string"},
				"domain":       map[string]interface{}{"type": "string"},
				"domain_base":  map[string]interface{}{"type": "string"},
				"host_ip":      map[string]interface{}{"type": "string"},
				"provider":     map[string]interface{}{"type": "string"},
				"dist":         map[string]interface{}{"type": "string", "description": "Path relative to the bound DoOps session workspace."},
				"config_file":  map[string]interface{}{"type": "string", "description": "Candidate config path relative to the bound DoOps session workspace."},
				"config":       map[string]interface{}{"type": "string", "description": "Candidate Nginx config text for config-set; mutually exclusive with config_file."},
				"snippet_file": map[string]interface{}{"type": "string", "description": "Snippet path relative to the bound DoOps session workspace."},
				"snippet":      map[string]interface{}{"type": "string", "description": "Candidate snippet text for snippet-set; mutually exclusive with snippet_file."},
				"snippet_name": map[string]interface{}{"type": "string"},
				"tls_cert":     map[string]interface{}{"type": "string"},
				"tls_key":      map[string]interface{}{"type": "string"},
				"health_urls":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"proxies":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"upstreams":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"wildcard":     map[string]interface{}{"type": "boolean"},
			},
		},
	}
}

func boundSessionFromWorkingDirectory() (string, error) {
	root, err := filepath.EvalSymlinks(workspaceRoot())
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve MCP working directory: %w", err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve MCP working directory: %w", err)
	}
	relative, err := filepath.Rel(root, cwd)
	if err != nil || relative == "." || filepath.Dir(relative) != "." {
		return "", fmt.Errorf("MCP working directory is not a bound DoOps session workspace")
	}
	if err := validateSession(relative); err != nil {
		return "", fmt.Errorf("MCP working directory has invalid session binding: %w", err)
	}
	return relative, nil
}

func bindK8SMCPSession(arguments json.RawMessage, sessionID string) (json.RawMessage, error) {
	if err := validateSession(sessionID); err != nil {
		return nil, err
	}
	var input map[string]interface{}
	if err := json.Unmarshal(arguments, &input); err != nil || input == nil {
		return nil, fmt.Errorf("tool arguments must be an object")
	}
	if _, exists := input["session_id"]; exists {
		return nil, fmt.Errorf("session_id is runtime-bound and must not be supplied")
	}
	input["session_id"] = sessionID
	bound, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("bind runtime session: %w", err)
	}
	return bound, nil
}

func k8sMCPOperationAllowed(arguments json.RawMessage, allowed []string) bool {
	var input struct {
		Operation string `json:"operation"`
	}
	if json.Unmarshal(arguments, &input) != nil {
		return false
	}
	normalizedOperation := strings.ToLower(strings.TrimSpace(input.Operation))
	for _, operation := range allowed {
		if normalizedOperation == operation {
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
			"required":             []string{"operation"},
			"properties": map[string]interface{}{
				"operation": map[string]interface{}{
					"type":        "string",
					"enum":        operations,
					"description": "Use get, describe, logs, top, events, or rollout-status. For health checks use get nodes, get pods, then events.",
				},
				"kind": map[string]interface{}{"type": "string"},
				"name": map[string]interface{}{"type": "string"},
				"resource": map[string]interface{}{
					"type":        "string",
					"description": "Only for logs, rollout-status, or object-filtered events. Events must use kind/name; omit for namespace-wide events. Do not pass a namespace here.",
				},
				"namespace": map[string]interface{}{
					"type":        "string",
					"description": "Explicit Kubernetes namespace. Required for namespaced health reads; never place it in resource.",
				},
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
		"description": "Converge an explicitly named Helm release from remote assets or a runtime-bound session workspace. asset_source defaults to remote.",
		"inputSchema": map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"operation", "release", "namespace", "confirm"},
			"properties": map[string]interface{}{
				"operation": map[string]interface{}{
					"type": "string",
					"enum": helmMutationOperations,
				},
				"release": map[string]interface{}{"type": "string"},
				"asset_source": map[string]interface{}{
					"type":        "string",
					"enum":        []string{helmAssetSourceRemote, helmAssetSourceWorkspace},
					"default":     helmAssetSourceRemote,
					"description": "remote uses explicit https:// or oci:// assets; workspace uses paths relative to the runtime-bound session workspace.",
				},
				"chart": map[string]interface{}{
					"type":        "string",
					"description": "remote: explicit HTTPS chart download; workspace: relative Chart directory containing Chart.yaml. Workspace paths must not be absolute, contain .., or escape the session through symlinks.",
				},
				"namespace": map[string]interface{}{"type": "string"},
				"value_files": map[string]interface{}{
					"type":        "array",
					"description": "Optional ordered YAML values overlays. remote: HTTP(S) URLs; workspace: regular files relative to the session, with no absolute paths, .., or symlink escape.",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"set_strings": map[string]interface{}{
					"type":                 "object",
					"description":          "Optional non-sensitive chart-specific string overrides. No credential payloads. Chart schema determines valid business keys, including image and CORS configuration.",
					"additionalProperties": map[string]interface{}{"type": "string"},
				},
				"expected_image": map[string]interface{}{
					"type":        "string",
					"description": "Optional explicit complete repository:tag or repository@sha256:<64 hex digest>, checked against dry-run and final Deployment manifests. Image fields are not inferred from chart values.",
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

func webMCPErrorResponse(id interface{}, code int, message string) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &api.RPCError{Code: code, Message: fmt.Sprintf("doops_web: %s", message)},
	}
}
