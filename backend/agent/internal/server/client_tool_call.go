package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/user/doops/agent/api"
	"log"
	"strings"
	"time"
)

func (h *ClientService) handleGatewayToolCall(ctx context.Context, auth TokenAuth, cluster, instance string, req api.JSONRPCRequest, writeRaw func([]byte) error, writeJSON func(interface{}) error) error {
	if h.isDraining() {
		return writeJSON(buildErrorResponse(req.ID, -32010, "gateway_draining: the Gateway is restarting; retry in a moment"))
	}
	if err := h.store.validateUserTokenAuth(auth); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return writeJSON(buildErrorResponse(req.ID, -32001, authFailureCode(err)))
		}
		return writeJSON(buildErrorResponse(req.ID, -32603, "gateway_auth_unavailable"))
	}
	ctx = contextWithInternalAuditAuth(ctx, auth)
	var params api.ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(buildErrorResponse(req.ID, -32602, "invalid tools/call params"))
		return nil
	}
	spec := gatewayToolIndex[params.Name]
	if spec.visibility == gatewayInternal && spec.publicDenial != "" {
		writeJSON(buildErrorResponse(req.ID, -32003, spec.publicDenial))
		return nil
	}
	if cosToolName(params.Name) {
		return h.handleCOSToolCall(ctx, auth, cluster, instance, req, params, writeJSON)
	}
	if params.Name == "doops_node_info" && extractStringArg(params.Arguments, "section") == "app_metrics" {
		writeJSON(buildErrorResponse(req.ID, -32003, "app_metrics_internal_only"))
		return nil
	}
	var promptArgs api.AgentPromptParams
	if params.Name == "doops_agent_prompt" {
		if err := json.Unmarshal(params.Arguments, &promptArgs); err != nil {
			writeJSON(buildErrorResponse(req.ID, -32602, "invalid agent prompt arguments"))
			return nil
		}
		operation := extractStringArg(params.Arguments, "operation")
		if operation != "" && operation != "ask" && operation != "apply" {
			writeJSON(buildErrorResponse(req.ID, -32602, "unsupported agent prompt operation: "+operation))
			return nil
		}
		if err := validateExplicitCredentialNames(promptArgs.CredentialRefs); err != nil {
			writeJSON(buildErrorResponse(req.ID, -32602, err.Error()))
			return nil
		}
		if promptArgs.CredentialMode != "" && promptArgs.CredentialMode != "auto" {
			writeJSON(buildErrorResponse(req.ID, -32602, "unsupported credential mode"))
			return nil
		}
		if promptArgs.CredentialMode == "auto" && promptArgs.Skill != "dosql" {
			writeJSON(buildErrorResponse(req.ID, -32602, "automatic credential mode is only supported by dosql"))
			return nil
		}
		// DoSql only uses the target's database bindings; a caller-chosen
		// reference would reintroduce the second selection path.
		if promptArgs.Skill == "dosql" && len(promptArgs.CredentialRefs) != 0 {
			writeJSON(buildErrorResponse(req.ID, -32602, "dosql_credential_refs_unsupported: pick one bound database credential with sql_credential (doops sql --credential <name>)"))
			return nil
		}
		// sql_credential narrows the automatic set to one bound credential by
		// name or ID; it never adds a credential the target has not bound.
		if promptArgs.SQLCredential != "" {
			if promptArgs.Skill != "dosql" || promptArgs.CredentialMode != "auto" {
				writeJSON(buildErrorResponse(req.ID, -32602, "sql_credential requires skill dosql with automatic credential mode"))
				return nil
			}
			if !validCredentialMetadataToken(promptArgs.SQLCredential, 128) {
				writeJSON(buildErrorResponse(req.ID, -32602, "credential reference name is invalid"))
				return nil
			}
		}
		if err := validatePromptCredentialAccess(operation, len(promptArgs.CredentialRefs) != 0 || promptArgs.CredentialMode == "auto"); err != nil {
			writeJSON(buildErrorResponse(req.ID, -32602, err.Error()))
			return nil
		}
		if len(promptArgs.CredentialBindings) != 0 {
			writeJSON(buildErrorResponse(req.ID, -32602, "credential_bindings are gateway-managed"))
			return nil
		}
	}
	detached, strippedArgs, detachErr := detachedArg(params.Name, params.Arguments)
	if detachErr != nil {
		writeJSON(buildErrorResponse(req.ID, -32602, detachErr.Error()))
		return nil
	}
	params.Arguments = strippedArgs
	var action GatewayAction
	if spec.visibility == clientCallable && spec.action != nil {
		action = spec.action(params.Arguments)
	}
	if action == "" {
		writeJSON(buildErrorResponse(req.ID, -32601, "unknown doops action for tool: "+params.Name))
		return nil
	}
	if action == ActionAgentUpgrade {
		arguments, err := prepareUpgradeAuditArguments(params.Arguments, auth, 0)
		if err != nil {
			return writeJSON(buildErrorResponse(req.ID, -32602, err.Error()))
		}
		params.Arguments = arguments
	}
	startAudit := h.store.StartAudit
	if params.Name == "doops_file_write" || action == ActionAgentUpgrade {
		startAudit = h.store.startMutationAudit
	}
	correlationID, digest, initialModel := "", "", ""
	if action == ActionAgentUpgrade {
		correlationID = extractStringArg(params.Arguments, "upgrade_operation_id")
		_, digest, _ = strings.Cut(extractStringArg(params.Arguments, "image"), "@")
	}
	if params.Name == "doops_agent_prompt" && validCredentialMetadataToken(strings.TrimSpace(promptArgs.Model), 128) {
		initialModel = strings.TrimSpace(promptArgs.Model)
	}
	var observedModel = initialModel
	var observedInputTokens, observedOutputTokens, observedTotalTokens int64
	auditID, auditErr := startAudit(AuditEvent{
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Cluster:        cluster,
		Instance:       instance,
		Action:         action,
		Session:        extractSession(params.Arguments),
		CommandSummary: summarizeToolCall(params.Name, params.Arguments),
		CorrelationID:  correlationID,
		Digest:         digest,
		Model:          initialModel,
		StartedAt:      time.Now().UTC(),
	})
	if auditErr != nil {
		writeJSON(buildErrorResponse(req.ID, -32603, "audit_unavailable"))
		return nil
	}
	if action == ActionAgentUpgrade {
		params.Arguments, _ = prepareUpgradeAuditArguments(params.Arguments, auth, auditID)
	}
	var opID string
	var pendingFinish *AuditFinish
	defer func() {
		if opID != "" || pendingFinish == nil {
			return
		}
		// Rejected admission has no execution lease, but still owns its audit.
		id := h.registerActiveOperation(GatewayActiveOperation{
			AuditID: auditID, UserID: auth.UserID, TokenID: auth.TokenID,
			Cluster: cluster, Instance: instance, Action: action,
			Session: extractSession(params.Arguments), Kind: "rpc",
			CommandSummary: summarizeToolCall(params.Name, params.Arguments),
		}, func() {})
		_ = h.finishExecutionAudit(auditID, *pendingFinish)
		h.finishActiveOperation(id)
	}()
	finishAudit := func(status, errMsg, tail string, bytesOut int64) error {
		if action != ActionAgentUpgrade {
			errMsg, tail = sanitizeAgentPromptAudit(params.Name, errMsg, tail)
		}
		errMsg = sanitizeSecretText(errMsg)
		if action != ActionAgentUpgrade {
			tail = sanitizeSecretText(tail)
		}
		finish := AuditFinish{
			Status:       status,
			Error:        errMsg,
			Tail:         tail,
			BytesIn:      int64(len(params.Arguments)),
			BytesOut:     bytesOut,
			Model:        observedModel,
			InputTokens:  observedInputTokens,
			OutputTokens: observedOutputTokens,
			TotalTokens:  observedTotalTokens,
			EndedAt:      time.Now().UTC(),
		}
		if err := h.finishExecutionAudit(auditID, finish); err != nil {
			pendingFinish = &finish
			log.Printf("[gateway] audit_completion_failed: id=%d", auditID)
			return err
		}
		if action == ActionAgentUpgrade && status != "observing" {
			log.Printf("[gateway] agent upgrade audit=%d operation=%s phase=%s status=%s category=%q",
				auditID, correlationID, upgradeAuditPhase(params.Arguments), status, errMsg)
		}
		return nil
	}
	if !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
		errMsg := fmt.Sprintf("forbidden: %s on %s/%s", action, cluster, instance)
		finishAudit("forbidden", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32003, errMsg))
		return nil
	}
	agent := h.registry.Wait(ctx, cluster, instance, h.targetReconnectGrace(params.Name))
	if agent == nil || agent.Cluster != cluster || agent.Instance != instance {
		errMsg := fmt.Sprintf("target offline: %s/%s", cluster, instance)
		finishAudit("offline", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32004, errMsg))
		return nil
	}
	// A model that was missing or rotated after connect is fetched from the
	// Gateway before doagent runs. The same active version is not probed again.
	if params.Name == "doops_agent_prompt" && h.hub != nil {
		if category := h.hub.deliverModelProvider(ctx, cluster, instance); category != "" {
			finishAudit("error", category, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, category))
			return nil
		}
	}
	if params.Name == "doops_ssh" && !agent.StructuredSSH {
		errMsg := fmt.Sprintf(
			"target Agent does not advertise protocol %q; upgrade the Agent before using structured doops ssh",
			structuredSSHProtocol,
		)
		finishAudit("blocked", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32003, errMsg))
		return nil
	}
	if err := agent.requireExecutionLifecycle(params.Name); err != nil {
		finishAudit("blocked", err.Error(), "", 0)
		writeJSON(buildErrorResponse(req.ID, -32003, err.Error()))
		return nil
	}
	if action == ActionAgentUpgrade {
		if !agent.UpgradeAudit {
			errMsg := "upgrade_audit_protocol_required: target Agent must advertise upgradeAudit v1 before an upgrade can be submitted"
			finishAudit("blocked", errMsg, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32003, errMsg))
			return nil
		}
		var args agentUpgradeParams
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			errMsg := fmt.Sprintf("invalid agent upgrade params: %v", err)
			finishAudit("error", errMsg, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32602, errMsg))
			return nil
		}
		if err := validateAgentUpgradeDispatch(args); err != nil {
			finishAudit("error", err.Error(), "", 0)
			writeJSON(buildErrorResponse(req.ID, -32003, err.Error()))
			return nil
		}
	}
	var err error
	var authorizedCredentials []authorizedCredentialMaterialization
	if params.Name == "doops_agent_prompt" && (len(promptArgs.CredentialRefs) != 0 ||
		(promptArgs.Skill == "dosql" && promptArgs.CredentialMode == "auto") ||
		// A change task resolves the Helm credential already bound to the target
		// without naming it; resolution returns nothing when none is bound.
		automaticHelmCredentialEligible(action, promptArgs.Skill)) {
		authorizedCredentials, err = h.hub.resolveExplicitCredentials(
			auth, cluster, instance,
			promptArgs.Instruction, promptArgs.CredentialRefs, action, promptArgs.Skill, promptArgs.CredentialMode,
			promptArgs.SQLCredential,
		)
		if err != nil {
			category := credentialErrorCategory(err)
			finishAudit("blocked", category, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32003, category))
			return nil
		}
	}
	opCtx, cancelOp := context.WithCancel(ctx)
	defer cancelOp()
	resourceKey := resourceKeyForTool(action, params.Name, params.Arguments, cluster, instance)
	operation := GatewayActiveOperation{
		AuditID:        auditID,
		Resource:       resourceKey,
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Cluster:        cluster,
		Instance:       instance,
		Action:         action,
		Session:        extractSession(params.Arguments),
		CommandSummary: summarizeToolCall(params.Name, params.Arguments),
		Kind:           "rpc",
	}
	if h.hasCancellationBarrier(operation) {
		finishAudit("busy", "target has a cancellation pending; wait for terminal evidence", "", 0)
		writeJSON(buildErrorResponse(req.ID, -32005, "target busy: cancellation pending"))
		return nil
	}
	var holderID string
	opID, holderID = h.registerActiveOperationForAgent(operation, cancelOp, agent)
	operationFinished := false
	defer func() {
		if !operationFinished {
			h.finishActiveOperation(opID)
		}
	}()
	if holderID != "" {
		errMsg := "session has an active operation; wait for terminal evidence: " + holderID
		finishAudit("busy", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32005, errMsg))
		return nil
	}

	// Credential use does not take the credential mutation lock: the grant is
	// re-read right before materialization, so a concurrent promotion only
	// decides which version this operation materializes.
	lease, err := agent.acquireActionLeaseForSession(opCtx, action, resourceKey, extractSession(params.Arguments), h.opts.MaxQueuedPerTarget, h.opts.TargetQueueTimeout)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			finishAudit("canceled", "operation_canceled", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32007, "operation canceled"))
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			finishAudit("timeout", "operation_timeout", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, "operation_timeout"))
			return nil
		}
		errMsg := fmt.Sprintf("%v: %s/%s", err, cluster, instance)
		finishAudit("busy", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32005, errMsg))
		return nil
	}
	h.attachOperationLease(opID, lease)
	opCtx = withInternalCredentialLock(opCtx, agent, resourceKey)
	if err := opCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			finishAudit("timeout", "operation_timeout", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, "operation_timeout"))
		} else {
			finishAudit("canceled", "operation_canceled", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32007, "operation canceled before dispatch"))
		}
		return nil
	}
	guarded := requiresExecutionOutcomeGuard(action, params.Name, params.Arguments, resourceKey)
	h.markOperationRunning(opID)
	if h.registry != nil {
		h.registry.recordLoadSampleMinInterval(15 * time.Second)
	}
	opCtx = h.withExecutionOwner(opCtx, agent, resourceKey, opID)
	if guarded {
		if err := h.beginExecution(agent, opID, auditID); err != nil {
			_ = finishAudit("blocked", err.Error(), "", 0)
			writeJSON(buildErrorResponse(req.ID, -32005, err.Error()))
			return nil
		}
	}
	var credentialPreparation credentialApplyPreparation
	if len(authorizedCredentials) != 0 {
		for index := range authorizedCredentials {
			item := &authorizedCredentials[index]
			resolved, refreshErr := h.hub.store.AuthorizeCredentialUse(CredentialUseRequest{
				ActorID: auth.UserID, CredentialID: item.Authorized.Resource.ID, Scope: item.Reference.Scope,
			})
			if refreshErr != nil || h.store.validateUserTokenAuth(auth) != nil ||
				!h.store.UserCanUseTarget(auth.UserID, cluster, instance) ||
				resolved.Grant.ID != item.Authorized.Grant.ID ||
				!credentialScopeEqual(resolved.Grant.Scope, item.Authorized.Grant.Scope) {
				finishAudit("blocked", "credential_authorization_changed", "", 0)
				writeJSON(buildErrorResponse(req.ID, -32003, "credential_authorization_changed"))
				return nil
			}
			item.Authorized = resolved
		}
		credentialPreparation, err = h.hub.materializeExplicitCredentialApply(
			opCtx, auth, promptArgs.SessionID, authorizedCredentials,
		)
		if err != nil {
			category := credentialOperationErrorCategory(err)
			finishAudit("error", category, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, category))
			return nil
		}
		promptArgs.CredentialRefs = nil
		if h.store.validateUserTokenAuth(auth) != nil || !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
			if cleanupErr := h.hub.rollbackCredentialPrepare(opCtx, credentialPreparation.completed); cleanupErr != nil {
				category := credentialOperationErrorCategory(cleanupErr)
				finishAudit("error", category, "", 0)
				writeJSON(buildErrorResponse(req.ID, -32603, category))
				return nil
			}
			finishAudit("blocked", "credential_authorization_changed", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32003, "credential_authorization_changed"))
			return nil
		}
		promptArgs.CredentialBindings = credentialPreparation.Bindings
		params.Arguments, err = json.Marshal(promptArgs)
		if err != nil {
			_ = h.hub.rollbackCredentialPrepare(opCtx, credentialPreparation.completed)
			finishAudit("error", "credential_binding_encode_failed", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, "credential_binding_encode_failed"))
			return nil
		}
	}
	tail := newAuditTailBuffer(8192)
	var bytesOut int64
	finalStatus := "success"
	finalErr := ""
	knownApplyFailure := false
	var fileWriteResult []byte
	var finalResponse []byte
	var upgradeEvidence string

	relayTimeout := h.opts.OperationTimeout
	terminalDetermined := false
	detachedFinished := false
	finishDetached := func() {
		if detachedFinished {
			return
		}
		detachedFinished = true
		status := finalStatus
		if !terminalDetermined {
			status = "error"
		}
		h.detached.finish(auditID, status)
	}
	if detached {
		relayTimeout = h.opts.DetachedTaskTimeout
		h.detached.register(&detachedTask{
			AuditID:        auditID,
			OperationID:    opID,
			UserID:         auth.UserID,
			Cluster:        cluster,
			Instance:       instance,
			Session:        extractSession(params.Arguments),
			CommandSummary: summarizeToolCall(params.Name, params.Arguments),
			Status:         "running",
			StartedAt:      time.Now().UTC(),
			cancel:         cancelOp,
		})
		defer finishDetached()
		// The background_task notification goes out once audit/lease are
		// established, before the Agent relay starts.
		writeJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/doops/background_task",
			"params": map[string]interface{}{
				"request_id":   req.ID,
				"audit_id":     auditID,
				"operation_id": opID,
			},
		})
	}

	err = agent.relayToolCall(opCtx, params, relayTimeout, func(msg gatewayWSMessage) error {
		if method, _ := msg.Parsed["method"].(string); method == "notifications/message" {
			if p, ok := msg.Parsed["params"].(map[string]interface{}); ok {
				if chunk, ok := p["data"].(string); ok {
					bytesOut += int64(len(chunk))
					if kind, _ := p["kind"].(string); kind == "acp_update" {
						observePromptACPUpdateUsage(chunk, &observedModel, &observedInputTokens, &observedOutputTokens, &observedTotalTokens)
					}
					if detached {
						h.detached.appendOutput(auditID, chunk)
					}
					if params.Name != "doops_file_write" {
						tail.WriteString(chunk)
					}
				}
			}
			forwarded := msg.Parsed
			if p, ok := forwarded["params"].(map[string]interface{}); ok {
				p["request_id"] = req.ID
			}
			raw, marshalErr := json.Marshal(forwarded)
			if marshalErr != nil {
				return marshalErr
			}
			return writeRaw(raw)
		}
		if id, ok := msg.Parsed["id"]; ok {
			msg.Parsed["id"] = req.ID
			if result, ok := msg.Parsed["result"].(map[string]interface{}); ok {
				extractAndStripPromptUsage(result, &observedModel, &observedInputTokens, &observedOutputTokens, &observedTotalTokens)
				if action == ActionAgentUpgrade {
					finalStatus, finalErr, upgradeEvidence = upgradeAuditResult(result, params.Arguments)
					bytesOut += int64(len(resultContentText(result)))
					knownApplyFailure = finalStatus == "error" || finalStatus == "busy"
				}
				if isErr, ok := result["isError"]; ok && fmt.Sprintf("%v", isErr) == "true" {
					if action != ActionAgentUpgrade {
						finalStatus = "error"
						finalErr = resultContentText(result)
						if action == ActionAsk && params.Name == "doops_agent_prompt" {
							switch code := stringField(result, "errorCode"); code {
							case askToolTimeout:
								finalStatus, finalErr = "timeout", code
							case askToolCancelled:
								finalStatus, finalErr = "canceled", code
							case askToolFailed:
								finalErr = code
							}
						}
					}
					knownApplyFailure = true
				}
			}
			if rpcErr, ok := msg.Parsed["error"]; ok && rpcErr != nil {
				finalStatus = "error"
				finalErr = relayErrorAuditText(params.Name, rpcErr)
				if executionResponseCanceled(msg.Parsed) {
					finalStatus = "canceled"
					if finalErr == "operation canceled" {
						finalErr = "operation_canceled"
					}
				} else if agentToolTimeoutResponse(msg.Parsed) {
					// The Agent's own tool deadline fired and it replied with a
					// terminal -32603. The command was stopped, not canceled, and
					// its outcome is known.
					finalStatus, finalErr = "timeout", AgentToolTimeoutCategory
				} else if params.Name == "doops_agent_prompt" {
					switch finalErr {
					case "operation_timeout", doagentPromptAdmissionTimeout, "doagent_prompt_queue_timeout":
						finalStatus = "timeout"
					}
				}
				knownApplyFailure = true
			}
			raw, _ := json.Marshal(msg.Parsed)
			if params.Name == "doops_file_write" {
				fileWriteResult = raw
				return nil
			}
			finalResponse = raw
			_ = id
		}
		return nil
	})
	if knownApplyFailure && len(credentialPreparation.completed) != 0 {
		if rollbackErr := h.hub.rollbackCredentialPrepare(context.WithoutCancel(opCtx), credentialPreparation.completed); rollbackErr != nil {
			err = fmt.Errorf("%w: credential_apply_outcome_unknown", errExecutionOutcomeUnknown)
		}
	}
	if err != nil {
		if action == ActionAgentUpgrade && upgradeAuditMutation(params.Arguments) && len(finalResponse) == 0 {
			finalStatus = "outcome_unknown"
			finalErr = "upgrade outcome unknown; the controller Job may have been created: " + sanitizeSecretText(err.Error())
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32008, finalErr))
		} else if errors.Is(err, errExecutionOutcomeUnknown) {
			finalStatus = "outcome_unknown"
			// Keep the reason: the code alone cannot say which check failed.
			finalErr = sanitizeSecretText(err.Error())
			response := buildErrorResponse(req.ID, -32008, finalErr)
			var receipt *unknownMutationReceipt
			if params.Name == "doops_agent_prompt" && errors.As(err, &receipt) {
				response.Error.Data = map[string]interface{}{"structuredContent": receipt.report}
			}
			finalResponse, _ = json.Marshal(response)
		} else if errors.Is(err, errDoSQLReadCanceled) {
			finalStatus = "canceled"
			finalErr = doSQLReadCanceledCategory
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32007, errDoSQLReadCanceled.Error()))
		} else if errors.Is(err, errGatewayClientDisconnected) {
			finalStatus = "canceled"
			finalErr = "gateway_client_disconnected"
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32603, errGatewayClientDisconnected.Error()))
		} else if errors.Is(err, context.Canceled) {
			finalStatus = "canceled"
			finalErr = "operation_canceled"
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32007, "operation canceled"))
		} else if errors.Is(err, errGatewayOperationTimeout) || errors.Is(err, context.DeadlineExceeded) {
			finalStatus, finalErr = "timeout", "operation_timeout"
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32603, err.Error()))
		} else {
			finalStatus = "error"
			finalErr = sanitizeSecretText(err.Error())
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32603, finalErr))
		}
	}
	auditTail := tail.String()
	if action == ActionAgentUpgrade {
		// Upgrade evidence is redacted field by field before JSON encoding.
		// Text redaction here can consume JSON quotes and corrupt the receipt.
		auditTail = upgradeEvidence
	}
	terminalDetermined = true
	if detached {
		finishDetached()
	}
	if err := finishAudit(finalStatus, finalErr, auditTail, bytesOut); err != nil {
		h.finishActiveOperation(opID)
		operationFinished = true
		response := buildErrorResponse(req.ID, -32603, "audit_completion_failed; execution outcome requires verification")
		if action == ActionAgentUpgrade {
			response = upgradeAuditCompletionError(req.ID, auditID, params.Arguments, finalResponse)
			log.Printf("[gateway] upgrade audit completion pending: audit=%d operation=%s evidence=%s",
				auditID, correlationID, upgradeEvidence)
		}
		failure, _ := json.Marshal(response)
		return writeRaw(failure)
	}
	// Persist the terminal audit state before releasing the resource lease. The
	// client response is sent last so a follow-up mutation cannot race cleanup.
	h.finishActiveOperation(opID)
	operationFinished = true
	if len(fileWriteResult) != 0 {
		return writeRaw(fileWriteResult)
	}
	if len(finalResponse) != 0 {
		return writeRaw(finalResponse)
	}
	return nil
}

