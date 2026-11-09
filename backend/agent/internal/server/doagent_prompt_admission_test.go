package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func promptRPCResult(fields map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"jsonrpc": "2.0", "result": fields}
}

func promptRPCError(code int, extra map[string]interface{}) map[string]interface{} {
	errorObject := map[string]interface{}{"code": code, "message": "synthetic"}
	for key, value := range extra {
		errorObject[key] = value
	}
	return map[string]interface{}{"jsonrpc": "2.0", "error": errorObject}
}

func TestClassifyDoagentPromptAdmission(t *testing.T) {
	cases := []struct {
		name      string
		response  map[string]interface{}
		kind      promptAdmissionKind
		turnID    string
		scope     promptQueueScope
		queuePos  int
		globalPos int
	}{
		{name: "accepted names its turn", response: promptRPCResult(map[string]interface{}{"status": "accepted", "turnId": "turn-1"}), kind: promptAdmissionAccepted, turnID: "turn-1"},
		{name: "accepted without turn id", response: promptRPCResult(map[string]interface{}{"status": "accepted"}), kind: promptAdmissionMalformed},
		{name: "queued on the global gate", response: promptRPCResult(map[string]interface{}{"status": "queued", "queuePosition": 1, "globalPosition": 0, "delivery": "sse"}), kind: promptAdmissionQueuedNoTurn, scope: promptQueueGlobal, queuePos: 1, globalPos: 0},
		{name: "queued in the session queue", response: promptRPCResult(map[string]interface{}{"status": "queued", "queuePosition": 2, "delivery": "sse"}), kind: promptAdmissionQueuedNoTurn, scope: promptQueueSession, queuePos: 2},
		{name: "queued already promoted to a turn", response: promptRPCResult(map[string]interface{}{"status": "queued", "turnId": "turn-2"}), kind: promptAdmissionAccepted, turnID: "turn-2"},
		{name: "empty prompt", response: promptRPCResult(map[string]interface{}{"status": "no_prompt"}), kind: promptAdmissionNoPrompt},
		{name: "result is not an object", response: map[string]interface{}{"jsonrpc": "2.0", "result": "queued"}, kind: promptAdmissionMalformed},
		{name: "result is null", response: map[string]interface{}{"jsonrpc": "2.0", "result": nil}, kind: promptAdmissionMalformed},
		{name: "result is absent", response: map[string]interface{}{"jsonrpc": "2.0"}, kind: promptAdmissionMalformed},
		{name: "unknown prompt status", response: promptRPCResult(map[string]interface{}{"status": "running"}), kind: promptAdmissionMalformed},
		{name: "pre-admission session error", response: promptRPCError(-32001, nil), kind: promptAdmissionRejected},
		{name: "pre-admission validation error", response: promptRPCError(-32602, nil), kind: promptAdmissionRejected},
		{name: "internal rpc error", response: promptRPCError(-32000, nil), kind: promptAdmissionTransportFailed},
		{name: "admission timeout", response: promptRPCError(-32000, map[string]interface{}{"data": map[string]interface{}{"failureClass": "admission_timeout"}}), kind: promptAdmissionTransportFailed},
		{name: "error with result", response: map[string]interface{}{"jsonrpc": "2.0", "error": map[string]interface{}{"code": -32602, "message": "synthetic"}, "result": map[string]interface{}{"status": "accepted", "turnId": "turn-3"}}, kind: promptAdmissionTransportFailed},
		{name: "nil response", response: nil, kind: promptAdmissionMalformed},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			admission := classifyDoagentPromptAdmission(testCase.response)
			if admission.kind != testCase.kind {
				t.Fatalf("kind = %s, want %s (detail=%q)", admission.kind, testCase.kind, admission.detail)
			}
			if admission.turnID != testCase.turnID {
				t.Fatalf("turn id = %q, want %q", admission.turnID, testCase.turnID)
			}
			if testCase.kind == promptAdmissionQueuedNoTurn {
				if admission.queueScope != testCase.scope || admission.queuePosition != testCase.queuePos || admission.globalPosition != testCase.globalPos {
					t.Fatalf("queue = %s/%d/%d, want %s/%d/%d",
						admission.queueScope, admission.queuePosition, admission.globalPosition,
						testCase.scope, testCase.queuePos, testCase.globalPos)
				}
			}
		})
	}
}

