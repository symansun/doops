package main

import (
	"os"
	"testing"
)

func TestConsumeAgentEnrollTokenUnsetsEnv(t *testing.T) {
	const canary = "synthetic-agent-enroll-canary-123456"

	t.Setenv(agentEnrollTokenEnv, canary)
	got, err := consumeAgentEnrollToken("")
	if err != nil {
		t.Fatalf("consumeAgentEnrollToken from env: %v", err)
	}
	if got != canary {
		t.Fatalf("consumeAgentEnrollToken = %q, want %q", got, canary)
	}
	if v, ok := os.LookupEnv(agentEnrollTokenEnv); ok {
		t.Fatalf("%s remained set in process env after consumption: %q", agentEnrollTokenEnv, v)
	}

	// Flag takes precedence, and the environment variable is still scrubbed.
	t.Setenv(agentEnrollTokenEnv, canary)
	got, err = consumeAgentEnrollToken("synthetic-flag-token-654321")
	if err != nil {
		t.Fatalf("consumeAgentEnrollToken from flag: %v", err)
	}
	if got != "synthetic-flag-token-654321" {
		t.Fatalf("consumeAgentEnrollToken flag = %q, want flag value", got)
	}
	if v, ok := os.LookupEnv(agentEnrollTokenEnv); ok {
		t.Fatalf("%s remained set in process env when flag was supplied: %q", agentEnrollTokenEnv, v)
	}
}
