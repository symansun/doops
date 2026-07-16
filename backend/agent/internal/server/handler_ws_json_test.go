package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPromptJSONReturnsResultFileInStructuredContent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	workspaceCommit := strings.Repeat("a", 40)
	workspace := filepath.Join(root, "json-prompt")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(workspaceCommit+"\n"), 0o600); err != nil {
		t.Fatalf("write workspace commit: %v", err)
	}
	resultPath := filepath.Join(workspace, ".doops", "structured-result.json")
	doagent := newJSONAgentPromptTestServer(t, "```json\n{\"ignored\":true}\n```", func(prompt string, params map[string]interface{}) {
		allowed, ok := params["allowedTools"].([]interface{})
		if !ok || len(allowed) != 0 {
			t.Errorf("plain JSON prompt must disable all tools: %#v", params)
		}
		if _, ok := params["trustedReconciliationContext"]; ok {
			t.Errorf("plain JSON prompt must not carry trusted reconciliation context: %#v", params)
		}
		if !strings.Contains(prompt, resultPath) {
			t.Errorf("structured prompt must declare the result file path: %s", prompt)
		}
		if !strings.Contains(prompt, "exactly subject, observedAt, and toolRef") ||
			!strings.Contains(prompt, "toolRef.input") ||
			!strings.Contains(prompt, "injects toolCall") {
			t.Errorf("structured prompt must define one runtime tool identity: %s", prompt)
		}
		if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
			t.Fatalf("create result directory: %v", err)
		}
		if err := os.WriteFile(resultPath, []byte(`{
			"apiVersion":"doops.sh/v2",
			"kind":"DeploymentRun",
			"spec":{"mode":"dry-run"},
			"status":{
				"phase":"planned",
				"mutationCount":0,
					"evidence":[{
						"subject":"source",
						"toolRef":{
							"tool":"doops-source-observer",
							"input":{"value":9007199254740993}
						},
						"observedAt":"2026-07-15T00:00:00Z"
				}]
			}
		}`), 0o600); err != nil {
			t.Fatalf("write structured result: %v", err)
		}
	})
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":       "json-prompt",
		"instruction":      testCICDInstructionJSON(t),
		"response_format":  "json",
		"workspace_commit": workspaceCommit,
	})
	if result["error"] != nil {
		t.Fatalf("json prompt returned error: %#v", result)
	}
	toolResult, _ := result["result"].(map[string]interface{})
	structured, _ := toolResult["structuredContent"].(map[string]interface{})
	if structured["apiVersion"] != "doops.sh/v2" || structured["kind"] != "DeploymentRun" {
		t.Fatalf("unexpected structured content: %#v", toolResult)
	}
	status, _ := structured["status"].(map[string]interface{})
	if status["phase"] != "planned" || status["mutationCount"] != float64(0) {
		t.Fatalf("unexpected DeploymentRun status: %#v", structured)
	}
	if _, exists := status["runtimeAttestation"]; exists {
		t.Fatalf("Gateway must not add a second attestation layer: %#v", status)
	}
	if _, exists := status["resultDigest"]; exists {
		t.Fatalf("Gateway must not add a self-consistency digest: %#v", status)
	}
	evidence, _ := status["evidence"].([]interface{})
	entry, _ := evidence[0].(map[string]interface{})
	toolCall, _ := entry["toolCall"].(map[string]interface{})
	if toolCall["id"] != "runtime-tool-source" ||
		toolCall["tool"] != "doops-source-observer" {
		t.Fatalf("Gateway must inject the resolved runtime call: %#v", entry)
	}
	runtimeInput, _ := toolCall["input"].(map[string]interface{})
	if _, exists := runtimeInput["value"]; !exists {
		t.Fatalf("Gateway must inject the runtime tool input: %#v", entry)
	}
	runtimeResult, _ := toolCall["result"].(map[string]interface{})
	if runtimeResult["revision"] != "immutable" {
		t.Fatalf("Gateway must inject the exact runtime tool result: %#v", entry)
	}
	if _, exists := entry["toolRef"]; exists {
		t.Fatalf("Gateway must consume the model-visible toolRef: %#v", entry)
	}
}

