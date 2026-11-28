package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentPromptNotificationIncludesAssistantDeltaKind(t *testing.T) {
	prompted := make(chan struct{})
	receivedPrompt := make(chan string, 1)
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc":
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode rpc: %v", err)
			}
			switch req["method"] {
			case "initialize":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  doagentAdmissionCapabilities(),
				})
			case "session/new":
				params, _ := req["params"].(map[string]interface{})
				if params["permissionMode"] != "auto" {
					t.Fatalf("doagent sessions must use native auto mode with DoOps tool boundaries: %#v", params)
				}
				if strings.Contains(fmt.Sprint(params["systemPrompt"]), "Current DoOps session_id") {
					t.Fatalf("runtime session leaked into model prompt: %#v", params)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{"sessionId": "doagent-kind"},
				})
			case "session/setMode":
				params, _ := req["params"].(map[string]interface{})
				if params["modeId"] != "auto" {
					t.Fatalf("generic prompt must use native auto mode: %#v", req)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{},
				})
			case "session/prompt":
				params, _ := req["params"].(map[string]interface{})
				prompt, _ := params["prompt"].(string)
				allowed, _ := params["allowedTools"].([]interface{})
				if strings.Join(interfaceStringValues(allowed), ",") != "Read,glob,grep,Skill,Bash,mcp_doops_k8s_read,mcp_doops_web_read,mcp_doops_kb_read" {
					t.Fatalf("ordinary prompt must expose the Auto diagnostic tool surface: %#v", params)
				}
				if params["responseMode"] != "stream" {
					t.Fatalf("prompt must request SSE terminal delivery: %#v", params)
				}
				receivedPrompt <- prompt
				close(prompted)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"status":   "accepted",
						"delivery": "sse",
						"turnId":   "turn-kind",
					},
				})
			default:
				t.Fatalf("unexpected rpc method: %v", req["method"])
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-prompted
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","turnId":"turn-kind","content":{"type":"text","text":"hello"}}}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","turnId":"turn-kind","content":{"type":"text","text":"done"}}}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-kind","status":"completed","stopReason":"end_turn"}}}`)
			fmt.Fprintln(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	rawArgs, _ := json.Marshal(map[string]interface{}{
		"session_id":  "kind-prompt",
		"instruction": "hello",
	})
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_agent_prompt",
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("call prompt: %v", err)
	}

	msg := readNotification(t, conn)
	params, _ := msg["params"].(map[string]interface{})
	if params["kind"] != "assistant_delta" {
		t.Fatalf("expected assistant_delta notification kind, got %#v", params)
	}
	if params["data"] != "hello" {
		t.Fatalf("expected first assistant chunk, got %#v", params["data"])
	}

	for {
		var result map[string]interface{}
		if err := conn.ReadJSON(&result); err != nil {
			t.Fatalf("read prompt result: %v", err)
		}
		if _, ok := result["method"]; ok {
			continue
		}
		toolResult, _ := result["result"].(map[string]interface{})
		content, _ := toolResult["content"].([]interface{})
		if len(content) != 1 {
			t.Fatalf("expected text-only prompt result, got %#v", result)
		}
		if _, ok := toolResult["structuredContent"]; ok {
			t.Fatalf("doops_agent_prompt must not return structured content: %#v", toolResult)
		}
		break
	}

	if prompt := <-receivedPrompt; strings.Contains(prompt, "deploy.sh") {
		t.Fatalf("agent prompt must not inject a deploy.sh generation contract: %s", prompt)
	}
}

