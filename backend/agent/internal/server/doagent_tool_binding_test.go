package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestApplyRejectsRawKubectlMutationEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	if err := collector.requireCompletedToolEvidence(); err == nil ||
		err.Error() != "doagent_apply_tool_evidence_missing" {
		t.Fatalf("missing tool evidence error = %v", err)
	}

	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "shell",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"command":"kubectl patch deployment doops-agent"}`),
		resultJSON: json.RawMessage(`{"exitCode":0}`),
	}
	if err := collector.requireCompletedToolEvidence(); err == nil ||
		!strings.Contains(err.Error(), "doagent_apply_forbidden_mutation: shell") {
		t.Fatalf("raw kubectl mutation evidence accepted: %v", err)
	}
}

func TestApplyRejectsAllShellMutationToolAliases(t *testing.T) {
	for _, tool := range []string{"shell", "doops_shell", "mcp_doops_shell", "Bash", "bash"} {
		t.Run(tool, func(t *testing.T) {
			trace := doagentToolTrace{
				ToolName:  tool,
				inputJSON: json.RawMessage(`{"command":"kubectl rollout restart deployment/app"}`),
			}
			if !trace.usesRawClusterMutation() {
				t.Fatalf("raw mutation was not detected for %s", tool)
			}
		})
	}
}

func TestApplyRejectsRemovedStructuredK8sMutationEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	bindApplyCollector(collector, "apply-test")
	collector.calls["call-1"] = attestedMutationTrace(t, "mcp_doops_k8s", map[string]interface{}{
		"session_id": "apply-test",
		"operation":  "set-image",
		"resource":   "deployment/doops-agent",
		"container":  "doops-agent",
		"image":      "repo/doops-agent@sha256:" + strings.Repeat("a", 64),
		"confirm":    true,
	})
	if err := collector.requireCompletedToolEvidence(); err == nil ||
		!strings.Contains(err.Error(), "mcp_doops_k8s=structured_mutation_tool_required") {
		t.Fatalf("removed structured k8s evidence accepted: %v", err)
	}
}

func TestApplyAcceptsStructuredHelmMutationEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	bindApplyCollector(collector, "apply-test")
	collector.calls["call-1"] = attestedMutationTrace(t, "mcp_doops_helm", map[string]interface{}{
		"session_id": "apply-test",
		"operation":  "upgrade-install",
		"release":    "doops-agent",
		"chart":      "deploy/helm/doops-agent",
		"namespace":  "kz-ops",
		"confirm":    true,
	})
	if err := collector.requireCompletedToolEvidence(); err != nil {
		t.Fatalf("structured helm evidence rejected: %v", err)
	}
}

func TestApplyRejectsUnrelatedCompletedToolEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "Read",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"path":"README.md"}`),
		resultJSON: json.RawMessage(`{"content":"ok"}`),
	}
	err := collector.requireCompletedToolEvidence()
	if err == nil || !strings.Contains(err.Error(), "Read=structured_mutation_tool_required") {
		t.Fatalf("unrelated tool evidence accepted: %v", err)
	}
}

func TestApplyRejectsReadOnlyK8sEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "mcp_doops_k8s_read",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"operation":"get","kind":"pods"}`),
		resultJSON: json.RawMessage(`{"content":[{"type":"text","text":"pods"}]}`),
	}
	err := collector.requireCompletedToolEvidence()
	if err == nil || !strings.Contains(err.Error(), "mcp_doops_k8s_read=structured_mutation_tool_required") {
		t.Fatalf("read-only k8s evidence accepted: %v", err)
	}
}

func TestApplyRejectsForbiddenMutationEvenWithStructuredEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	bindApplyCollector(collector, "apply-test")
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"command":"kubectl patch deployment app --patch '{}'"}`),
		resultJSON: json.RawMessage(`{"exitCode":0}`),
	}
	collector.calls["call-2"] = attestedMutationTrace(t, "mcp_doops_helm", map[string]interface{}{
		"session_id": "apply-test",
		"operation":  "upgrade-install",
		"release":    "app",
		"chart":      "deploy/helm/app",
		"namespace":  "apps",
		"confirm":    true,
	})
	err := collector.requireCompletedToolEvidence()
	if err == nil || !strings.Contains(err.Error(), "doagent_apply_forbidden_mutation: Bash") {
		t.Fatalf("forbidden mutation was masked by structured evidence: %v", err)
	}
}

