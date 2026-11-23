package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/user/doops/agent/api"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// promptOutcomeUnknown keeps the execution_outcome_unknown contract and names the
// check that could not be proven, so callers know what to verify on the target.
func promptOutcomeUnknown(reason string) string {
	return errExecutionOutcomeUnknown.Error() + ": " + reason
}

func credentialBindingContext(bindings []api.AgentCredentialBinding) (string, error) {
	for _, binding := range bindings {
		if !validCredentialMetadataToken(binding.Name, 128) ||
			!validCredentialUse(CredentialUse(binding.Use)) ||
			!validKubernetesCredentialName(binding.Namespace) ||
			!validKubernetesCredentialName(binding.ResourceName) ||
			!validCredentialMetadataToken(binding.ResourceVersion, 256) ||
			credentialScalarContainsInlineMaterial(binding.ResourceVersion) {
			return "", errors.New("credential_binding_metadata_invalid")
		}
		switch binding.SecretType {
		case "", "Opaque", "kubernetes.io/dockerconfigjson", "kubernetes.io/tls":
		default:
			return "", errors.New("credential_binding_metadata_invalid")
		}
		if binding.Digest != "" && !validCredentialSHA256Digest(binding.Digest) {
			return "", errors.New("credential_binding_metadata_invalid")
		}
		if binding.Use == string(CredentialUseDatabase) {
			if len(binding.Keys) == 0 {
				return "", errors.New("credential_binding_metadata_invalid")
			}
		}
		if binding.Use == string(CredentialUseHelmPull) {
			if binding.ContextKind != "helm-native" || !validNativeHelmSocketPath(binding.ContextSocket) || binding.SecretType != "" {
				return "", errors.New("credential_binding_metadata_invalid")
			}
		}
		for _, key := range binding.Keys {
			if !validCredentialMetadataToken(key, 253) || credentialScalarContainsInlineMaterial(key) {
				return "", errors.New("credential_binding_metadata_invalid")
			}
		}
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return "", errors.New("credential_binding_metadata_invalid")
	}
	return string(encoded), nil
}

func hasHelmCredentialBinding(bindings []api.AgentCredentialBinding) bool {
	for _, binding := range bindings {
		if binding.Use == string(CredentialUseHelmPull) || binding.ContextKind == "helm-native" {
			return true
		}
	}
	return false
}

func hasDatabaseCredentialBinding(bindings []api.AgentCredentialBinding) bool {
	for _, binding := range bindings {
		if binding.Use == string(CredentialUseDatabase) {
			return true
		}
	}
	return false
}

func doagentModeForPrompt(operation, executionMode string) (string, error) {
	operation = strings.ToLower(strings.TrimSpace(operation))
	executionMode = strings.ToLower(strings.TrimSpace(executionMode))
	if executionMode != "" {
		return "", fmt.Errorf("execution_mode is not supported for ordinary agent prompts")
	}
	switch operation {
	case "", "ask":
		return "auto", nil
	case "apply":
		return "build", nil
	default:
		return "", fmt.Errorf("unsupported agent prompt operation: %s", operation)
	}
}

type agentPromptExecutionContext struct {
	NativeMode          string
	RequireToolEvidence bool
	StructuredEvents    bool
	Skill               string
}

// handleAgentPromptWS 封装 doops_agent_prompt 处理逻辑，通过 ACP HTTP API 调用本地 doagent 服务。
// doagentPromptSessionSequence makes every machine-result doagent session id
// unique to one prompt so machine-result tool traces cannot include an old turn.
var doagentPromptSessionSequence atomic.Uint64

const doagentPromptExecutionTimeout = 60 * time.Minute

