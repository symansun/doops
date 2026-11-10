package server

import (
	"context"
	"errors"
	"log"
	"strings"
)

// doagent answers session/prompt with a normal JSON-RPC result for states that
// are decided before admission. None of them admits a turn, so none of them may
// be reported as an unknown mutation outcome. The RPC codes below are emitted
// before admission and are stable protocol categories, not internal failures.
const (
	doagentRPCInvalidRequest  = -32602
	doagentRPCSessionNotFound = -32001
	doagentRPCRestricted      = -32003
)

// promptAdmissionKind is the Gateway-side classification of one session/prompt
// reply. The handler maps it to the wire error, the audit category and the UI
// state in one place instead of re-parsing result maps mid-flow.
type promptAdmissionKind string

const (
	promptAdmissionAccepted        promptAdmissionKind = "accepted"
	promptAdmissionQueuedNoTurn    promptAdmissionKind = "queued_no_turn"
	promptAdmissionNoPrompt        promptAdmissionKind = "no_prompt"
	promptAdmissionRejected        promptAdmissionKind = "rejected"
	promptAdmissionBusy            promptAdmissionKind = "busy"
	promptAdmissionTimedOut        promptAdmissionKind = "timed_out"
	promptAdmissionTransportFailed promptAdmissionKind = "transport_failed"
	promptAdmissionMalformed       promptAdmissionKind = "malformed"
)

// promptQueueScope names the doagent queue that held the request: the
// per-session FIFO (a turn is already running in that session) or the
// process-global admission gate (the session was idle but every global turn
// permit was held).
type promptQueueScope string

const (
	promptQueueSession promptQueueScope = "session"
	promptQueueGlobal  promptQueueScope = "global"
)

// promptAdmission is one classified prompt reply. detail carries only stable,
// non-sensitive text for logs; it never carries prompt content or credentials.
type promptAdmission struct {
	kind           promptAdmissionKind
	turnID         string
	queueRequestID string
	queueScope     promptQueueScope
	queuePosition  int
	globalPosition int
	rpcCode        int
	detail         string
}

func (a promptAdmission) admitted() bool { return a.kind == promptAdmissionAccepted }

// classifyDoagentPromptAdmission maps a session/prompt JSON-RPC response to the
// admission state. A nil response or one without a usable result is malformed;
// the caller still owns what that means on the wire, because only it knows
// whether the transport also failed.
func classifyDoagentPromptAdmission(response map[string]interface{}) promptAdmission {
	if response == nil {
		return promptAdmission{kind: promptAdmissionMalformed, detail: "empty response"}
	}
	if rpcErr, ok := response["error"]; ok && rpcErr != nil {
		if response["result"] != nil {
			// An error envelope that still carries a result proves neither an
			// admission nor a pre-admission refusal. Keep the old conservative
			// rule: treat it as a transport failure and probe the session.
			return promptAdmission{kind: promptAdmissionTransportFailed, detail: "error with result"}
		}
		errorObject, _ := rpcErr.(map[string]interface{})
		data, _ := errorObject["data"].(map[string]interface{})
		admission := promptAdmission{rpcCode: doagentIntField(errorObject, "code")}
		if data["failureClass"] == "admission_timeout" {
			// Only an atomic withdrawal before preparation/admission proves the
			// request cannot start later. Older timeout replies lack that fact.
			if data["executionStarted"] == false && data["retryable"] == true {
				admission.kind = promptAdmissionTimedOut
				admission.detail = "request withdrawn before admission"
				return admission
			}
			admission.kind = promptAdmissionTransportFailed
			admission.detail = "admission_timeout"
			return admission
		}
		message, _ := errorObject["message"].(string)
		if admission.rpcCode == -32000 && (message == doagentPromptBusy || strings.HasPrefix(message, doagentPromptBusy+":")) {
			admission.kind = promptAdmissionBusy
			admission.detail = "busy request was not queued"
			return admission
		}
		switch admission.rpcCode {
		case doagentRPCInvalidRequest, doagentRPCSessionNotFound, doagentRPCRestricted:
			admission.kind = promptAdmissionRejected
			admission.detail = "pre-admission rpc error"
		default:
			admission.kind = promptAdmissionTransportFailed
			admission.detail = "rpc error"
		}
		return admission
	}
	result, ok := response["result"].(map[string]interface{})
	if !ok {
		return promptAdmission{kind: promptAdmissionMalformed, detail: "result must be an object"}
	}
	switch status, _ := result["status"].(string); status {
	case "accepted":
		turnID := strings.TrimSpace(doagentStringField(result, "turnId"))
		if turnID == "" {
			return promptAdmission{kind: promptAdmissionMalformed, detail: "accepted without turn id"}
		}
		return promptAdmission{kind: promptAdmissionAccepted, turnID: turnID}
	case "queued":
		// A queued reply that already names its turn was promoted to admission:
		// that is a normal turn, not a pre-admission state.
		if turnID := strings.TrimSpace(doagentStringField(result, "turnId")); turnID != "" {
			return promptAdmission{kind: promptAdmissionAccepted, turnID: turnID}
		}
		admission := promptAdmission{
			kind:           promptAdmissionQueuedNoTurn,
			queueScope:     promptQueueSession,
			queuePosition:  doagentIntField(result, "queuePosition"),
			queueRequestID: strings.TrimSpace(doagentStringField(result, "queueRequestId")),
		}
		if global, ok := result["globalPosition"]; ok && global != nil {
			// doagent consults the global gate only while the session is idle, so
			// this scope also proves the session had no active turn when the
			// prompt arrived.
			admission.queueScope = promptQueueGlobal
			admission.globalPosition = doagentIntField(result, "globalPosition")
		}
		return admission
	case "no_prompt":
		return promptAdmission{kind: promptAdmissionNoPrompt, detail: "prompt was empty"}
	default:
		return promptAdmission{kind: promptAdmissionMalformed, detail: "unknown prompt status"}
	}
}