type promptAdmissionCalls struct {
	mu               sync.Mutex
	rpcCalls         int
	promptCalls      int
	cancelCalls      int
	cancelStatus     string
	lastCancelParams map[string]interface{}
}

func (c *promptAdmissionCalls) lastCancel() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastCancelParams
}

func (c *promptAdmissionCalls) counts() (rpc, prompt, cancel int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rpcCalls, c.promptCalls, c.cancelCalls
}

// newPromptAdmissionDoagent serves the ACP HTTP surface the prompt path needs.
// promptReply decides what session/prompt answers with, so each test can replay
// a real doagent reply shape.
func newPromptAdmissionDoagent(t *testing.T, promptReply func() map[string]interface{}) (*httptest.Server, *promptAdmissionCalls) {
	t.Helper()
	calls := &promptAdmissionCalls{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc":
			var request map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode rpc: %v", err)
				return
			}
			calls.mu.Lock()
			calls.rpcCalls++
			calls.mu.Unlock()
			switch method, _ := request["method"].(string); method {
			case "session/new":
				params, _ := request["params"].(map[string]interface{})
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  map[string]interface{}{"sessionId": params["sessionId"]},
				})
			case "session/setMode", "session/setModel":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": request["id"], "result": map[string]interface{}{}})
			case "session/prompt":
				calls.mu.Lock()
				calls.promptCalls++
				calls.mu.Unlock()
				reply := promptReply()
				reply["jsonrpc"] = "2.0"
				reply["id"] = request["id"]
				_ = json.NewEncoder(w).Encode(reply)
			case "session/cancel":
				params, _ := request["params"].(map[string]interface{})
				calls.mu.Lock()
				calls.cancelCalls++
				calls.lastCancelParams = params
				status := calls.cancelStatus
				calls.mu.Unlock()
				if status == "" {
					status = "cancelling"
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": request["id"], "result": map[string]interface{}{"status": status}})
			default:
				t.Errorf("unexpected rpc method: %v", method)
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	return server, calls
}

func dialPromptAdmissionAgent(t *testing.T, doagentURL string) (*Gateway, *websocket.Conn, func()) {
	t.Helper()
	t.Setenv("DO_AGENT_URL", doagentURL)
	gateway := NewGateway()
	server := newGatewayAuthorizedAgentServer(t, gateway)
	conn := dialAgentTestWS(t, server.URL)
	initializeAgentTestWS(t, conn)
	return gateway, conn, func() {
		conn.Close()
		server.Close()
	}
}

func promptWireError(t *testing.T, result map[string]interface{}) (int, string) {
	t.Helper()
	errorObject, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an error response, got %#v", result)
	}
	code, _ := errorObject["code"].(float64)
	message, _ := errorObject["message"].(string)
	return int(code), message
}

func TestAgentPromptStreamingQueuedSessionIsNotCancelled(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{"status": "queued", "queuePosition": 1, "globalPosition": 0, "delivery": "sse"})
	})
	defer doagent.Close()
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "queued-global",
		"instruction": "deploy the chart",
	})
	code, message := promptWireError(t, result)
	if code != -32603 || message != doagentPromptCleanupUnknown {
		t.Fatalf("wire error = %d/%q, want -32603/%q", code, message, doagentPromptCleanupUnknown)
	}
	_, promptCalls, cancelCalls := calls.counts()
	if promptCalls != 1 || cancelCalls != 0 {
		t.Fatalf("calls: prompt=%d cancel=%d, want 1/0 (a streaming prompt reuses a possibly shared session)", promptCalls, cancelCalls)
	}
}

