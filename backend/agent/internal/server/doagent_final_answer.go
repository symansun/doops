package server

import (
	"fmt"
	"sort"
	"strings"
)

// Internal-only collector event for a reply that arrived as chunks without a
// closing agent_message; it never overrides a real final message.
const doagentStreamedReplyUpdate = "doops_streamed_reply"

// A turn that ends without any assistant text used to be reported as
// "Operation complete.", which made read-only checks indistinguishable from
// empty results. The caller now always sees either the answer or which tools ran.
func (c *doagentToolTraceCollector) finalAnswer() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if text := strings.TrimSpace(c.finalMessage); text != "" {
		// The answer is the caller's result: redact it, never truncate it.
		return redactSecretText(text), nil
	}
	tools := make([]string, 0, len(c.calls))
	for _, trace := range c.calls {
		name := modelVisibleDoagentToolName(trace.ToolName)
		if name == "" {
			name = "unknown"
		}
		tools = append(tools, fmt.Sprintf("%s=%s", name, trace.Status))
	}
	sort.Strings(tools)
	if len(tools) == 0 {
		return "", fmt.Errorf("doagent_no_final_answer: the model ended the turn without calling a tool or replying")
	}
	return "", fmt.Errorf("doagent_no_final_answer: the model ended the turn without replying; tool calls: %s", strings.Join(tools, ","))
}