func TestBindDeploymentRunEvidenceUsesEligibleRuntimeCalls(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{"sessionUpdate": "tool_call_update", "toolCallId": "agent-1", "toolName": "Agent", "status": "completed", "input": map[string]interface{}{"task": "inspect"}, "result": "delegated"},
		{"sessionUpdate": "tool_call_update", "toolCallId": "image-1", "toolName": "image_gen", "status": "completed", "input": map[string]interface{}{"prompt": "diagram"}, "result": map[string]interface{}{"path": "output/diagram.png"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-target", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "target.json"}, "result": map[string]interface{}{"revision": "target"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-source", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "source.json"}, "result": map[string]interface{}{"revision": "source"}},
	})

	result := deploymentRunWithToolRef("Read", map[string]interface{}{"path": "source.json"})
	_, bound, err := bindTestDeploymentRun(result, traces)
	if err != nil {
		t.Fatalf("bind exact runtime evidence: %v", err)
	}
	entry := bound["status"].(map[string]interface{})["evidence"].([]interface{})[0].(map[string]interface{})
	toolCall := entry["toolCall"].(map[string]interface{})
	if toolCall["id"] != "read-source" ||
		toolCall["result"].(map[string]interface{})["revision"] != "source" {
		t.Fatalf("toolRef must resolve by exact input: %#v", entry)
	}
	ambiguous := completedDoagentToolTraces(t, []map[string]interface{}{
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-1", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "same.json"}, "result": "one"},
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-2", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "same.json"}, "result": "two"},
	})
	for _, test := range []struct {
		name   string
		result map[string]interface{}
		traces []doagentToolTrace
		want   string
	}{
		{name: "ambiguous input", result: deploymentRunWithToolRef("Read", map[string]interface{}{"path": "same.json"}), traces: ambiguous, want: "ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := bindTestDeploymentRun(test.result, test.traces)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestBindDeploymentRunEvidenceRejectsModelAuthoredRuntimePayload(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-1", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "state.json"}, "result": map[string]interface{}{"revision": "current"}},
	})
	for _, test := range []struct {
		name   string
		mutate func(status map[string]interface{})
		want   string
	}{
		{
			name: "tool call",
			mutate: func(status map[string]interface{}) {
				status["evidence"].([]interface{})[0].(map[string]interface{})["toolCall"] = map[string]interface{}{}
			},
			want: "must contain only",
		},
		{name: "retired status object", mutate: func(status map[string]interface{}) { status["runtimeAttestation"] = map[string]interface{}{} }, want: "is not supported"},
		{name: "retired status string", mutate: func(status map[string]interface{}) { status["resultDigest"] = "sha256:" + strings.Repeat("a", 64) }, want: "is not supported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := deploymentRunWithToolRef("Read", map[string]interface{}{"path": "state.json"})
			test.mutate(result["status"].(map[string]interface{}))
			_, _, err := bindTestDeploymentRun(result, traces)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("model-authored managed payload must fail: %v", err)
			}
		})
	}
}

func TestDoagentToolTraceCollectorRejectsConflictingDuplicateCallID(t *testing.T) {
	collector := newDoagentToolTraceCollector()
	if err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "duplicate-call",
		"toolName":      "Read",
		"status":        "in_progress",
		"input":         map[string]interface{}{"path": "source.json"},
	}); err != nil {
		t.Fatalf("collect first call: %v", err)
	}
	err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "duplicate-call",
		"toolName":      "Read",
		"status":        "in_progress",
		"input":         map[string]interface{}{"path": "target.json"},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting duplicate toolCallId") {
		t.Fatalf("duplicate runtime IDs must not overwrite an earlier call: %v", err)
	}
}

