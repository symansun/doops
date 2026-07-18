package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	rawKubectlMutationPattern = regexp.MustCompile(`(?i)\bkubectl\s+(apply|patch|replace|create|delete|set|scale|annotate|label|edit|rollout\s+restart)\b`)
	rawHelmMutationPattern    = regexp.MustCompile(`(?i)\bhelm\b.*\b(upgrade|install|rollback|uninstall)\b`)
)

type doagentToolTrace struct {
	ToolCallID                      string
	ToolName                        string
	Status                          string
	AttestationSchema               string
	ContextSchema                   string
	OperationID                     string
	ContextDigest                   string
	PlanDigest                      string
	PlanBindingDigest               string
	ExecutionMode                   string
	MutationAuthorized              *bool
	MutationAuthorityDigest         string
	CapabilityKey                   string
	AttestedTool                    string
	EvidenceKind                    string
	EvidenceSubject                 string
	ObservedAt                      string
	CanonicalScope                  map[string]interface{}
	ScopeDigest                     string
	InputDigest                     string
	AttestedResultDigest            string
	PublicResultDigest              string
	inputJSON                       json.RawMessage
	resultJSON                      json.RawMessage
	expectedSessionID               string
	expectedMutationAuthorityDigest string
}

type doagentToolTraceCollector struct {
	mu                              sync.Mutex
	calls                           map[string]doagentToolTrace
	collectAgentReport              bool
	expectedSessionID               string
	expectedMutationAuthorityDigest string
	finalMessage                    string
	finalReport                     json.RawMessage
}

func newDoagentToolTraceCollector(collectAgentReport bool) *doagentToolTraceCollector {
	return &doagentToolTraceCollector{
		calls:              make(map[string]doagentToolTrace),
		collectAgentReport: collectAgentReport,
	}
}

func (c *doagentToolTraceCollector) bindSession(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expectedSessionID = strings.TrimSpace(sessionID)
}

func (c *doagentToolTraceCollector) bindMutationAuthority(digest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expectedMutationAuthorityDigest = strings.TrimSpace(digest)
}