func TestAgentPromptRefreshesPermissionModeForReusedSession(t *testing.T) {
	var mu sync.Mutex
	permissionRefreshes := 0
	promptCount := 0
	prompted := make(chan string, 2)
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc":
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode rpc: %v", err)
				return
			}
			id := req["id"]
			switch req["method"] {
			case "initialize":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0", "id": id,
					"result": doagentAdmissionCapabilities(),
				})
			case "session/new":
				params, _ := req["params"].(map[string]interface{})
				if params["permissionMode"] != "auto" {
					t.Fatalf("DoOps must always establish auto permission mode: %#v", params)
				}
				if _, hasCwd := params["cwd"]; !hasCwd {
					mu.Lock()
					permissionRefreshes++
					mu.Unlock()
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]interface{}{"sessionId": "reused-agent-session"},
				})
			case "session/setMode":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0", "id": id, "result": map[string]interface{}{},
				})
			case "session/prompt":
				mu.Lock()
				promptCount++
				turnID := fmt.Sprintf("reused-turn-%d", promptCount)
				mu.Unlock()
				prompted <- turnID
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]interface{}{"status": "accepted", "turnId": turnID},
				})
			default:
				t.Errorf("unexpected rpc method: %v", req["method"])
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			turnID := <-prompted
			fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":%q,"status":"completed","stopReason":"end_turn"}}}`, turnID))
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	args := map[string]interface{}{"session_id": "reused-permission", "instruction": "inspect"}
	for range 2 {
		result := callToolResult(t, conn, "doops_agent_prompt", args)
		if _, ok := result["error"]; ok {
			t.Fatalf("reused prompt failed: %#v", result)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if permissionRefreshes != 1 {
		t.Fatalf("expected exactly one permission refresh for the reused session, got %d", permissionRefreshes)
	}
}

// The prompt tells the model which tools to call. Every mcp_* tool it names must be
// one the Agent actually offers in some mode, or the model is sent to a tool that
// does not exist.
func TestAgentSystemPromptNamesOnlyOfferedTools(t *testing.T) {
	prompt, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "skills", "agent", "system_prompt.md"))
	if err != nil {
		t.Fatalf("read system prompt: %v", err)
	}
	offered := map[string]bool{}
	for _, apply := range []bool{false, true} {
		for _, skill := range []string{"", "dosql", "doweb"} {
			for _, tool := range doagentAllowedTools(apply, skill, true) {
				offered[tool] = true
			}
		}
	}
	named := regexp.MustCompile(`mcp_[a-z0-9_]+`).FindAllString(string(prompt), -1)
	if len(named) == 0 {
		t.Fatal("system prompt names no tools")
	}
	for _, tool := range named {
		if !offered[tool] {
			t.Errorf("system prompt names %s, which the Agent never offers", tool)
		}
	}
}

func TestAgentPromptReturnsAdmissionErrorImmediately(t *testing.T) {
	prompted := make(chan struct{})
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc":
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode rpc: %v", err)
				return
			}
			switch req["method"] {
			case "initialize":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0", "id": req["id"], "result": doagentAdmissionCapabilities(),
				})
			case "session/new":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{"sessionId": "admission-error"},
				})
			case "session/setMode":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{},
				})
			case "session/prompt":
				close(prompted)
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"error":   map[string]interface{}{"code": -32003, "message": "queue full"},
				})
			default:
				t.Errorf("unexpected rpc method: %v", req["method"])
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-prompted
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	started := time.Now()
	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "admission-error",
		"instruction": "inspect",
	})
	if !strings.Contains(fmt.Sprint(result), "doagent_prompt_rejected") ||
		strings.Contains(fmt.Sprint(result), "queue full") {
		t.Fatalf("prompt admission error must return only a stable category: %#v", result)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("prompt admission error must not wait for SSE timeout, took %s", elapsed)
	}
}

