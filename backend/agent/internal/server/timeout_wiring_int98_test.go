package server

import (
	"encoding/json"
	"testing"
	"time"
)

// TestMonitorTickDrivesScheduledRuns proves the monitor runner keeps claiming
// due schedules on every tick: after each run the schedule is re-aged and a
// short tick picks it up again. A runner that only claims once (or a one-hour
// tick) cannot produce the second run inside the two-second window.
func TestMonitorTickDrivesScheduledRuns(t *testing.T) {
	f := monitorEngineFixture(t)
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	putInstanceStatus(t, f.store, "dev", "tick-offline", "offline", time.Now().UTC().Add(-time.Minute))

	body := "{\"action\":\"create\",\"name\":\"tick schedule\",\"scope_mode\":\"targets\"," +
		"\"targets\":[{\"cluster\":\"dev\",\"instance\":\"tick-offline\"}],\"skills\":[],\"interval_seconds\":60}"
	raw := f.http(t, f.admin, "POST", "/v1/monitor/schedules", body, 200)
	var created struct {
		Schedule monitorScheduleRecord `json:"schedule"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Schedule.ID == "" {
		t.Fatalf("create schedule: %s (%v)", raw, err)
	}
	id := created.Schedule.ID
	age := func() {
		if _, err := f.store.db.Exec("UPDATE monitor_schedules SET next_run_at_ms = ? WHERE id = ?",
			time.Now().UTC().Add(-time.Second).UnixMilli(), id); err != nil {
			t.Fatalf("age schedule: %v", err)
		}
	}
	waitRuns := func(want int) {
		deadline := time.Now().Add(2 * time.Second)
		for {
			var runs int
			if err := f.store.db.QueryRow("SELECT COUNT(*) FROM monitor_runs WHERE schedule_id = ?", id).Scan(&runs); err != nil {
				t.Fatalf("count runs: %v", err)
			}
			if runs >= want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("a 50ms monitor tick produced %d runs in 2s, want %d", runs, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	age()
	f.hub.StartMonitorRunner(50 * time.Millisecond)
	waitRuns(1)
	age()
	waitRuns(2)
}

func hasAgentInstance(t *testing.T, store *GatewayStore, cluster, instance string) bool {
	t.Helper()
	statuses, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list agent status: %v", err)
	}
	for _, status := range statuses {
		if status.Cluster == cluster && status.Instance == instance {
			return true
		}
	}
	return false
}

// TestOfflineInstanceRetentionComesFromTheTimeoutTable proves the reaper uses
// the configured retention window instead of the retired 90-day constant.
func TestOfflineInstanceRetentionComesFromTheTimeoutTable(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	hub := &GatewayHub{store: store}
	putInstanceStatus(t, store, "dev", "short-lived", "offline", time.Now().UTC().Add(-5*time.Second))

	// With the shipped 90-day default the row is kept.
	hub.pruneOfflineInstances()
	if !hasAgentInstance(t, store, "dev", "short-lived") {
		t.Fatal("5s-old offline row was pruned with the 90-day default")
	}

	withTimeoutField(t, func(d *timeoutDefaults) { d.OfflineInstanceRetention = time.Second })
	hub.pruneOfflineInstances()
	if hasAgentInstance(t, store, "dev", "short-lived") {
		t.Fatal("offline row survived a 1s retention window; the reaper still uses a hardcoded window")
	}
}