func TestApplyRejectsShellHelmMutationEvenWithStructuredEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	bindApplyCollector(collector, "apply-test")
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "shell",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"command":"helm --namespace kz-ops upgrade --install app deploy/helm/app"}`),
		resultJSON: json.RawMessage(`{"exitCode":0}`),
	}
	collector.calls["call-2"] = attestedMutationTrace(t, "mcp_doops_helm", map[string]interface{}{
		"session_id": "apply-test",
		"operation":  "upgrade-install",
		"release":    "app",
		"chart":      "deploy/helm/app",
		"namespace":  "kz-ops",
		"confirm":    true,
	})
	err := collector.requireCompletedToolEvidence()
	if err == nil || !strings.Contains(err.Error(), "doagent_apply_forbidden_mutation: shell") {
		t.Fatalf("shell helm mutation was masked by structured evidence: %v", err)
	}
}

func TestApplyRejectsExecuteCodeKubectlMutation(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "ExecuteCode",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"language":"shell","code":"kubectl rollout restart deployment/app"}`),
		resultJSON: json.RawMessage(`{"exitCode":0}`),
	}
	err := collector.requireCompletedToolEvidence()
	if err == nil || !strings.Contains(err.Error(), "doagent_apply_forbidden_mutation: ExecuteCode") {
		t.Fatalf("ExecuteCode kubectl mutation accepted: %v", err)
	}
}

func TestApplyRejectsForgedOrMismatchedRuntimeAttestation(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(*doagentToolTrace)
		collectorBound string
		reason         string
	}{
		{
			name: "missing attestation",
			mutate: func(trace *doagentToolTrace) {
				trace.AttestationSchema = ""
			},
			collectorBound: "apply-test",
			reason:         "attestation_schema_invalid",
		},
		{
			name: "forged input digest",
			mutate: func(trace *doagentToolTrace) {
				trace.InputDigest = "sha256:" + strings.Repeat("0", 64)
			},
			collectorBound: "apply-test",
			reason:         "input_digest_mismatch",
		},
		{
			name:           "wrong session",
			mutate:         func(*doagentToolTrace) {},
			collectorBound: "other",
			reason:         "session_binding_mismatch",
		},
		{
			name: "missing confirmation",
			mutate: func(trace *doagentToolTrace) {
				var input map[string]interface{}
				if err := json.Unmarshal(trace.inputJSON, &input); err != nil {
					t.Fatal(err)
				}
				input["confirm"] = false
				trace.inputJSON, _ = json.Marshal(input)
				realTool := strings.TrimPrefix(modelVisibleDoagentToolName(trace.ToolName), "doops_")
				trace.InputDigest, _ = digestRuntimeAttestationJSON(map[string]interface{}{
					"arguments": input,
					"server":    "doops",
					"tool":      realTool,
				})
			},
			collectorBound: "apply-test",
			reason:         "mutation_confirmation_missing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			collector := newDoagentToolTraceCollector(false)
			bindApplyCollector(collector, test.collectorBound)
			trace := attestedMutationTrace(t, "mcp_doops_helm", map[string]interface{}{
				"session_id": "apply-test",
				"operation":  "rollback",
				"release":    "app",
				"namespace":  "apps",
				"revision":   2,
				"confirm":    true,
			})
			test.mutate(&trace)
			collector.calls["call-1"] = trace
			err := collector.requireCompletedToolEvidence()
			if err == nil || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("error = %v, want %s", err, test.reason)
			}
		})
	}
}

func TestApplyRejectsCompletedACPToolPublicContentWithoutRawOutput(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	if err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call-1",
		"toolName":      "mcp_doops_helm",
		"status":        "in_progress",
		"input": map[string]interface{}{
			"operation":  "upgrade-install",
			"release":    "doops-agent",
			"chart":      "deploy/helm/doops-agent",
			"namespace":  "kz-ops",
			"session_id": "apply-test",
			"confirm":    true,
		},
	}); err != nil {
		t.Fatalf("collect in-progress tool update: %v", err)
	}
	if err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call-1",
		"toolName":      "mcp_doops_helm",
		"status":        "completed",
		"content": []interface{}{
			map[string]interface{}{
				"type": "content",
				"content": map[string]interface{}{
					"type": "text",
					"text": "Kubernetes operation complete.",
				},
			},
		},
	}); err != nil {
		t.Fatalf("collect completed tool update: %v", err)
	}
	if err := collector.requireCompletedToolEvidence(); err == nil ||
		!strings.Contains(err.Error(), "raw_output_missing") {
		t.Fatalf("content-only ACP tool evidence accepted: %v", err)
	}
}