func TestAgentApplyPreservesVerifiedHelmWhenTurnIsIncomplete(t *testing.T) {
	for _, terminal := range []struct {
		status, stopReason, failureClass string
	}{
		{"failed", "error", "agent_loop_error"},
		{"interrupted", "budget_exceeded", "budget_exceeded"},
		{"interrupted", "context_limit", "context_limit"},
		{"interrupted", "diminishing_returns", "diminishing_returns"},
		{"failed", "goal_evaluation_failed", "goal_evaluation_failed"},
		{"cancelled", "cancelled", ""},
		{"needs_input", "needs_input", ""},
	} {
		t.Run(terminal.stopReason, func(t *testing.T) {
			testAgentApplyTerminalHelmResult(t, false, false, map[string]interface{}{
				"status": terminal.status, "stopReason": terminal.stopReason,
				"failureClass":  terminal.failureClass,
				"failureReason": `{"password":"` + strings.Repeat("synthetic-turn-json-secret", 100) + `"}`,
				"error":         "token: " + strings.Repeat("synthetic-turn-yaml-secret", 100),
			})
		})
	}
}

func TestAgentApplySucceedsWhenTurnAndHelmComplete(t *testing.T) {
	testAgentApplyTerminalHelmResult(t, false, false, map[string]interface{}{
		"status": "completed", "stopReason": "end_turn",
	})
}

func TestAgentApplyKnownHelmFailureDoesNotBecomeUnknown(t *testing.T) {
	testAgentApplyTerminalHelmResult(t, true, false, nil)
}

func TestAgentApplyEmptyHelmArgumentsThenScopeFailureIsTerminal(t *testing.T) {
	testAgentApplyTerminalHelmResult(t, true, true, nil)
}

