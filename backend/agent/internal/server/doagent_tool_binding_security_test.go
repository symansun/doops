package server

import (
	"strings"
	"testing"
)

func TestRejectSensitiveToolResultRejectsSecretValuesWithoutSensitiveKeys(t *testing.T) {
	for _, value := range []string{
		"ghp_" + strings.Repeat("a", 36),
		"AKIA" + strings.Repeat("A", 16),
		"scheduled release Bearer synthetic-canary",
	} {
		if err := rejectSensitiveToolResult(map[string]interface{}{"message": value}); err == nil {
			t.Fatalf("sensitive tool result value %q was accepted", value)
		}
	}
}

func TestDoagentToolTraceCollectorRequiresMatchingTrustedAttestationForCICD(t *testing.T) {
	scope := map[string]interface{}{"repository": "example/repo"}
	scopeDigest, err := digestJSON(scope)
	if err != nil {
		t.Fatalf("digest scope: %v", err)
	}
	context := trustedReconciliationContext{
		SchemaVersion:      trustedReconciliationContextSchema,
		OperationID:        "op_0123456789abcdef0123456789abcdef",
		ExecutionMode:      "dry-run",
		MutationAuthorized: false,
		PlanDigest:         "sha256:" + strings.Repeat("3", 64),
		PlanBindingDigest:  "sha256:" + strings.Repeat("4", 64),
		Source: trustedReconciliationSource{
			Repository:      "https://example.test/repo.git",
			Revision:        strings.Repeat("a", 40),
			Branch:          "main",
			WorkspaceCommit: strings.Repeat("b", 40),
		},
		Capabilities: map[string]trustedReconciliationCapability{
			"source-identity": {
				Tool:            "ObserveWorkspaceSource",
				EvidenceKind:    "source-identity",
				EvidenceSubject: "source-identity",
				CanonicalScope:  scope,
				ScopeDigest:     scopeDigest,
			},
		},
	}
	context.ContextDigest, err = context.calculatedDigest()
	if err != nil {
		t.Fatalf("digest context: %v", err)
	}
	inputDigest, err := digestJSON(map[string]interface{}{
		"arguments":     map[string]interface{}{},
		"capabilityKey": "source-identity",
		"contextDigest": context.ContextDigest,
		"tool":          "ObserveWorkspaceSource",
	})
	if err != nil {
		t.Fatalf("digest input: %v", err)
	}
	result := map[string]interface{}{
		"structuredContent": map[string]interface{}{
			"schemaVersion": "doops.reconciliation-tool-result/v2",
			"tool":          "ObserveWorkspaceSource",
			"status":        "observed",
		},
	}
	resultDigest, err := digestJSON(result)
	if err != nil {
		t.Fatalf("digest result: %v", err)
	}
	admission := &trustedReconciliationAdmission{
		AllowedTools: []string{"mcp_doops_plan_ObserveWorkspaceSource"},
		Context:      context,
	}
	update := map[string]interface{}{
		"sessionUpdate":            "tool_call_update",
		"toolCallId":               "call-source",
		"toolName":                 "mcp_doops_plan_ObserveWorkspaceSource",
		"status":                   "completed",
		"input":                    map[string]interface{}{},
		"result":                   result,
		"attestationSchemaVersion": "doops.tool-attestation/v1",
		"contextSchemaVersion":     trustedReconciliationContextSchema,
		"operationId":              context.OperationID,
		"contextDigest":            context.ContextDigest,
		"planDigest":               context.PlanDigest,
		"planBindingDigest":        context.PlanBindingDigest,
		"executionMode":            context.ExecutionMode,
		"mutationAuthorized":       context.MutationAuthorized,
		"capabilityKey":            "source-identity",
		"attestedTool":             "ObserveWorkspaceSource",
		"evidenceKind":             "source-identity",
		"evidenceSubject":          "source-identity",
		"canonicalScope":           scope,
		"scopeDigest":              scopeDigest,
		"inputDigest":              inputDigest,
		"resultDigest":             resultDigest,
	}

	collector := newDoagentToolTraceCollector(admission)
	if err := collector.collect(update); err != nil {
		t.Fatalf("collect matching trusted call: %v", err)
	}
	if calls := collector.completed(); len(calls) != 1 {
		t.Fatalf("matching trusted call was not eligible: %#v", calls)
	}

	tampered := make(map[string]interface{}, len(update))
	for key, value := range update {
		tampered[key] = value
	}
	tampered["operationId"] = "op_ffffffffffffffffffffffffffffffff"
	collector = newDoagentToolTraceCollector(admission)
	if err := collector.collect(tampered); err != nil {
		t.Fatalf("collect mismatched trusted call: %v", err)
	}
	if calls := collector.completed(); len(calls) != 0 {
		t.Fatalf("mismatched trusted call became eligible: %#v", calls)
	}
}
