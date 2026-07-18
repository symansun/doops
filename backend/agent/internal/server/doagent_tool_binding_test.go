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
		!strings.Contains(err.Error(), "shell=raw_kubectl_mutation_forbidden") {
		t.Fatalf("raw kubectl mutation evidence accepted: %v", err)
	}
}

func TestApplyAcceptsStructuredK8sMutationEvidence(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "mcp_doops_k8s",
		Status:     "completed",
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		inputJSON:  json.RawMessage(`{"operation":"set-image","resource":"deployment/doops-agent"}`),
		resultJSON: json.RawMessage(`{"content":[{"type":"text","text":"Kubernetes operation complete."}]}`),
	}
	if err := collector.requireCompletedToolEvidence(); err != nil {
		t.Fatalf("structured k8s evidence rejected: %v", err)
	}
}

func TestApplyAcceptsCompletedACPToolPublicContent(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	if err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call-1",
		"toolName":      "mcp_doops_k8s",
		"status":        "in_progress",
		"input": map[string]interface{}{
			"operation": "set-image",
			"resource":  "deployment/doops-agent",
		},
	}); err != nil {
		t.Fatalf("collect in-progress tool update: %v", err)
	}
	if err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call-1",
		"toolName":      "mcp_doops_k8s",
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
	if err := collector.requireCompletedToolEvidence(); err != nil {
		t.Fatalf("completed ACP tool public content rejected: %v", err)
	}
}

func TestApplyRejectsInvalidACPToolEvidence(t *testing.T) {
	tests := []struct {
		name   string
		update map[string]interface{}
	}{
		{
			name: "empty input",
			update: map[string]interface{}{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    "call-1",
				"toolName":      "mcp_doops_k8s",
				"status":        "in_progress",
				"input":         map[string]interface{}{},
			},
		},
		{
			name: "empty public content",
			update: map[string]interface{}{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    "call-1",
				"toolName":      "mcp_doops_k8s",
				"status":        "completed",
				"content":       []interface{}{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := newDoagentToolTraceCollector(false)
			if err := collector.collect(tt.update); err == nil {
				t.Fatal("invalid tool evidence accepted")
			}
		})
	}
}

func TestApplyRejectsFailedToolTransitionToCompleted(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	collector.calls["call-1"] = doagentToolTrace{
		ToolCallID: "call-1",
		ToolName:   "mcp_doops_k8s",
		Status:     "failed",
		inputJSON:  json.RawMessage(`{"operation":"set-image"}`),
	}
	err := collector.collect(map[string]interface{}{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call-1",
		"toolName":      "mcp_doops_k8s",
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
	if err == nil || !strings.Contains(err.Error(), "raw_kubectl_mutation_forbidden") {
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
