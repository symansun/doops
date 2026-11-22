package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/user/doops/agent/internal/dispatcher"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type doagentToolTrace struct {
	ToolCallID                      string
	ToolName                        string
	Status                          string
	ErrorCode                       string
	ExitCode                        *int32
	policyDenial                    string
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
	sequence                        uint64
	terminalSequence                uint64
	expectedSessionID               string
	expectedMutationAuthorityDigest string
}

type doagentToolTraceCollector struct {
	mu                              sync.Mutex
	calls                           map[string]doagentToolTrace
	sequence                        uint64
	collectAgentReport              bool
	ordinaryAsk                     bool
	expectedSessionID               string
	expectedMutationAuthorityDigest string
	finalMessage                    string
	finalReport                     json.RawMessage
	turnResult                      map[string]interface{}
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
	if update["sessionUpdate"] == "turn_finished" {
		terminal, err := doagentUIStrings(update, []string{"turnId", "status", "stopReason", "failureClass", "failureReason", "error"})
		if err != nil {
			return err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.turnResult = terminal
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
	if update["sessionUpdate"] == doagentStreamedReplyUpdate {
		text, err := doagentAgentMessageText(update["content"])
		if err != nil {
			return err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finalMessage == "" {
			c.finalMessage = text
		}
		return nil
	}
	updateType, _ := update["sessionUpdate"].(string)
	if updateType != "tool_call" && updateType != "tool_call_update" {
		return nil
	}
	if c.ordinaryAsk && stringField(update, "turnId") == "" {
		return nil
	}
	toolCallID, _ := update["toolCallId"].(string)
	if strings.TrimSpace(toolCallID) == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	trace, exists := c.calls[toolCallID]
	c.sequence++
	if !exists {
		trace.ToolCallID = toolCallID
		trace.sequence = c.sequence
	}
	if toolName := doagentToolName(update); toolName != "" {
		if trace.ToolName != "" && trace.ToolName != toolName {
			return fmt.Errorf("conflicting duplicate toolCallId %q tool name", toolCallID)
		}
		trace.ToolName = toolName
	}
	if input, ok, err := doagentToolInput(update); err != nil {
		return fmt.Errorf("read tool call %q input: %w", toolCallID, err)
	} else if ok {
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
		if c.ordinaryAsk && status == "failed" && trace.ToolName == "Bash" && trace.policyDenial == "" {
			output, _ := doagentAgentMessageText(update["content"])
			if denial := dispatcher.ReportedDenial(output); denial != nil {
				trace.policyDenial = denial.Error()
			}
		}
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
	if c.ordinaryAsk && (trace.Status == "completed" || trace.Status == "failed") {
		if number, ok := update["exitCode"].(json.Number); ok {
			value, err := number.Int64()
			if err != nil || value != int64(int32(value)) {
				return fmt.Errorf("invalid tool exitCode")
			}
			exit := int32(value)
			if trace.ExitCode != nil && *trace.ExitCode != exit {
				return fmt.Errorf("conflicting tool exitCode")
			}
			trace.ExitCode = &exit
		}
		if code := stringField(update, "errorCode"); code != "" {
			if trace.ErrorCode != "" && trace.ErrorCode != code {
				return fmt.Errorf("conflicting tool errorCode")
			}
			trace.ErrorCode = code
		}
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
	if (trace.Status == "completed" || trace.Status == "failed") && trace.ObservedAt == "" {
		trace.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if (trace.Status == "completed" || trace.Status == "failed") && trace.terminalSequence == 0 {
		trace.terminalSequence = c.sequence
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

func doagentToolName(update map[string]interface{}) string {
	for _, field := range []string{"toolName", "name"} {
		if value, _ := update[field].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	for _, field := range []string{"input", "args", "rawInput"} {
		value, ok := update[field]
		if !ok {
			continue
		}
		_, toolName := normalizeDoagentToolInput(value)
		if toolName != "" {
			return toolName
		}
	}
	return ""
}

func doagentToolInput(update map[string]interface{}) (interface{}, bool, error) {
	var input interface{}
	found := false
	for _, field := range []string{"input", "args", "rawInput"} {
		value, ok := update[field]
		if !ok {
			continue
		}
		normalized, _ := normalizeDoagentToolInput(value)
		if found {
			current, err := stableDoagentJSON(normalized)
			if err != nil {
				return nil, false, err
			}
			previous, err := stableDoagentJSON(input)
			if err != nil {
				return nil, false, err
			}
			if !bytes.Equal(previous, current) {
				return nil, false, fmt.Errorf("conflicting input fields")
			}
			continue
		}
		input = normalized
		found = true
	}
	return input, found, nil
}

func normalizeDoagentToolInput(value interface{}) (interface{}, string) {
	object, ok := value.(map[string]interface{})
	if !ok {
		return value, ""
	}
	toolName, _ := object["toolName"].(string)
	if strings.TrimSpace(toolName) == "" {
		toolName, _ = object["name"].(string)
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return value, ""
	}
	for _, field := range []string{"args", "arguments", "input"} {
		if args, exists := object[field]; exists {
			return args, toolName
		}
	}
	return value, toolName
}

func (c *doagentToolTraceCollector) orderedTraces() []doagentToolTrace {
	c.mu.Lock()
	defer c.mu.Unlock()

	traces := make([]doagentToolTrace, 0, len(c.calls))
	for id, trace := range c.calls {
		trace.ToolCallID = id
		trace.expectedSessionID = c.expectedSessionID
		trace.expectedMutationAuthorityDigest = c.expectedMutationAuthorityDigest
		traces = append(traces, trace)
	}
	sort.Slice(traces, func(i, j int) bool {
		if traces[i].sequence == traces[j].sequence {
			return traces[i].ToolCallID < traces[j].ToolCallID
		}
		return traces[i].sequence < traces[j].sequence
	})
	return traces
}

func (c *doagentToolTraceCollector) completed() []doagentToolTrace {
	var traces []doagentToolTrace
	for _, trace := range c.orderedTraces() {
		if trace.eligible() {
			traces = append(traces, trace)
		}
	}
	return traces
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

type doagentRuntimeToolBinding struct {
	Server string
	Tool   string
}

func runtimeToolBinding(modelTool string) doagentRuntimeToolBinding {
	switch modelTool {
	case "doops_helm":
		return doagentRuntimeToolBinding{Server: "doops", Tool: "helm"}
	case "doops_web":
		return doagentRuntimeToolBinding{Server: "doops", Tool: "web"}
	default:
		return doagentRuntimeToolBinding{}
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