func validateExplicitCredentialNames(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !validCredentialMetadataToken(name, 128) {
			return errors.New("credential reference name is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("credential reference name is duplicated")
		}
		seen[name] = struct{}{}
	}
	return nil
}

func sanitizeAgentPromptAudit(toolName, errMsg, tail string) (string, string) {
	if toolName == "doops_file_write" {
		if errMsg != "" {
			errMsg = "file_write_failed"
		}
		return errMsg, ""
	}
	if toolName != "doops_agent_prompt" {
		return sanitizeSecretText(errMsg), sanitizeSecretText(tail)
	}
	if errMsg != "" {
		errMsg = agentPromptAuditCategory(errMsg)
	}
	return errMsg, ""
}

// relayErrorAuditText keeps the audit text stable. A prompt error message is
// already a stable category, so recording the formatted envelope would make
// every prompt failure sanitize to credential_operation_failed and lose the
// category the operator and the audit need.
func relayErrorAuditText(tool string, rpcErr interface{}) string {
	errorObject, _ := rpcErr.(map[string]interface{})
	if tool == "doops_agent_prompt" || isAgentToolTimeout(errorObject) {
		if message, _ := errorObject["message"].(string); strings.TrimSpace(message) != "" {
			return sanitizeSecretText(strings.TrimSpace(message))
		}
	}
	return fmt.Sprintf("%v", rpcErr)
}