func TestAgentPromptMachineResultQueuedCleansItsOwnSession(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{"status": "queued", "queuePosition": 1, "globalPosition": 0, "delivery": "sse"})
	})
	defer doagent.Close()
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":      "queued-machine",
		"instruction":     "deploy the chart",
		"response_format": "json",
	})
	code, message := promptWireError(t, result)
	if code != -32603 || message != doagentPromptQueued {
		t.Fatalf("wire error = %d/%q, want -32603/%q", code, message, doagentPromptQueued)
	}
	_, promptCalls, cancelCalls := calls.counts()
	if promptCalls != 1 || cancelCalls != 1 {
		t.Fatalf("calls: prompt=%d cancel=%d, want 1/1 (a minted machine-result session may be cleaned up)", promptCalls, cancelCalls)
	}
}

func TestAgentPromptSessionQueueIsNeverCancelled(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{"status": "queued", "queuePosition": 2, "delivery": "sse"})
	})
	defer doagent.Close()
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "queued-session",
		"instruction": "inspect the target",
	})
	code, message := promptWireError(t, result)
	if code != -32603 || message != doagentPromptCleanupUnknown {
		t.Fatalf("wire error = %d/%q, want -32603/%q", code, message, doagentPromptCleanupUnknown)
	}
	_, promptCalls, cancelCalls := calls.counts()
	if promptCalls != 1 || cancelCalls != 0 {
		t.Fatalf("calls: prompt=%d cancel=%d, want 1/0 (a per-session queue must not be cancelled)", promptCalls, cancelCalls)
	}
}

func TestAgentPromptReusedSessionGlobalQueueIsNotCancelled(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{"status": "queued", "queuePosition": 1, "globalPosition": 0, "delivery": "sse"})
	})
	defer doagent.Close()
	gateway, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	gateway.sessionMapMu.Lock()
	gateway.sessionMap["queued-reused"] = &sessionEntry{doagentSessionID: "queued-reused"}
	gateway.sessionMapMu.Unlock()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "queued-reused",
		"instruction": "inspect the target",
	})
	code, message := promptWireError(t, result)
	if code != -32603 || message != doagentPromptCleanupUnknown {
		t.Fatalf("wire error = %d/%q, want -32603/%q", code, message, doagentPromptCleanupUnknown)
	}
	_, promptCalls, cancelCalls := calls.counts()
	if promptCalls != 1 || cancelCalls != 0 {
		t.Fatalf("calls: prompt=%d cancel=%d, want 1/0 (a reused session must not be cancelled)", promptCalls, cancelCalls)
	}
}

func TestAgentPromptNoPromptReplyIsInputErrorWithoutCancel(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{"status": "no_prompt"})
	})
	defer doagent.Close()
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "no-prompt",
		"instruction": "   ",
	})
	code, message := promptWireError(t, result)
	if code != -32602 || message != doagentPromptNoPrompt {
		t.Fatalf("wire error = %d/%q, want -32602/%q", code, message, doagentPromptNoPrompt)
	}
	_, promptCalls, cancelCalls := calls.counts()
	if promptCalls != 0 || cancelCalls != 0 {
		t.Fatalf("calls: prompt=%d cancel=%d, want 0/0 (empty input never reaches doagent)", promptCalls, cancelCalls)
	}
}

func TestAgentPromptAcceptedWithoutTurnIDStaysUnknown(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{"status": "accepted"})
	})
	defer doagent.Close()
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "accepted-without-turn",
		"instruction": "deploy the chart",
	})
	code, message := promptWireError(t, result)
	if code != -32603 || message != promptOutcomeUnknown("prompt_turn_id_missing") {
		t.Fatalf("wire error = %d/%q, want -32603/%q", code, message, promptOutcomeUnknown("prompt_turn_id_missing"))
	}
	_, promptCalls, cancelCalls := calls.counts()
	if promptCalls != 1 || cancelCalls != 1 {
		t.Fatalf("calls: prompt=%d cancel=%d, want 1/1 (an unaccounted turn still gets the cancel probe)", promptCalls, cancelCalls)
	}
}

