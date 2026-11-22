package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/user/doops/agent/internal/dispatcher"
)

// toolInvocation carries the per-request state shared by every tool handler.
type toolInvocation struct {
	ctx               context.Context
	reqID             interface{}
	gatewayTimeoutMS  int64
	args              json.RawMessage
	writeJSON         func(v interface{})
	sessionID         string
	pushProgress      func(evt notificationEvent)
	writePolicyDenial func(err error) bool
}

// agentToolHandlers maps every executable Agent tool, including private relays.
// Only the Gateway catalogue decides which names are reachable from client RPC.
var agentToolHandlers = map[string]func(*Gateway, toolInvocation){
	"doops_shell":                  (*Gateway).handleToolShell,
	"doops_ssh":                    (*Gateway).handleToolSSH,
	"doops_database_inventory":     (*Gateway).handleToolDatabaseInventory,
	"doops_node_info":              (*Gateway).handleToolNodeInfo,
	"doops_k8s":                    (*Gateway).handleToolK8s,
	"doops_web":                    (*Gateway).handleToolWeb,
	"doops_check_deployment":       (*Gateway).handleToolCheckDeployment,
	"doops_clean_workspace":        (*Gateway).handleToolCleanWorkspace,
	"doops_agent_upgrade":          (*Gateway).handleToolAgentUpgrade,
	"doops_bg":                     (*Gateway).handleToolBackground,
	"doops_task_status":            (*Gateway).handleToolTaskStatus,
	"doops_agent_prompt":           (*Gateway).handleToolAgentPrompt,
	"doops_credential_import":      (*Gateway).handleToolCredentialImport,
	"doops_credential_materialize": (*Gateway).handleToolCredentialMaterialize,
	"doops_credential_cleanup":     (*Gateway).handleToolCredentialCleanup,
	"doops_cos_sync":               (*Gateway).handleToolCosSync,
	"doops_file_write":             (*Gateway).handleToolFileWrite,
	"doops_kb":                     (*Gateway).handleToolKb,
	"doops_file_read":              (*Gateway).handleToolFileRead,
	"doops_workspace_read":         (*Gateway).handleToolWorkspaceRead,
	"doops_workspace_begin":        (*Gateway).handleToolWorkspaceBegin,
	"doops_workspace_chunk":        (*Gateway).handleToolWorkspaceChunk,
	"doops_workspace_commit":       (*Gateway).handleToolWorkspaceCommit,
	"doops_workspace_pull_begin":   (*Gateway).handleToolWorkspacePullBegin,
	"doops_workspace_pull_chunk":   (*Gateway).handleToolWorkspacePullChunk,
	diagnosticReadTool:             (*Gateway).handleDiagnosticReadTool,
	monitorInspectTool:             (*Gateway).handleToolMonitorInspect,
}

// handleToolCallOverWS 处理具体的 MCP tool 调用（复用原有的处理逻辑，但直接向 WS 写入结果）。
// 校验会话后按 agentToolHandlers 分发给各工具的处理函数（tool_handlers_*.go）。
func (gw *Gateway) handleToolCallOverWS(ctx context.Context, reqID interface{}, toolName string, argBytes json.RawMessage, gatewayTimeoutMS int64, writeJSON func(v interface{})) {
	var sessionID string
	var argsMap map[string]interface{}
	json.Unmarshal(argBytes, &argsMap)
	if sid, ok := argsMap["session_id"].(string); ok {
		sessionID = sid
	} else {
		sessionID = "default" // fallback
	}
	if err := validateSession(sessionID); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
		return
	}

	// 统一流式推送方法
	pushProgress := func(evt notificationEvent) {
		if evt.Kind == "" {
			evt.Kind = "raw"
		}
		params := map[string]interface{}{
			"request_id": reqID,
			"sessionID":  sessionID,
			"data":       evt.Data,
			"kind":       evt.Kind,
		}
		if evt.Tool != "" {
			params["tool"] = evt.Tool
		}
		if evt.Status != "" {
			params["status"] = evt.Status
		}
		if evt.Path != "" {
			params["path"] = evt.Path
		}
		writeJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/message",
			"params":  params,
		})
	}
	writePolicyDenial := func(err error) bool {
		var denied *dispatcher.BlockedError
		if !errors.As(err, &denied) {
			return false
		}
		log.Printf("command denied: tool=%s session=%s rule=%s", toolName, sessionID, denied.Rule)
		writeJSON(buildErrorResponse(reqID, -32602, denied.Error()))
		return true
	}

	// 工具分发
	if handler, ok := agentToolHandlers[toolName]; ok {
		handler(gw, toolInvocation{
			ctx:               ctx,
			reqID:             reqID,
			gatewayTimeoutMS:  gatewayTimeoutMS,
			args:              argBytes,
			writeJSON:         writeJSON,
			sessionID:         sessionID,
			pushProgress:      pushProgress,
			writePolicyDenial: writePolicyDenial,
		})
		return
	}
	writeJSON(buildErrorResponse(reqID, -32601, "Unknown tool over WS: "+toolName))
}
