package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInternalFinalizationRetriesWithoutBlockingSameSession(t *testing.T) {
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
	lease, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, resource, "parent", 0, 0)
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
	// Pending audit writes preserve evidence without retaining execution locks.
	if err := f.store.FinishAudit(parentAudit, AuditFinish{Status: "error"}); err != nil {
		t.Fatal(err)
	}
	service.finishActiveOperation(parent)
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 4, "doops_shell", "independent", map[string]interface{}{"command": "true"})
	remote.complete(t, remote.call(t, "doops_shell", "independent"), "")
	lifecycleResponse(t, client, 4, "")
	lifecycleSend(t, client, 2, "doops_shell", "child", map[string]interface{}{"command": "true"})
	remote.complete(t, remote.call(t, "doops_shell", "child"), "")
	lifecycleResponse(t, client, 2, "audit_completion_failed")
	remote = f.agent(t, "replacement-during-audit-retry")
	if events, err := service.unresolvedExecutions("dev", "local"); err != nil || len(events) != 0 {
		t.Fatalf("pending terminal evidence became unknown: %#v, %v", events, err)
	}
	if _, err := f.store.db.Exec("DROP TRIGGER fail_child_finalization"); err != nil {
		t.Fatal(err)
	}
	requireEventually(t, 3*time.Second, func() bool { return len(service.listActiveOperations()) == 0 })
	events, err := f.store.ListAuditFiltered(AuditFilter{Session: "child"})
	if err != nil || len(events) != 2 {
		t.Fatalf("child audit history = %#v, %v", events, err)
	}
	for _, event := range events {
		if event.Status != "success" {
			t.Fatalf("original terminal evidence was lost: %#v", event)
		}
	}
	lifecycleSend(t, client, 3, "doops_shell", "after-finalization", map[string]interface{}{"command": "true"})
	remote.complete(t, remote.call(t, "doops_shell", "after-finalization"), "")
	lifecycleResponse(t, client, 3, "")
}

func TestReadOnlyFinalizationRetriesOriginalTerminalEvidence(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	remote := f.agent(t, "read-finalization")
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_read_completion
		BEFORE UPDATE ON audit_events
		WHEN OLD.session = 'read-finalization'
		BEGIN SELECT RAISE(FAIL, 'synthetic audit outage'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.store.db.Exec("DROP TRIGGER IF EXISTS fail_read_completion")
		requireEventually(t, 4*time.Second, func() bool { return len(f.hub.client.listActiveOperations()) == 0 })
	})
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_node_info", "read-finalization", map[string]interface{}{})
	call := remote.call(t, "doops_node_info", "read-finalization")
	op := f.operation(t, "read-finalization", "running")
	remote.complete(t, call, "")
	lifecycleResponse(t, client, 2, "audit_completion_failed")
	f.operation(t, "read-finalization", "finalizing")
	if got := executionAuditTestEvent(t, f.store, op.AuditID); got.Status != "running" {
		t.Fatalf("failed write unexpectedly changed audit: %#v", got)
	}
	// An unrelated request remains usable while this operation's audit retries.
	lifecycleSend(t, client, 3, "doops_shell", "independent", map[string]interface{}{"command": "true"})
	remote.complete(t, remote.call(t, "doops_shell", "independent"), "")
	lifecycleResponse(t, client, 3, "")
	if _, err := f.store.db.Exec("DROP TRIGGER fail_read_completion"); err != nil {
		t.Fatal(err)
	}
	requireEventually(t, 4*time.Second, func() bool { return len(f.hub.client.listActiveOperations()) == 0 })
	if got := executionAuditTestEvent(t, f.store, op.AuditID); got.Status != "success" ||
		got.Error != "" || got.EndedAt.IsZero() {
		t.Fatalf("retry lost the actual terminal evidence: %#v", got)
	}
}

func TestRejectedAdmissionRetriesAuditWithoutBlockingNewWork(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_offline_completion
		BEFORE UPDATE ON audit_events
		WHEN OLD.session = 'admission-rejected' AND NEW.status = 'offline'
		BEGIN SELECT RAISE(FAIL, 'synthetic admission audit outage'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.store.db.Exec("DROP TRIGGER IF EXISTS fail_offline_completion")
		requireEventually(t, 4*time.Second, func() bool { return len(f.hub.client.listActiveOperations()) == 0 })
	})
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_node_info", "admission-rejected", map[string]interface{}{})
	lifecycleResponse(t, client, 2, "target offline")
	op := f.operation(t, "admission-rejected", "finalizing")
	if got := executionAuditTestEvent(t, f.store, op.AuditID); got.Status != "running" {
		t.Fatalf("failed admission audit write changed status: %#v", got)
	}

	remote := f.agent(t, "admission-recovered")
	lifecycleSend(t, client, 3, "doops_shell", "admission-rejected", map[string]interface{}{"command": "true"})
	remote.complete(t, remote.call(t, "doops_shell", "admission-rejected"), "")
	lifecycleResponse(t, client, 3, "")
	if _, err := f.store.db.Exec("DROP TRIGGER fail_offline_completion"); err != nil {
		t.Fatal(err)
	}
	requireEventually(t, 4*time.Second, func() bool { return len(f.hub.client.listActiveOperations()) == 0 })
	if got := executionAuditTestEvent(t, f.store, op.AuditID); got.Status != "offline" ||
		got.Error != "target offline: dev/local" || got.EndedAt.IsZero() {
		t.Fatalf("admission retry lost the original rejection: %#v", got)
	}
}
