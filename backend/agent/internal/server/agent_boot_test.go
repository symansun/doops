package server

// INT-82b: the restart guard, its unknown-owner edge, and the persistence of the
// owning boot id.
//
// The guard is deliberately narrow: it fails a running record only when the
// owner is known and provably different. An Agent that predates the boot id, or
// a row written before the column existed, stays on the old reattach path.

import (
	"strings"
	"testing"
	"time"
)

func newBootGuardHub() (*GatewayHub, *executionRegistry) {
	registry := newExecutionRegistry(time.Minute)
	return &GatewayHub{client: &ClientService{executions: registry}}, registry
}

func bootGuardRecord(registry *executionRegistry, id, bootID string) {
	registry.begin(id, "doops_shell", "dev", "local", "user-boot")
	registry.bindAgent(id, bootID)
}

func TestExecutionRestartGuardOnlyFailsKnownDifferentOwner(t *testing.T) {
	hub, registry := newBootGuardHub()

	// Same process reconnecting: nothing to refuse.
	bootGuardRecord(registry, "exec-boot-same", "boot-a")
	if hub.executionAgentRestarted("exec-boot-same", &GatewayAgent{BootID: "boot-a"}) {
		t.Fatalf("same boot id was treated as a restart")
	}
	if record, _ := registry.get("exec-boot-same"); record.status != executionStatusRunning {
		t.Fatalf("same boot id changed the record to %q", record.status)
	}

	// Different process: refuse and land the record as failed.
	bootGuardRecord(registry, "exec-boot-diff", "boot-a")
	if !hub.executionAgentRestarted("exec-boot-diff", &GatewayAgent{BootID: "boot-b"}) {
		t.Fatalf("a different boot id was not treated as a restart")
	}
	record, _ := registry.get("exec-boot-diff")
	if record.status != executionStatusFailed {
		t.Fatalf("restart left the record in %q, want failed", record.status)
	}
	if !strings.Contains(record.errText, "execution_agent_restarted") || !strings.Contains(record.errText, "may have run") {
		t.Fatalf("restart error text = %q, want the category and the ambiguity", record.errText)
	}

	// Owner unknown (an Agent without a boot id): no fact to refuse on.
	bootGuardRecord(registry, "exec-boot-unknown", "")
	if record, _ := registry.get("exec-boot-unknown"); record.agentBootID != agentBootUnknown {
		t.Fatalf("unreported boot id stored as %q, want %q", record.agentBootID, agentBootUnknown)
	}
	if hub.executionAgentRestarted("exec-boot-unknown", &GatewayAgent{BootID: "boot-b"}) {
		t.Fatalf("an unknown owner must not be treated as a restart")
	}

	// Current Agent unknown (an old binary reattaching): also unjudgeable.
	bootGuardRecord(registry, "exec-boot-old-agent", "boot-a")
	if hub.executionAgentRestarted("exec-boot-old-agent", &GatewayAgent{}) {
		t.Fatalf("an Agent without a boot id must not be treated as a restart")
	}

	// A terminal record is never touched by the guard.
	registry.finish("exec-boot-same", executionStatusSucceeded, "", nil)
	if hub.executionAgentRestarted("exec-boot-same", &GatewayAgent{BootID: "boot-c"}) {
		t.Fatalf("a terminal record must not be re-judged")
	}
}

func TestExecutionBootIDSurvivesGatewayRestart(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	registry := f.hub.client.executions
	bootGuardRecord(registry, "exec-boot-persist", "boot-a")

	var stored string
	if err := f.store.db.QueryRow(
		"SELECT agent_boot_id FROM execution_records WHERE execution_id = ?", "exec-boot-persist").Scan(&stored); err != nil {
		t.Fatalf("read the stored boot id: %v", err)
	}
	if stored != "boot-a" {
		t.Fatalf("stored agent_boot_id = %q, want boot-a", stored)
	}

	restored := newExecutionRegistry(time.Minute)
	if err := restored.restore(f.store, time.Now()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	record, ok := restored.get("exec-boot-persist")
	if !ok {
		t.Fatalf("record did not come back")
	}
	if record.agentBootID != "boot-a" {
		t.Fatalf("restored agent_boot_id = %q, want boot-a", record.agentBootID)
	}
}

func TestExecutionBootIDRoundTripsOnMySQL(t *testing.T) {
	gatewayMySQLCredentialE2EPort(t)
	store := openGatewayMySQLCredentialE2EStore(t)
	registry := newExecutionRegistry(time.Minute)
	registry.store = store
	bootGuardRecord(registry, "exec-boot-mysql", "boot-mysql")

	restored := newExecutionRegistry(time.Minute)
	if err := restored.restore(store, time.Now()); err != nil {
		t.Fatalf("restore from MySQL: %v", err)
	}
	record, ok := restored.get("exec-boot-mysql")
	if !ok {
		t.Fatalf("record did not come back from MySQL")
	}
	if record.agentBootID != "boot-mysql" {
		t.Fatalf("agent_boot_id from MySQL = %q, want boot-mysql", record.agentBootID)
	}
}