func TestDeploymentToolBindingPreservesLargeJSONIntegers(t *testing.T) {
	traceInput, err := decodeDoagentJSONObject(`{"value":9007199254740993}`)
	if err != nil {
		t.Fatalf("decode trace input: %v", err)
	}
	differentInput, err := decodeDoagentJSONObject(`{"value":9007199254740992}`)
	if err != nil {
		t.Fatalf("decode different input: %v", err)
	}
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    "read-large-int",
			"toolName":      "Read",
			"status":        "completed",
			"input":         traceInput,
			"result":        map[string]interface{}{"revision": "current"},
		},
	})
	_, _, err = bindTestDeploymentRun(
		deploymentRunWithToolRef("Read", differentInput),
		traces,
	)
	if err == nil || !strings.Contains(err.Error(), "was not observed") {
		t.Fatalf("distinct large integers must not collapse to the same input digest: %v", err)
	}

	traces = completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    "read-large-result",
			"toolName":      "Read",
			"status":        "completed",
			"input":         map[string]interface{}{"path": "state.json"},
			"result":        traceInput,
		},
	})
	_, bound, err := bindTestDeploymentRun(
		deploymentRunWithToolRef("Read", map[string]interface{}{"path": "state.json"}),
		traces,
	)
	if err != nil {
		t.Fatalf("bind large integer result: %v", err)
	}
	toolCall := bound["status"].(map[string]interface{})["evidence"].([]interface{})[0].(map[string]interface{})["toolCall"].(map[string]interface{})
	resultValue := toolCall["result"].(map[string]interface{})["value"]
	if fmt.Sprint(resultValue) != "9007199254740993" {
		t.Fatalf("runtime result integer changed during injection: %v", resultValue)
	}
}

func completedDoagentToolTraces(t *testing.T, updates []map[string]interface{}) []doagentToolTrace {
	t.Helper()
	collector := newDoagentToolTraceCollector()
	for _, update := range updates {
		if err := collector.collect(update); err != nil {
			t.Fatalf("collect completed trace: %v", err)
		}
	}
	return collector.completed()
}

func deploymentRunWithToolRef(tool string, input interface{}) map[string]interface{} {
	return map[string]interface{}{
		"kind": "DeploymentRun",
		"status": map[string]interface{}{
			"mutationCount": 0,
			"evidence": []interface{}{
				map[string]interface{}{
					"subject":    "source",
					"toolRef":    map[string]interface{}{"tool": tool, "input": input},
					"observedAt": "2026-07-15T00:00:00Z",
				},
			},
		},
	}
}

func bindTestDeploymentRun(
	result map[string]interface{},
	traces []doagentToolTrace,
) (string, map[string]interface{}, error) {
	return bindDeploymentRunEvidence(result, traces)
}

func testCICDInstructionJSON(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"task":          "execute-doops-cicd-workflow",
		"skill":         "$doops-cicd",
		"executionMode": "dry-run",
	})
	if err != nil {
		t.Fatalf("encode test doops-cicd instruction: %v", err)
	}
	return string(data)
}

func TestAgentPromptRejectsWorkspaceCommitMismatchBeforeDoagent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	workspace := filepath.Join(root, "workspace-mismatch")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(strings.Repeat("b", 40)+"\n"), 0o600); err != nil {
		t.Fatalf("write workspace commit: %v", err)
	}

	doagentRequests := 0
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doagentRequests++
		t.Fatalf("workspace mismatch must fail before contacting doagent: %s", r.URL.Path)
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":       "workspace-mismatch",
		"instruction":      "return JSON",
		"response_format":  "json",
		"workspace_commit": strings.Repeat("a", 40),
	})
	errObj, _ := result["error"].(map[string]interface{})
	if errObj == nil {
		t.Fatalf("workspace commit mismatch must fail: %#v", result)
	}
	if !strings.Contains(fmt.Sprint(errObj["message"]), "workspace commit mismatch") {
		t.Fatalf("unexpected mismatch error: %#v", errObj)
	}
	if doagentRequests != 0 {
		t.Fatalf("workspace mismatch contacted doagent %d times", doagentRequests)
	}
}

