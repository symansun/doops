package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
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
	CanonicalScope       map[string]interface{}
	ScopeDigest          string
	InputDigest          string
	AttestedResultDigest string
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
		encoded, err := stableDoagentJSON(result)
		if err != nil {
			return fmt.Errorf("encode tool call %q result: %w", toolCallID, err)
		}
		if len(trace.resultJSON) != 0 && !bytes.Equal(trace.resultJSON, encoded) {
			return fmt.Errorf("conflicting duplicate toolCallId %q result", toolCallID)
		}
		trace.resultJSON = encoded
	}
	trace.AttestationSchema, _ = update["attestationSchemaVersion"].(string)
	trace.ContextSchema, _ = update["contextSchemaVersion"].(string)
	trace.OperationID, _ = update["operationId"].(string)
	trace.ContextDigest, _ = update["contextDigest"].(string)
	trace.PlanDigest, _ = update["planDigest"].(string)
	trace.PlanBindingDigest, _ = update["planBindingDigest"].(string)
	trace.ExecutionMode, _ = update["executionMode"].(string)
	if value, ok := update["mutationAuthorized"].(bool); ok {
		trace.MutationAuthorized = &value
	}
	trace.CapabilityKey, _ = update["capabilityKey"].(string)
	trace.AttestedTool, _ = update["attestedTool"].(string)
	trace.EvidenceKind, _ = update["evidenceKind"].(string)
	trace.EvidenceSubject, _ = update["evidenceSubject"].(string)
	trace.CanonicalScope, _ = update["canonicalScope"].(map[string]interface{})
	trace.ScopeDigest, _ = update["scopeDigest"].(string)
	trace.InputDigest, _ = update["inputDigest"].(string)
	trace.AttestedResultDigest, _ = update["resultDigest"].(string)
	c.calls[toolCallID] = trace
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

func (trace doagentToolTrace) eligibleForContext(expected *trustedReconciliationContext) bool {
	if trace.Status != "completed" ||
		trace.ToolName == "" ||
		len(trace.inputJSON) == 0 ||
		len(trace.resultJSON) == 0 {
		return false
	}
	if expected == nil {
		return true
	}
	if expected.ValidateDigest() != nil ||
		trace.AttestationSchema != "doops.tool-attestation/v1" ||
		trace.ContextSchema != expected.SchemaVersion ||
		trace.OperationID != expected.OperationID ||
		trace.ContextDigest != expected.ContextDigest ||
		trace.PlanDigest != expected.PlanDigest ||
		trace.PlanBindingDigest != expected.PlanBindingDigest ||
		trace.ExecutionMode != expected.ExecutionMode ||
		trace.MutationAuthorized == nil ||
		*trace.MutationAuthorized != expected.MutationAuthorized {
		return false
	}
	capability, ok := expected.Capabilities[trace.CapabilityKey]
	if !ok ||
		trace.ToolName != "mcp_doops_plan_"+capability.Tool ||
		trace.AttestedTool != capability.Tool ||
		trace.EvidenceKind != capability.EvidenceKind ||
		trace.EvidenceSubject != capability.EvidenceSubject ||
		trace.ScopeDigest != capability.ScopeDigest ||
		!validSHA256Digest(trace.InputDigest) ||
		!validSHA256Digest(trace.AttestedResultDigest) {
		return false
	}
	expectedScopeDigest, err := digestJSON(capability.CanonicalScope)
	if err != nil || expectedScopeDigest != trace.ScopeDigest {
		return false
	}
	actualScopeDigest, err := digestJSON(trace.CanonicalScope)
	if err != nil || actualScopeDigest != trace.ScopeDigest {
		return false
	}
	input, err := decodeDoagentJSONValue(trace.inputJSON)
	if err != nil {
		return false
	}
	arguments, ok := input.(map[string]interface{})
	if !ok || len(arguments) != 0 {
		return false
	}
	expectedInputDigest, err := digestJSON(map[string]interface{}{
		"arguments":     arguments,
		"capabilityKey": trace.CapabilityKey,
		"contextDigest": expected.ContextDigest,
		"tool":          capability.Tool,
	})
	if err != nil || expectedInputDigest != trace.InputDigest {
		return false
	}
	_, err = trace.verifiedResult()
	return err == nil
}

func (trace doagentToolTrace) verifiedResult() (map[string]interface{}, error) {
	result, err := decodeDoagentJSONValue(trace.resultJSON)
	if err != nil {
		return nil, fmt.Errorf("decode runtime tool result: %w", err)
	}
	resultObject, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("runtime tool result must be an object")
	}
	delete(resultObject, "_meta")
	expectedResultDigest, err := digestJSON(resultObject)
	if err != nil {
		return nil, fmt.Errorf("digest runtime tool result: %w", err)
	}
	if expectedResultDigest != trace.AttestedResultDigest {
		return nil, fmt.Errorf("runtime tool result digest mismatch")
	}
	if err := rejectSensitiveToolResult(resultObject); err != nil {
		return nil, err
	}
	return resultObject, nil
}

func rejectSensitiveToolResult(value interface{}) error {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, item := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "token", "password", "passwd", "secret", "cookie", "authorization",
				"privatekey", "private_key", "clientsecret", "client_secret",
				"stringdata", "dockerconfigjson":
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
	}
	return nil
}

func bindDeploymentRunEvidence(
	result map[string]interface{},
	traces []doagentToolTrace,
) (string, map[string]interface{}, error) {
	if result["kind"] != "DeploymentRun" {
		data, err := json.Marshal(result)
		return string(data), result, err
	}
	status, ok := result["status"].(map[string]interface{})
	if !ok {
		return "", nil, fmt.Errorf("DeploymentRun.status must be an object")
	}
	for field := range status {
		switch field {
		case "phase", "mutationCount", "capabilities", "evidence", "failures":
		default:
			return "", nil, fmt.Errorf("DeploymentRun.status field %q is not supported", field)
		}
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
		runtimeInput, err := decodeDoagentJSONValue(trace.inputJSON)
		if err != nil {
			return "", nil, fmt.Errorf("decode runtime tool input: %w", err)
		}
		runtimeResult, err := trace.verifiedResult()
		if err != nil {
			return "", nil, fmt.Errorf("verify runtime tool result: %w", err)
		}
		delete(entry, "toolRef")
		entry["toolCall"] = map[string]interface{}{
			"id":     trace.ToolCallID,
			"tool":   trace.ToolName,
			"input":  runtimeInput,
			"result": runtimeResult,
		}
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return "", nil, fmt.Errorf("encode DeploymentRun: %w", err)
	}
	return string(payload), result, nil
}

func resolveDoagentToolTrace(
	tool string,
	inputJSON json.RawMessage,
	traces []doagentToolTrace,
) (doagentToolTrace, error) {
	var matches []doagentToolTrace
	for _, trace := range traces {
		if trace.ToolName == tool && bytes.Equal(trace.inputJSON, inputJSON) {
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
