package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// toolVisibility describes the RPC surface, not a user permission. Target grants
// are checked separately. The zero value never exposes a handler to clients.
type toolVisibility uint8

const (
	clientCallable toolVisibility = iota + 1
	gatewayInternal
)

// gatewayToolSpec describes a tool executed by the Agent. An action classifies
// calls for audit and admission; it is not evidence that a client may call it.
type gatewayToolSpec struct {
	name       string
	visibility toolVisibility
	// publicDenial preserves the wire error for internal tools that return
	// forbidden. An unadvertised internal tool otherwise looks unknown.
	publicDenial string
	action       func(json.RawMessage) GatewayAction
	summary      func(m map[string]interface{}, raw json.RawMessage) string
}

func constantAction(action GatewayAction) func(json.RawMessage) GatewayAction {
	return func(json.RawMessage) GatewayAction { return action }
}

func constantSummary(summary string) func(map[string]interface{}, json.RawMessage) string {
	return func(map[string]interface{}, json.RawMessage) string { return summary }
}

const credentialToolInternalMessage = "credential control-plane tools are internal only"

// gatewayToolSpecs is the single declaration point for Gateway-side tool metadata.
var gatewayToolSpecs = []gatewayToolSpec{
	{name: "doops_shell", visibility: clientCallable, action: constantAction(ActionExec), summary: shellCommandSummary},
	{name: "doops_bg", visibility: clientCallable, action: constantAction(ActionExec), summary: shellCommandSummary},
	{name: "doops_ssh", visibility: clientCallable, action: constantAction(ActionExec), summary: sshCommandSummary},
	{name: "doops_task_status", visibility: clientCallable, action: constantAction(ActionExec)},
	{name: "doops_k8s", visibility: clientCallable, action: constantAction(ActionExec), summary: k8sCommandSummary},
	{name: "doops_terminal", visibility: clientCallable, action: terminalToolAction, summary: terminalToolSummary},
	{name: "doops_web", visibility: clientCallable, action: webToolAction, summary: webToolSummary},
	{name: "doops_agent_prompt", visibility: clientCallable, action: agentPromptAction, summary: agentPromptSummary},
	{name: "doops_file_read", visibility: clientCallable, action: constantAction(ActionRead), summary: filePathSummary("doops_file_read")},
	{name: "doops_workspace_read", visibility: clientCallable, action: constantAction(ActionRead)},
	{name: "doops_file_write", visibility: clientCallable, action: constantAction(ActionWrite), summary: filePathSummary("doops_file_write")},
	{name: "doops_kb", visibility: clientCallable, action: knowledgeBaseToolAction, summary: knowledgeBaseToolSummary},
	{name: "doops_cos_sync", visibility: gatewayInternal, publicDenial: "cos_sync_internal", action: constantAction(ActionWrite)},
	{name: "doops_node_info", visibility: clientCallable, action: constantAction(ActionInfo)},
	{name: "doops_database_inventory", visibility: clientCallable, action: constantAction(ActionInfo), summary: constantSummary("doops_database_inventory")},
	{name: "doops_check_deployment", visibility: clientCallable, action: constantAction(ActionCheck)},
	{name: "doops_clean_workspace", visibility: clientCallable, action: constantAction(ActionClean)},
	{name: "doops_agent_upgrade", visibility: clientCallable, action: constantAction(ActionAgentUpgrade), summary: upgradeCommandSummary},
	{name: "doops_credential_materialize", visibility: gatewayInternal, publicDenial: credentialToolInternalMessage, action: constantAction(ActionCredentialUse)},
	{name: "doops_credential_import", visibility: gatewayInternal, publicDenial: credentialToolInternalMessage, action: constantAction(ActionCredentialRotate)},
	{name: "doops_credential_cleanup", visibility: gatewayInternal, publicDenial: credentialToolInternalMessage, action: constantAction(ActionCredentialRevoke)},
	{name: "doops_workspace_begin", visibility: clientCallable, action: constantAction(ActionPush)},
	{name: "doops_workspace_chunk", visibility: clientCallable, action: constantAction(ActionPush)},
	{name: "doops_workspace_commit", visibility: clientCallable, action: constantAction(ActionPush)},
	{name: "doops_workspace_pull_begin", visibility: clientCallable, action: constantAction(ActionPull), summary: pullBeginSummary},
	{name: "doops_workspace_pull_chunk", visibility: clientCallable, action: constantAction(ActionPull)},
	// Diagnostic collection uses a direct relay, not RunInternalToolCall.
	{name: diagnosticReadTool, visibility: gatewayInternal},
	{name: skillSyncTool, visibility: gatewayInternal, publicDenial: "skill_sync_internal_only", action: constantAction(ActionWrite)},
	{name: skillsInspectTool, visibility: gatewayInternal, publicDenial: "skills_inspect_internal_only", action: constantAction(ActionInfo)},
	{name: mcpSyncTool, visibility: gatewayInternal, publicDenial: "mcp_sync_internal_only", action: constantAction(ActionWrite)},
	{name: mcpInspectTool, visibility: gatewayInternal, publicDenial: "mcp_inspect_internal_only", action: constantAction(ActionInfo)},
}

