package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/user/doops/agent/api"
	"strings"
	"sync/atomic"
	"time"
)

// isAgentToolTimeout recognizes the Agent's own-deadline reply: -32603 with the
// agent_tool_timeout message prefix or a matching error.data category. It is a
// known terminal outcome, never a cancellation or execution_outcome_unknown.
func isAgentToolTimeout(rpcErr map[string]interface{}) bool {
	if rpcErr == nil {
		return false
	}
	if code, ok := numericID(rpcErr["code"]); !ok || code != -32603 {
		return false
	}
	if message, _ := rpcErr["message"].(string); strings.HasPrefix(strings.TrimSpace(message), AgentToolTimeoutCategory) {
		return true
	}
	if data, ok := rpcErr["data"].(map[string]interface{}); ok {
		category, _ := data["category"].(string)
		return category == AgentToolTimeoutCategory
	}
	return false
}

func agentToolTimeoutResponse(response map[string]interface{}) bool {
	rpcErr, _ := response["error"].(map[string]interface{})
	return isAgentToolTimeout(rpcErr)
}

// A pre-admission category alone is not enough: cleanup_unknown can still run.
func promptReplyProvesNoExecution(tool string, response map[string]interface{}) bool {
	if tool != "doops_agent_prompt" || response["result"] != nil || !promptPreAdmissionReply(response) {
		return false
	}
	rpcError, _ := response["error"].(map[string]interface{})
	message, _ := rpcError["message"].(string)
	return strings.TrimSpace(message) != doagentPromptCleanupUnknown
}

func promptReplyUnknownError(tool string, response map[string]interface{}) error {
	if tool != "doops_agent_prompt" {
		return nil
	}
	if executionResponseUnknown(response) {
		return executionUnknownFromResponse(response)
	}
	rpcError, _ := response["error"].(map[string]interface{})
	message, _ := rpcError["message"].(string)
	if strings.TrimSpace(message) == doagentPromptCleanupUnknown {
		return fmt.Errorf("%w: %s", errExecutionOutcomeUnknown, doagentPromptCleanupUnknown)
	}
	return nil
}

var lastProbe atomic.Value
var errGatewayOperationTimeout = errors.New("operation timed out")

// toolCallParamsFor builds the tools/call params. Only an Agent that declared
// toolDeadline gets timeout_ms: an older Agent would ignore it and still end
// its own 30-minute limit as a cancellation.
func toolCallParamsFor(a *GatewayAgent, params api.ToolCallParams, timeout time.Duration) map[string]interface{} {
	callParams := map[string]interface{}{
		"name":      params.Name,
		"arguments": params.Arguments,
	}
	if len(params.TargetManifest) != 0 {
		callParams["target_manifest"] = params.TargetManifest
	}
	if a != nil && a.ToolDeadline && timeout > 0 {
		callParams["timeout_ms"] = timeout.Milliseconds()
	}
	return callParams
}

