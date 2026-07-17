package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type doagentToolTrace struct {
	ToolCallID           string
	ToolName             string
	Status               string
	AttestationSchema    string
	ContextSchema        string
	OperationID          string
	ContextDigest        string
	PlanDigest           string
	PlanBindingDigest    string
	ExecutionMode        string
	MutationAuthorized   *bool
	CapabilityKey        string
	AttestedTool         string
	EvidenceKind         string
	EvidenceSubject      string
	ObservedAt           string
	CanonicalScope       map[string]interface{}
	ScopeDigest          string
	InputDigest          string
	AttestedResultDigest string
	PublicResultDigest   string
	inputJSON            json.RawMessage
	resultJSON           json.RawMessage
}

type doagentToolTraceCollector struct {
	mu           sync.Mutex
	calls        map[string]doagentToolTrace
	expected     *trustedReconciliationContext
	finalMessage string
}

func newDoagentToolTraceCollector(
	admission ...*trustedReconciliationAdmission,
) *doagentToolTraceCollector {
	collector := &doagentToolTraceCollector{calls: make(map[string]doagentToolTrace)}
	if len(admission) > 0 && admission[0] != nil {
		collector.expected = &admission[0].Context
	}
	return collector
}

func (c *doagentToolTraceCollector) collect(update map[string]interface{}) error {
	if update["replay"] == true {
		return nil
	}
	if update["sessionUpdate"] == "agent_message" {
		text, err := doagentAgentMessageText(update["content"])
		if err != nil {
			return err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finalMessage != "" {
			return fmt.Errorf("multiple final agent messages were observed")
		}
		c.finalMessage = text
		return nil
	}
	if update["sessionUpdate"] != "tool_call_update" {
		return nil
	}
	toolCallID, _ := update["toolCallId"].(string)
	if strings.TrimSpace(toolCallID) == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	trace, exists := c.calls[toolCallID]
	if !exists {
		trace.ToolCallID = toolCallID
	}
	if toolName, _ := update["toolName"].(string); strings.TrimSpace(toolName) != "" {
		if trace.ToolName != "" && trace.ToolName != toolName {
			return fmt.Errorf("conflicting duplicate toolCallId %q tool name", toolCallID)
		}
		trace.ToolName = toolName
	}
	if input, ok := update["input"]; ok {
		encoded, err := stableDoagentJSON(input)
		if err != nil {
			return fmt.Errorf("encode tool call %q input: %w", toolCallID, err)
		}
		if len(trace.inputJSON) != 0 && !bytes.Equal(trace.inputJSON, encoded) {
			return fmt.Errorf("conflicting duplicate toolCallId %q input", toolCallID)
		}
		trace.inputJSON = encoded
	}
	if status, _ := update["status"].(string); strings.TrimSpace(status) != "" {
		if trace.Status == "completed" && status != "completed" {
			return fmt.Errorf("conflicting duplicate toolCallId %q status", toolCallID)
		}
		trace.Status = status
	}
	if result, ok := update["rawOutput"]; ok {
		if err := mergeDoagentToolAttestationUpdate(update, result); err != nil {
			return fmt.Errorf("read tool call %q attestation: %w", toolCallID, err)
		}
		encoded, err := stableDoagentJSON(result)
		if err != nil {
			return fmt.Errorf("encode tool call %q result: %w", toolCallID, err)
		}
		if len(trace.resultJSON) != 0 && !bytes.Equal(trace.resultJSON, encoded) {
			return fmt.Errorf("conflicting duplicate toolCallId %q result", toolCallID)
		}
		trace.resultJSON = encoded
	}
	stringFields := []struct {
		source string
		target *string
	}{
		{"attestationSchemaVersion", &trace.AttestationSchema},
		{"contextSchemaVersion", &trace.ContextSchema},
		{"operationId", &trace.OperationID},
		{"contextDigest", &trace.ContextDigest},
		{"planDigest", &trace.PlanDigest},
		{"planBindingDigest", &trace.PlanBindingDigest},
		{"executionMode", &trace.ExecutionMode},
		{"capabilityKey", &trace.CapabilityKey},
		{"attestedTool", &trace.AttestedTool},
		{"evidenceKind", &trace.EvidenceKind},
		{"evidenceSubject", &trace.EvidenceSubject},
		{"scopeDigest", &trace.ScopeDigest},
		{"inputDigest", &trace.InputDigest},
		{"resultDigest", &trace.AttestedResultDigest},
		{"publicResultDigest", &trace.PublicResultDigest},
	}
	for _, field := range stringFields {
		value, exists := update[field.source]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", field.source)
		}
		if *field.target != "" && *field.target != text {
			return fmt.Errorf("conflicting %s", field.source)
		}
		*field.target = text
	}
	if trace.Status == "completed" && trace.ObservedAt == "" {
		trace.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if value, exists := update["mutationAuthorized"]; exists {
		authorized, ok := value.(bool)
		if !ok {
			return fmt.Errorf("mutationAuthorized must be a boolean")
		}
		if trace.MutationAuthorized != nil && *trace.MutationAuthorized != authorized {
			return fmt.Errorf("conflicting mutationAuthorized")
		}
		trace.MutationAuthorized = &authorized
	}
	if value, exists := update["canonicalScope"]; exists {
		scope, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("canonicalScope must be an object")
		}
		if trace.CanonicalScope != nil && !reflect.DeepEqual(trace.CanonicalScope, scope) {
			return fmt.Errorf("conflicting canonicalScope")
		}
		trace.CanonicalScope = scope
	}
	c.calls[toolCallID] = trace
	return nil
}

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
		"schemaVersion":        "attestationSchemaVersion",
		"contextSchemaVersion": "contextSchemaVersion",
		"operationId":          "operationId",
		"contextDigest":        "contextDigest",
		"planDigest":           "planDigest",
		"planBindingDigest":    "planBindingDigest",
		"executionMode":        "executionMode",
		"mutationAuthorized":   "mutationAuthorized",
		"capabilityKey":        "capabilityKey",
		"tool":                 "attestedTool",
		"evidenceKind":         "evidenceKind",
		"evidenceSubject":      "evidenceSubject",
		"canonicalScope":       "canonicalScope",
		"scopeDigest":          "scopeDigest",
		"inputDigest":          "inputDigest",
		"resultDigest":         "resultDigest",
		"publicResultDigest":   "publicResultDigest",
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