func TestAgentPromptEmptyInstructionNeverReachesDoagent(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		t.Fatalf("an empty prompt must not reach doagent")
		return nil
	})
	defer doagent.Close()
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "empty-input",
		"instruction": " ",
	})
	code, message := promptWireError(t, result)
	if code != -32602 || message != doagentPromptNoPrompt {
		t.Fatalf("wire error = %d/%q, want -32602/%q", code, message, doagentPromptNoPrompt)
	}
	rpcCalls, _, _ := calls.counts()
	if rpcCalls != 0 {
		t.Fatalf("rpc calls = %d, want 0", rpcCalls)
	}
}

func TestPromptPreAdmissionCategoriesAreNotMutationUnknown(t *testing.T) {
	applyArgs := json.RawMessage(`{"operation":"apply"}`)
	for _, category := range promptPreAdmissionCategories {
		response := map[string]interface{}{"error": map[string]interface{}{"code": -32603, "message": category}}
		if !promptPreAdmissionReply(response) {
			t.Fatalf("%s must be recognized as a pre-admission answer", category)
		}
		if remoteMutationFailed("doops_agent_prompt", applyArgs, response) {
			t.Fatalf("%s must not be rewritten into a mutation-unknown outcome", category)
		}
	}

	genuineFailure := map[string]interface{}{"error": map[string]interface{}{"code": -32603, "message": "doagent_internal_failure"}}
	if promptPreAdmissionReply(genuineFailure) {
		t.Fatal("an unstructured failure must not be treated as a pre-admission answer")
	}
	if !remoteMutationFailed("doops_agent_prompt", applyArgs, genuineFailure) {
		t.Fatal("an apply prompt failure with an unknown reason must keep the mutation-unknown guardrail")
	}

	withResult := map[string]interface{}{
		"error":  map[string]interface{}{"code": -32603, "message": "doagent_prompt_queued"},
		"result": map[string]interface{}{"isError": true},
	}
	if promptPreAdmissionReply(withResult) {
		t.Fatal("an error envelope with a structured result is not a pre-admission answer")
	}
}

func TestPromptAuditKeepsTheAdmissionCategory(t *testing.T) {
	for _, category := range promptPreAdmissionCategories {
		auditText := relayErrorAuditText("doops_agent_prompt", map[string]interface{}{"code": -32603, "message": category})
		if auditText != category {
			t.Fatalf("audit text = %q, want %q", auditText, category)
		}
		if got := agentPromptAuditCategory(auditText); got != category {
			t.Fatalf("audit category = %q, want %q", got, category)
		}
	}

	envelope := map[string]interface{}{"code": -32603, "message": "synthetic"}
	if got := relayErrorAuditText("doops_shell", envelope); got == "synthetic" {
		t.Fatal("non-prompt tools must keep the formatted envelope text")
	}
}

