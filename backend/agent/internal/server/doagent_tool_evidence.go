package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

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

func isBashApplyToolName(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(modelVisibleDoagentToolName(raw))) {
	case "bash", "shell":
		return true
	default:
		return false
	}
}

func isPluginCustomMCPToolName(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "mcp_") {
		return false
	}
	if strings.HasPrefix(trimmed, "mcp_doops_") || strings.HasPrefix(trimmed, "mcp_dosql_") {
		return false
	}
	return len(trimmed) > len("mcp_")
}

func traceToolResultIsError(trace doagentToolTrace) bool {
	if len(trace.resultJSON) == 0 {
		return false
	}
	var parsed struct {
		IsError bool `json:"isError"`
	}
	return json.Unmarshal(trace.resultJSON, &parsed) == nil && parsed.IsError
}

func (c *doagentToolTraceCollector) verifiedMutationResult() (string, map[string]interface{}, error) {
	traces := c.orderedTraces()
	mutations := make([]interface{}, 0, len(traces))
	verified, outcome := true, "succeeded"
	for i, trace := range traces {
		tool := modelVisibleDoagentToolName(trace.ToolName)
		if !isStructuredDoopsMutationTool(tool) {
			if c.allowBashApplyEvidence && (isBashApplyToolName(trace.ToolName) || isPluginCustomMCPToolName(trace.ToolName)) {
				isMCP := isPluginCustomMCPToolName(trace.ToolName)
				mcpErr := isMCP && traceToolResultIsError(trace)
				label := "Bash"
				if isMCP {
					label = strings.TrimSpace(trace.ToolName)
				}
				entry := map[string]interface{}{
					"tool":         label,
					"tool_call_id": trace.ToolCallID,
					"status":       trace.Status,
					"verified":     trace.Status == "completed" && !mcpErr && (trace.ExitCode == nil || *trace.ExitCode == 0),
				}
				if trace.ExitCode != nil {
					entry["exit_code"] = *trace.ExitCode
				}
				if trace.ErrorCode != "" {
					entry["error_code"] = trace.ErrorCode
				}
				if trace.Status == "failed" || mcpErr || (trace.ExitCode != nil && *trace.ExitCode != 0) {
					if outcome != "unknown" {
						outcome = "failed"
					}
					verified = false
				} else if trace.Status != "completed" {
					outcome = "unknown"
					verified = false
				}
				mutations = append(mutations, entry)
			}
			continue
		}
		entry := map[string]interface{}{
			"tool":         tool,
			"tool_call_id": trace.ToolCallID,
			"status":       trace.Status,
		}
		reason := trace.terminalMutationReason()
		switch reason {
		case "", "tool_result_failed", "execution_outcome_unknown":
			result, err := trace.verifiedResult()
			if err != nil {
				return "", nil, fmt.Errorf("decode verified mutation result: %w", err)
			}
			entry["verified"], entry["result"] = true, result
			if reason == "execution_outcome_unknown" {
				outcome = "unknown"
			} else if reason == "tool_result_failed" {
				if resolvedBy := correctedHelmCall(traces, i); resolvedBy != "" {
					entry["resolved_by"] = resolvedBy
				} else if outcome != "unknown" {
					outcome = "failed"
				}
			}
		default:
			entry["verified"], entry["reason"] = false, reason
			verified, outcome = false, "unknown"
		}
		mutations = append(mutations, entry)
	}
	if len(mutations) == 0 {
		verified, outcome = false, "failed"
	}
	report := map[string]interface{}{
		"kind":      "doops.verified-mutation",
		"verified":  verified,
		"outcome":   outcome,
		"mutations": mutations,
	}
	c.mu.Lock()
	summary := c.finalMessage
	turn := c.turnResult
	c.mu.Unlock()
	if len(turn) != 0 {
		report["turn"] = turn
		if outcome != "unknown" && doagentTurnFinishedError(turn) != nil {
			report["outcome"] = "failed"
		}
	}
	if strings.TrimSpace(summary) != "" {
		report["summary"] = redactSecretText(summary)
	}
	encoded, err := stableDoagentJSON(report)
	if err != nil {
		return "", nil, fmt.Errorf("encode verified mutation result: %w", err)
	}
	return string(encoded), report, nil
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
	if unresolved := c.unresolvedMutationSummary(); unresolved != "" {
		return fmt.Errorf("doagent_apply_tool_evidence_missing: %s", unresolved)
	}
	traces := c.orderedTraces()
	for i, trace := range traces {
		if trace.terminalMutationReason() == "tool_result_failed" {
			if correctedHelmCall(traces, i) != "" {
				continue
			}
			return fmt.Errorf("doagent_apply_tool_failed: %s: %s",
				modelVisibleDoagentToolName(trace.ToolName), trace.failedResultText())
		}
	}
	if len(c.completed()) != 0 {
		return nil
	}
	if c.allowBashApplyEvidence {
		evidenceCompleted := false
		var lastToolName, lastToolErr string
		for _, trace := range traces {
			isBash := isBashApplyToolName(trace.ToolName)
			isCustomMCP := isPluginCustomMCPToolName(trace.ToolName)
			if !isBash && !isCustomMCP {
				continue
			}
			mcpErr := isCustomMCP && traceToolResultIsError(trace)
			if trace.Status == "completed" && !mcpErr && (trace.ExitCode == nil || *trace.ExitCode == 0) {
				evidenceCompleted = true
				lastToolErr = ""
				lastToolName = ""
				continue
			}
			if trace.Status == "failed" || mcpErr || (trace.ExitCode != nil && *trace.ExitCode != 0) {
				detail := trace.ErrorCode
				if detail == "" && trace.ExitCode != nil {
					detail = fmt.Sprintf("exit_code=%d", *trace.ExitCode)
				}
				if detail == "" {
					detail = "command_failed"
					if isCustomMCP {
						detail = "mcp_tool_failed"
					}
				}
				lastToolName = "Bash"
				if isCustomMCP {
					lastToolName = strings.TrimSpace(trace.ToolName)
				}
				lastToolErr = detail
			}
		}
		if lastToolErr != "" {
			return fmt.Errorf("doagent_apply_tool_failed: %s: %s", lastToolName, lastToolErr)
		}
		if evidenceCompleted {
			return nil
		}
	}
	if rejected := c.rejectionSummary(); rejected != "" {
		return fmt.Errorf("doagent_apply_tool_evidence_missing: %s", rejected)
	}
	return fmt.Errorf("doagent_apply_tool_evidence_missing")
}