func (c *doagentToolTraceCollector) structuredResult() (string, map[string]interface{}, error) {
	c.mu.Lock()
	text := c.finalMessage
	c.mu.Unlock()
	if strings.TrimSpace(text) == "" {
		return "", nil, fmt.Errorf("final machine result was not emitted")
	}
	object, err := decodeDoagentJSONObject(text)
	if err != nil {
		return "", nil, fmt.Errorf("final machine result must contain one JSON object: %w", err)
	}
	return text, object, nil
}

func (c *doagentToolTraceCollector) completed() []doagentToolTrace {
	c.mu.Lock()
	defer c.mu.Unlock()

	traces := make([]doagentToolTrace, 0, len(c.calls))
	for _, trace := range c.calls {
		if trace.eligibleForContext(c.expected) {
			traces = append(traces, trace)
		}
	}
	return traces
}

func (c *doagentToolTraceCollector) rejectionSummary() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	rejections := make([]string, 0, len(c.calls))
	for _, trace := range c.calls {
		if reason := trace.ineligibilityReason(c.expected); reason != "" {
			tool := modelVisibleDoagentToolName(trace.ToolName)
			if tool == "" {
				tool = "unknown"
			}
			rejections = append(rejections, tool+"="+reason)
		}
	}
	sort.Strings(rejections)
	return strings.Join(rejections, ",")
}

func (trace doagentToolTrace) eligibleForContext(expected *trustedReconciliationContext) bool {
	return trace.ineligibilityReason(expected) == ""
}

