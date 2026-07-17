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

func TestAgentPromptJSONReturnsFinalMessageInStructuredContent(t *testing.T) {
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
	finalResult := `{
		"apiVersion":"doops.sh/v2",
		"kind":"DeploymentRun",
		"metadata":{
			"workflow":"deploy/release.yaml",
			"runId":"run-json-prompt",
			"workspaceCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		"spec":{"mode":"dry-run"},
		"status":{
			"phase":"planned",
			"mutationCount":0,
			"capabilities":{"source-identity":"observed"},
			"evidence":[{
				"subject":"source",
				"toolRef":{
					"tool":"doops-source-observer",
					"input":{"value":9007199254740993}
				},
				"observedAt":"2026-07-15T00:00:00Z"
			}]
		}
	}`
	doagent := newJSONAgentPromptTestServer(t, finalResult, func(prompt string, params map[string]interface{}) {
		allowed, ok := params["allowedTools"].([]interface{})
		if !ok || len(allowed) != 0 {
			t.Errorf("plain JSON prompt must disable all tools: %#v", params)
		}
		if _, ok := params["trustedReconciliationContext"]; ok {
			t.Errorf("plain JSON prompt must not carry trusted reconciliation context: %#v", params)
		}
		if !strings.Contains(prompt, "your final response is the machine result") ||
			!strings.Contains(prompt, "exactly one JSON object with no Markdown") {
			t.Errorf("structured prompt must declare the final response contract: %s", prompt)
		}
		if !strings.Contains(prompt, "status may contain only phase, mutationCount, capabilities, evidence, and failures") ||
			!strings.Contains(prompt, "Do not emit plannedMutations") {
			t.Errorf("structured prompt must define DeploymentRun status fields: %s", prompt)
		}
		if !strings.Contains(prompt, "exactly five top-level fields: apiVersion, kind, metadata, spec, and status") ||
			!strings.Contains(prompt, "Never put phase, mutationCount") {
			t.Errorf("structured prompt must forbid the legacy flat DeploymentRun shape: %s", prompt)
		}
		if !strings.Contains(prompt, "allowed trusted DoOps plan tools") ||
			!strings.Contains(prompt, "generic Bash") {
			t.Errorf("structured prompt must restrict DeploymentRun evidence tools: %s", prompt)
		}
		if !strings.Contains(prompt, "evidence must be a non-empty array") ||
			!strings.Contains(prompt, "call at least one allowed trusted DoOps plan tool") {
			t.Errorf("structured prompt must require runtime evidence before the final response: %s", prompt)
		}
		if !strings.Contains(prompt, "exactly subject, observedAt, and toolRef") ||
			!strings.Contains(prompt, "toolRef.input") ||
			!strings.Contains(prompt, "injects toolCall") {
			t.Errorf("structured prompt must define one runtime tool identity: %s", prompt)
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
	if _, exists := runtimeResult["_meta"]; exists {
		t.Fatalf("Gateway must not inject unverified MCP metadata: %#v", entry)
	}
	if _, exists := entry["toolRef"]; exists {
		t.Fatalf("Gateway must consume the model-visible toolRef: %#v", entry)
	}
}

func TestBindDeploymentRunEvidenceUsesEligibleRuntimeCalls(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{"sessionUpdate": "tool_call_update", "toolCallId": "agent-1", "toolName": "Agent", "status": "completed", "input": map[string]interface{}{"task": "inspect"}, "rawOutput": map[string]interface{}{"status": "delegated"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "image-1", "toolName": "image_gen", "status": "completed", "input": map[string]interface{}{"prompt": "diagram"}, "rawOutput": map[string]interface{}{"path": "output/diagram.png"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-target", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "target.json"}, "rawOutput": map[string]interface{}{"revision": "target"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-source", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "source.json"}, "rawOutput": map[string]interface{}{"revision": "source"}},
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
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-1", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "same.json"}, "rawOutput": map[string]interface{}{"value": "one"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-2", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "same.json"}, "rawOutput": map[string]interface{}{"value": "two"}},
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

func TestBindDeploymentRunEvidenceUsesTrustedPlanReferenceName(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    "source-1",
			"toolName":      "mcp_doops_plan_ObserveWorkspaceSource",
			"status":        "completed",
			"input":         map[string]interface{}{},
			"rawOutput":     map[string]interface{}{"revision": "immutable"},
		},
	})

	result := deploymentRunWithToolRef(
		"plan.ObserveWorkspaceSource",
		map[string]interface{}{},
	)
	_, bound, err := bindTestDeploymentRun(result, traces)
	if err != nil {
		t.Fatalf("bind trusted plan reference: %v", err)
	}
	entry := bound["status"].(map[string]interface{})["evidence"].([]interface{})[0].(map[string]interface{})
	toolCall := entry["toolCall"].(map[string]interface{})
	if toolCall["id"] != "source-1" ||
		toolCall["tool"] != "mcp_doops_plan_ObserveWorkspaceSource" {
		t.Fatalf("unexpected trusted runtime call: %#v", entry)
	}
}

