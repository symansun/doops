package server

import (
	"strings"
	"testing"
)

func textUpdate(kind, text string) map[string]interface{} {
	return map[string]interface{}{
		"sessionUpdate": kind,
		"content":       map[string]interface{}{"type": "text", "text": text},
	}
}

func TestFinalAnswerReturnsModelReply(t *testing.T) {
	c := newDoagentToolTraceCollector(false)
	if err := c.collect(textUpdate("agent_message", "zyj pods are on release-20261015")); err != nil {
		t.Fatal(err)
	}
	answer, err := c.finalAnswer()
	if err != nil || answer != "zyj pods are on release-20261015" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
}

func TestFinalAnswerUsesStreamedReplyWhenNoFinalMessage(t *testing.T) {
	c := newDoagentToolTraceCollector(false)
	if err := c.collect(textUpdate(doagentStreamedReplyUpdate, "streamed only")); err != nil {
		t.Fatal(err)
	}
	if answer, err := c.finalAnswer(); err != nil || answer != "streamed only" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
}

func TestStreamedReplyNeverOverridesFinalMessage(t *testing.T) {
	c := newDoagentToolTraceCollector(false)
	if err := c.collect(textUpdate("agent_message", "final")); err != nil {
		t.Fatal(err)
	}
	if err := c.collect(textUpdate(doagentStreamedReplyUpdate, "chunks")); err != nil {
		t.Fatalf("streamed reply after final message must not fail: %v", err)
	}
	if answer, _ := c.finalAnswer(); answer != "final" {
		t.Fatalf("answer = %q, want final", answer)
	}
}

func TestFinalAnswerReportsSilentTurnInsteadOfOperationComplete(t *testing.T) {
	c := newDoagentToolTraceCollector(false)
	_, err := c.finalAnswer()
	if err == nil || !strings.HasPrefix(err.Error(), "doagent_no_final_answer") {
		t.Fatalf("silent turn must be an explicit error, got %v", err)
	}
}

func TestFinalAnswerRedactsSecrets(t *testing.T) {
	c := newDoagentToolTraceCollector(false)
	if err := c.collect(textUpdate("agent_message", "uri postgres://app:s3cret@db/app")); err != nil {
		t.Fatal(err)
	}
	answer, _ := c.finalAnswer()
	if strings.Contains(answer, "s3cret") {
		t.Fatalf("secret leaked: %q", answer)
	}
}