var gatewayToolIndex = func() map[string]gatewayToolSpec {
	index := make(map[string]gatewayToolSpec, len(gatewayToolSpecs))
	for _, spec := range gatewayToolSpecs {
		index[spec.name] = spec
	}
	return index
}()

// gatewayToolNames lists every declared Gateway tool, sorted.
func gatewayToolNames() []string {
	names := make([]string, 0, len(gatewayToolSpecs))
	for _, spec := range gatewayToolSpecs {
		names = append(names, spec.name)
	}
	sort.Strings(names)
	return names
}

// actionForTool classifies an Agent tool for audit and admission on either path.
// Direct relays without an audited action return the empty action.
func actionForTool(tool string, args json.RawMessage) GatewayAction {
	if spec := gatewayToolIndex[tool]; spec.action != nil {
		return spec.action(args)
	}
	return ""
}

// summarizeToolCall builds the audit summary for one tool call. The tool name
// itself is the fallback when no per-tool summary applies.
func summarizeToolCall(tool string, args json.RawMessage) string {
	var m map[string]interface{}
	if json.Unmarshal(args, &m) != nil {
		return tool
	}
	if spec, ok := gatewayToolIndex[tool]; ok && spec.summary != nil {
		if summary := spec.summary(m, args); summary != "" {
			return summary
		}
	}
	return tool
}

func webToolAction(args json.RawMessage) GatewayAction {
	operation := strings.ToLower(strings.TrimSpace(extractStringArg(args, "operation")))
	if containsWebString(webReadOperations, operation) {
		return ActionAsk
	}
	if containsWebString(webMutationOperations, operation) {
		return ActionExec
	}
	return ""
}

func agentPromptAction(args json.RawMessage) GatewayAction {
	if extractStringArg(args, "operation") == "apply" {
		return ActionExec
	}
	switch strings.ToLower(strings.TrimSpace(extractStringArg(args, "mode"))) {
	case "history", "sessions":
		// Read-only Agent session history/catalog queries stay concurrent info
		// reads so they never block on active prompts or pollute ask audits.
		return ActionInfo
	}
	return ActionAsk
}

// terminalToolAction 把「读输出、改尺寸、关闭」当成只读类调用，只有 open/input
// 才算执行类：这样终端轮询不会被 Agent 的 exec 互斥队列拖住。
func terminalToolAction(args json.RawMessage) GatewayAction {
	switch strings.ToLower(strings.TrimSpace(extractStringArg(args, "op"))) {
	case "read", "resize", "close":
		return ActionInfo
	default:
		return ActionExec
	}
}

func terminalToolSummary(m map[string]interface{}, _ json.RawMessage) string {
	op, _ := m["op"].(string)
	term, _ := m["term"].(string)
	summary := "doops_terminal op=" + strings.ToLower(strings.TrimSpace(op))
	if term != "" {
		summary += " term=" + term
	}
	source, _ := m["source"].(string)
	if strings.ToLower(strings.TrimSpace(source)) == terminalInputSourceAISuggestion {
		summary += " source=" + terminalInputSourceAISuggestion
		if rawID, _ := m["suggestion_id"].(string); rawID != "" {
			if id := sanitizeTerminalSuggestionID(rawID); id != "" {
				summary += " suggestion=" + id
			}
		}
	}
	return summary
}