func (trace doagentToolTrace) ineligibilityReason(expected *trustedReconciliationContext) string {
	switch {
	case trace.Status != "completed":
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
	_, err := publicDoagentToolResult(trace)
	if err != nil {
		return "public_result_invalid"
	}
	if expected == nil {
		return ""
	}
	switch {
	case expected.ValidateDigest() != nil:
		return "expected_context_invalid"
	case trace.AttestationSchema != "doops.tool-attestation/v1":
		return "attestation_schema_mismatch"
	case trace.ContextSchema != expected.SchemaVersion:
		return "context_schema_mismatch"
	case trace.OperationID != expected.OperationID:
		return "operation_mismatch"
	case trace.ContextDigest != expected.ContextDigest:
		return "context_digest_mismatch"
	case trace.PlanDigest != expected.PlanDigest:
		return "plan_digest_mismatch"
	case trace.PlanBindingDigest != expected.PlanBindingDigest:
		return "plan_binding_digest_mismatch"
	case trace.ExecutionMode != expected.ExecutionMode:
		return "execution_mode_mismatch"
	case trace.MutationAuthorized == nil:
		return "mutation_authorization_missing"
	case *trace.MutationAuthorized != expected.MutationAuthorized:
		return "mutation_authorization_mismatch"
	}
	capability, ok := expected.Capabilities[trace.CapabilityKey]
	switch {
	case !ok:
		return "capability_missing"
	case trace.ToolName != "mcp_doops_plan_"+capability.Tool:
		return "runtime_tool_mismatch"
	case trace.AttestedTool != capability.Tool:
		return "attested_tool_mismatch"
	case trace.EvidenceKind != capability.EvidenceKind:
		return "evidence_kind_mismatch"
	case trace.EvidenceSubject != capability.EvidenceSubject:
		return "evidence_subject_mismatch"
	case trace.ScopeDigest != capability.ScopeDigest:
		return "scope_digest_mismatch"
	case !validSHA256Digest(trace.InputDigest):
		return "input_digest_invalid"
	case !validSHA256Digest(trace.effectiveResultDigest()):
		return "result_digest_invalid"
	}
	expectedScopeDigest, err := digestJSON(capability.CanonicalScope)
	if err != nil || expectedScopeDigest != trace.ScopeDigest {
		return "expected_scope_digest_mismatch"
	}
	actualScopeDigest, err := digestJSON(trace.CanonicalScope)
	if err != nil || actualScopeDigest != trace.ScopeDigest {
		return "actual_scope_digest_mismatch"
	}
	input, err := decodeDoagentJSONValue(trace.inputJSON)
	if err != nil {
		return "input_invalid"
	}
	arguments, ok := input.(map[string]interface{})
	if !ok || len(arguments) != 0 {
		return "input_not_empty_object"
	}
	expectedInputDigest, err := digestJSON(map[string]interface{}{
		"arguments":     arguments,
		"capabilityKey": trace.CapabilityKey,
		"contextDigest": expected.ContextDigest,
		"tool":          capability.Tool,
	})
	if err != nil || expectedInputDigest != trace.InputDigest {
		return "input_digest_mismatch"
	}
	_, err = trace.verifiedResult()
	if err != nil {
		return "result_digest_mismatch"
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
	expectedResultDigest, err := digestJSON(resultObject)
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

func rejectSensitiveToolResult(value interface{}) error {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, item := range typed {
			if credentialSecretLikeKey(key) || strings.EqualFold(strings.TrimSpace(key), "passwd") {
				return fmt.Errorf("runtime tool result contains forbidden sensitive field %q", key)
			}
			if err := rejectSensitiveToolResult(item); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, item := range typed {
			if err := rejectSensitiveToolResult(item); err != nil {
				return err
			}
		}
	case string:
		if credentialScalarContainsInlineMaterial(typed) {
			return fmt.Errorf("runtime tool result contains forbidden sensitive scalar")
		}
	}
	return nil
}

type deploymentRunContractBinding struct {
	APIVersion      string
	Workflow        string
	RunID           string
	WorkspaceCommit string
	Mode            string
	PrimaryTool     string
}

func bindDeploymentRunEvidence(
	result map[string]interface{},
	traces []doagentToolTrace,
	contract *deploymentRunContractBinding,
) (string, map[string]interface{}, error) {
	status, err := validateDeploymentRunEnvelope(result, contract)
	if err != nil {
		return "", nil, err
	}
	if err := rejectRetainedSensitiveDeploymentRunFields(result); err != nil {
		return "", nil, err
	}
	for field := range status {
		switch field {
		case "phase", "mutationCount", "capabilities", "evidence", "failures":
		default:
			return "", nil, fmt.Errorf("DeploymentRun.status field %q is not supported", field)
		}
	}
	switch status["phase"] {
	case "planned", "converged", "blocked", "failed", "outcome-unknown":
	default:
		return "", nil, fmt.Errorf("DeploymentRun.status.phase is invalid")
	}
	evidence, ok := status["evidence"].([]interface{})
	if !ok || len(evidence) == 0 {
		return "", nil, fmt.Errorf("DeploymentRun evidence must be a non-empty array")
	}

	for index, item := range evidence {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return "", nil, fmt.Errorf("DeploymentRun evidence[%d] must be an object", index)
		}
		if strings.TrimSpace(stringField(entry, "subject")) == "" ||
			strings.TrimSpace(stringField(entry, "observedAt")) == "" {
			return "", nil, fmt.Errorf(
				"DeploymentRun evidence[%d] must contain subject and observedAt",
				index,
			)
		}
		if len(entry) != 3 {
			return "", nil, fmt.Errorf(
				"DeploymentRun evidence[%d] must contain only subject, observedAt, and toolRef",
				index,
			)
		}

		toolRef, ok := entry["toolRef"].(map[string]interface{})
		if !ok || len(toolRef) != 2 {
			return "", nil, fmt.Errorf(
				"DeploymentRun evidence[%d].toolRef must contain only tool and input",
				index,
			)
		}
		tool, _ := toolRef["tool"].(string)
		input, hasInput := toolRef["input"]
		if strings.TrimSpace(tool) == "" || !hasInput {
			return "", nil, fmt.Errorf(
				"DeploymentRun evidence[%d].toolRef must contain tool and input",
				index,
			)
		}
		inputJSON, err := stableDoagentJSON(input)
		if err != nil {
			return "", nil, fmt.Errorf("encode DeploymentRun evidence[%d] input: %w", index, err)
		}
		trace, err := resolveDoagentToolTrace(tool, inputJSON, traces)
		if err != nil {
			return "", nil, fmt.Errorf("DeploymentRun evidence[%d]: %w", index, err)
		}
		if index == 0 && contract != nil &&
			contract.PrimaryTool != "" &&
			trace.ToolName != contract.PrimaryTool {
			return "", nil, fmt.Errorf(
				"DeploymentRun evidence[0] must reference %s",
				modelVisibleDoagentToolName(contract.PrimaryTool),
			)
		}
		runtimeInput, err := decodeDoagentJSONValue(trace.inputJSON)
		if err != nil {
			return "", nil, fmt.Errorf("decode runtime tool input: %w", err)
		}
		runtimeResult, err := trace.verifiedResult()
		if err != nil {
			return "", nil, fmt.Errorf("verify runtime tool result: %w", err)
		}
		delete(entry, "toolRef")
		if trace.EvidenceSubject != "" {
			entry["subject"] = trace.EvidenceSubject
		}
		entry["observedAt"] = trace.ObservedAt
		entry["toolCall"] = map[string]interface{}{
			"id":     trace.ToolCallID,
			"tool":   trace.ToolName,
			"input":  runtimeInput,
			"result": runtimeResult,
		}
	}

	if contract != nil {
		result = map[string]interface{}{
			"apiVersion": contract.APIVersion,
			"kind":       "DeploymentRun",
			"metadata": map[string]interface{}{
				"workflow":        contract.Workflow,
				"runId":           contract.RunID,
				"workspaceCommit": contract.WorkspaceCommit,
			},
			"spec": map[string]interface{}{
				"mode": contract.Mode,
			},
			"status": status,
		}
	}
	if err := rejectSensitiveToolResult(result); err != nil {
		return "", nil, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return "", nil, fmt.Errorf("encode DeploymentRun: %w", err)
	}
	return string(payload), result, nil
}

func validateDeploymentRunEnvelope(
	result map[string]interface{},
	contract *deploymentRunContractBinding,
) (map[string]interface{}, error) {
	if result["kind"] != "DeploymentRun" {
		return nil, fmt.Errorf("structured result kind must be DeploymentRun")
	}
	allowedTopLevel := map[string]bool{
		"apiVersion": true,
		"kind":       true,
		"metadata":   true,
		"spec":       true,
		"status":     true,
	}
	if len(result) != len(allowedTopLevel) {
		return nil, fmt.Errorf("DeploymentRun must contain exactly apiVersion, kind, metadata, spec, and status")
	}
	for field := range result {
		if !allowedTopLevel[field] {
			return nil, fmt.Errorf("DeploymentRun top-level field %q is not supported", field)
		}
	}
	apiVersion, _ := result["apiVersion"].(string)
	if apiVersion != "doops.sh/v2" && apiVersion != "doops.sh/v3" {
		return nil, fmt.Errorf("DeploymentRun.apiVersion is invalid")
	}
	metadata, ok := result["metadata"].(map[string]interface{})
	if !ok || len(metadata) != 3 {
		return nil, fmt.Errorf("DeploymentRun.metadata must contain exactly workflow, runId, and workspaceCommit")
	}
	for _, field := range []string{"workflow", "runId", "workspaceCommit"} {
		if strings.TrimSpace(stringField(metadata, field)) == "" {
			return nil, fmt.Errorf("DeploymentRun.metadata.%s is required", field)
		}
	}
	for field := range metadata {
		switch field {
		case "workflow", "runId", "workspaceCommit":
		default:
			return nil, fmt.Errorf("DeploymentRun.metadata field %q is not supported", field)
		}
	}
	spec, ok := result["spec"].(map[string]interface{})
	if !ok || len(spec) != 1 {
		return nil, fmt.Errorf("DeploymentRun.spec must contain exactly mode")
	}
	mode, _ := spec["mode"].(string)
	if mode != "dry-run" && mode != "apply" {
		return nil, fmt.Errorf("DeploymentRun.spec.mode is invalid")
	}
	status, ok := result["status"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("DeploymentRun.status must be an object")
	}
	if contract != nil {
		if apiVersion != contract.APIVersion ||
			stringField(metadata, "workflow") != contract.Workflow ||
			stringField(metadata, "runId") != contract.RunID ||
			stringField(metadata, "workspaceCommit") != contract.WorkspaceCommit ||
			mode != contract.Mode {
			return nil, fmt.Errorf("DeploymentRun envelope does not match the trusted reconciliation instruction")
		}
	}
	return status, nil
}

func rejectRetainedSensitiveDeploymentRunFields(result map[string]interface{}) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode DeploymentRun for sensitive-field validation: %w", err)
	}
	var retained map[string]interface{}
	if err := json.Unmarshal(encoded, &retained); err != nil {
		return fmt.Errorf("decode DeploymentRun for sensitive-field validation: %w", err)
	}
	status, _ := retained["status"].(map[string]interface{})
	evidence, _ := status["evidence"].([]interface{})
	for _, item := range evidence {
		entry, _ := item.(map[string]interface{})
		delete(entry, "subject")
		delete(entry, "observedAt")
	}
	return rejectSensitiveToolResult(retained)
}