func TestBindDeploymentRunEvidenceReplacesModelEvidenceIdentity(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate":   "tool_call_update",
			"toolCallId":      "source-1",
			"toolName":        "mcp_doops_plan_ObserveWorkspaceSource",
			"status":          "completed",
			"input":           map[string]interface{}{},
			"rawOutput":       map[string]interface{}{"revision": "immutable"},
			"evidenceSubject": "source-identity",
		},
	})
	result := deploymentRunWithToolRef("plan.ObserveWorkspaceSource", map[string]interface{}{})
	entry := result["status"].(map[string]interface{})["evidence"].([]interface{})[0].(map[string]interface{})
	entry["subject"] = "workspace://https://example.test/app.git@revision"
	entry["observedAt"] = "model-authored"

	_, bound, err := bindTestDeploymentRun(result, traces)
	if err != nil {
		t.Fatalf("bind authoritative evidence identity: %v", err)
	}
	entry = bound["status"].(map[string]interface{})["evidence"].([]interface{})[0].(map[string]interface{})
	observedAt, _ := entry["observedAt"].(string)
	if entry["subject"] != "source-identity" ||
		observedAt == "model-authored" ||
		!validDoagentObservedAt(observedAt) {
		t.Fatalf("model evidence identity was not replaced: %#v", entry)
	}
}

func TestBindDeploymentRunEvidenceEnforcesPrimarySourceObservation(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate":   "tool_call_update",
			"toolCallId":      "helm-1",
			"toolName":        "mcp_doops_plan_ObserveHelmRelease",
			"status":          "completed",
			"input":           map[string]interface{}{},
			"rawOutput":       map[string]interface{}{"status": "deployed"},
			"evidenceSubject": "helm-release",
		},
	})
	result := deploymentRunWithToolRef("plan.ObserveHelmRelease", map[string]interface{}{})
	_, _, err := bindDeploymentRunEvidence(
		result,
		traces,
		&deploymentRunContractBinding{
			APIVersion:      "doops.sh/v2",
			Workflow:        "deploy/release.yaml",
			RunID:           "run-test",
			WorkspaceCommit: strings.Repeat("a", 40),
			Mode:            "dry-run",
			PrimaryTool:     "mcp_doops_plan_ObserveWorkspaceSource",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "evidence[0] must reference plan.ObserveWorkspaceSource") {
		t.Fatalf("primary source observation was not enforced: %v", err)
	}
}

