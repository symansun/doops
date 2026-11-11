package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUpgradeFinalizationReleasesExclusiveLockWhileAuditRetries(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "upgrade-finalization")
	ownerClient, other := f.client(t, f.admin), f.client(t, f.admin)
	const session = "upgrade-finalization"
	const operationID = "upg-finalization"
	args := map[string]interface{}{
		"upgrade_operation_id":    operationID,
		"image":                   "registry.example/doops@sha256:" + strings.Repeat("a", 64),
		"expected_release_commit": strings.Repeat("b", 40),
		"mode":                    "k8s",
	}
	lifecycleSend(t, ownerClient, 2, "doops_agent_upgrade", session, args)
	call := agent.call(t, "doops_agent_upgrade", session)
	owner := f.operation(t, session, "running")

	// Both contenders must first demonstrate that the upgrade owns the lock.
	lifecycleSend(t, other, 2, "doops_node_info", "query", map[string]interface{}{})
	lifecycleResponse(t, other, 2, "target busy")
	lifecycleSend(t, other, 3, "doops_file_write", session, map[string]interface{}{"path": "config.txt", "content": "blocked"})
	lifecycleResponse(t, other, 3, "target busy")
	before := executionAuditTestEvent(t, f.store, owner.AuditID)
	if _, err := f.store.db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_upgrade_finalization
		BEFORE UPDATE ON audit_events WHEN OLD.id = %d
		BEGIN SELECT RAISE(FAIL, 'synthetic upgrade audit outage'); END`, owner.AuditID)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.store.db.Exec("DROP TRIGGER IF EXISTS fail_upgrade_finalization"); err != nil {
			t.Error(err)
		}
	})

	const output = "self-upgrade-accepted: controller Job continues independently"
	result := upgradeAuditTestResult(upgradeAuditCall{args: args}, "accepted", output)
	result["isError"] = false
	fields := result["structuredContent"].(map[string]interface{})
	fields["phase"] = "apply"
	fields["expected_release_commit"] = args["expected_release_commit"]
	if err := agent.write(map[string]interface{}{"jsonrpc": "2.0", "id": call.id, "result": result}); err != nil {
		t.Fatal(err)
	}
	response := lifecycleResponse(t, ownerClient, 2, "audit_completion_failed")
	rpcError := response["error"].(map[string]interface{})
	data, ok := rpcError["data"].(map[string]interface{})
	if !ok || data["audit_id"] != float64(owner.AuditID) || data["upgrade_operation_id"] != operationID {
		t.Fatalf("audit failure lost the original operation identity: %#v", response)
	}
	actualResult, ok := data["result"].(map[string]interface{})
	if !ok || !reflect.DeepEqual(actualResult["structuredContent"], fields) || resultContentText(actualResult) != output {
		t.Fatalf("audit failure lost the accepted Job evidence: %#v", data)
	}
	if pending := f.operation(t, session, "finalizing"); pending.ID != owner.ID || pending.AuditID != owner.AuditID {
		t.Fatalf("finalization replaced the original operation: %#v", pending)
	}

	// Leave the trigger installed: progress must not depend on audit recovery.
	lifecycleSend(t, other, 4, "doops_node_info", "query", map[string]interface{}{})
	agent.complete(t, agent.call(t, "doops_node_info", "query"), "")
	lifecycleResponse(t, other, 4, "")
	lifecycleSend(t, other, 5, "doops_file_write", session, map[string]interface{}{"path": "config.txt", "content": "released"})
	agent.complete(t, agent.call(t, "doops_file_write", session), "")
	lifecycleResponse(t, other, 5, "")
	if pending := f.operation(t, session, "finalizing"); pending.ID != owner.ID || pending.AuditID != owner.AuditID {
		t.Fatalf("new work discarded the pending upgrade audit: %#v", pending)
	}
	if got := executionAuditTestEvent(t, f.store, owner.AuditID); !reflect.DeepEqual(got, before) {
		t.Fatalf("audit changed while its completion writes were blocked: %#v", got)
	}

	recoveryAt := time.Now()
	if _, err := f.store.db.Exec("DROP TRIGGER fail_upgrade_finalization"); err != nil {
		t.Fatal(err)
	}
	requireEventually(t, 4*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
	got := executionAuditTestEvent(t, f.store, owner.AuditID)
	if got.Status != "accepted" || got.Error != "" || got.CorrelationID != operationID ||
		got.EndedAt.IsZero() || got.EndedAt.After(recoveryAt) || got.BytesOut != int64(len(output)) {
		t.Fatalf("recovery lost the original upgrade outcome or completion time: %#v", got)
	}
	var evidence struct {
		Result map[string]interface{} `json:"result"`
		Output string                 `json:"output"`
	}
	if err := json.Unmarshal([]byte(got.Tail), &evidence); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(evidence.Result, fields) || evidence.Output != output {
		t.Fatalf("recovery replaced the accepted Job evidence: %#v", evidence)
	}
}
