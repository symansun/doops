package server

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

func mergeDoagentToolAttestationUpdate(update map[string]interface{}, result interface{}) error {
	var attestations []map[string]interface{}
	if direct, exists := update["attestation"]; exists {
		attestation, ok := direct.(map[string]interface{})
		if !ok {
			return fmt.Errorf("attestation must be an object")
		}
		attestations = append(attestations, attestation)
	}
	if resultObject, ok := result.(map[string]interface{}); ok {
		if meta, ok := resultObject["_meta"].(map[string]interface{}); ok {
			if attestation, ok := meta["doops.sh/tool-attestation"].(map[string]interface{}); ok {
				attestations = append(attestations, attestation)
			}
		}
	}
	fields := map[string]string{
		"schemaVersion":           "attestationSchemaVersion",
		"contextSchemaVersion":    "contextSchemaVersion",
		"operationId":             "operationId",
		"contextDigest":           "contextDigest",
		"planDigest":              "planDigest",
		"planBindingDigest":       "planBindingDigest",
		"executionMode":           "executionMode",
		"mutationAuthorized":      "mutationAuthorized",
		"mutationAuthorityDigest": "mutationAuthorityDigest",
		"capabilityKey":           "capabilityKey",
		"tool":                    "attestedTool",
		"evidenceKind":            "evidenceKind",
		"evidenceSubject":         "evidenceSubject",
		"canonicalScope":          "canonicalScope",
		"scopeDigest":             "scopeDigest",
		"inputDigest":             "inputDigest",
		"resultDigest":            "resultDigest",
		"publicResultDigest":      "publicResultDigest",
	}
	for _, attestation := range attestations {
		for source, target := range fields {
			value, exists := attestation[source]
			if !exists {
				continue
			}
			if current, exists := update[target]; exists && !reflect.DeepEqual(current, value) {
				return fmt.Errorf("conflicting %s", target)
			}
			update[target] = value
		}
	}
	return nil
}

func (trace doagentToolTrace) eligible() bool {
	return trace.ineligibilityReason() == ""
}

func (trace doagentToolTrace) ineligibilityReason() string {
	return trace.terminalMutationReason()
}

// A verified failed tool result is terminal evidence, not successful deployment.
func (trace doagentToolTrace) terminalMutationReason() string {
	switch {
	case trace.Status != "completed" && trace.Status != "failed":
		return "status_not_completed"
	case trace.ToolName == "":
		return "tool_name_missing"
	case len(trace.inputJSON) == 0:
		return "input_missing"
	case len(trace.resultJSON) == 0:
		return "raw_output_missing"
	case !validDoagentObservedAt(trace.ObservedAt):
		return "observed_at_invalid"
	}
	tool := modelVisibleDoagentToolName(trace.ToolName)
	if !isStructuredDoopsMutationTool(tool) {
		return "structured_mutation_tool_required"
	}
	if reason := trace.runtimeMutationAttestationReason(); reason != "" {
		return reason
	}
	return ""
}

func (trace doagentToolTrace) runtimeMutationAttestationReason() string {
	tool := modelVisibleDoagentToolName(trace.ToolName)
	binding := runtimeToolBinding(tool)
	if binding.Server == "" {
		return "structured_mutation_tool_required"
	}
	realTool := binding.Tool
	if trace.AttestationSchema != "doops.tool-attestation/v2" {
		return "attestation_schema_invalid"
	}
	if trace.AttestedTool != realTool || trace.EvidenceSubject != realTool {
		return "attested_tool_mismatch"
	}
	if trace.EvidenceKind != "runtime-tool-result" {
		return "evidence_kind_invalid"
	}
	if trace.expectedSessionID == "" {
		return "session_binding_mismatch"
	}
	if stringField(trace.CanonicalScope, "runtimeSessionId") != trace.expectedSessionID {
		return "session_binding_mismatch"
	}
	expectedScope := map[string]interface{}{
		"runtimeSessionId": trace.expectedSessionID,
		"server":           binding.Server,
		"tool":             realTool,
	}
	if !reflect.DeepEqual(trace.CanonicalScope, expectedScope) {
		return "canonical_scope_mismatch"
	}
	expectedScopeDigest, err := digestRuntimeAttestationJSON(expectedScope)
	if err != nil || trace.ScopeDigest != expectedScopeDigest {
		return "scope_digest_mismatch"
	}
	input, err := decodeDoagentJSONValue(trace.inputJSON)
	if err != nil {
		return "input_invalid"
	}
	inputObject, ok := input.(map[string]interface{})
	if !ok {
		return "input_invalid"
	}
	expectedInputDigest, err := digestRuntimeAttestationJSON(map[string]interface{}{
		"arguments":        inputObject,
		"runtimeSessionId": trace.expectedSessionID,
		"server":           binding.Server,
		"tool":             realTool,
	})
	if err != nil || trace.InputDigest != expectedInputDigest {
		return "input_digest_mismatch"
	}
	if trace.MutationAuthorized == nil || !*trace.MutationAuthorized {
		return "mutation_not_authorized"
	}
	if trace.expectedMutationAuthorityDigest == "" ||
		trace.MutationAuthorityDigest != trace.expectedMutationAuthorityDigest {
		return "mutation_authority_binding_mismatch"
	}
	if trace.ExecutionMode != "apply" {
		return "execution_mode_invalid"
	}
	result, err := trace.verifiedResult()
	if err != nil {
		return "public_result_invalid"
	}
	facts, _ := result["structuredContent"].(map[string]interface{})
	if facts["executionOutcome"] == "unknown" {
		return "execution_outcome_unknown"
	}
	isError, ok := result["isError"].(bool)
	if !ok {
		return "tool_result_invalid"
	}
	if isError {
		return "tool_result_failed"
	}
	// An attested rejection can have invalid arguments; only success proves a mutation.
	operation := strings.ToLower(strings.TrimSpace(stringField(inputObject, "operation")))
	spec, ok := lookupStructuredDoopsMutationSpec(tool)
	if !ok || !stringSliceContains(spec.AllowedOperations(), operation) {
		return "mutation_operation_invalid"
	}
	if trace.Status != "completed" {
		return "status_result_mismatch"
	}
	return ""
}

func validDoagentObservedAt(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func (trace doagentToolTrace) verifiedResult() (map[string]interface{}, error) {
	result, err := publicDoagentToolResult(trace)
	if err != nil {
		return nil, err
	}
	resultObject, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("runtime tool result must be an object")
	}
	expectedResultDigest, err := digestRuntimeAttestationJSON(resultObject)
	if err != nil {
		return nil, fmt.Errorf("digest runtime tool result: %w", err)
	}
	if expectedResultDigest != trace.effectiveResultDigest() {
		return nil, fmt.Errorf("runtime tool result digest mismatch")
	}
	return resultObject, nil
}

func (trace doagentToolTrace) effectiveResultDigest() string {
	if trace.PublicResultDigest != "" {
		return trace.PublicResultDigest
	}
	return trace.AttestedResultDigest
}
