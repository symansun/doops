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
	ToolCallID string
	ToolName   string
	Status     string
	inputJSON  json.RawMessage
	resultJSON json.RawMessage
}

type doagentToolTraceCollector struct {
	mu    sync.Mutex
	calls map[string]doagentToolTrace
}

func newDoagentToolTraceCollector() *doagentToolTraceCollector {
	return &doagentToolTraceCollector{calls: make(map[string]doagentToolTrace)}
}

func (c *doagentToolTraceCollector) collect(update map[string]interface{}) error {
	if update["sessionUpdate"] != "tool_call_update" || update["replay"] == true {
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
	if result, ok := update["result"]; ok {
		encoded, err := stableDoagentJSON(result)
		if err != nil {
			return fmt.Errorf("encode tool call %q result: %w", toolCallID, err)
		}
		if len(trace.resultJSON) != 0 && !bytes.Equal(trace.resultJSON, encoded) {
			return fmt.Errorf("conflicting duplicate toolCallId %q result", toolCallID)
		}
		trace.resultJSON = encoded
	}
	c.calls[toolCallID] = trace
	return nil
}

func (c *doagentToolTraceCollector) completed() []doagentToolTrace {
	c.mu.Lock()
	defer c.mu.Unlock()

	traces := make([]doagentToolTrace, 0, len(c.calls))
	for _, trace := range c.calls {
		if trace.Status == "completed" &&
			trace.ToolName != "" &&
			len(trace.inputJSON) != 0 &&
			len(trace.resultJSON) != 0 {
			traces = append(traces, trace)
		}
	}
	return traces
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
		runtimeResult, err := decodeDoagentJSONValue(trace.resultJSON)
		if err != nil {
			return "", nil, fmt.Errorf("decode runtime tool result: %w", err)
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
