package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGatewayTargetBusySnapshotUsesTargetScopedUpgradeSlot(t *testing.T) {
	agent := &GatewayAgent{
		Cluster:        "dev",
		Instance:       "local",
		Key:            "dev/local",
		pending:        make(map[int64]*gatewayMessageQueue),
		resources:      make(map[string]*agentResourceSlot),
		sessionWriters: make(map[string]int),
	}

	if got := agent.snapshot(); got.Busy {
		t.Fatalf("idle target must not be marked busy: %#v", got)
	}

	if err := agent.acquireForAction(context.Background(), ActionAgentUpgrade, "target:dev/local", 0, time.Second); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := agent.snapshot(); !got.Busy || got.Status != "busy" || got.BusyReason != "exclusive_operation" {
		t.Fatalf("acquired target should be busy: %#v", got)
	}
	agent.releaseForAction(ActionAgentUpgrade, "target:dev/local")
	if got := agent.snapshot(); got.Busy || got.Status != "idle" {
		t.Fatalf("released target should be idle: %#v", got)
	}
}

func TestGatewaySessionMutationLocksArePerSession(t *testing.T) {
	agent := &GatewayAgent{
		Cluster:        "dev",
		Instance:       "local",
		Key:            "dev/local",
		pending:        make(map[int64]*gatewayMessageQueue),
		resources:      make(map[string]*agentResourceSlot),
		sessionWriters: make(map[string]int),
	}
	first, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, "target-mutation:dev/local", "session-a", 0, time.Second)
	if err != nil {
		t.Fatalf("first session acquire: %v", err)
	}
	defer first.Release()
	sameSession, err := agent.acquireActionLeaseForSession(context.Background(), ActionExec, "target-mutation:dev/local", "session-a", 0, time.Second)
	if err != nil {
		t.Fatalf("same-session shell calls must run concurrently: %v", err)
	}
	if _, err := agent.acquireActionLeaseForSession(context.Background(), ActionWrite, "workspace:session-a", "session-a", 0, time.Second); err == nil || !strings.Contains(err.Error(), "target busy") {
		t.Fatalf("same-session workspace write must wait for running shell calls, got %v", err)
	}
	other, err := agent.acquireActionLeaseForSession(context.Background(), ActionWrite, "workspace:session-b", "session-b", 0, time.Second)
	if err != nil {
		t.Fatalf("another session's workspace must not be blocked: %v", err)
	}
	other.Release()
	sameSession.Release()
	if _, err := agent.acquireActionLeaseForSession(context.Background(), ActionWrite, "workspace:session-a", "session-a", 0, time.Second); err == nil {
		t.Fatal("workspace write entered while one same-session shell call is still running")
	}
	first.Release()
	write, err := agent.acquireActionLeaseForSession(context.Background(), ActionWrite, "workspace:session-a", "session-a", 0, time.Second)
	if err != nil {
		t.Fatalf("workspace write blocked after every shell call released: %v", err)
	}
	write.Release()
}

func TestGatewayTargetBusySnapshotUsesResourceLocks(t *testing.T) {
	agent := &GatewayAgent{
		Cluster:   "dev",
		Instance:  "local",
		Key:       "dev/local",
		pending:   make(map[int64]*gatewayMessageQueue),
		resources: make(map[string]*agentResourceSlot),
	}

	if err := agent.acquireForAction(context.Background(), ActionPush, "workspace:release", 0, time.Second); err != nil {
		t.Fatalf("acquire resource: %v", err)
	}
	if got := agent.snapshot(); got.Busy || got.Status != "active" || got.ActiveOps != 1 || len(got.Resources) != 1 || got.Resources[0] != "workspace:release" {
		t.Fatalf("resource-locked target should be active but not target-busy: %#v", got)
	}
	agent.releaseForAction(ActionPush, "workspace:release")
	if got := agent.snapshot(); got.Busy || got.Status != "idle" {
		t.Fatalf("released resource target should be idle: %#v", got)
	}
}