func TestBindDeploymentRunEvidenceRequiresTargetRegistryObservation(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate":   "tool_call_update",
			"toolCallId":      "source-1",
			"toolName":        "mcp_doops_plan_ObserveWorkspaceSource",
			"status":          "completed",
			"input":           map[string]interface{}{},
			"rawOutput":       map[string]interface{}{"revision": "immutable"},
			"evidenceSubject": "source-identity",
		},
	})
	result := deploymentRunWithToolRef("plan.ObserveWorkspaceSource", map[string]interface{}{})
	_, _, err := bindDeploymentRunEvidence(
		result,
		traces,
		&deploymentRunContractBinding{
			APIVersion:      "doops.sh/v2",
			Workflow:        "deploy/release.yaml",
			RunID:           "run-test",
			WorkspaceCommit: strings.Repeat("a", 40),
			Mode:            "dry-run",
			PrimaryTool:     "mcp_doops_plan_ObserveWorkspaceSource",
			RequiredTools: []string{
				"mcp_doops_plan_ObserveWorkspaceSource",
				"mcp_doops_plan_ObserveTargetRegistryImageSet",
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "plan.ObserveTargetRegistryImageSet") {
		t.Fatalf("target registry observation was not enforced: %v", err)
	}
}

func TestBindDeploymentRunEvidenceBindsTrustedEnvelope(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate":   "tool_call_update",
			"toolCallId":      "source-1",
			"toolName":        "mcp_doops_plan_ObserveWorkspaceSource",
			"status":          "completed",
			"input":           map[string]interface{}{},
			"rawOutput":       map[string]interface{}{"revision": "immutable"},
			"evidenceSubject": "source-identity",
		},
	})
	result := deploymentRunWithToolRef("plan.ObserveWorkspaceSource", map[string]interface{}{})
	result["apiVersion"] = "doops.sh/v3"
	result["metadata"] = map[string]interface{}{
		"workflow":        "deploy/environments/scu/environment.yaml",
		"runId":           "run-verified",
		"workspaceCommit": strings.Repeat("a", 40),
	}

	_, bound, err := bindDeploymentRunEvidence(
		result,
		traces,
		&deploymentRunContractBinding{
			APIVersion:      "doops.sh/v3",
			Workflow:        "deploy/environments/scu/environment.yaml",
			RunID:           "run-verified",
			WorkspaceCommit: strings.Repeat("a", 40),
			Mode:            "dry-run",
			PrimaryTool:     "mcp_doops_plan_ObserveWorkspaceSource",
		},
	)
	if err != nil {
		t.Fatalf("bind trusted DeploymentRun envelope: %v", err)
	}
	if len(bound) != 5 || bound["apiVersion"] != "doops.sh/v3" || bound["kind"] != "DeploymentRun" {
		t.Fatalf("trusted DeploymentRun top level was not rebuilt: %#v", bound)
	}
	metadata := bound["metadata"].(map[string]interface{})
	spec := bound["spec"].(map[string]interface{})
	if metadata["workflow"] != "deploy/environments/scu/environment.yaml" ||
		metadata["runId"] != "run-verified" ||
		metadata["workspaceCommit"] != strings.Repeat("a", 40) ||
		spec["mode"] != "dry-run" {
		t.Fatalf("trusted DeploymentRun metadata/spec mismatch: %#v", bound)
	}

	result = deploymentRunWithToolRef("plan.ObserveWorkspaceSource", map[string]interface{}{})
	result["apiVersion"] = "doops.sh/v3"
	result["metadata"] = map[string]interface{}{
		"workflow":        "deploy/environments/scu/environment.yaml",
		"runId":           "spoofed",
		"workspaceCommit": strings.Repeat("a", 40),
	}
	_, _, err = bindDeploymentRunEvidence(
		result,
		traces,
		&deploymentRunContractBinding{
			APIVersion:      "doops.sh/v3",
			Workflow:        "deploy/environments/scu/environment.yaml",
			RunID:           "run-verified",
			WorkspaceCommit: strings.Repeat("a", 40),
			Mode:            "dry-run",
			PrimaryTool:     "mcp_doops_plan_ObserveWorkspaceSource",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "does not match the trusted reconciliation instruction") {
		t.Fatalf("spoofed trusted DeploymentRun envelope was accepted: %v", err)
	}
}

func TestBindDeploymentRunEvidenceRejectsInvalidTopLevelEnvelope(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    "read-1",
			"toolName":      "Read",
			"status":        "completed",
			"input":         map[string]interface{}{"path": "state.json"},
			"rawOutput":     map[string]interface{}{"revision": "current"},
		},
	})
	for _, test := range []struct {
		name   string
		mutate func(map[string]interface{})
		want   string
	}{
		{
			name: "missing metadata",
			mutate: func(result map[string]interface{}) {
				delete(result, "metadata")
			},
			want: "must contain exactly apiVersion, kind, metadata, spec, and status",
		},
		{
			name: "legacy flat field",
			mutate: func(result map[string]interface{}) {
				result["phase"] = "planned"
			},
			want: "must contain exactly apiVersion, kind, metadata, spec, and status",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := deploymentRunWithToolRef("Read", map[string]interface{}{"path": "state.json"})
			test.mutate(result)
			_, _, err := bindTestDeploymentRun(result, traces)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid DeploymentRun envelope was accepted: %v", err)
			}
		})
	}
}