func TestAgentPromptRejectsInstructionWorkspaceCommitMismatchBeforeDoagent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	sessionID := "instruction-workspace-mismatch"
	workspace := filepath.Join(root, sessionID)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workspaceCommit := strings.Repeat("a", 40)
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(workspaceCommit+"\n"), 0o600); err != nil {
		t.Fatalf("write workspace commit: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "deploy"), 0o700); err != nil {
		t.Fatalf("create deployment directory: %v", err)
	}
	writeTrustedAdmissionFixture(t, workspace)
	doagentRequests := 0
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doagentRequests++
		t.Fatalf("instruction workspace mismatch must fail before contacting doagent: %s", r.URL.Path)
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id": sessionID,
		"instruction": `{
			"task":"execute-doops-cicd-workflow",
			"skill":"$doops-cicd",
			"executionMode":"dry-run",
			"runId":"instruction-workspace-mismatch-0123456789abcdef",
			"workflowPath":"deploy/release.yaml",
			"workspaceCommit":"` + strings.Repeat("b", 40) + `",
			"inputs":{"releaseId":"` + strings.Repeat("c", 40) + `"},
			"credentialRun":{"id":"credrun_123","materializations":[]},
			"resultContract":{"apiVersion":"doops.sh/v2","kind":"DeploymentRun","requireEvidence":true}
		}`,
		"response_format":  "json",
		"workspace_commit": workspaceCommit,
	})
	if result["error"] == nil {
		t.Fatalf("instruction workspace mismatch must fail: %#v", result)
	}
	if doagentRequests != 0 {
		t.Fatalf("instruction workspace mismatch contacted doagent %d times", doagentRequests)
	}
}

func TestAgentPromptJSONRejectsMissingResultFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	resultPath := filepath.Join(root, "invalid-json-prompt", ".doops", "structured-result.json")
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
		t.Fatalf("create stale result directory: %v", err)
	}
	if err := os.WriteFile(resultPath, []byte(`{"status":"stale"}`), 0o600); err != nil {
		t.Fatalf("write stale structured result: %v", err)
	}
	doagent := newJSONAgentPromptTestServer(t, `{"status":"converged"}`, nil)
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":      "invalid-json-prompt",
		"instruction":     "return JSON",
		"response_format": "json",
	})
	toolResult, _ := result["result"].(map[string]interface{})
	if toolResult["isError"] != true {
		t.Fatalf("missing structured result file must fail: %#v", result)
	}
	if !strings.Contains(fmt.Sprint(toolResult["content"]), "structured result file") {
		t.Fatalf("expected structured result file error, got %#v", result)
	}
	if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
		t.Fatalf("stale structured result must be removed before prompting, stat error: %v", err)
	}
}

func TestReadDoagentStructuredResultRejectsMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "structured-result.json")
	if err := os.WriteFile(path, []byte("```json\n{\"status\":\"converged\"}\n```"), 0o600); err != nil {
		t.Fatalf("write structured result: %v", err)
	}
	if _, _, err := readDoagentStructuredResult(path); err == nil || !strings.Contains(err.Error(), "one JSON object") {
		t.Fatalf("Markdown-wrapped result file must be rejected, got %v", err)
	}
}

func TestAgentPromptJSONRejectsUnsupportedResponseFormat(t *testing.T) {
	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":      "unsupported-response-format",
		"instruction":     "hello",
		"response_format": "text",
	})
	errObj, _ := result["error"].(map[string]interface{})
	if !strings.Contains(fmt.Sprint(errObj["message"]), "response_format") {
		t.Fatalf("unsupported response format must be rejected: %#v", result)
	}
}

func newJSONAgentPromptTestServer(t *testing.T, finalMessage string, onPrompt func(string, map[string]interface{})) *httptest.Server {
	t.Helper()
	prompted := make(chan struct{})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"result":  map[string]interface{}{},
				})
			case "session/new":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{"sessionId": "doagent-json"},
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
				if params["responseMode"] != "stream" {
					t.Fatalf("prompt must request SSE terminal delivery: %#v", params)
				}
				if onPrompt != nil {
					onPrompt(prompt, params)
				}
				close(prompted)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"status":   "accepted",
						"turnId":   "turn-json",
						"delivery": "sse",
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
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"open_widget","widgetId":"terminal","expand":false}}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"stale-turn","status":"completed","stopReason":"end_turn"}}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","turnId":"turn-json","toolCallId":"runtime-tool-source","toolName":"doops-source-observer","status":"in_progress","input":{"value":9007199254740993}}}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","turnId":"turn-json","toolCallId":"runtime-tool-source","toolName":"doops-source-observer","status":"completed","input":{"value":9007199254740993},"result":{"revision":"immutable"}}}}`)
			fmt.Fprintln(w)
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message\",\"turnId\":\"turn-json\",\"content\":{\"type\":\"text\",\"text\":%q}}}}\n\n", finalMessage)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-json","status":"completed","stopReason":"end_turn"}}}`)
			fmt.Fprintln(w)
		default:
			http.NotFound(w, r)
		}
	}))
}