func (c *doagentToolTraceCollector) collect(update map[string]interface{}) error {
	if update["replay"] == true {
		return nil
	}
	if update["sessionUpdate"] == "agent_report" {
		if !c.collectAgentReport {
			return nil
		}
		report, ok := update["report"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("agent report must be an object")
		}
		encoded, err := stableDoagentJSON(report)
		if err != nil {
			return fmt.Errorf("encode agent report: %w", err)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if len(c.finalReport) != 0 {
			return fmt.Errorf("multiple agent reports were observed")
		}
		c.finalReport = encoded
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
		if (trace.Status == "completed" || trace.Status == "failed") && status != trace.Status {
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
		{"mutationAuthorityDigest", &trace.MutationAuthorityDigest},
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

func (c *doagentToolTraceCollector) structuredResult(requireAgentReport bool) (string, map[string]interface{}, error) {
	c.mu.Lock()
	text := c.finalMessage
	report := append(json.RawMessage(nil), c.finalReport...)
	c.mu.Unlock()
	if requireAgentReport {
		if len(report) == 0 {
			return "", nil, fmt.Errorf("structured CI/CD report was not emitted")
		}
		var object map[string]interface{}
		if err := json.Unmarshal(report, &object); err != nil {
			return "", nil, fmt.Errorf("decode structured CI/CD report: %w", err)
		}
		return string(report), object, nil
	}
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
		trace.expectedSessionID = c.expectedSessionID
		trace.expectedMutationAuthorityDigest = c.expectedMutationAuthorityDigest
		if trace.eligible() {
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
		trace.expectedSessionID = c.expectedSessionID
		trace.expectedMutationAuthorityDigest = c.expectedMutationAuthorityDigest
		if reason := trace.ineligibilityReason(); reason != "" {
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

func (c *doagentToolTraceCollector) requireCompletedToolEvidence() error {
	if forbidden := c.forbiddenMutationSummary(); forbidden != "" {
		return fmt.Errorf("doagent_apply_forbidden_mutation: %s", forbidden)
	}
	if len(c.completed()) != 0 {
		return nil
	}
	if rejected := c.rejectionSummary(); rejected != "" {
		return fmt.Errorf("doagent_apply_tool_evidence_missing: %s", rejected)
	}
	return fmt.Errorf("doagent_apply_tool_evidence_missing")
}

func (c *doagentToolTraceCollector) forbiddenMutationSummary() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var forbidden []string
	for _, trace := range c.calls {
		if trace.usesRawClusterMutation() {
			tool := modelVisibleDoagentToolName(trace.ToolName)
			if tool == "" {
				tool = "unknown"
			}
			forbidden = append(forbidden, tool)
		}
	}
	sort.Strings(forbidden)
	return strings.Join(forbidden, ",")
}

func (trace doagentToolTrace) eligible() bool {
	return trace.ineligibilityReason() == ""
}

func (trace doagentToolTrace) ineligibilityReason() string {
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
	case trace.usesRawClusterMutation():
		return "raw_cluster_mutation_forbidden"
	}
	tool := modelVisibleDoagentToolName(trace.ToolName)
	if tool != "doops_helm" {
		return "structured_mutation_tool_required"
	}
	if reason := trace.runtimeMutationAttestationReason(); reason != "" {
		return reason
	}
	return ""
}

func (trace doagentToolTrace) runtimeMutationAttestationReason() string {
	tool := modelVisibleDoagentToolName(trace.ToolName)
	realTool := strings.TrimPrefix(tool, "doops_")
	if trace.AttestationSchema != "doops.tool-attestation/v2" {
		return "attestation_schema_invalid"
	}
	if trace.AttestedTool != realTool || trace.EvidenceSubject != realTool {
		return "attested_tool_mismatch"
	}
	if trace.EvidenceKind != "runtime-tool-result" {
		return "evidence_kind_invalid"
	}
	expectedScope := map[string]interface{}{"server": "doops", "tool": realTool}
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
		"arguments": inputObject,
		"server":    "doops",
		"tool":      realTool,
	})
	if err != nil || trace.InputDigest != expectedInputDigest {
		return "input_digest_mismatch"
	}
	if trace.expectedSessionID == "" || stringField(inputObject, "session_id") != trace.expectedSessionID {
		return "session_binding_mismatch"
	}
	confirm, _ := inputObject["confirm"].(bool)
	if !confirm {
		return "mutation_confirmation_missing"
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
	operation := stringField(inputObject, "operation")
	if !stringSliceContains(helmMutationOperations, operation) {
		return "mutation_operation_invalid"
	}
	result, err := trace.verifiedResult()
	if err != nil {
		return "public_result_invalid"
	}
	if isError, ok := result["isError"].(bool); !ok || isError {
		return "tool_result_failed"
	}
	return ""
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (trace doagentToolTrace) usesRawClusterMutation() bool {
	tool := modelVisibleDoagentToolName(trace.ToolName)
	switch tool {
	case "shell", "doops_shell", "mcp_doops_shell", "Bash", "bash":
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(trace.inputJSON, &input) != nil {
			return false
		}
		command := strings.TrimSpace(input.Command)
		return rawKubectlMutationPattern.MatchString(command) || rawHelmMutationPattern.MatchString(command)
	case "ExecuteCode":
		var input struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(trace.inputJSON, &input) != nil {
			return false
		}
		code := strings.TrimSpace(input.Code)
		return rawKubectlMutationPattern.MatchString(code) || rawHelmMutationPattern.MatchString(code)
	default:
		return false
	}
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
	RequiredTools   []string
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
	switch runtimeName {
	case "mcp_doops_helm":
		return "doops_helm"
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
