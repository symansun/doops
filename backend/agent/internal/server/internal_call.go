package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

type internalCredentialLockKey struct{}

type internalCredentialLock struct {
	agent       *GatewayAgent
	resourceKey string
}

func withInternalCredentialLock(ctx context.Context, agent *GatewayAgent, resourceKey string) context.Context {
	return context.WithValue(ctx, internalCredentialLockKey{}, internalCredentialLock{agent: agent, resourceKey: resourceKey})
}

type internalAuditAuthKey struct{}

func contextWithInternalAuditAuth(ctx context.Context, auth TokenAuth) context.Context {
	if strings.TrimSpace(auth.UserID) == "" {
		return ctx
	}
	return context.WithValue(ctx, internalAuditAuthKey{}, auth)
}

func internalAuditAuthFromContext(ctx context.Context) TokenAuth {
	auth, _ := ctx.Value(internalAuditAuthKey{}).(TokenAuth)
	return auth
}

// RunInternalToolCall 让 gateway 内部组件（如调度器）在服务端直接对某个在线 agent
// 发起一次 tool call，并把流式输出 + 最终结果文本聚合返回。它复用了与客户端 RPC
// 相同的目标串行/资源锁语义（acquireForAction），因此不会和用户操作互相踩踏。
//
// 注意：这是受信任的内部路径，不做 user RBAC 校验；调用方需自行保证来源可信。
func (h *GatewayHub) RunInternalToolCall(ctx context.Context, cluster, instance, tool string, args map[string]interface{}) (string, error) {
	return h.client.RunInternalToolCall(ctx, cluster, instance, tool, args)
}