func TestGatewayRelayForwardsPromptAdmissionCategory(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "prompt-admission-agent")
	client := f.client(t, f.admin)

	lifecycleSend(t, client, 2, "doops_agent_prompt", "prompt-admission-session", map[string]interface{}{
		"operation":   "apply",
		"instruction": "deploy the chart",
	})
	agent.complete(t, agent.call(t, "doops_agent_prompt", "prompt-admission-session"), doagentPromptQueued)
	response := lifecycleResponse(t, client, 2, doagentPromptQueued)
	errorObject, _ := response["error"].(map[string]interface{})
	message, _ := errorObject["message"].(string)
	if message != doagentPromptQueued {
		t.Fatalf("apply relay rewrote the admission category: %q", message)
	}
	if strings.Contains(message, "execution_outcome_unknown") {
		t.Fatalf("apply relay still reports an unknown mutation outcome: %q", message)
	}

	// A genuine apply failure with an unknown reason must keep the guardrail.
	lifecycleSend(t, client, 3, "doops_agent_prompt", "prompt-genuine-failure", map[string]interface{}{
		"operation":   "apply",
		"instruction": "deploy the chart",
	})
	agent.complete(t, agent.call(t, "doops_agent_prompt", "prompt-genuine-failure"), "doagent_internal_failure")
	unknown := lifecycleResponse(t, client, 3, "execution_outcome_unknown")
	unknownError, _ := unknown["error"].(map[string]interface{})
	unknownMessage, _ := unknownError["message"].(string)
	if !strings.Contains(unknownMessage, "execution_outcome_unknown: remote error: doagent_internal_failure") {
		t.Fatalf("an apply failure with an unknown reason lost the mutation-unknown guardrail: %q", unknownMessage)
	}
}

func TestClassifyDoagentPromptAdmissionCapturesQueueToken(t *testing.T) {
	admission := classifyDoagentPromptAdmission(promptRPCResult(map[string]interface{}{
		"status": "queued", "queuePosition": 2, "queueRequestId": "qreq_synthetic_1",
	}))
	if admission.kind != promptAdmissionQueuedNoTurn {
		t.Fatalf("kind = %s, want %s", admission.kind, promptAdmissionQueuedNoTurn)
	}
	if admission.queueRequestID != "qreq_synthetic_1" {
		t.Fatalf("queue request id = %q, want %q", admission.queueRequestID, "qreq_synthetic_1")
	}
	if embedded := classifyDoagentPromptAdmission(promptRPCResult(map[string]interface{}{"status": "queued"})); embedded.queueRequestID != "" {
		t.Fatalf("a token-free queued reply must stay token-free, got %q", embedded.queueRequestID)
	}
}

func TestAgentPromptQueuedTokenIsCancelledByRequest(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{
			"status": "queued", "queuePosition": 1, "queueRequestId": "qreq_synthetic_2", "delivery": "sse",
		})
	})
	defer doagent.Close()
	calls.cancelStatus = "queued_cancelled"
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "queued-token",
		"instruction": "deploy the chart",
	})
	code, message := promptWireError(t, result)
	if code != -32603 || message != doagentPromptQueued {
		t.Fatalf("wire error = %d/%q, want -32603/%q", code, message, doagentPromptQueued)
	}
	_, promptCalls, cancelCalls := calls.counts()
	if promptCalls != 1 || cancelCalls != 1 {
		t.Fatalf("calls: prompt=%d cancel=%d, want 1/1", promptCalls, cancelCalls)
	}
	params := calls.lastCancel()
	if params["queueRequestId"] != "qreq_synthetic_2" || params["sessionId"] != "queued-token" {
		t.Fatalf("targeted cancel params = %#v, want the session and the reply's queue token", params)
	}
}

func TestAgentPromptQueuedTokenNotFoundStaysCleanupUnknown(t *testing.T) {
	doagent, calls := newPromptAdmissionDoagent(t, func() map[string]interface{} {
		return promptRPCResult(map[string]interface{}{
			"status": "queued", "queuePosition": 1, "queueRequestId": "qreq_synthetic_3", "delivery": "sse",
		})
	})
	defer doagent.Close()
	calls.cancelStatus = "queued_not_found"
	_, conn, cleanup := dialPromptAdmissionAgent(t, doagent.URL)
	defer cleanup()

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "queued-token-missing",
		"instruction": "deploy the chart",
	})
	code, message := promptWireError(t, result)
	if code != -32603 || message != doagentPromptCleanupUnknown {
		t.Fatalf("wire error = %d/%q, want -32603/%q", code, message, doagentPromptCleanupUnknown)
	}
	_, _, cancelCalls := calls.counts()
	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1 targeted attempt", cancelCalls)
	}
}
