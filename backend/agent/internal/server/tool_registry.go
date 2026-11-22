package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// gatewayToolSpec declares one DoOps tool on the Gateway side: the action the
// control plane authorises and the summary line it records in the audit log.
//
// The Agent-side handler lives in the Agent process (handleToolCallOverWS) and
// cannot be declared here, so the two halves are held together by a contract
// test (TestToolCatalogueMatchesAgentDispatch). Adding a tool means adding one
// entry here, one dispatch case in the Agent, and the CLI/Web callers; a tool
// registered on only one side fails the tests instead of becoming silently
// unreachable at runtime, which is what happened to the removed doops_docker.
//
// Resource keys are deliberately not part of this table: resourceKeyForTool
// encodes ordered, action-dependent compatibility rules for the concurrency
// locks and is not a per-tool constant.
type gatewayToolSpec struct {
	name    string
	action  func(json.RawMessage) GatewayAction
	summary func(m map[string]interface{}, raw json.RawMessage) string
}

func constantAction(action GatewayAction) func(json.RawMessage) GatewayAction {
	return func(json.RawMessage) GatewayAction { return action }
}

func constantSummary(summary string) func(map[string]interface{}, json.RawMessage) string {
	return func(map[string]interface{}, json.RawMessage) string { return summary }
}

// gatewayToolSpecs is the single declaration point for Gateway-side tool metadata.
var gatewayToolSpecs = []gatewayToolSpec{
	{name: "doops_shell", action: constantAction(ActionExec), summary: shellCommandSummary},
	{name: "doops_bg", action: constantAction(ActionExec), summary: shellCommandSummary},
	{name: "doops_ssh", action: constantAction(ActionExec), summary: sshCommandSummary},
	{name: "doops_task_status", action: constantAction(ActionExec)},
	{name: "doops_k8s", action: constantAction(ActionExec), summary: k8sCommandSummary},
	{name: "doops_web", action: webToolAction, summary: webToolSummary},
	{name: "doops_agent_prompt", action: agentPromptAction, summary: agentPromptSummary},
	{name: "doops_file_read", action: constantAction(ActionRead), summary: filePathSummary("doops_file_read")},
	{name: "doops_workspace_read", action: constantAction(ActionRead)},
	{name: "doops_file_write", action: constantAction(ActionWrite), summary: filePathSummary("doops_file_write")},
	{name: "doops_cos_sync", action: constantAction(ActionWrite)},
	{name: "doops_node_info", action: constantAction(ActionInfo)},
	{name: "doops_database_inventory", action: constantAction(ActionInfo), summary: constantSummary("doops_database_inventory")},
	{name: "doops_check_deployment", action: constantAction(ActionCheck)},
	{name: "doops_clean_workspace", action: constantAction(ActionClean)},
	{name: "doops_agent_upgrade", action: constantAction(ActionAgentUpgrade), summary: upgradeCommandSummary},
	{name: "doops_credential_materialize", action: constantAction(ActionCredentialUse)},
	{name: "doops_credential_import", action: constantAction(ActionCredentialRotate)},
	{name: "doops_credential_cleanup", action: constantAction(ActionCredentialRevoke)},
	{name: "doops_workspace_begin", action: constantAction(ActionPush)},
	{name: "doops_workspace_chunk", action: constantAction(ActionPush)},
	{name: "doops_workspace_commit", action: constantAction(ActionPush)},
	{name: "doops_workspace_pull_begin", action: constantAction(ActionPull), summary: pullBeginSummary},
	{name: "doops_workspace_pull_chunk", action: constantAction(ActionPull)},
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

// actionForTool maps a tool call to the action the Gateway authorises. An empty
// action means the Gateway does not know the tool and rejects the call.
func actionForTool(tool string, args json.RawMessage) GatewayAction {
	if spec, ok := gatewayToolIndex[tool]; ok {
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
	return ActionAsk
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
	if skill, _ := m["skill"].(string); skill == "doweb" || skill == "dosql" {
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
