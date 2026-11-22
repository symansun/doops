package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/user/doops/agent/api"
)

// Tool handlers for prompt-driven Agent work and background task status.

func (gw *Gateway) handleToolTaskStatus(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	var args api.TaskStatusParams
	json.Unmarshal(argBytes, &args)
	info, err := gw.getTaskStatus(args.TaskID, args.Lines)
	if err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
	} else {
		resultText := fmt.Sprintf("TaskID: %s\nPID: %d\nStatus: %s\nExitCode: %d\nLog: %s\n---\n%s",
			info.TaskID, info.PID, info.Status, info.ExitCode, info.LogPath, info.LogTail)
		writeJSON(buildSuccessResponse(reqID, resultText))
	}
}

func (gw *Gateway) handleToolAgentPrompt(call toolCall) {
	ctx, reqID, argBytes, writeJSON, sessionID, pushProgress := call.ctx, call.reqID, call.args, call.write, call.sessionID, call.progress
	var args api.AgentPromptParams
	json.Unmarshal(argBytes, &args)
	operation := strings.ToLower(strings.TrimSpace(args.Operation))
	if operation == "apply" {
		if strings.TrimSpace(args.Instruction) == "" {
			writeJSON(buildErrorResponse(reqID, -32602, "apply requires a non-empty instruction"))
			return
		}
	}
	if len(args.CredentialRefs) != 0 {
		writeJSON(buildErrorResponse(reqID, -32602, "credential_refs must be resolved by the gateway"))
		return
	}
	if len(args.CredentialBindings) != 0 {
		if operation != "" && operation != "ask" && operation != "apply" {
			writeJSON(buildErrorResponse(reqID, -32602, "credential bindings require an ask or apply operation"))
			return
		}
		if err := validatePromptCredentialAccess(operation, true); err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
			return
		}
		bindingContext, err := credentialBindingContext(args.CredentialBindings)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
			return
		}
		args.Instruction += "\n\nGateway-managed credential bindings (metadata only; never read Secret values). Use name or credential_id as credential_ref; grant_id and version_id are audit metadata:\n" + bindingContext
		if hasHelmCredentialBinding(args.CredentialBindings) {
			if operation != "apply" {
				writeJSON(buildErrorResponse(reqID, -32602, "Helm credential bindings require apply"))
				return
			}
			cleanup, err := bindHelmCredentialsForPrompt(sessionID, args.CredentialBindings)
			if err != nil {
				writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
				return
			}
			defer cleanup()
		}
		if hasDatabaseCredentialBinding(args.CredentialBindings) {
			cleanup, err := bindDoSQLCredentialsForPrompt(sessionID, args.CredentialBindings)
			if err != nil {
				writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
				return
			}
			defer cleanup()
			if strings.EqualFold(strings.TrimSpace(args.Skill), "dosql") &&
				strings.TrimSpace(args.SQLDatabase) != "" && !doSQLDirectRequested(args) {
				resolution, hint, resolveErr := resolveDoSQLPromptResolution(ctx, args.CredentialBindings,
					args.SQLDatabase, readDoSQLSecretData, discoverDatabaseCandidateList)
				if resolveErr == nil {
					args.Instruction += "\n\n" + hint
					if cleanupResolution, writeErr := writeDoSQLResolution(sessionID, resolution); writeErr == nil {
						defer cleanupResolution()
					}
				} else {
					args.Instruction += "\n\n[DoSQL server-side resolution failed] " +
						sanitizeSecretText(resolveErr.Error()) + "；不要猜测 credential/asset，直接报告该错误。"
				}
			}
		}
	}
	if doSQLDirectRequested(args) || doSQLCopyRequested(args) {
		handle := handleDirectDoSQLPrompt
		if doSQLCopyRequested(args) {
			handle = handleDoSQLCopy
		}
		result, err := handle(ctx, sessionID, args)
		if err != nil {
			if errors.Is(err, errExecutionOutcomeUnknown) {
				writeJSON(buildErrorResponse(reqID, -32008, sanitizeSecretText(err.Error())))
				return
			}
			if errors.Is(err, errDoSQLReadCanceled) {
				reply := buildErrorResponse(reqID, -32007, errDoSQLReadCanceled.Error())
				reply.Error.Data = map[string]string{"sqlstate": "57014", "category": doSQLReadCanceledCategory}
				writeJSON(reply)
				return
			}
			if ctx.Err() != nil {
				writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
				return
			}
			writeJSON(buildToolErrorResponse(reqID, sanitizeSecretText(err.Error())))
			return
		}
		writeJSON(buildSuccessResponse(reqID, result))
		return
	}
	nativeMode, err := doagentModeForPrompt(operation, "")
	if err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
		return
	}
	if args.ResponseFormat != "" && args.ResponseFormat != "json" {
		writeJSON(buildErrorResponse(reqID, -32602, `response_format must be "json" when specified`))
		return
	}
	if args.EventFormat != "" && args.EventFormat != "acp" {
		writeJSON(buildErrorResponse(reqID, -32602, `event_format must be "acp" when specified`))
		return
	}
	if operation == "apply" && len(args.Attachments) > 0 {
		writeJSON(buildErrorResponse(reqID, -32602, "deployment form does not accept conversation attachments"))
		return
	}
	if err := validateAgentPromptAttachments(args.Attachments); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
		return
	}
	switch strings.ToLower(strings.TrimSpace(args.Mode)) {
	case "metadata":
		if args.ResponseFormat != "" {
			writeJSON(buildErrorResponse(reqID, -32602, "response_format is only supported for agent prompts"))
			return
		}
		result, err := gw.handleAgentMetadataWS(ctx, sessionID)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}
		return
	case "history":
		if args.ResponseFormat != "" {
			writeJSON(buildErrorResponse(reqID, -32602, "response_format is only supported for agent prompts"))
			return
		}
		result, err := gw.handleAgentHistoryWS(ctx, sessionID)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}
		return
	}
	gw.handleAgentPromptWS(
		ctx,
		reqID,
		sessionID,
		args.Instruction,
		args.Attachments,
		args.Model,
		args.ResponseFormat,
		agentPromptExecutionContext{
			NativeMode:          nativeMode,
			RequireToolEvidence: operation == "apply",
			StructuredEvents:    args.EventFormat == "acp",
			Skill:               args.Skill,
		},
		pushProgress,
		writeJSON,
	)
}
