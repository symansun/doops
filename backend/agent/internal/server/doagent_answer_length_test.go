package server

import (
	"strings"
	"testing"
)

// A long ask answer used to come back as its last 1200 characters, so tables
// and conclusions lost their beginning. Answers are redacted, never truncated.
func TestDoagentAnswersAreRedactedNotTruncated(t *testing.T) {
	const head = "HEAD-MARKER: revision 249 table follows"
	const canary = "synthetic-answer-canary"
	long := head + "\n" + strings.Repeat("| row | value |\n", 400) +
		"connection https://user:" + canary + "@db.example.test/app\nTAIL-MARKER"

	for _, tt := range []struct {
		name string
		got  func(t *testing.T) string
	}{
		{"final answer", func(t *testing.T) string {
			collector := newDoagentToolTraceCollector(false)
			if err := collector.collect(map[string]interface{}{
				"sessionUpdate": "agent_message",
				"content":       map[string]interface{}{"type": "text", "text": long},
			}); err != nil {
				t.Fatal(err)
			}
			answer, err := collector.finalAnswer()
			if err != nil {
				t.Fatal(err)
			}
			return answer
		}},
		{"session update message", func(t *testing.T) string {
			update := sanitizeDoagentSessionUpdate(map[string]interface{}{
				"sessionUpdate": "agent_message",
				"content":       map[string]interface{}{"type": "text", "text": long},
			})
			text, err := doagentAgentMessageText(update["content"])
			if err != nil {
				t.Fatal(err)
			}
			return text
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.got(t)
			if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, "TAIL-MARKER") {
				t.Fatalf("answer was truncated: %d bytes, starts %q", len(got), got[:min(len(got), 40)])
			}
			if strings.Contains(got, canary) {
				t.Fatal("answer kept credential material")
			}
		})
	}

	// Error text keeps its cap: it is not a result.
	if got := sanitizeSecretText(long); len(got) > 1200 {
		t.Fatalf("error text cap removed: %d bytes", len(got))
	}
}