func shellCommandSummary(m map[string]interface{}, _ json.RawMessage) string {
	if cmd, _ := m["command"].(string); cmd != "" {
		return trimTail(cmd, 512)
	}
	return ""
}

func sshCommandSummary(m map[string]interface{}, _ json.RawMessage) string {
	host, _ := m["host"].(string)
	command, _ := m["command"].([]interface{})
	return fmt.Sprintf("doops_ssh host=%s remote_args=%d", strings.TrimSpace(host), len(command))
}

func agentPromptSummary(m map[string]interface{}, _ json.RawMessage) string {
	operation, _ := m["operation"].(string)
	summary := "doops_agent_prompt operation=" + strings.ToLower(strings.TrimSpace(operation))
	if mode, _ := m["mode"].(string); strings.TrimSpace(mode) != "" {
		summary += " mode=" + strings.ToLower(strings.TrimSpace(mode))
	}
	if skill, _ := m["skill"].(string); skill == "doweb" || skill == "dosql" || skill == "dodns" || skill == "doemail" || skill == "dosecurity" {
		summary += " skill=" + skill
	}
	return summary
}

func webToolSummary(_ map[string]interface{}, raw json.RawMessage) string {
	return trimTail("doops_web "+extractStringArg(raw, "operation")+" site="+extractStringArg(raw, "site_id"), 512)
}

func filePathSummary(tool string) func(map[string]interface{}, json.RawMessage) string {
	return func(m map[string]interface{}, _ json.RawMessage) string {
		if path, _ := m["path"].(string); path != "" {
			return tool + " " + path
		}
		return ""
	}
}

// knowledgeBaseToolAction keeps the knowledge base on the same boundary as the
// filesystem tools: reading is a read, writing or deleting is a write. An
// unknown operation has no action, so the Gateway rejects the call.
func knowledgeBaseToolAction(args json.RawMessage) GatewayAction {
	switch strings.ToLower(strings.TrimSpace(extractStringArg(args, "operation"))) {
	case "list", "read":
		return ActionRead
	case "write", "delete":
		return ActionWrite
	}
	return ""
}

// knowledgeBaseToolSummary records the document name only; the audit log must
// never contain the document body.
func knowledgeBaseToolSummary(m map[string]interface{}, _ json.RawMessage) string {
	operation, _ := m["operation"].(string)
	operation = strings.ToLower(strings.TrimSpace(operation))
	if name, _ := m["name"].(string); name != "" {
		return trimTail("doops_kb "+operation+" "+name, 512)
	}
	return trimTail("doops_kb "+operation, 512)
}

func pullBeginSummary(m map[string]interface{}, _ json.RawMessage) string {
	if sessionID, _ := m["session_id"].(string); sessionID != "" {
		return "doops_workspace_pull_begin " + sessionID
	}
	return ""
}

func upgradeCommandSummary(_ map[string]interface{}, raw json.RawMessage) string {
	return summarizeUpgradeAudit(raw)
}

func k8sCommandSummary(m map[string]interface{}, _ json.RawMessage) string {
	operation, _ := m["operation"].(string)
	resource, _ := m["resource"].(string)
	if resource == "" {
		kind, _ := m["kind"].(string)
		name, _ := m["name"].(string)
		resource = strings.Trim(strings.TrimSpace(kind)+"/"+strings.TrimSpace(name), "/")
	}
	namespace, _ := m["namespace"].(string)
	summary := "doops_k8s " + strings.TrimSpace(operation) + " namespace=" + strings.TrimSpace(namespace) + " resource=" + resource
	if replicas, ok := m["replicas"]; ok {
		summary += " replicas=" + fmt.Sprint(replicas)
	}
	if container, _ := m["container"].(string); container != "" {
		summary += " container=" + container
	}
	if image, _ := m["image"].(string); image != "" {
		summary += " image=" + image
	}
	return trimTail(summary, 512)
}
