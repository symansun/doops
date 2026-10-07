package server

import (
	"context"
	"testing"
	"time"
)

// Low-level fixtures explicitly release a lease; production finalizers own it.
func (a *GatewayAgent) acquireForAction(ctx context.Context, action GatewayAction, resourceKey string, maxQueued int, wait time.Duration) error {
	_, err := a.acquireActionLease(ctx, action, resourceKey, maxQueued, wait)
	return err
}

func (a *GatewayAgent) releaseForAction(action GatewayAction, resourceKey string) {
	switch {
	case isExclusiveOperation(action, resourceKey):
		a.releaseExclusive(resourceKey)
	case isConcurrentQuery(action, resourceKey):
		a.releaseReadOnly()
	case resourceKey != "":
		a.releaseResource(resourceKey)
	case isConcurrentReadOnlyAction(action):
		a.releaseReadOnly()
	default:
		a.releaseExclusive("target:" + a.Cluster + "/" + a.Instance)
	}
}

func TestSessionLeaseCannotEnterDuringAgentUpgrade(t *testing.T) {
	agent := &GatewayAgent{}
	upgrade, err := agent.acquireActionLeaseForSession(context.Background(), ActionAgentUpgrade, "target:dev/node", "upgrade", 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer upgrade.Release()
	for _, request := range []struct {
		action   GatewayAction
		resource string
	}{
		{ActionExec, "target-mutation:dev/node"},
		{ActionWrite, "workspace:release"},
		{ActionInfo, "query:node"},
	} {
		lease, err := agent.acquireActionLeaseForSession(context.Background(), request.action, request.resource, "release", 0, time.Second)
		if err == nil {
			lease.Release()
			t.Fatalf("%s entered during Agent upgrade", request.action)
		}
	}
	upgrade.Release()
	lease, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, "target-mutation:dev/node", "release", 0, time.Second)
	if err != nil {
		t.Fatalf("upgrade left a stale lock: %v", err)
	}
	lease.Release()
	if state := agent.runtimeState(); state.activeOps != 0 || state.busy {
		t.Fatalf("release did not restore idle state: %#v", state)
	}
}

func TestSessionConflictPolicyMatchesMutationAndWorkspaceLeases(t *testing.T) {
	holder := GatewayActiveOperation{Cluster: "dev", Instance: "node", Session: "release", Action: ActionExec, Resource: "target-mutation:dev/node"}
	for _, tc := range []struct {
		name     string
		action   GatewayAction
		resource string
		session  string
		conflict bool
	}{
		{"same mutation", ActionExec, holder.Resource, "release", true},
		{"different mutation", ActionExec, holder.Resource, "other", false},
		{"same workspace", ActionWrite, "workspace:release", "release", true},
		{"different workspace", ActionWrite, "workspace:other", "other", false},
		{"same session nginx", ActionExec, "nginx:dev/node", "release", true},
		{"different session nginx", ActionExec, "nginx:dev/node", "website", false},
		{"clean held workspace", ActionClean, "workspace:release", "maintenance", true},
		{"read-only query", ActionInfo, "query:node", "release", false},
		{"upgrade", ActionAgentUpgrade, "target:dev/node", "upgrade", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := GatewayActiveOperation{Cluster: "dev", Instance: "node", Session: tc.session, Action: tc.action, Resource: tc.resource}
			if got := operationsConflict(candidate, holder); got != tc.conflict {
				t.Fatalf("operation conflict = %t, want %t", got, tc.conflict)
			}
			service := &ClientService{activeOps: map[string]*gatewayActiveOperation{"holder": {GatewayActiveOperation: holder}}}
			service.activeOps["holder"].State = "cancel_requested"
			if got := service.hasCancellationBarrier(candidate); got != tc.conflict {
				t.Fatalf("cancellation conflict = %t, want %t", got, tc.conflict)
			}
		})
	}
}

func TestNginxLeaseProtectsItsResourceAndSessionWithoutTargetExclusion(t *testing.T) {
	agent := &GatewayAgent{}
	website, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, "nginx:dev/node", "website", 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer website.Release()
	other, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, "target-mutation:dev/node", "release", 0, time.Second)
	if err != nil {
		t.Fatalf("Nginx publish blocked an independent session: %v", err)
	}
	defer other.Release()
	for _, request := range []struct {
		resource string
		session  string
	}{
		{"nginx:dev/node", "second-website"},
		{"target-mutation:dev/node", "website"},
		{"workspace:website", "website"},
	} {
		if lease, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, request.resource, request.session, 0, time.Second); err == nil {
			lease.Release()
			t.Fatalf("conflicting lease was admitted: %#v", request)
		}
	}
	if state := agent.runtimeState(); state.busy || state.activeOps != 2 {
		t.Fatalf("Nginx lease became target-wide or double-counted: %#v", state)
	}
	website.Release()
	other.Release()
	if state := agent.runtimeState(); state.activeOps != 0 || len(state.resources) != 0 {
		t.Fatalf("release left stale leases: %#v", state)
	}
}

func TestSessionQueueAllowsOtherSessionAndReleasesAfterCancellation(t *testing.T) {
	agent := &GatewayAgent{closed: make(chan struct{})}
	holder, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, "target-mutation:dev/node", "release", 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		lease, err := agent.acquireActionLeaseForSession(ctx, ActionExec, "target-mutation:dev/node", "release", 1, time.Second)
		if lease != nil {
			lease.Release()
		}
		finished <- err
	}()
	requireEventually(t, time.Second, func() bool { return agent.runtimeState().queuedOps == 1 })
	other, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, "target-mutation:dev/node", "other", 0, time.Second)
	if err != nil {
		t.Fatalf("queued session blocked an independent session: %v", err)
	}
	other.Release()
	cancel()
	if err := <-finished; err != context.Canceled {
		t.Fatalf("queued cancellation = %v", err)
	}
	holder.Release()
	if state := agent.runtimeState(); state.activeOps != 0 || state.queuedOps != 0 || state.busy {
		t.Fatalf("cancellation left stale operation state: %#v", state)
	}
}

func TestSessionMutationRequiresExplicitSession(t *testing.T) {
	agent := &GatewayAgent{}
	if lease, err := agent.acquireActionLease(context.Background(), ActionExec, "target-mutation:dev/node", 0, time.Second); err == nil {
		lease.Release()
		t.Fatal("mutation without a session acquired a target-wide lock")
	}
	if agent.runtimeState().activeOps != 0 {
		t.Fatal("rejected request acquired a lease")
	}
}
