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
	if !strings.Contains(text, "/root/.doops/audit") {
		t.Fatalf("expected agent-entrypoint.sh to keep audit logs outside workspaces")
	}
	if strings.Contains(text, "/root/ws/${DOOPS_SESSION") {
		t.Fatalf("agent-entrypoint.sh must not write audit logs into workspaces")
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

func TestAgentEntrypointsGenerateSharedMutationAuthority(t *testing.T) {
	for _, path := range []string{"agent-entrypoint.sh", "sandbox-entrypoint.sh"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		script := string(data)
		if !strings.Contains(script, "DO_AGENT_MUTATION_AUTHORITY_SECRET") ||
			!strings.Contains(script, "secrets.token_hex(32)") {
			t.Fatalf("%s must generate the shared doagent mutation authority", path)
		}
		if strings.Contains(script, "export DO_AGENT_MUTATION_AUTHORITY_SECRET") {
			t.Fatalf("%s must not export mutation authority to unrelated child processes", path)
		}
	}
}