func (h *ClientService) RunInternalToolCall(ctx context.Context, cluster, instance, tool string, args map[string]interface{}) (output string, finalErr error) {
	agent := h.registry.Get(cluster, instance)
	if agent == nil {
		return "", fmt.Errorf("target offline: %s/%s", cluster, instance)
	}

	rawArgs, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("invalid tool args: %w", err)
	}
	action := actionForTool(tool, rawArgs)
	if action == "" {
		return "", fmt.Errorf("unknown doops action for tool: %s", tool)
	}
	if err := h.validateCommand(tool, rawArgs); err != nil {
		return "", err
	}
	if action == ActionAgentUpgrade {
		var upgrade agentUpgradeParams
		if err := json.Unmarshal(rawArgs, &upgrade); err != nil {
			return "", fmt.Errorf("invalid agent upgrade arguments: %w", err)
		}
		if err := validateAgentUpgradeDispatch(upgrade); err != nil {
			return "", err
		}
	}
	resourceKey := resourceKeyForTool(action, tool, rawArgs, cluster, instance)
	inherited := h.inheritsExecutionOwner(ctx, agent, resourceKey)
	held, ownsLock := ctx.Value(internalCredentialLockKey{}).(internalCredentialLock)
	auth := internalAuditAuthFromContext(ctx)
	operationUserID := strings.TrimSpace(auth.UserID)
	if operationUserID == "" {
		operationUserID = "internal"
	}
	auditID, err := h.store.StartAudit(AuditEvent{
		UserID: operationUserID, TokenID: auth.TokenID,
		Cluster: cluster, Instance: instance, Action: action, Session: extractSession(rawArgs),
		CommandSummary: "internal " + tool,
	})
	if err != nil {
		return "", fmt.Errorf("execution_audit_unavailable")
	}
	if err := agent.requireExecutionLifecycle(tool); err != nil {
		if auditErr := h.store.FinishAudit(auditID, AuditFinish{
			Status: "blocked", Error: err.Error(), EndedAt: time.Now().UTC(),
		}); auditErr != nil {
			return "", fmt.Errorf("execution_audit_completion_failed: %w", auditErr)
		}
		return "", err
	}
	opCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	opID := h.registerActiveOperation(GatewayActiveOperation{
		AuditID: auditID, UserID: operationUserID, TokenID: auth.TokenID,
		Cluster: cluster, Instance: instance, Action: action,
		Session: extractSession(rawArgs), Resource: resourceKey, Kind: "internal",
		CommandSummary: "internal " + tool,
	}, cancel)
	defer h.finishActiveOperation(opID)
	if ownsLock && internalCredentialTool(tool) {
		if held.agent != agent || held.resourceKey != resourceKey {
			return "", fmt.Errorf("credential_target_lock_changed")
		}
	} else if !inherited {
		lease, err := agent.acquireActionLease(opCtx, action, resourceKey, h.opts.MaxQueuedPerTarget, h.opts.TargetQueueTimeout)
		if err != nil {
			_ = h.store.FinishAudit(auditID, AuditFinish{Status: "blocked", Error: err.Error()})
			return "", err
		}
		h.attachOperationLease(opID, lease)
	}
	dispatched := false
	defer func() {
		status, errMsg := "success", ""
		if finalErr != nil {
			status, errMsg = "error", sanitizeSecretText(finalErr.Error())
			if errors.Is(finalErr, context.Canceled) {
				status = "canceled"
			}
			if errors.Is(finalErr, errExecutionOutcomeUnknown) {
				status = "blocked"
				if dispatched {
					status = "outcome_unknown"
				}
			}
		}
		if err := h.store.FinishAudit(auditID, AuditFinish{Status: status, Error: errMsg, EndedAt: time.Now().UTC()}); err != nil {
			finalErr = fmt.Errorf("execution_audit_completion_failed: %w", err)
			if dispatched && requiresExecutionOutcomeGuard(action, tool, rawArgs, resourceKey) {
				finalErr = fmt.Errorf("%w: %v", errExecutionOutcomeUnknown, finalErr)
			}
		}
	}()
	if err := opCtx.Err(); err != nil {
		return "", err
	}
	guarded := requiresExecutionOutcomeGuard(action, tool, rawArgs, resourceKey)
	if guarded {
		if err := h.checkExecutionGuard(cluster, instance); err != nil {
			return "", err
		}
	}
	if !inherited {
		releaseLimit, err := h.acquireOperationSlot(operationUserID)
		if err != nil {
			return "", err
		}
		defer releaseLimit()
	}
	h.markOperationRunning(opID)
	opCtx = h.withExecutionOwner(opCtx, agent, resourceKey, opID)
	if guarded {
		if err := h.beginExecution(agent, opID, auditID); err != nil {
			return "", err
		}
	}
	dispatched = true

	var out strings.Builder
	relayErr := agent.relayToolCall(opCtx, api.ToolCallParams{Name: tool, Arguments: rawArgs}, h.opts.OperationTimeout, func(msg gatewayWSMessage) error {
		if method, _ := msg.Parsed["method"].(string); method == "notifications/message" {
			if p, ok := msg.Parsed["params"].(map[string]interface{}); ok {
				if chunk, ok := p["data"].(string); ok {
					out.WriteString(chunk)
				}
			}
			return nil
		}
		if _, ok := msg.Parsed["id"]; ok {
			if result, ok := msg.Parsed["result"].(map[string]interface{}); ok {
				text := resultContentText(result)
				if text != "" {
					if out.Len() > 0 {
						out.WriteString("\n")
					}
					out.WriteString(text)
				}
				if isErr, ok := result["isError"]; ok && fmt.Sprintf("%v", isErr) == "true" {
					if internalCredentialTool(tool) {
						return fmt.Errorf("%s", credentialToolErrorCategory(text))
					}
					return fmt.Errorf("tool returned an error")
				}
			} else if rpcErr, ok := msg.Parsed["error"]; ok && rpcErr != nil {
				return fmt.Errorf("%v", rpcErr)
			}
		}
		return nil
	})
	return out.String(), relayErr
}

func credentialToolErrorCategory(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(normalized, "inline credential material is forbidden"):
		return "inline_credential_material_forbidden"
	case strings.Contains(normalized, "credential declaration is invalid"):
		return "credential_declaration_invalid"
	case strings.Contains(normalized, "workspace commit mismatch"):
		return "credential_workspace_commit_mismatch"
	case strings.Contains(normalized, "workspace is not ready"):
		return "credential_workspace_not_ready"
	case strings.Contains(normalized, "source revision") ||
		strings.Contains(normalized, "immutable source revision"):
		return "credential_source_revision_mismatch"
	}
	category := sanitizeCredentialCategory(strings.TrimSpace(text))
	if category == "credential_operation_failed" {
		return "credential_tool_failed"
	}
	return category
}

func resultContentText(result map[string]interface{}) string {
	content, _ := result["content"].([]interface{})
	var sb strings.Builder
	for _, item := range content {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "text" {
			if text, ok := m["text"].(string); ok && text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(text)
			}
		}
	}
	return sb.String()
}