func TestBindDeploymentRunEvidenceRejectsModelAuthoredRuntimePayload(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{"sessionUpdate": "tool_call_update", "toolCallId": "read-1", "toolName": "Read", "status": "completed", "input": map[string]interface{}{"path": "state.json"}, "rawOutput": map[string]interface{}{"revision": "current"}},
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
		{name: "capabilities array", mutate: func(status map[string]interface{}) {
			status["capabilities"] = []interface{}{"source-identity"}
		}, want: "capabilities must be a non-empty JSON object"},
		{name: "sensitive failure field", mutate: func(status map[string]interface{}) {
			status["failures"] = []interface{}{map[string]interface{}{"githubToken": "token-canary"}}
		}, want: "forbidden sensitive field"},
		{name: "sensitive capability scalar", mutate: func(status map[string]interface{}) {
			status["capabilities"] = []interface{}{"https://storage.example.test/object?X-Amz-Signature=signature-canary"}
		}, want: "forbidden sensitive scalar"},
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

func TestBindDeploymentRunEvidenceRejectsOtherStructuredKinds(t *testing.T) {
	_, _, err := bindTestDeploymentRun(
		map[string]interface{}{"kind": "ArbitraryResult", "status": map[string]interface{}{}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "kind must be DeploymentRun") {
		t.Fatalf("non-DeploymentRun structured result must fail closed: %v", err)
	}
}

func TestBindDeploymentRunEvidenceRemovesRuntimeMetadata(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    "read-1",
			"toolName":      "Read",
			"status":        "completed",
			"input":         map[string]interface{}{"path": "state.json"},
			"rawOutput": map[string]interface{}{
				"revision": "current",
				"_meta":    map[string]interface{}{"model": "test-model"},
			},
		},
	})
	_, bound, err := bindTestDeploymentRun(
		deploymentRunWithToolRef("Read", map[string]interface{}{"path": "state.json"}),
		traces,
	)
	if err != nil {
		t.Fatalf("bind runtime evidence: %v", err)
	}
	entry := bound["status"].(map[string]interface{})["evidence"].([]interface{})[0].(map[string]interface{})
	runtimeResult := entry["toolCall"].(map[string]interface{})["result"].(map[string]interface{})
	if _, exists := runtimeResult["_meta"]; exists {
		t.Fatalf("runtime metadata must not enter DeploymentRun: %#v", runtimeResult)
	}
}

func TestDoagentToolTraceCollectorRejectsSensitiveRuntimeMetadata(t *testing.T) {
	traces := completedDoagentToolTraces(t, []map[string]interface{}{
		{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    "read-1",
			"toolName":      "Read",
			"status":        "completed",
			"input":         map[string]interface{}{"path": "state.json"},
			"rawOutput": map[string]interface{}{
				"revision": "current",
				"_meta":    map[string]interface{}{"token": "token-canary"},
			},
		},
	})
	if len(traces) != 0 {
		t.Fatalf("sensitive runtime metadata must make the tool trace ineligible: %#v", traces)
	}
}

func TestDoagentToolTraceCollectorRejectsSensitiveRuntimeScalars(t *testing.T) {
	for index, value := range []string{
		"ghp_" + strings.Repeat("a", 36),
		"glpat-" + strings.Repeat("a", 20),
		"AKIA" + strings.Repeat("A", 16),
	} {
		traces := completedDoagentToolTraces(t, []map[string]interface{}{
			{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    fmt.Sprintf("read-%d", index),
				"toolName":      "Read",
				"status":        "completed",
				"input":         map[string]interface{}{"path": "state.json"},
				"rawOutput":     map[string]interface{}{"value": value},
			},
		})
		if len(traces) != 0 {
			t.Fatalf("sensitive runtime scalar must make the tool trace ineligible: %#v", traces)
		}
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
			"rawOutput":     map[string]interface{}{"revision": "current"},
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
			"rawOutput":     traceInput,
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
		if rawOutput, ok := update["rawOutput"]; ok {
			encoded, err := stableDoagentJSON(rawOutput)
			if err != nil {
				t.Fatalf("encode raw output: %v", err)
			}
			resultView, err := decodeDoagentJSONValue(encoded)
			if err != nil {
				t.Fatalf("decode raw output: %v", err)
			}
			resultObject, ok := resultView.(map[string]interface{})
			if !ok {
				t.Fatalf("raw output must be an object: %#v", rawOutput)
			}
			delete(resultObject, "_meta")
			resultDigest, err := digestJSON(resultObject)
			if err != nil {
				t.Fatalf("digest raw output: %v", err)
			}
			update["resultDigest"] = resultDigest
		}
		if err := collector.collect(update); err != nil {
			t.Fatalf("collect completed trace: %v", err)
		}
	}
	return collector.completed()
}