// agentPromptAuditCategory keeps only stable categories for prompt audits. An
// unknown outcome keeps its code, and its reason only when the reason is itself
// a stable category, so the audit still says the outcome must be verified.
func agentPromptAuditCategory(errMsg string) string {
	prefix := errExecutionOutcomeUnknown.Error()
	message := strings.TrimSpace(errMsg)
	if message == prefix {
		return prefix
	}
	if strings.HasPrefix(message, prefix+":") {
		reason := strings.TrimSpace(strings.TrimPrefix(message, prefix+":"))
		if category := sanitizeCredentialCategory(reason); category == reason {
			return prefix + ": " + category
		}
		return prefix
	}
	return sanitizeCredentialCategory(message)
}

func internalCredentialTool(tool string) bool {
	return tool == "doops_credential_import" || tool == "doops_credential_materialize" ||
		tool == "doops_credential_cleanup"
}

func parsePositiveAuditTokenCount(raw interface{}) (int64, bool) {
	if raw == nil {
		return 0, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return 0, false
	}
	var count uint64
	if err := json.Unmarshal(encoded, &count); err != nil {
		return 0, false
	}
	const maxAuditInt = uint64(1<<63 - 1)
	if count > maxAuditInt {
		return int64(maxAuditInt), true
	}
	return int64(count), true
}