// A later serial success can recover a known failure of the same Helm intent.
// Unexecuted validation/preparation errors may also correct invalid parameters.
func correctedHelmCall(traces []doagentToolTrace, index int) string {
	failed := traces[index]
	if modelVisibleDoagentToolName(failed.ToolName) != "doops_helm" ||
		failed.terminalSequence == 0 || failed.terminalMutationReason() != "tool_result_failed" {
		return ""
	}
	result, _ := failed.verifiedResult()
	facts, _ := result["structuredContent"].(map[string]interface{})
	executed := facts["executionStarted"] == true && facts["phase"] == "execution"
	unexecuted := facts["executionStarted"] == false &&
		(facts["phase"] == "validation" || facts["phase"] == "preparation")
	if !executed && !unexecuted {
		return ""
	}
	value, err := decodeDoagentJSONValue(failed.inputJSON)
	input, ok := value.(map[string]interface{})
	if err != nil || !ok {
		return ""
	}
	release := strings.TrimSpace(stringField(input, "release"))
	namespace := strings.TrimSpace(stringField(input, "namespace"))
	operation := strings.ToLower(strings.TrimSpace(stringField(input, "operation")))
	if release == "" || namespace == "" || (executed && !stringSliceContains(helmMutationOperations, operation)) {
		return ""
	}
	for _, next := range traces[index+1:] {
		if next.sequence <= failed.terminalSequence ||
			modelVisibleDoagentToolName(next.ToolName) != "doops_helm" || !next.eligible() {
			continue
		}
		nextValue, err := decodeDoagentJSONValue(next.inputJSON)
		nextInput, ok := nextValue.(map[string]interface{})
		if err != nil || !ok {
			continue
		}
		nextOperation := strings.ToLower(strings.TrimSpace(stringField(nextInput, "operation")))
		if stringSliceContains(helmMutationOperations, operation) && operation != nextOperation {
			continue
		}
		if executed {
			switch operation {
			case "upgrade-install":
				source, nextSource := strings.TrimSpace(stringField(input, "asset_source")), strings.TrimSpace(stringField(nextInput, "asset_source"))
				if source == "" {
					source = helmAssetSourceRemote
				}
				if nextSource == "" {
					nextSource = helmAssetSourceRemote
				}
				chart := strings.TrimSpace(stringField(input, "chart"))
				nextChart := strings.TrimSpace(stringField(nextInput, "chart"))
				if chart == "" || nextChart == "" || chart != nextChart || source != nextSource {
					continue
				}
			case "rollback":
				revision, ok := input["revision"].(json.Number)
				number, err := revision.Int64()
				if !ok || err != nil || number <= 0 || revision != nextInput["revision"] {
					continue
				}
			}
		}
		if release == strings.TrimSpace(stringField(nextInput, "release")) &&
			namespace == strings.TrimSpace(stringField(nextInput, "namespace")) {
			return next.ToolCallID
		}
	}
	return ""
}

// unresolvedMutationSummary lists tool=reason for every mutation whose outcome
// cannot be proven from its trace, or "" when every mutation is resolved.
func (c *doagentToolTraceCollector) unresolvedMutationSummary() string {
	traces := c.orderedTraces()
	var unresolved []string
	for _, trace := range traces {
		tool := modelVisibleDoagentToolName(trace.ToolName)
		if tool == "" {
			tool = "unknown"
		}
		if !isStructuredDoopsMutationTool(tool) {
			continue
		}
		trace.expectedSessionID = c.expectedSessionID
		trace.expectedMutationAuthorityDigest = c.expectedMutationAuthorityDigest
		if reason := trace.terminalMutationReason(); reason != "" && reason != "tool_result_failed" {
			unresolved = append(unresolved, tool+"="+reason)
		}
	}
	sort.Strings(unresolved)
	return strings.Join(unresolved, ",")
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

func modelVisibleDoagentToolName(runtimeName string) string {
	for _, spec := range structuredDoopsMutationSpecs {
		if spec.RuntimeTool == runtimeName {
			return spec.ModelTool
		}
	}
	return runtimeName
}

// failedResultText is the tool's own error, so a failed apply names the cause
// instead of only "tool failed". It is redacted and capped like other reasons.
func (trace doagentToolTrace) failedResultText() string {
	result, err := trace.verifiedResult()
	if err != nil {
		return "tool error text unavailable"
	}
	content, _ := result["content"].([]interface{})
	for _, item := range content {
		part, _ := item.(map[string]interface{})
		if text, _ := part["text"].(string); strings.TrimSpace(text) != "" {
			text = redactSecretText(strings.TrimSpace(text))
			if len([]rune(text)) > 400 {
				text = string([]rune(text)[:400]) + "..."
			}
			return text
		}
	}
	return "tool returned no error text"
}
