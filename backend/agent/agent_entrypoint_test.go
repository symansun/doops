package main_test

import (
	"os"
	"strings"
	"testing"
)

func TestAgentEntrypointDisablesShellHistoryAndKeepsAuditLog(t *testing.T) {
	data, err := os.ReadFile("agent-entrypoint.sh")
	if err != nil {
		t.Fatalf("read agent-entrypoint.sh: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, "unset HISTFILE") {
		t.Fatalf("expected agent-entrypoint.sh to disable shell history via unset HISTFILE")
	}
	if !strings.Contains(text, ".doops-audit-log") {
		t.Fatalf("expected agent-entrypoint.sh to keep doops audit log output")
	}
}

func TestAgentEntrypointDisablesSSHDAndWebIDEByDefault(t *testing.T) {
	data, err := os.ReadFile("agent-entrypoint.sh")
	if err != nil {
		t.Fatalf("read agent-entrypoint.sh: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, "DOOPS_ENABLE_WEBIDE") {
		t.Fatalf("expected explicit DOOPS_ENABLE_WEBIDE gate")
	}
	if !strings.Contains(text, "DOOPS_ENABLE_SSHD") {
		t.Fatalf("expected explicit DOOPS_ENABLE_SSHD gate")
	}
	if !strings.Contains(text, "WebIDE disabled by default") {
		t.Fatalf("expected default WebIDE disabled message")
	}
	if !strings.Contains(text, "SSHD disabled by default") {
		t.Fatalf("expected default SSHD disabled message")
	}
}

func TestAgentEntrypointsMaterializeSettingsOutsideMountedAgentHome(t *testing.T) {
	for _, path := range []string{"agent-entrypoint.sh", "sandbox-entrypoint.sh"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		script := string(data)
		if !strings.Contains(script, "${DO_AGENT_SETTINGS:-/tmp/doops-doagent/settings.json}") {
			t.Fatalf("%s must default effective settings to a writable runtime path", path)
		}
		if strings.Contains(script, "${DO_AGENT_SETTINGS:-/root/.agent/settings.json}") {
			t.Fatalf("%s must not overwrite a mounted or persisted settings file", path)
		}
	}
}
