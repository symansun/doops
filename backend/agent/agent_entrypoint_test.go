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

func TestAgentEntrypointsWriteBaseSettingsWithoutMountedModel(t *testing.T) {
	for _, path := range []string{"agent-entrypoint.sh", "sandbox-entrypoint.sh"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		script := string(data)
		// The model provider arrives from the Gateway; the entrypoint only writes
		// doagent's fixed settings file with the runtime MCP servers.
		if !strings.Contains(script, "/app/doops-agent doagent-settings") {
			t.Fatalf("%s must write doagent base settings through doops-agent", path)
		}
		for _, forbidden := range []string{
			"/opt/doagent_config", "configure_doagent_settings.py", "DO_AGENT_MODEL_ROUTING_POLICY",
		} {
			if strings.Contains(script, forbidden) {
				t.Fatalf("%s must not depend on a mounted model settings file (%s)", path, forbidden)
			}
		}
		// doagent no longer reads any environment variable for its settings path.
		if strings.Contains(script, "DO_AGENT_SETTINGS") {
			t.Fatalf("%s must not select doagent settings through the environment", path)
		}
		if strings.Contains(script, "/tmp/doops-doagent/settings.json") {
			t.Fatalf("%s must not write a settings file doagent never reads", path)
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
