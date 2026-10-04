package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInternalFinalizationFailureBlocksAfterParentLeaseRelease(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	remote := f.agent(t, "internal-finalization")
	service := f.hub.client
	agent := f.hub.registry.Get("dev", "local")
	resource := "target-mutation:dev/local"
	parentAudit, err := f.store.StartAudit(AuditEvent{
		UserID: "internal", Cluster: "dev", Instance: "local", Action: ActionExec,
		Session: "parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := service.registerActiveOperation(GatewayActiveOperation{
		AuditID: parentAudit, Cluster: "dev", Instance: "local", Action: ActionExec,
		Session: "parent", Resource: resource,
	}, func() {})
	defer service.finishActiveOperation(parent)
	lease, err := agent.acquireActionLease(context.Background(), ActionExec, resource, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	service.attachOperationLease(parent, lease)
	service.markOperationRunning(parent)
	if err := service.beginExecution(agent, parent, parentAudit); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_child_finalization
		BEFORE UPDATE OF status ON audit_events
		WHEN OLD.session = 'child' AND OLD.status = 'executing'
		BEGIN SELECT RAISE(FAIL, 'synthetic child audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.store.db.Exec("DROP TRIGGER IF EXISTS fail_child_finalization"); err != nil {
			t.Error(err)
		}
		requireEventually(t, 3*time.Second, func() bool {
			return len(service.listActiveOperations()) == 0
		})
	})
	ctx := service.withExecutionOwner(context.Background(), agent, resource, parent)
	completed := make(chan error, 1)
	go func() {
		_, err := service.RunInternalToolCall(ctx, "dev", "local", "doops_shell",
			map[string]interface{}{"session_id": "child", "command": "true"})
		completed <- err
	}()
	remote.complete(t, remote.call(t, "doops_shell", "child"), "")
	select {
	case err := <-completed:
		if !errors.Is(err, errExecutionOutcomeUnknown) {
			t.Fatalf("unpersisted child outcome was not propagated: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("internal finalization did not return")
	}
	// Even a parent that successfully records an ordinary error cannot hide the
	// unpersisted child's uncertainty after releasing the actual target lease.
	if err := f.store.FinishAudit(parentAudit, AuditFinish{Status: "error"}); err != nil {
		t.Fatal(err)
	}
	service.finishActiveOperation(parent)
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_shell", "next", map[string]interface{}{"command": "true"})
	response := readUntilID(t, client, 2)
	rpcError, ok := response["error"].(map[string]interface{})
	if !ok || !strings.Contains(rpcError["message"].(string), "execution_guard_unavailable") {
		t.Fatalf("mutation admitted while child persistence fails: %#v", response)
	}
	select {
	case call := <-remote.calls:
		t.Fatalf("unexpected mutation reached Agent: %#v", call)
	default:
	}
}