func deploymentRunWithToolRef(tool string, input interface{}) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "doops.sh/v2",
		"kind":       "DeploymentRun",
		"metadata": map[string]interface{}{
			"workflow":        "deploy/release.yaml",
			"runId":           "run-test",
			"workspaceCommit": strings.Repeat("a", 40),
		},
		"spec": map[string]interface{}{
			"mode": "dry-run",
		},
		"status": map[string]interface{}{
			"phase":         "planned",
			"mutationCount": 0,
			"capabilities":  map[string]interface{}{"source-identity": "observed"},
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
	return bindDeploymentRunEvidence(result, traces, nil)
}

func TestRejectSensitiveToolResultRejectsSensitiveFieldsAndScalars(t *testing.T) {
	for _, result := range []interface{}{
		map[string]interface{}{"token": "opaque-canary"},
		map[string]interface{}{"url": "https://storage.example.test/object?X-Amz-Signature=signature-canary"},
		map[string]interface{}{"nested": []interface{}{"scheduled Bearer bearer-canary"}},
	} {
		if err := rejectSensitiveToolResult(result); err == nil {
			t.Fatalf("sensitive tool result was accepted: %#v", result)
		}
	}
	if err := rejectSensitiveToolResult(map[string]interface{}{
		"status": "healthy",
		"digest": "sha256:" + strings.Repeat("a", 64),
	}); err != nil {
		t.Fatalf("safe tool result rejected: %v", err)
	}
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
			"credentialRun":{"apiVersion":"doops.sh/v2","kind":"CredentialRun","id":"credrun_123","mode":"dry-run","cluster":"doops","instance":"runner","template":"release","project":"app","environment":"production","materializations":[]},
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

func TestAgentPromptJSONRejectsMissingFinalMachineResult(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	doagent := newJSONAgentPromptTestServer(t, "", nil)
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
		t.Fatalf("missing final machine result must fail: %#v", result)
	}
	if !strings.Contains(fmt.Sprint(toolResult["content"]), "final machine result") {
		t.Fatalf("expected final machine result error, got %#v", result)
	}
}

func TestDoagentStructuredResultCollectorRejectsMarkdown(t *testing.T) {
	collector := newDoagentToolTraceCollector()
	if err := collector.collect(map[string]interface{}{
		"sessionUpdate": "agent_message",
		"content":       map[string]interface{}{"text": "```json\n{\"status\":\"converged\"}\n```"},
	}); err != nil {
		t.Fatalf("collect final message: %v", err)
	}
	if _, _, err := collector.structuredResult(); err == nil || !strings.Contains(err.Error(), "one JSON object") {
		t.Fatalf("Markdown-wrapped final message must be rejected, got %v", err)
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
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"runtime-tool-source","toolName":"doops-source-observer","status":"in_progress","input":{"value":9007199254740993}}}}`)
			fmt.Fprintln(w)
			rawOutput := map[string]interface{}{
				"revision": "immutable",
				"_meta":    map[string]interface{}{"untrusted": "must-not-enter-evidence"},
			}
			resultDigest, err := digestJSON(map[string]interface{}{"revision": "immutable"})
			if err != nil {
				t.Fatalf("digest test raw output: %v", err)
			}
			payload, err := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]interface{}{
					"update": map[string]interface{}{
						"sessionUpdate": "tool_call_update",
						"toolCallId":    "runtime-tool-source",
						"toolName":      "doops-source-observer",
						"status":        "completed",
						"input":         map[string]interface{}{"value": json.Number("9007199254740993")},
						"rawOutput":     rawOutput,
						"resultDigest":  resultDigest,
					},
				},
			})
			if err != nil {
				t.Fatalf("encode test tool update: %v", err)
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			if finalMessage != "" {
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message\",\"content\":{\"type\":\"text\",\"text\":%q}}}}\n\n", finalMessage)
			}
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-json","status":"completed","stopReason":"end_turn"}}}`)
			fmt.Fprintln(w)
		default:
			http.NotFound(w, r)
		}
	}))
}
