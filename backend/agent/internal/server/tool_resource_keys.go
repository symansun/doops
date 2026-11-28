package server

import (
	"encoding/json"
	"github.com/user/doops/agent/api"
	"strings"
)

func resourceKeyForTool(action GatewayAction, tool string, args json.RawMessage, cluster, instance string) string {
	targetMutation := "target-mutation:" + cluster + "/" + instance
	if tool == "doops_credential_import" {
		return "query:credential:" + cluster + "/" + instance + "/" +
			extractStringArg(args, "namespace") + "/" + extractStringArg(args, "secret_name")
	}
	if tool == "doops_agent_prompt" {
		var prompt api.AgentPromptParams
		if json.Unmarshal(args, &prompt) == nil &&
			(len(prompt.CredentialRefs) != 0 || len(prompt.CredentialBindings) != 0 ||
				(prompt.Skill == "dosql" && prompt.CredentialMode == "auto")) {
			return targetMutation
		}
	}
	if tool == "doops_web" || (tool == "doops_agent_prompt" && extractStringArg(args, "skill") == "doweb") {
		if action == ActionExec {
			// Nginx activation and reload are shared across website sessions.
			return "nginx:" + cluster + "/" + instance
		}
		if tool == "doops_web" {
			return "query:web:" + cluster + "/" + instance
		}
	}
	switch action {
	case ActionExec:
		if tool == "doops_agent_prompt" && extractStringArg(args, "operation") == "apply" {
			return targetMutation
		}
		if tool == "doops_shell" || tool == "doops_bg" || tool == "doops_ssh" {
			// A session selects a workspace, not an OS or remote-resource sandbox.
			return targetMutation
		}
		if tool == "doops_task_status" {
			return "query:task:" + extractStringArg(args, "task_id")
		}
		if tool == "doops_k8s" {
			if !gatewayK8SReadOnly(extractStringArg(args, "operation")) {
				return targetMutation
			}
			if resource := extractStringArg(args, "resource"); resource != "" {
				return "query:k8s:" + extractStringArg(args, "namespace") + "/" + canonicalK8SResource(resource)
			}
			if name := extractStringArg(args, "name"); name != "" {
				return "query:k8s:" + extractStringArg(args, "namespace") + "/" + extractStringArg(args, "kind") + "/" + name
			}
			return "query:k8s:" + cluster + "/" + instance
		}
		return targetMutation
	case ActionAsk:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
	case ActionClean:
		workspace := strings.TrimSpace(extractStringArg(args, "workspace"))
		if validateSession(workspace) == nil {
			return "workspace:" + workspace
		}
		return targetMutation
	case ActionPush, ActionWrite:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
		return targetMutation
	case ActionPull, ActionRead:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
	case ActionCredentialUse, ActionCredentialRevoke:
		return targetMutation
	case ActionAgentUpgrade:
		return "target:" + cluster + "/" + instance
	case ActionCredentialMetadata:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
	}
	return ""
}

func canonicalK8SResource(resource string) string {
	parts := strings.SplitN(strings.TrimSpace(resource), "/", 2)
	if len(parts) != 2 {
		return strings.TrimSpace(resource)
	}
	aliases := map[string]string{
		"deploy": "deployment", "deployments": "deployment",
		"ds": "daemonset", "daemonsets": "daemonset",
		"sts": "statefulset", "statefulsets": "statefulset",
		"po": "pod", "pods": "pod",
	}
	kind := parts[0]
	if canonical := aliases[kind]; canonical != "" {
		kind = canonical
	}
	return kind + "/" + parts[1]
}
