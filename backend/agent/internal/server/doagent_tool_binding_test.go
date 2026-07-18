package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestApplyRequiresCompletedToolEvidence(t *testing.T) {
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
	if err := collector.requireCompletedToolEvidence(); err != nil {
		t.Fatalf("completed tool evidence rejected: %v", err)
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