func testAgentApplyTerminalHelmResult(t *testing.T, toolFailed, invalidArgumentsFirst bool, terminal map[string]interface{}) {
	t.Helper()
	workspace := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", workspace)
	const (
		doopsSessionID = "apply-final-turn-error"
		agentSessionID = "apply-final-turn-error-agent"
		turnID         = "turn-apply-final-turn-error"
	)
	secret := strings.Repeat("b", 64)
	previousSecret := processMutationAuthoritySecret
	processMutationAuthoritySecret = secret
	t.Cleanup(func() {
		processMutationAuthoritySecret = previousSecret
	})

	promptParamsReady := make(chan map[string]interface{}, 1)
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc":
			var request map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode rpc: %v", err)
				return
			}
			id := request["id"]
			switch request["method"] {
			case "initialize":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0", "id": id, "result": doagentAdmissionCapabilities(),
				})
			case "session/new":
				params, ok := request["params"].(map[string]interface{})
				if !ok {
					t.Fatalf("session/new params = %#v", request["params"])
				}
				if params["cwd"] != "/root/ws/"+doopsSessionID {
					t.Fatalf("apply session must use its bound workspace cwd, got %#v", params["cwd"])
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      id,
					"result":  map[string]interface{}{"sessionId": agentSessionID},
				})
			case "session/setMode":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      id,
					"result":  map[string]interface{}{},
				})
			case "session/prompt":
				params, ok := request["params"].(map[string]interface{})
				if !ok {
					t.Errorf("prompt params = %#v", request["params"])
					return
				}
				promptParamsReady <- params
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]interface{}{
						"status": "accepted",
						"turnId": turnID,
					},
				})
			default:
				t.Errorf("unexpected rpc method: %v", request["method"])
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("SSE response does not support flushing")
			}
			flusher.Flush()
			promptParams := <-promptParamsReady
			authorityID, _ := promptParams["mutationAuthorizationId"].(string)
			issuedAt, _ := promptParams["mutationAuthorizationIssuedAt"].(float64)
			contextDigest, _ := promptParams["mutationContextDigest"].(string)
			_, authorityDigest, err := signMutationAuthorization(
				secret,
				agentSessionID,
				authorityID,
				int64(issuedAt),
				doagentAllowedTools(true, "", true),
				contextDigest,
			)
			if err != nil {
				t.Errorf("sign mutation authority: %v", err)
				return
			}
			input := map[string]interface{}{
				"operation": "upgrade-install",
				"release":   "doops-agent",
				"chart":     "deploy/helm/doops-agent",
				"namespace": "kz-ops",
			}
			trace := attestedMutationTraceForSession(
				t,
				"mcp_doops_helm",
				input,
				doopsSessionID,
				authorityDigest,
			)
			toolStatus, turnStatus := "completed", "failed"
			stopReason, turnError := "error", "final model response failed after Helm completed"
			if toolFailed {
				toolStatus = "failed"
				failure := "helm_verification_failed"
				if invalidArgumentsFirst {
					failure = "helm_mutation_failed"
				}
				failedResult := map[string]interface{}{
					"isError": true,
					"content": []interface{}{map[string]interface{}{"type": "text", "text": failure}},
				}
				trace.resultJSON, _ = json.Marshal(failedResult)
				trace.AttestedResultDigest, _ = digestRuntimeAttestationJSON(failedResult)
			}
			var rawOutput interface{}
			if err := json.Unmarshal(trace.resultJSON, &rawOutput); err != nil {
				t.Fatalf("decode raw output: %v", err)
			}
			rawInput := map[string]interface{}{
				"toolName": trace.ToolName,
				"args":     input,
			}
			writeEvent := func(update map[string]interface{}) {
				data, err := json.Marshal(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params":  map[string]interface{}{"update": update},
				})
				if err != nil {
					t.Fatalf("encode SSE event: %v", err)
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			toolUpdate := map[string]interface{}{
				"sessionUpdate":            "tool_call_update",
				"turnId":                   turnID,
				"toolCallId":               trace.ToolCallID,
				"toolName":                 trace.ToolName,
				"status":                   toolStatus,
				"rawInput":                 rawInput,
				"rawOutput":                rawOutput,
				"attestationSchemaVersion": trace.AttestationSchema,
				"executionMode":            trace.ExecutionMode,
				"mutationAuthorized":       true,
				"mutationAuthorityDigest":  trace.MutationAuthorityDigest,
				"attestedTool":             trace.AttestedTool,
				"evidenceKind":             trace.EvidenceKind,
				"evidenceSubject":          trace.EvidenceSubject,
				"canonicalScope":           trace.CanonicalScope,
				"scopeDigest":              trace.ScopeDigest,
				"inputDigest":              trace.InputDigest,
				"resultDigest":             trace.AttestedResultDigest,
			}
			if invalidArgumentsFirst {
				rejected := callK8SMCPTool(t.Context(), k8sMCPRequest{
					ID: 10, Params: json.RawMessage(`{"name":"helm","arguments":{}}`),
				}, doopsSessionID)
				if rejected.Error != nil {
					t.Errorf("Helm argument rejection must be attestable: %#v", rejected.Error)
					return
				}
				emptyInput := map[string]interface{}{}
				rejectedTrace := attestedMutationTraceForSession(t, "mcp_doops_helm", emptyInput, doopsSessionID, authorityDigest)
				rejectedUpdate := make(map[string]interface{}, len(toolUpdate))
				for key, value := range toolUpdate {
					rejectedUpdate[key] = value
				}
				rejectedUpdate["toolCallId"] = "invalid-arguments"
				rejectedUpdate["rawInput"] = map[string]interface{}{"toolName": trace.ToolName, "args": emptyInput}
				rejectedUpdate["inputDigest"] = rejectedTrace.InputDigest
				rejectedUpdate["rawOutput"] = rejected.Result
				rejectedUpdate["resultDigest"], err = digestRuntimeAttestationJSON(rejected.Result)
				if err != nil {
					t.Errorf("digest rejected Helm result: %v", err)
					return
				}
				writeEvent(rejectedUpdate)
			}
			writeEvent(map[string]interface{}{
				"sessionUpdate": "tool_call",
				"turnId":        turnID,
				"toolCallId":    trace.ToolCallID,
				"toolName":      trace.ToolName,
				"status":        "in_progress",
				"rawInput":      rawInput,
			})
			writeEvent(toolUpdate)
			turnUpdate := map[string]interface{}{
				"sessionUpdate": "turn_finished",
				"turnId":        turnID,
				"status":        turnStatus,
				"stopReason":    stopReason,
				"error":         turnError,
			}
			if terminal != nil {
				delete(turnUpdate, "error")
				for key, value := range terminal {
					turnUpdate[key] = value
				}
			}
			writeEvent(turnUpdate)
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":      doopsSessionID,
		"instruction":     "upgrade doops-agent and a second release, then verify both",
		"operation":       "apply",
		"response_format": "json",
	})
	if strings.Contains(fmt.Sprint(result), "synthetic-turn-json-secret") ||
		strings.Contains(fmt.Sprint(result), "synthetic-turn-yaml-secret") {
		t.Fatalf("turn error leaked credential-like text: %#v", result)
	}
	if result["error"] != nil {
		t.Fatalf("verified Helm mutation was returned as RPC error: %#v", result)
	}
	response, ok := result["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing successful tool result: %#v", result)
	}
	if toolFailed {
		if response["isError"] != true || !strings.Contains(fmt.Sprint(response), "doagent_apply_tool_failed") {
			t.Fatalf("known Helm failure must remain a failed terminal result: %#v", response)
		}
		if strings.Contains(fmt.Sprint(result), "execution_outcome_unknown") {
			t.Fatalf("known Helm failure quarantined the target: %#v", result)
		}
		structured, _ := response["structuredContent"].(map[string]interface{})
		if structured["verified"] != true || structured["outcome"] != "failed" {
			t.Fatalf("known failure lost the verified receipt: %#v", response)
		}
		followup := callToolResult(t, conn, "doops_shell", map[string]interface{}{
			"session_id": doopsSessionID, "command": "printf followup-ok",
		})
		if followup["error"] != nil {
			t.Fatalf("follow-up command failed after terminal tool failure: %#v", followup)
		}
		return
	}
	if terminal != nil && terminal["status"] != "completed" {
		if response["isError"] != true || !strings.Contains(fmt.Sprint(response), "doagent_apply_incomplete") {
			t.Fatalf("partially completed request was reported as successful: %#v", response)
		}
		structured, _ := response["structuredContent"].(map[string]interface{})
		if structured["verified"] != true || structured["outcome"] != "failed" {
			t.Fatalf("incomplete turn must keep verified receipts without claiming success: %#v", structured)
		}
		turn, _ := structured["turn"].(map[string]interface{})
		if turn["status"] != terminal["status"] || turn["stopReason"] != terminal["stopReason"] {
			t.Fatalf("incomplete turn lost its terminal reason: %#v", turn)
		}
		if terminal["failureClass"] != "" && turn["failureClass"] != terminal["failureClass"] {
			t.Fatalf("incomplete turn lost its failure class: %#v", turn)
		}
		mutations, _ := structured["mutations"].([]interface{})
		if len(mutations) != 1 {
			t.Fatalf("completed mutation missing: %#v", mutations)
		}
		mutation, _ := mutations[0].(map[string]interface{})
		receipt, _ := mutation["result"].(map[string]interface{})
		if mutation["verified"] != true || mutation["status"] != "completed" || receipt["isError"] != false {
			t.Fatalf("successful Helm mutation was rewritten as failed: %#v", mutation)
		}
		if strings.Contains(fmt.Sprint(result), "execution_outcome_unknown") {
			t.Fatalf("known incomplete turn was misclassified as an unknown mutation: %#v", result)
		}
		return
	}
	if response["isError"] == true {
		t.Fatalf("verified Helm mutation was returned as tool error: %#v", response)
	}
	content, _ := response["content"].([]interface{})
	if len(content) == 0 || !strings.Contains(fmt.Sprint(content[0]), "Operation complete") {
		if !strings.Contains(fmt.Sprint(response["structuredContent"]), "doops.verified-mutation") {
			t.Fatalf("unexpected successful tool content: %#v", response)
		}
	}
	structured, _ := response["structuredContent"].(map[string]interface{})
	if structured["kind"] != "doops.verified-mutation" || structured["verified"] != true {
		t.Fatalf("verified mutation structured result missing: %#v", response)
	}
}
