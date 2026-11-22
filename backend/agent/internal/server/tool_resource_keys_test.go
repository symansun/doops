package server

import (
	"encoding/json"
	"testing"
)

func TestGatewayResourceKeysUseSessionWorkspacePathAndTarget(t *testing.T) {
	if got := gitResourceKey(ActionPush, "dev", "node", "push-a"); got != "workspace:push-a" {
		t.Fatalf("git push resource key mismatch: %q", got)
	}
	if got := gitResourceKey(ActionPull, "dev", "node", "pull-a"); got != "workspace:pull-a" {
		t.Fatalf("git pull resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionExec, "doops_shell", json.RawMessage(`{"session_id":"exec-a","command":"sleep 30"}`), "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("exec resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionExec, "doops_shell", json.RawMessage(`{"session_id":"exec-a","command":"kubectl apply -f app.yaml"}`), "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("mutating exec resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionExec, "doops_shell", json.RawMessage(`{"session_id":"exec-a","command":"python3 /root/ws/restore.py"}`), "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("restore exec resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionExec, "doops_ssh", json.RawMessage(`{"session_id":"ssh-a","host":"ops@example.com","command":["bash","-lc","true"]}`), "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("SSH resource key mismatch: %q", got)
	}
	genericPromptArgs := json.RawMessage(`{"session_id":"ask-a","instruction":"inspect and repair the service"}`)
	if got := actionForTool("doops_agent_prompt", genericPromptArgs); got != ActionAsk {
		t.Fatalf("agent prompt must require ask permission: %q", got)
	}
	applyPromptArgs := json.RawMessage(`{"operation":"apply","session_id":"apply-a"}`)
	if got := actionForTool("doops_agent_prompt", applyPromptArgs); got != ActionExec {
		t.Fatalf("apply agent prompt must require exec permission: %q", got)
	}
	if got := resourceKeyForTool(ActionAsk, "doops_agent_prompt", genericPromptArgs, "dev", "node"); got != "workspace:ask-a" {
		t.Fatalf("agent prompt session resource key mismatch: %q", got)
	}
	boundPromptArgs := json.RawMessage(
		`{"operation":"apply","session_id":"release-a","workspace_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
	)
	if got := resourceKeyForTool(ActionExec, "doops_agent_prompt", boundPromptArgs, "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("bound apply prompt resource key mismatch: %q", got)
	}
	autoSQLPromptArgs := json.RawMessage(`{"operation":"ask","skill":"dosql","credential_mode":"auto","session_id":"sql-a"}`)
	if got := resourceKeyForTool(ActionAsk, "doops_agent_prompt", autoSQLPromptArgs, "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("automatic SQL credential prompt resource key mismatch: %q", got)
	}
	metadataArgs := json.RawMessage(`{"session_id":"ask-a","mode":"metadata"}`)
	if got := actionForTool("doops_agent_prompt", metadataArgs); got != ActionAsk {
		t.Fatalf("metadata prompt must remain a read-only ask: %q", got)
	}
	if got := resourceKeyForTool(ActionPush, "doops_workspace_begin", json.RawMessage(`{"session_id":"push-a"}`), "dev", "node"); got != "workspace:push-a" {
		t.Fatalf("push resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionPull, "doops_workspace_pull_begin", json.RawMessage(`{"session_id":"pull-a"}`), "dev", "node"); got != "workspace:pull-a" {
		t.Fatalf("pull resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionWrite, "doops_file_write", json.RawMessage(`{"path":"/tmp/x"}`), "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("write resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionWrite, "doops_file_write", json.RawMessage(`{"session_id":"write-a","path":"config.yaml"}`), "dev", "node"); got != "workspace:write-a" {
		t.Fatalf("session write resource key mismatch: %q", got)
	}
	if got := resourceKeyForTool(ActionAgentUpgrade, "doops_agent_upgrade", json.RawMessage(`{}`), "dev", "node"); got != "target:dev/node" {
		t.Fatalf("upgrade resource key mismatch: %q", got)
	}
	k8sArgs := json.RawMessage(`{"operation":"rollout-status","namespace":"kz-ops","resource":"deployment/example"}`)
	if got := resourceKeyForTool(ActionExec, "doops_k8s", k8sArgs, "dev", "node"); got != "query:k8s:kz-ops/deployment/example" {
		t.Fatalf("k8s resource key mismatch: %q", got)
	}
	k8sAliasArgs := json.RawMessage(`{"operation":"scale","namespace":"kz-ops","resource":"deploy/example"}`)
	if got := resourceKeyForTool(ActionExec, "doops_k8s", k8sAliasArgs, "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("k8s alias resource key mismatch: %q", got)
	}
	cleanupArgs := json.RawMessage(`{"credential_id":"cred_123","session_id":"release-a"}`)
	if got := actionForTool("doops_credential_cleanup", cleanupArgs); got != ActionCredentialRevoke {
		t.Fatalf("credential cleanup must require revoke permission: %q", got)
	}
	if got := resourceKeyForTool(ActionCredentialRevoke, "doops_credential_cleanup", cleanupArgs, "dev", "node"); got != "target-mutation:dev/node" {
		t.Fatalf("credential cleanup resource key mismatch: %q", got)
	}
}