func publicDoagentToolResult(trace doagentToolTrace) (interface{}, error) {
	result, err := decodeDoagentJSONValue(trace.resultJSON)
	if err != nil {
		return nil, err
	}
	if resultObject, ok := result.(map[string]interface{}); ok {
		if metadata, exists := resultObject["_meta"]; exists {
			attestationOnly := false
			if meta, ok := metadata.(map[string]interface{}); ok && len(meta) == 1 {
				_, attestationOnly = meta["doops.sh/tool-attestation"].(map[string]interface{})
			}
			if !attestationOnly {
				if err := rejectSensitiveToolResult(metadata); err != nil {
					return nil, err
				}
			}
			delete(resultObject, "_meta")
		}
	}
	if err := rejectSensitiveToolResult(result); err != nil {
		return nil, err
	}
	return result, nil
}

func resolveDoagentToolTrace(
	tool string,
	inputJSON json.RawMessage,
	traces []doagentToolTrace,
) (doagentToolTrace, error) {
	var matches []doagentToolTrace
	for _, trace := range traces {
		if modelVisibleDoagentToolName(trace.ToolName) == tool &&
			bytes.Equal(trace.inputJSON, inputJSON) {
			matches = append(matches, trace)
		}
	}
	switch len(matches) {
	case 0:
		return doagentToolTrace{}, fmt.Errorf("completed runtime tool call was not observed")
	case 1:
		return matches[0], nil
	default:
		return doagentToolTrace{}, fmt.Errorf("runtime tool reference is ambiguous")
	}
}

func modelVisibleDoagentToolName(runtimeName string) string {
	const trustedPlanPrefix = "mcp_doops_plan_"
	if strings.HasPrefix(runtimeName, trustedPlanPrefix) {
		name := strings.TrimPrefix(runtimeName, trustedPlanPrefix)
		if name != "" {
			return "plan." + name
		}
	}
	return runtimeName
}

func stableDoagentJSON(value interface{}) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeDoagentJSONValue(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func decodeDoagentJSONValue(data []byte) (interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("expected exactly one JSON value")
		}
		return nil, err
	}
	return value, nil
}

func stringField(object map[string]interface{}, field string) string {
	value, _ := object[field].(string)
	return value
}
