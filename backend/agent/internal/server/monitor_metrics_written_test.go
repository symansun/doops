package server

// INT-48 contract: metrics_written is the number of metric rows a run left
// behind. Skill-driven runs persist their metrics into inspection_metric_points
// and the bridged snapshots, so a run whose metrics are readable from
// /v1/monitor/metrics must never report 0.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/user/doops/agent/api"
)

func openWrittenTestStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func writtenTestRun(store *GatewayStore, t *testing.T, runID string) monitorRunRecord {
	t.Helper()
	run := monitorRunRecord{
		ID:           runID,
		TriggerKind:  "manual",
		TriggeredBy:  "metrics-admin",
		Status:       "success",
		StartedAtMs:  1764000000000,
		FinishedAtMs: 1764000001000,
	}
	if err := store.CreateMonitorRun(context.Background(), run); err != nil {
		t.Fatalf("create monitor run: %v", err)
	}
	return run
}

func writtenTestItem(runID string) monitorRunItemRecord {
	return monitorRunItemRecord{
		RunID:           runID,
		Cluster:         "local",
		Instance:        "host-metrics",
		AssetID:         "inspect-vm-host",
		AssetName:       "host-metrics",
		AssetKind:       "vm",
		Strategy:        "skill:inspect-vm-host",
		CollectorMethod: "skill_script",
		StartedAtMs:     1764000000100,
		FinishedAtMs:    1764000000900,
		Status:          "pass",
		Checks:          []api.MonitorInspectCheck{{ID: "host.cpu_memory", Status: "pass", Detail: "ok"}},
		Evidence:        map[string]any{"skill_slug": "inspect-vm-host"},
	}
}

func countRunRows(t *testing.T, store *GatewayStore, table, runID string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE run_id=?", runID).Scan(&n); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return n
}

// TestMonitorRunCountsInspectionMetricRows pins the whole definition: the
// returned count, the persisted column, and the rows actually present in the
// metric series and snapshot tables agree.
func TestMonitorRunCountsInspectionMetricRows(t *testing.T) {
	store := openWrittenTestStore(t)
	const runID = "run_metrics_written_skill"
	artifact := `{"status":"pass","metrics":[
		{"key":"cpu_percent","value":42.5,"unit":"%"},
		{"key":"memory_percent","value":61.2,"unit":"%"},
		{"key":"disk_percent","value":71,"unit":"%"},
		{"key":"load_1m","value":1.75}
	]}`
	run := writtenTestRun(store, t, runID)
	item := writtenTestItem(runID)
	item.ArtifactJSON = json.RawMessage(artifact)

	written, err := store.PersistMonitorRunResults(context.Background(), run, []monitorRunItemRecord{item}, nil, nil)
	if err != nil {
		t.Fatalf("persist run: %v", err)
	}
	points := countRunRows(t, store, "inspection_metric_points", runID)
	snapshots := countRunRows(t, store, "monitor_resource_snapshots", runID)
	if points != 4 {
		t.Fatalf("inspection_metric_points rows = %d, want 4", points)
	}
	if snapshots != 1 {
		t.Fatalf("bridged snapshot rows = %d, want 1", snapshots)
	}
	if written != points+snapshots {
		t.Fatalf("metrics_written = %d, want %d (points %d + snapshots %d)", written, points+snapshots, points, snapshots)
	}
	var stored int
	if err := store.db.QueryRow("SELECT metrics_written FROM monitor_runs WHERE id=?", runID).Scan(&stored); err != nil {
		t.Fatalf("read stored metrics_written: %v", err)
	}
	if stored != written {
		t.Fatalf("stored metrics_written = %d, want the returned %d", stored, written)
	}
}

// TestMonitorRunWithStrategySnapshotSkipsTheBridge keeps the counter honest when
// the caller already supplied a richer snapshot for the same target: the bridge
// is skipped, so the count is the series rows plus that one supplied snapshot.
func TestMonitorRunWithStrategySnapshotSkipsTheBridge(t *testing.T) {
	store := openWrittenTestStore(t)
	const runID = "run_metrics_written_snapshot"
	artifact := `{"status":"pass","metrics":[
		{"key":"cpu_percent","value":11.5,"unit":"%"},
		{"key":"memory_percent","value":22.5,"unit":"%"}
	]}`
	run := writtenTestRun(store, t, runID)
	item := writtenTestItem(runID)
	item.ArtifactJSON = json.RawMessage(artifact)
	provided := []monitorResourceSnapshot{{
		Cluster:         "local",
		Instance:        "host-metrics",
		AssetID:         "inspect-vm-host",
		AssetKind:       "vm",
		Strategy:        "skill:inspect-vm-host",
		CollectorMethod: "skill_script",
	}}

	written, err := store.PersistMonitorRunResults(context.Background(), run, []monitorRunItemRecord{item}, provided, nil)
	if err != nil {
		t.Fatalf("persist run: %v", err)
	}
	points := countRunRows(t, store, "inspection_metric_points", runID)
	snapshots := countRunRows(t, store, "monitor_resource_snapshots", runID)
	if points != 2 {
		t.Fatalf("inspection_metric_points rows = %d, want 2", points)
	}
	if snapshots != 1 {
		t.Fatalf("snapshot rows = %d, want 1 (the supplied snapshot only)", snapshots)
	}
	if written != points+snapshots {
		t.Fatalf("metrics_written = %d, want %d", written, points+snapshots)
	}
}