// doagentPromptQueued is the stable category for a queued request whose queue
// entry this call removed: this request has no turn, no mutation can have
// started, and the caller may retry (preferably with a new session).
const doagentPromptQueued = "doagent_prompt_queued"

// doagentPromptCleanupUnknown is the stable category for a queued request that
// may still run in the background: this request has no turn, but its queue entry
// was not provably removed. It must never be retried automatically.
const doagentPromptCleanupUnknown = "doagent_prompt_queue_cleanup_unknown"

// doagentPromptNoPrompt is the stable category for an empty prompt.
const doagentPromptNoPrompt = "doagent_prompt_no_prompt"

const doagentPromptBusy = "doagent_prompt_busy"
const doagentPromptAdmissionUnsupported = "doagent_prompt_admission_unsupported"
const doagentPromptAdmissionUnavailable = "doagent_prompt_admission_unavailable"
const doagentPromptAdmissionTimeout = "doagent_prompt_admission_timeout"
const doagentPromptCancelledBeforeAdmission = "doagent_prompt_cancelled_before_admission"

// Reject busy requests before they become background work. Checking the
// capability before each prompt also detects a runtime replaced at the same URL.
func requireDoagentPromptAdmission(ctx context.Context, baseURL string) error {
	response, err := doagentRPCContext(ctx, baseURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "prompt-admission-capability",
		"method":  "initialize",
		"params":  map[string]interface{}{},
	})
	if err != nil {
		return errors.New(doagentPromptAdmissionUnavailable)
	}
	result, ok := response["result"].(map[string]interface{})
	if !ok || response["error"] != nil {
		return errors.New(doagentPromptAdmissionUnavailable)
	}
	capabilities, _ := result["agentCapabilities"].(map[string]interface{})
	prompt, _ := capabilities["promptCapabilities"].(map[string]interface{})
	if supported, _ := prompt["rejectWhenBusy"].(bool); !supported {
		return errors.New(doagentPromptAdmissionUnsupported)
	}
	return nil
}

// A queued response violates the negotiated rejectWhenBusy contract. Only an
// exact request cancellation can establish that it will not execute later.
func settleQueuedPromptWS(ctx context.Context, reqID interface{}, doagentURL, targetSessionID string, admission promptAdmission, writeJSON func(v interface{})) {
	log.Printf("doagent violated rejectWhenBusy admission contract: doagent_session=%s", targetSessionID)
	if admission.queueRequestID != "" {
		// doagent named this queue entry, so the cleanup can remove exactly one
		// request and is safe even in a session the Gateway shares with another
		// caller.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), doagentCancellationTimeout)
		defer cancel()
		status, err := cancelDoagentQueuedRequest(cleanupCtx, doagentURL, targetSessionID, admission.queueRequestID)
		if err != nil || status != "queued_cancelled" {
			log.Printf("doagent prompt queue cleanup unconfirmed: doagent_session=%s queue_request=%s status=%s error=%v",
				targetSessionID, admission.queueRequestID, status, err)
			writeJSON(buildErrorResponse(reqID, -32603, doagentPromptCleanupUnknown))
			return
		}
		writeJSON(buildErrorResponse(reqID, -32603, doagentPromptQueued))
		return
	}
	writeJSON(buildErrorResponse(reqID, -32603, doagentPromptCleanupUnknown))
}

// cancelDoagentQueuedRequest removes exactly the queued request the reply named.
// It reports doagent's status (queued_cancelled / queued_not_found) so the
// caller can tell a confirmed cleanup from an unconfirmed one.
func cancelDoagentQueuedRequest(ctx context.Context, baseURL, sessionID, queueRequestID string) (string, error) {
	response, err := doagentRPCContext(ctx, baseURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "cancel-queued-" + queueRequestID,
		"method":  "session/cancel",
		"params": map[string]interface{}{
			"sessionId":      sessionID,
			"queueRequestId": queueRequestID,
		},
	})
	if err != nil {
		return "", err
	}
	result, _ := response["result"].(map[string]interface{})
	status, _ := result["status"].(string)
	return status, nil
}

// logDoagentPromptAdmission records the classified admission without prompt
// content, credentials or model payloads.
func logDoagentPromptAdmission(doopsSessionID, targetSessionID string, admission promptAdmission) {
	log.Printf("doagent prompt admission: session=%s doagent_session=%s category=%s queue_scope=%s queue_position=%d global_position=%d rpc_code=%d carried_turn=%t detail=%s",
		doopsSessionID, targetSessionID, admission.kind, admission.queueScope,
		admission.queuePosition, admission.globalPosition, admission.rpcCode,
		admission.turnID != "", admission.detail)
}

func doagentStringField(fields map[string]interface{}, key string) string {
	value, _ := fields[key].(string)
	return value
}

func doagentIntField(fields map[string]interface{}, key string) int {
	switch value := fields[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	default:
		return 0
	}
}
