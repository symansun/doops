package server

import (
	"errors"
	"strings"
	"testing"
)

// An unknown outcome must carry the reason the Agent gave, so the caller and
// the audit can tell which check could not be proven. Every unknown outcome
// reading the same is how a process-group timeout could not be told apart
// from a drain timeout.
func TestExecutionUnknownFromResponseKeepsTheAgentReason(t *testing.T) {
	cases := []struct {
		name    string
		message interface{}
		want    string
		notWant string
	}{
		{name: "bare code stays bare", message: "execution_outcome_unknown", want: "execution_outcome_unknown"},
		{name: "process group reason is kept", message: "execution_outcome_unknown: detached process group was terminated",
			want: "execution_outcome_unknown: detached process group was terminated"},
		{name: "drain reason is kept", message: "execution_outcome_unknown: command output drain timed out",
			want: "execution_outcome_unknown: command output drain timed out"},
		{name: "empty reason falls back to the code", message: "execution_outcome_unknown:   ", want: "execution_outcome_unknown"},
		{name: "other remote error is named", message: "agent crashed", want: "execution_outcome_unknown: remote error: agent crashed"},
		{name: "missing message", message: nil, want: "execution_outcome_unknown"},
		{name: "secret in the reason is redacted", message: "execution_outcome_unknown: password=hunter2-canary leaked",
			want: "execution_outcome_unknown: ", notWant: "hunter2-canary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := map[string]interface{}{"error": map[string]interface{}{"code": -32603, "message": tc.message}}
			err := executionUnknownFromResponse(response)
			if !errors.Is(err, errExecutionOutcomeUnknown) {
				t.Fatalf("error %v no longer matches errExecutionOutcomeUnknown", err)
			}
			if tc.notWant == "" && err.Error() != tc.want {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("error = %q, want prefix %q", err.Error(), tc.want)
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Fatalf("error leaked %q: %q", tc.notWant, err.Error())
			}
		})
	}
}

// The reason must still be recognised as an unknown outcome downstream.
func TestExecutionUnknownReasonIsStillRecognised(t *testing.T) {
	err := executionUnknownFromResponse(map[string]interface{}{"error": map[string]interface{}{
		"message": "execution_outcome_unknown: process group did not terminate",
	}})
	response := map[string]interface{}{"error": map[string]interface{}{"message": err.Error()}}
	if !executionResponseUnknown(response) {
		t.Fatalf("%q is not recognised as an unknown outcome", err.Error())
	}
}

// A prompt audit keeps only stable categories. An unknown outcome keeps its
// code; its reason is kept only when it is itself a stable category, so free
// text from a prompt never reaches the audit.
func TestAgentPromptAuditCategoryKeepsOnlyStableCategories(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare unknown", "execution_outcome_unknown", "execution_outcome_unknown"},
		{"unknown with category", "execution_outcome_unknown: credential_apply_outcome_unknown", "execution_outcome_unknown: credential_apply_outcome_unknown"},
		{"unknown with free text", "execution_outcome_unknown: process group did not terminate", "execution_outcome_unknown"},
		{"unknown with secret", "execution_outcome_unknown: token=synthetic-canary-value", "execution_outcome_unknown"},
		{"plain category", "synthetic_apply_failed", "synthetic_apply_failed"},
		{"free text", "apply failed: password=synthetic-canary-value", "credential_operation_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentPromptAuditCategory(tc.in); got != tc.want {
				t.Fatalf("agentPromptAuditCategory(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