func (a *GatewayAgent) relayToolCall(ctx context.Context, params api.ToolCallParams, timeout time.Duration, forward func(gatewayWSMessage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := atomic.AddInt64(&a.reqID, 1)
	ch := a.registerPending(id, "tools/cancel")
	defer a.unregisterPending(id)
	uncertainIfInterrupted := interruptionNeedsReconciliation(params.Name, params.Arguments)
	var prompt api.AgentPromptParams
	readSQL := params.Name == "doops_agent_prompt" && json.Unmarshal(params.Arguments, &prompt) == nil &&
		strings.EqualFold(strings.TrimSpace(prompt.Operation), "ask") && doSQLDirectRequested(prompt) && !doSQLCopyRequested(prompt)
	uncertainWithoutTerminal := readSQL || missingTerminalNeedsReconciliation(params.Name, params.Arguments)
	sessionID := extractSession(params.Arguments)
	routeNotifications := params.Name != "doops_task_status"
	a.pendingMu.Lock()
	if routeNotifications {
		if a.activeByRequest == nil {
			a.activeByRequest = make(map[int64]*gatewayMessageQueue)
		}
		a.activeByRequest[id] = ch
		if sessionID != "" {
			if a.activeSessions == nil {
				a.activeSessions = make(map[string]int)
			}
			a.activeSessions[sessionID]++
		}
	}
	a.pendingMu.Unlock()
	defer func() {
		a.pendingMu.Lock()
		if routeNotifications && a.activeByRequest[id] == ch {
			delete(a.activeByRequest, id)
		}
		if routeNotifications && sessionID != "" {
			if count := a.activeSessions[sessionID]; count <= 1 {
				delete(a.activeSessions, sessionID)
			} else {
				a.activeSessions[sessionID] = count - 1
			}
		}
		a.pendingMu.Unlock()
	}()

	callParams := toolCallParamsFor(a, params, timeout)
	if err := a.writeJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      id,
		"params":  callParams,
	}); err != nil {
		if uncertainWithoutTerminal {
			return fmt.Errorf("%w: dispatch failed: %v", errExecutionOutcomeUnknown, err)
		}
		return fmt.Errorf("%w: dispatch failed: %v", errGatewayAgentDisconnected, err)
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, timeout)
	defer cancelWait()
	var forwardErr error
	forwardMessage := func(msg gatewayWSMessage) (bool, error) {
		if msg.Parsed == nil {
			return false, nil
		}
		if _, ok := msg.Parsed["id"]; ok {
			// An unknown prompt must not enter the known-failure callback:
			// it can still be using the credentials prepared for this request.
			if err := promptReplyUnknownError(params.Name, msg.Parsed); err != nil {
				return true, err
			}
			if result, ok := msg.Parsed["result"].(map[string]interface{}); readSQL && ok {
				if _, ack := result["canceled"]; ack {
					return false, nil
				}
			}
			if readSQL && executionResponseCanceled(msg.Parsed) {
				rpcError, _ := msg.Parsed["error"].(map[string]interface{})
				data, _ := rpcError["data"].(map[string]interface{})
				if data["category"] == doSQLReadCanceledCategory && data["sqlstate"] == "57014" {
					return true, errDoSQLReadCanceled
				}
				return true, errExecutionOutcomeUnknown
			}
			if (uncertainWithoutTerminal && executionResponseUnknown(msg.Parsed)) ||
				remoteMutationFailed(params.Name, params.Arguments, msg.Parsed) {
				return true, executionUnknownFromResponse(msg.Parsed)
			}
			if uncertainIfInterrupted && !promptReplyProvesNoExecution(params.Name, msg.Parsed) &&
				(ctx.Err() != nil || executionResponseCanceled(msg.Parsed)) {
				return true, fmt.Errorf("%w: mutation was interrupted before it finished", errExecutionOutcomeUnknown)
			}
			if forwardErr == nil {
				if err := forward(msg); err != nil {
					forwardErr = err
				}
			}
			return true, forwardErr
		}
		if forwardErr == nil {
			if err := forward(msg); err != nil {
				forwardErr = err
			}
		}
		return false, nil
	}
	cancelAndWait := func() (bool, error) {
		err := a.waitPendingCancel(ch)
		// A terminal may race the disconnect selected by waitPendingCancel.
		// Read the saved reply only after its publication barrier has closed.
		select {
		case <-ch.terminal:
			if err := promptReplyUnknownError(params.Name, ch.terminalMessage.Parsed); err != nil {
				return true, err
			}
			if readSQL || promptReplyProvesNoExecution(params.Name, ch.terminalMessage.Parsed) {
				return forwardMessage(ch.terminalMessage)
			}
		default:
		}
		if err != nil {
			if !uncertainWithoutTerminal && errors.Is(err, errExecutionOutcomeUnknown) {
				return false, nil
			}
			return true, err
		}
		// Local termination cannot fence a remote controller or detached process.
		if uncertainIfInterrupted {
			return true, errExecutionOutcomeUnknown
		}
		return false, nil
	}
	interruptionResult := func() (bool, error) {
		if waitCtx.Err() == nil {
			return false, nil
		}
		if done, err := cancelAndWait(); done {
			return true, err
		}
		if err := ctx.Err(); err != nil {
			return true, err
		}
		return true, errGatewayOperationTimeout
	}
	for {
		// A busy output queue must not starve cancellation or the deadline.
		if done, err := interruptionResult(); done {
			return err
		}
		if msg, ok, queueErr := ch.dequeue(); queueErr != nil {
			if done, err := cancelAndWait(); done {
				return err
			}
			return queueErr
		} else if ok {
			if done, err := forwardMessage(msg); done {
				return err
			}
			continue
		}

		select {
		case <-waitCtx.Done():
		case <-ch.notify:
		case <-a.closed:
			for {
				if done, err := interruptionResult(); done {
					return err
				}
				msg, ok, queueErr := ch.dequeue()
				if queueErr != nil {
					if done, err := cancelAndWait(); done {
						return err
					}
					return queueErr
				}
				if !ok {
					break
				}
				if done, err := forwardMessage(msg); done {
					return err
				}
			}
			select {
			case <-ch.terminal:
				if ch.terminalErr != nil &&
					!(errors.Is(ch.terminalErr, errExecutionOutcomeUnknown) && !uncertainWithoutTerminal) {
					return ch.terminalErr
				}
				_, err := forwardMessage(ch.terminalMessage)
				return err
			default:
				if !uncertainWithoutTerminal {
					return errGatewayAgentDisconnected
				}
				return fmt.Errorf("%w: agent disconnected before a terminal reply", errExecutionOutcomeUnknown)
			}
		}
	}
}

func requestIDFromNotification(parsed map[string]interface{}) (int64, bool) {
	params, _ := parsed["params"].(map[string]interface{})
	if params == nil {
		return 0, false
	}
	requestID, ok := params["request_id"]
	if !ok {
		return 0, false
	}
	return numericID(requestID)
}