func TestCollectorAcceptsPartialACPToolUpdatesButNotAsApplyEvidence(t *testing.T) {
	tests := []struct {
		name   string
		update map[string]interface{}
		reason string
	}{
		{
			name: "empty input",
			update: map[string]interface{}{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    "call-1",
				"toolName":      "mcp_doops_helm",
				"status":        "in_progress",
				"input":         map[string]interface{}{},
			},
			reason: "status_not_completed",
		},
		{
			name: "empty public content",
			update: map[string]interface{}{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    "call-1",
				"toolName":      "mcp_doops_helm",
				"status":        "completed",
				"content":       []interface{}{},
			},
			reason: "input_missing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := newDoagentToolTraceCollector(false)
			if err := collector.collect(tt.update); err != nil {
				t.Fatalf("partial update rejected: %v", err)
			}
			if err := collector.requireCompletedToolEvidence(); err == nil ||
				!strings.Contains(err.Error(), tt.reason) {
				t.Fatalf("partial update accepted as apply evidence: %v", err)
			}
		})
	}
}

func TestApplyRejectsFailedToolTransitionToCompleted(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "mcp_doops_helm",
		Status:     "failed",
		inputJSON:  json.RawMessage(`{"operation":"upgrade-install"}`),
	}
	err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call-1",
		"toolName":      "mcp_doops_helm",
		"status":        "completed",
		"content": []interface{}{
			map[string]interface{}{
				"type": "content",
				"content": map[string]interface{}{
					"type": "text",
					"text": "Kubernetes operation complete.",
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("failed tool transition accepted: %v", err)
	}
}

func TestApplyRejectsBashKubectlMutationEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"command":"kubectl patch deployment doops-agent"}`),
		resultJSON: json.RawMessage(`{"content":[{"type":"content","content":{"type":"text","text":"patched"}}]}`),
	}
	err := collector.requireCompletedToolEvidence()
	if err == nil || !strings.Contains(err.Error(), "doagent_apply_forbidden_mutation: Bash") {
		t.Fatalf("Bash kubectl mutation evidence accepted: %v", err)
	}
}

func TestApplyToolEvidenceReportsRejectedTrace(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "shell",
		Status:     "running",
	}

	err := collector.requireCompletedToolEvidence()
	if err == nil || !strings.Contains(err.Error(), "shell=status_not_completed") {
		t.Fatalf("rejected trace error = %v", err)
	}
}

func attestedMutationTrace(t *testing.T, runtimeTool string, input map[string]interface{}) doagentToolTrace {
	t.Helper()
	realTool := strings.TrimPrefix(modelVisibleDoagentToolName(runtimeTool), "doops_")
	scope := map[string]interface{}{"server": "doops", "tool": realTool}
	result := map[string]interface{}{
		"content": []interface{}{map[string]interface{}{
			"type": "text",
			"text": "operation complete",
		}},
		"isError": false,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	scopeDigest, err := digestRuntimeAttestationJSON(scope)
	if err != nil {
		t.Fatal(err)
	}
	inputDigest, err := digestRuntimeAttestationJSON(map[string]interface{}{
		"arguments": input,
		"server":    "doops",
		"tool":      realTool,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultDigest, err := digestRuntimeAttestationJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	return doagentToolTrace{
		ToolCallID:              "call-1",
		ToolName:                runtimeTool,
		Status:                  "completed",
		AttestationSchema:       "doops.tool-attestation/v2",
		ExecutionMode:           "apply",
		MutationAuthorized:      boolPointer(true),
		MutationAuthorityDigest: testMutationAuthorityDigest,
		AttestedTool:            realTool,
		EvidenceKind:            "runtime-tool-result",
		EvidenceSubject:         realTool,
		CanonicalScope:          scope,
		ScopeDigest:             scopeDigest,
		InputDigest:             inputDigest,
		AttestedResultDigest:    resultDigest,
		ObservedAt:              time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:               inputJSON,
		resultJSON:              resultJSON,
	}
}

const testMutationAuthorityDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func bindApplyCollector(collector *doagentToolTraceCollector, sessionID string) {
	collector.bindSession(sessionID)
	collector.bindMutationAuthority(testMutationAuthorityDigest)
}

func boolPointer(value bool) *bool {
	return &value
}