func observePromptACPUpdateUsage(raw string, model *string, inTok, outTok, totTok *int64) {
	var parsed map[string]interface{}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return
	}
	if parsed["replay"] == true {
		return
	}
	if m, _ := parsed["model"].(string); strings.TrimSpace(m) != "" && validCredentialMetadataToken(strings.TrimSpace(m), 128) {
		*model = strings.TrimSpace(m)
	}
	apply := func(src map[string]interface{}) {
		if n, ok := parsePositiveAuditTokenCount(src["inputTokens"]); ok && n > *inTok {
			*inTok = n
		}
		if n, ok := parsePositiveAuditTokenCount(src["outputTokens"]); ok && n > *outTok {
			*outTok = n
		}
		if n, ok := parsePositiveAuditTokenCount(src["totalTokens"]); ok && n > *totTok {
			*totTok = n
		}
	}
	if usage, ok := parsed["usage"].(map[string]interface{}); ok {
		apply(usage)
	}
	apply(parsed)
}

func extractAndStripPromptUsage(result map[string]interface{}, model *string, inTok, outTok, totTok *int64) {
	raw, exists := result["_doops_usage"]
	if !exists {
		return
	}
	delete(result, "_doops_usage")
	usage, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if m, _ := usage["model"].(string); strings.TrimSpace(m) != "" && validCredentialMetadataToken(strings.TrimSpace(m), 128) {
		*model = strings.TrimSpace(m)
	}
	if n, ok := parsePositiveAuditTokenCount(usage["input_tokens"]); ok && n > *inTok {
		*inTok = n
	}
	if n, ok := parsePositiveAuditTokenCount(usage["output_tokens"]); ok && n > *outTok {
		*outTok = n
	}
	if n, ok := parsePositiveAuditTokenCount(usage["total_tokens"]); ok && n > *totTok {
		*totTok = n
	}
}
