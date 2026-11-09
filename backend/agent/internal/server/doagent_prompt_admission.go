package server

import (
	"context"
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
		errorObject, _ := rpcErr.(map[string]interface{})
		data, _ := errorObject["data"].(map[string]interface{})
		admission := promptAdmission{rpcCode: doagentIntField(errorObject, "code")}
		if data["failureClass"] == "admission_timeout" {
			// A 202 admission timeout proves the prompt was not admitted yet, but
			// not that no later turn starts; keep the unknown-outcome protection.
			admission.kind = promptAdmissionTransportFailed
			admission.detail = "admission_timeout"
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
			kind:          promptAdmissionQueuedNoTurn,
			queueScope:    promptQueueSession,
			queuePosition: doagentIntField(result, "queuePosition"),
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

// settleQueuedPromptWS decides what a queued admission means on the wire.
//
// doagent 0.2.42 has no request-scoped cancel: the only cleanup primitive is a
// session-wide session/cancel, which also stops the session's active turn and
// drops every other request queued there. That is safe only when this Agent
// created the session for this one prompt. A global-admission reply proves the
// session was idle, so the fresh, dedicated session holds nothing but this
// request's queue entry. Any other queued reply (a reused session, or a
// per-session FIFO reply that proves another turn is running) must not be
// cancelled, and the Gateway cannot prove the entry is gone, so it reports the
// cleanup as unknown and forbids automatic retry.
func settleQueuedPromptWS(ctx context.Context, reqID interface{}, doagentURL, targetSessionID string, sessionDedicated bool, admission promptAdmission, writeJSON func(v interface{})) {
	if admission.queueScope == promptQueueGlobal && sessionDedicated {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), doagentCancellationTimeout)
		defer cancel()
		if err := cancelDoagentSessionQueue(cleanupCtx, doagentURL, targetSessionID); err != nil {
			log.Printf("doagent prompt queue cleanup failed: doagent_session=%s global_position=%d error=%s",
				targetSessionID, admission.globalPosition, sanitizeSecretText(err.Error()))
			writeJSON(buildErrorResponse(reqID, -32603, doagentPromptCleanupUnknown))
			return
		}
		writeJSON(buildErrorResponse(reqID, -32603, doagentPromptQueued))
		return
	}
	writeJSON(buildErrorResponse(reqID, -32603, doagentPromptCleanupUnknown))
}

// cancelDoagentSessionQueue sends the only cleanup primitive doagent 0.2.42
// offers. A successful RPC means the cancel was acknowledged; it does not prove
// the queue entry was removed, so callers must use it only where the whole
// session belongs to the request being cleaned up.
func cancelDoagentSessionQueue(ctx context.Context, baseURL, sessionID string) error {
	_, err := doagentRPCContext(ctx, baseURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "cancel-" + sessionID,
		"method":  "session/cancel",
		"params":  map[string]interface{}{"sessionId": sessionID},
	})
	return err
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