func (gw *Gateway) handleAgentPromptWS(ctx context.Context, reqID interface{}, doopsSessionID string, instr string, attachments []api.AgentPromptAttachment, model string, responseFormat string, execution agentPromptExecutionContext, pushProgress notificationSender, writeJSON func(v interface{})) {
	ctx, cancelExecution := context.WithTimeout(ctx, doagentPromptExecutionTimeout)
	defer cancelExecution()
	log.Printf("🤖 WS Running doagent via ACP HTTP: session=%s structured=%t model=%s",
		doopsSessionID, responseFormat == "json", model)

	if strings.TrimSpace(instr) == "" && len(attachments) == 0 {
		// doagent decides an empty prompt before its admission path and creates
		// no turn; reject it here so the Gateway never opens a session or sends
		// a cancel for input it can already classify.
		writeJSON(buildErrorResponse(reqID, -32602, doagentPromptNoPrompt))
		return
	}

	var mutationContext string
	if execution.RequireToolEvidence {
		var err error
		mutationContext, err = mutationContextDigest(doopsSessionID, instr)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, "doagent_apply_mutation_context_invalid"))
			return
		}
	}

	doagentURL := configuredDoagentURL()
	if err := ensureDoagentAvailable(doagentURL); err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, "doagent_unavailable"))
		return
	}
	if err := requireDoagentPromptAdmission(ctx, doagentURL); err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
		return
	}
	// Machine-result prompts use a fresh doagent session so their tool trace
	// catalog and turn_finished boundary cannot include a previous turn.
	var entry *sessionEntry
	if responseFormat != "json" {
		gw.sessionMapMu.RLock()
		entry = gw.sessionMap[doopsSessionID]
		gw.sessionMapMu.RUnlock()
	}

	var targetSessionID string
	if entry != nil {
		targetSessionID = entry.doagentSessionID
		gw.sessionMapMu.Lock()
		entry.lastUsed = time.Now()
		gw.sessionMapMu.Unlock()
	}
	var systemPrompt string
	sysPromptPaths := []string{"/app/skills/system_prompt.md", "/app/self-docs/agent/skills/system_prompt.md"}
	for _, sp := range sysPromptPaths {
		if data, err := os.ReadFile(sp); err == nil {
			systemPrompt = string(data)
			log.Printf("📋 首次会话，已注入系统提示词: %s (%d bytes)", sp, len(data))
			break
		}
	}
	var kubernetes bool
	if manifestExecution, ok := ctx.Value(executionTargetManifestKey{}).(executionTargetManifest); ok {
		systemPrompt += manifestExecution.promptFacts(time.Now())
		kubernetes = manifestExecution.Manifest.Target.KubernetesAvailable != nil && *manifestExecution.Manifest.Target.KubernetesAvailable
	} else {
		runtime := currentTargetRuntime(ctx)
		systemPrompt += runtime.promptFacts()
		kubernetes = runtime.Kubernetes
	}

	// 首次会话：创建 doagent session 并注入系统提示词
	if targetSessionID == "" {
		requestedSessionID := doopsSessionID
		if responseFormat == "json" {
			requestedSessionID = fmt.Sprintf("%s-json-%d-%d", doopsSessionID, time.Now().UnixNano(), doagentPromptSessionSequence.Add(1))
		}
		createResp, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "create-" + doopsSessionID,
			"method":  "session/new",
			"params": map[string]interface{}{
				"sessionId":      requestedSessionID,
				"systemPrompt":   systemPrompt,
				"cwd":            "/root/ws/" + doopsSessionID,
				"permissionMode": "auto",
			},
		})
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_session_create_failed"))
			return
		}

		if result, ok := createResp["result"].(map[string]interface{}); ok {
			if sid, ok := result["sessionId"].(string); ok {
				targetSessionID = sid
			}
		}
		if targetSessionID == "" {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_session_create_invalid"))
			return
		}

		if responseFormat != "json" {
			gw.sessionMapMu.Lock()
			gw.sessionMap[doopsSessionID] = &sessionEntry{
				doagentSessionID: targetSessionID,
				lastUsed:         time.Now(),
			}
			gw.sessionMapMu.Unlock()
		}
		log.Printf("🔗 Bound Doops Session %s -> doagent Session %s", doopsSessionID, targetSessionID)
	} else {
		// DoOps owns the per-prompt allowlist and never exposes an interactive
		// permission UI. Refresh reused sessions so older sessions created with
		// restricted mode cannot emit permission.updated for an allowed tool.
		if _, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "refresh-permission-" + doopsSessionID,
			"method":  "session/new",
			"params": map[string]interface{}{
				"sessionId":      targetSessionID,
				"systemPrompt":   systemPrompt,
				"permissionMode": "auto",
			},
		}); err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_permission_mode_configuration_failed"))
			return
		}
	}

	// 仅当调用方显式指定时覆盖模型，并在复用会话时同样生效。
	if model != "" {
		targetModel := model
		if !strings.Contains(targetModel, "/") {
			targetModel = "openai/" + targetModel
		}
		if _, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "setmodel-" + doopsSessionID,
			"method":  "session/setModel",
			"params": map[string]interface{}{
				"sessionId": targetSessionID,
				"model":     targetModel,
			},
		}); err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_model_configuration_failed"))
			return
		}
	}

	if _, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "setmode-" + doopsSessionID,
		"method":  "session/setMode",
		"params": map[string]interface{}{
			"sessionId": targetSessionID,
			"modeId":    execution.NativeMode,
		},
	}); err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, "doagent_mode_configuration_failed"))
		return
	}
	log.Printf("🧭 Session %s using native doagent mode %s", targetSessionID, execution.NativeMode)

	var promptAttachments []map[string]interface{}
	if len(attachments) > 0 {
		var uploadErr error
		promptAttachments, uploadErr = gw.uploadAgentPromptAttachments(ctx, doagentURL, doopsSessionID, attachments)
		if uploadErr != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_attachment_upload_failed"))
			return
		}
	}

	// 启动 SSE 事件订阅（在 prompt 之前连接，防止丢失事件）
	// Caller cancellation must not close the terminal-evidence subscription.
	sseCtx, sseCancel := context.WithCancel(context.WithoutCancel(ctx))

	sseDone := make(chan struct{})
	var sseErr error
	var terminalSeen atomic.Bool
	sseReady := make(chan error, 1)
	turnIDReady := make(chan string, 1)
	toolTraces := newDoagentToolTraceCollector(false)
	toolTraces.ordinaryAsk = !execution.RequireToolEvidence
	toolTraces.bindSession(doopsSessionID)
	effectiveModel := strings.TrimSpace(model)
	if effectiveModel != "" && !strings.Contains(effectiveModel, "/") {
		effectiveModel = "openai/" + effectiveModel
	}
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(inspectDoagentModelChannels().model)
	}
	if validCredentialMetadataToken(effectiveModel, 128) {
		toolTraces.setDefaultModel(effectiveModel)
	}
	origWriteJSON := writeJSON
	writeJSON = func(v interface{}) {
		origWriteJSON(toolTraces.attachUsageToResponse(v))
	}
	go func() {
		defer close(sseDone)
		sseErr = subscribeDoagentSSEWithCollectorReady(
			sseCtx,
			doagentURL,
			targetSessionID,
			pushProgress,
			func(update map[string]interface{}) error {
				if err := toolTraces.collect(update); err != nil {
					return err
				}
				if update["sessionUpdate"] == "turn_finished" {
					switch update["status"] {
					case "completed", "cancelled", "failed", "interrupted", "needs_input", "stale_prompt_recovered":
						terminalSeen.Store(true)
					}
				}
				return nil
			},
			turnIDReady,
			true,
			responseFormat != "json" && !execution.RequireToolEvidence,
			sseReady,
			execution.StructuredEvents,
		)
	}()
	defer func() {
		sseCancel()
		<-sseDone
	}()
	select {
	case err := <-sseReady:
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_sse_unavailable"))
			return
		}
	case <-ctx.Done():
		writeJSON(promptContextResponse(reqID, ctx.Err(), true))
		return
	}

	// session/prompt 只返回本轮 turnId；终态仍以同 turn 的 SSE turn_finished 为准。
	promptValue := interface{}(instr)
	if len(promptAttachments) > 0 {
		content := make([]interface{}, 0, len(promptAttachments)+1)
		if instr != "" {
			content = append(content, map[string]interface{}{"type": "text", "text": instr})
		}
		for _, attachment := range promptAttachments {
			content = append(content, attachment)
		}
		promptValue = content
	}
	promptParams := map[string]interface{}{"sessionId": targetSessionID, "prompt": promptValue}
	promptParams["mode"] = "reject"
	if deadline, ok := ctx.Deadline(); ok {
		promptParams["wallClockTimeoutSecs"] = max(1, int64(time.Until(deadline)/time.Second))
	}
	allowedTools := doagentAllowedTools(execution.RequireToolEvidence, execution.Skill, kubernetes)
	promptParams["allowedTools"] = allowedTools
	promptParams["mutationAuthorized"] = execution.RequireToolEvidence
	if execution.RequireToolEvidence {
		// doagent owns bounded continuation within the same authorized turn.
		promptParams["completionReview"] = true
		authorityID, issuedAt, authorityToken, authorityDigest, authorityErr :=
			newMutationAuthorization(targetSessionID, allowedTools, mutationContext, time.Now())
		if authorityErr != nil {
			sseCancel()
			<-sseDone
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_mutation_authority_unavailable"))
			return
		}
		promptParams["mutationAuthorizationId"] = authorityID
		promptParams["mutationAuthorizationIssuedAt"] = issuedAt
		promptParams["mutationAuthorizationToken"] = authorityToken
		promptParams["mutationContextDigest"] = mutationContext
		toolTraces.bindMutationAuthority(authorityDigest)
	}
	if responseFormat == "json" {
		promptParams["response_format"] = "json"
	}
	promptParams["responseMode"] = "stream"
	if ctx.Err() != nil {
		writeJSON(promptContextResponse(reqID, ctx.Err(), true))
		return
	}
	promptResponse, promptErr := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "prompt-" + doopsSessionID,
		"method":  "session/prompt",
		"params":  promptParams,
	})
	admission := classifyDoagentPromptAdmission(promptResponse)
	if promptErr != nil && admission.kind == promptAdmissionMalformed {
		// A transport failure can hide an accepted prompt; only the wire shape
		// without a result can be promoted to the transport category here.
		admission = promptAdmission{kind: promptAdmissionTransportFailed, detail: "rpc transport error"}
	}
	logDoagentPromptAdmission(doopsSessionID, targetSessionID, admission)

	switch admission.kind {
	case promptAdmissionAccepted:
		turnIDReady <- admission.turnID
		close(turnIDReady)
	case promptAdmissionNoPrompt:
		// No turn exists and doagent never touched the queue; a cancel would
		// disturb the session's real work.
		writeJSON(buildErrorResponse(reqID, -32602, doagentPromptNoPrompt))
		return
	case promptAdmissionQueuedNoTurn:
		settleQueuedPromptWS(ctx, reqID, doagentURL, targetSessionID, admission, writeJSON)
		return
	case promptAdmissionBusy:
		writeJSON(buildErrorResponse(reqID, -32603, doagentPromptBusy))
		return
	case promptAdmissionTimedOut:
		writeJSON(buildErrorResponse(reqID, -32603, doagentPromptAdmissionTimeout))
		return
	case promptAdmissionRejected:
		writeJSON(buildErrorResponse(reqID, -32003, "doagent_prompt_rejected"))
		return
	default:
		// A malformed or transport-failed answer can hide an accepted prompt.
		// Without its turn ID even a cancel ACK cannot prove which execution has
		// stopped, so keep the unknown-outcome protection.
		if responseFormat == "json" {
			// This session belongs exclusively to this prompt. Shared streaming
			// sessions may contain another turn whose cancellation we do not own.
			_ = cancelDoagentPrompt(ctx, doagentURL, targetSessionID, nil, &terminalSeen, doagentCancellationTimeout)
		}
		reason := "prompt_turn_id_missing"
		if admission.kind == promptAdmissionTransportFailed {
			reason = "prompt_transport_failed"
		}
		writeJSON(buildErrorResponse(reqID, -32603, promptOutcomeUnknown(reason)))
		return
	}

	// Wait for the turn's terminal state. Verified tool success remains a fact,
	// but a partially completed or interrupted request must not become success.
	select {
	case <-ctx.Done():
		if err := cancelDoagentPrompt(ctx, doagentURL, targetSessionID, sseDone, &terminalSeen, doagentCancellationTimeout); err != nil {
			message := promptOutcomeUnknown("cancel_unconfirmed")
			if execution.RequireToolEvidence {
				writeJSON(buildDoagentApplyResponse(reqID, toolTraces, -32603, message))
			} else {
				writeJSON(buildErrorResponse(reqID, -32603, message))
			}
			return
		}
		if !execution.RequireToolEvidence {
			writeJSON(promptContextResponse(reqID, ctx.Err(), false))
			return
		}
	case <-sseDone:
	}
	if !terminalSeen.Load() {
		_ = cancelDoagentPrompt(ctx, doagentURL, targetSessionID, sseDone, &terminalSeen, doagentCancellationTimeout)
		reason := "turn_finished_missing"
		if sseErr != nil {
			reason += " (" + sanitizeSecretText(sseErr.Error()) + ")"
		}
		if execution.RequireToolEvidence {
			writeJSON(buildDoagentApplyResponse(reqID, toolTraces, -32603, promptOutcomeUnknown(reason)))
		} else {
			writeJSON(buildErrorResponse(reqID, -32603, promptOutcomeUnknown(reason)))
		}
		return
	}
	if ctx.Err() != nil && !execution.RequireToolEvidence {
		_ = cancelDoagentPrompt(ctx, doagentURL, targetSessionID, sseDone, &terminalSeen, doagentCancellationTimeout)
		writeJSON(promptContextResponse(reqID, ctx.Err(), false))
		return
	}
	if execution.RequireToolEvidence {
		if unresolved := toolTraces.unresolvedMutationSummary(); unresolved != "" {
			writeJSON(buildDoagentApplyResponse(reqID, toolTraces, -32603, promptOutcomeUnknown("unverified_mutation "+unresolved)))
			return
		}
		if err := toolTraces.requireCompletedToolEvidence(); err != nil {
			if ctx.Err() != nil && strings.HasPrefix(err.Error(), "doagent_apply_tool_evidence_missing") {
				writeJSON(buildDoagentApplyResponse(reqID, toolTraces, -32007, "operation canceled"))
			} else {
				writeJSON(buildDoagentApplyResponse(reqID, toolTraces, 0, err.Error()))
			}
			return
		}
		if sseErr != nil {
			writeJSON(buildDoagentApplyResponse(reqID, toolTraces, 0,
				"doagent_apply_incomplete: "+sanitizeSecretText(sseErr.Error())))
			return
		}
		writeJSON(buildDoagentApplyResponse(reqID, toolTraces, 0, ""))
		return
	}
	if failure := toolTraces.askFailureResponse(reqID); failure != nil {
		writeJSON(*failure)
		return
	}
	if sseErr != nil && !errors.Is(sseErr, context.Canceled) {
		log.Printf("doagent execution failed: session=%s category=doagent_execution_failed detail=%s",
			targetSessionID, sanitizeSecretText(sseErr.Error()))
		pushProgress(errorNotification("[error] " + sanitizeSecretText(sseErr.Error())))
		writeJSON(buildToolErrorResponse(reqID, "doagent_execution_failed"))
		return
	}
	if responseFormat == "json" {
		resultText, structuredContent, err := toolTraces.structuredResult(false)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, fmt.Sprintf("read final machine result: %v", err)))
			return
		}
		writeJSON(buildStructuredSuccessResponse(reqID, resultText, structuredContent))
		return
	}

	answer, err := toolTraces.finalAnswer()
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
		return
	}
	writeJSON(buildSuccessResponse(reqID, answer))
}

// Apply facts come only from verified runtime receipts. Keep unknown outcomes
// as RPC errors for the Gateway's quarantine contract, with the history in data.
func buildDoagentApplyResponse(reqID interface{}, traces *doagentToolTraceCollector, code int, message string) api.JSONRPCResponse {
	text, report, err := traces.verifiedMutationResult()
	if err != nil {
		return buildErrorResponse(reqID, -32603, promptOutcomeUnknown("mutation_report_invalid"))
	}
	if code != 0 {
		report["outcome"] = "unknown"
		if code == -32007 {
			report["outcome"] = "cancelled"
		}
		response := buildErrorResponse(reqID, code, message)
		response.Error.Data = map[string]interface{}{"structuredContent": report}
		return response
	}
	if message != "" {
		return buildStructuredToolErrorResponse(reqID, message+"\n"+text, map[string]interface{}{
			"structuredContent": report,
		})
	}
	response := buildStructuredSuccessResponse(reqID, text, report)
	response.Result.(map[string]interface{})["isError"] = false
	return response
}
