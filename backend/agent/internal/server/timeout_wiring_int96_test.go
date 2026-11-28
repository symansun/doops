package server

// INT-96: the last hand-written literals in the hub and the gateway command
// moved onto the same table. These tests drive the real call sites with a
// non-default value, so a site that keeps its old literal turns red.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openWiringStore opens a throwaway SQLite store for these wiring tests.
func openWiringStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestAgentLeaseAndQueueDefaultsComeFromTheTable replaces the 90s lease and the
// 2m queue fallback with table values: the registration handler must enforce
// the table lease, and the hub must keep the table queue wait.
func TestAgentLeaseAndQueueDefaultsComeFromTheTable(t *testing.T) {
	withTimeoutField(t, func(d *timeoutDefaults) {
		d.AgentLease = 5 * time.Minute
		d.TargetQueueTimeout = 3 * time.Second
	})
	hub := NewGatewayHub(openWiringStore(t), GatewayHubOptions{})
	if hub.opts.AgentLease != 5*time.Minute {
		t.Fatalf("hub lease = %s, want the 5m table value", hub.opts.AgentLease)
	}
	if hub.registration.lease != 5*time.Minute {
		t.Fatalf("registration read deadline = %s, want the 5m table value", hub.registration.lease)
	}
	if hub.opts.TargetQueueTimeout != 3*time.Second {
		t.Fatalf("queue wait = %s, want the 3s table value", hub.opts.TargetQueueTimeout)
	}
}

// TestHeartbeatFlusherFallbackUsesTheTableCadence hits the fallback branch: with
// no lease left to divide, the flusher must tick at the table period. A 40ms
// table entry persists a heartbeat that the old 30s literal would still hold in
// memory.
func TestHeartbeatFlusherFallbackUsesTheTableCadence(t *testing.T) {
	withTimeoutField(t, func(d *timeoutDefaults) { d.AgentHeartbeatFlush = 40 * time.Millisecond })
	store := openWiringStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Hour, AllowOpenAgentRegistration: true})
	t.Cleanup(hub.bgCancel)
	agent := newGatewayAgentSession("wiring", "flush", "", "198.51.100.9:1", nil, nil)
	if err := hub.registry.Register(agent); err != nil {
		t.Fatalf("register: %v", err)
	}
	future := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Millisecond)
	agent.touchMemory(future)
	hub.opts.AgentLease = 0
	hub.StartHeartbeatFlusher(0)
	deadline := time.Now().Add(2 * time.Second)
	for {
		statuses, err := store.ListAgentStatus()
		if err != nil {
			t.Fatalf("list status: %v", err)
		}
		persisted := false
		for _, status := range statuses {
			if status.Cluster == "wiring" && status.Instance == "flush" && status.LastSeen.Equal(future) {
				persisted = true
			}
		}
		if persisted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat was not flushed at the 40ms table cadence; the old 30s literal is still in charge")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPlatformLoadSampleRetentionComesFromTheTable ages two samples and checks
// which survive under a one-hour table window versus the 30-day default. The
// old hard-coded 30 days would keep both in the first case.
func TestPlatformLoadSampleRetentionComesFromTheTable(t *testing.T) {
	now := time.Time{}
	seed := func(t *testing.T) *GatewayStore {
		t.Helper()
		store := openWiringStore(t)
		now = time.Now().UTC().Truncate(time.Millisecond)
		for _, age := range []time.Duration{30 * time.Minute, 2 * time.Hour} {
			if err := store.RecordPlatformLoadSample(PlatformLoadSample{AtMs: now.Add(-age).UnixMilli(), OnlineAgents: 1}); err != nil {
				t.Fatalf("record sample: %v", err)
			}
		}
		return store
	}
	count := func(t *testing.T, store *GatewayStore) int {
		t.Helper()
		var n int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM platform_load_samples`).Scan(&n); err != nil {
			t.Fatalf("count samples: %v", err)
		}
		return n
	}
	withTimeoutField(t, func(d *timeoutDefaults) { d.PlatformLoadSampleRetention = time.Hour })
	store := seed(t)
	if _, err := store.PruneOfflineAgentStatus(now); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got := count(t, store); got != 1 {
		t.Fatalf("a 1h table window kept %d samples, want only the 30m one", got)
	}

	saved := timeouts
	timeouts.PlatformLoadSampleRetention = 30 * 24 * time.Hour
	t.Cleanup(func() { timeouts = saved })
	defaultStore := seed(t)
	if _, err := defaultStore.PruneOfflineAgentStatus(now); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got := count(t, defaultStore); got != 2 {
		t.Fatalf("the 30d default window dropped a 2h sample: %d left", got)
	}
}

// TestAuditAndTokenSweepIntervalsAreIndependent moves one interval and asserts
// the other keeps its 6h default, in both directions.
func TestAuditAndTokenSweepIntervalsAreIndependent(t *testing.T) {
	withTimeoutField(t, func(d *timeoutDefaults) { d.AuditRetentionInterval = 45 * time.Minute })
	if got := AuditRetentionInterval(); got != 45*time.Minute {
		t.Fatalf("audit interval = %s, want the 45m table value", got)
	}
	if got := ExpiredTokenSweepInterval(); got != 6*time.Hour {
		t.Fatalf("moving the audit sweep also moved the token sweep to %s", got)
	}
	withTimeoutField(t, func(d *timeoutDefaults) {
		d.AuditRetentionInterval = 6 * time.Hour
		d.ExpiredTokenSweepInterval = 2 * time.Hour
	})
	if got := AuditRetentionInterval(); got != 6*time.Hour {
		t.Fatalf("moving the token sweep also moved the audit sweep to %s", got)
	}
	if got := ExpiredTokenSweepInterval(); got != 2*time.Hour {
		t.Fatalf("token sweep = %s, want the 2h table value", got)
	}
}

// TestGatewayCommandReadsTheLoopPeriodsFromTheTable pins the process-level
// wiring that package tests cannot execute: the flag default and the periods of
// the background loops must come from the table, never from a literal.
func TestGatewayCommandReadsTheLoopPeriodsFromTheTable(t *testing.T) {
	path := filepath.Join("..", "..", "pkg", "gatewaycmd", "gatewaycmd.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(source)
	for _, want := range []string{
		`fs.Duration("target-queue-timeout", server.TargetQueueTimeout()`,
		`hub.StartHeartbeatFlusher(server.AgentHeartbeatFlush())`,
		`server.AuditRetentionInterval()`,
		`server.ExpiredTokenSweepInterval()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("gatewaycmd.go no longer contains %s", want)
		}
	}
	if len(text) < 10000 {
		t.Fatalf("scanned only %d bytes of gatewaycmd.go; the file changed shape", len(text))
	}
}
