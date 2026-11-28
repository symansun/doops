package server

// INT-58: D17 already proved the MySQL schema and SQL renderings one statement
// at a time. This file drives the whole inspection-retention path on a real
// mysql:8.4 store opened through the production DOOPS_GATEWAY_STORE=mysql
// contract: a run and its artifact/metrics are written through the store API,
// PruneMonitorRetention removes them, the post-run throttle announces itself
// once across three triggers, and audit retention deletes a completed row
// through the MySQL rendering. Unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT skips;
// a set but unreachable port fails (INT-14).

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedMonitorRetentionMySQLRun writes one run, a file-backed item carrying one
// metric, and its snapshot through the same store APIs the Gateway uses. The
// item's ArtifactJSON also feeds inspection_metric_points, so a later prune
// exercises the MySQL batch-delete against rows this process actually wrote.
func seedMonitorRetentionMySQLRun(t *testing.T, store *GatewayStore, runID, scheduleID string, startedAtMs int64, withArtifact bool) string {
	t.Helper()
	ctx := context.Background()
	run := monitorRunRecord{
		ID:          runID,
		ScheduleID:  scheduleID,
		TriggerKind: "scheduled",
		TriggeredBy: "retention-mysql-test",
		Status:      "running",
		StartedAtMs: startedAtMs,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("create run %s: %v", runID, err)
	}
	item := monitorRunItemRecord{
		Cluster:         "local",
		Instance:        "host-retention-mysql",
		AssetID:         "inspect-vm-host",
		AssetKind:       "skill",
		Strategy:        "skill:inspect-vm-host",
		CollectorMethod: "skill_script",
		Status:          "pass",
		ArtifactJSON:    json.RawMessage(`{"metrics":[{"key":"cpu_percent","label":"CPU","value":42.5,"unit":"%"}]}`),
	}
	if withArtifact {
		item.ArtifactFormat = "markdown"
		item.ArtifactMarkdown = "### retention mysql " + runID
	}
	snapshots := []monitorResourceSnapshot{{
		Cluster:       item.Cluster,
		Instance:      item.Instance,
		AssetID:       item.AssetID,
		AssetName:     "retention host",
		AssetKind:     item.AssetKind,
		Strategy:      item.Strategy,
		CollectedAtMs: startedAtMs,
		RunID:         runID,
		Status:        "pass",
		Online:        true,
	}}
	run.Status = "success"
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, snapshots, nil); err != nil {
		t.Fatalf("persist run %s: %v", runID, err)
	}
	if !withArtifact {
		return ""
	}
	row := readPersistedArtifactRow(t, store, runID)
	return filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(row.dir))
}

func monitorRetentionMySQLCount(t *testing.T, store *GatewayStore, query string, args ...any) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return count
}

func TestMonitorRetentionMySQLEndToEnd(t *testing.T) {
	if os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT") == "" {
		t.Skip("unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip the real mysql:8.4 retention test")
	}
	resetMonitorRetentionGate()
	store := openGatewayMySQLCredentialE2EStore(t)
	if store.StoreBackend() != "mysql" || gatewayDialect() != gatewayDialectMySQL {
		t.Fatalf("retention end-to-end must run on MySQL, got backend=%q dialect=%v", store.StoreBackend(), gatewayDialect())
	}
	now := time.Now().UTC()
	oldStarted := now.Add(-40 * 24 * time.Hour).UnixMilli()
	recentStarted := now.Add(-2 * time.Hour).UnixMilli()

	oldDir := seedMonitorRetentionMySQLRun(t, store, "run_ret_mysql_old", "sched_ret_mysql", oldStarted, true)
	recentDir := seedMonitorRetentionMySQLRun(t, store, "run_ret_mysql_recent", "sched_ret_mysql", recentStarted, true)
	if oldDir == "" || recentDir == "" {
		t.Fatal("both seeded runs must have a persisted artifact directory")
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM inspection_metric_points WHERE run_id=?", "run_ret_mysql_old"); got != 1 {
		t.Fatalf("old run metric rows = %d, want 1 (the store API must have written the expiry candidate)", got)
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM monitor_resource_snapshots WHERE run_id=?", "run_ret_mysql_old"); got != 1 {
		t.Fatalf("old run snapshot rows = %d, want 1", got)
	}

	// Completed audit rows: one outside the retention window and one inside it.
	oldAudit, err := store.StartAudit(AuditEvent{UserID: "retention-mysql", Action: GatewayAction("test:retention:old"), StartedAt: now.Add(-2 * time.Hour)})
	if err != nil {
		t.Fatalf("start old audit event: %v", err)
	}
	if err := store.FinishAudit(oldAudit, AuditFinish{Status: "success", EndedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("finish old audit event: %v", err)
	}
	freshAudit, err := store.StartAudit(AuditEvent{UserID: "retention-mysql", Action: GatewayAction("test:retention:fresh"), StartedAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatalf("start fresh audit event: %v", err)
	}
	if err := store.FinishAudit(freshAudit, AuditFinish{Status: "success", EndedAt: now.Add(-time.Minute)}); err != nil {
		t.Fatalf("finish fresh audit event: %v", err)
	}

	var logs strings.Builder
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	// The first post-run trigger is outside the throttle window and must scan.
	first := store.PruneMonitorRetention(now, retentionTestPolicy(30, 30, 1), false)
	if first.SkippedThrottled {
		t.Fatalf("the first post-run trigger must scan, got %s", first.summary())
	}
	if first.RunsDeleted != 1 || first.ItemsDeleted != 1 || first.MetricsDeleted != 1 || first.DirsRemoved != 1 {
		t.Fatalf("MySQL retention result = %s, want runs=1 items=1 metrics=1 dirs=1", first.summary())
	}
	if monitorRetentionRunExists(t, store, "run_ret_mysql_old") {
		t.Fatal("expired run must be deleted on MySQL")
	}
	if !monitorRetentionRunExists(t, store, "run_ret_mysql_recent") {
		t.Fatal("the newest run of the schedule must survive the keep-runs floor")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expired artifact directory must be removed on MySQL, stat err=%v", err)
	}
	if _, err := os.Stat(recentDir); err != nil {
		t.Fatalf("recent artifact directory must survive: %v", err)
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM inspection_metric_points WHERE run_id=?", "run_ret_mysql_old"); got != 0 {
		t.Fatalf("old metric rows after prune = %d, want 0", got)
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM inspection_metric_points WHERE run_id=?", "run_ret_mysql_recent"); got != 1 {
		t.Fatalf("recent metric rows after prune = %d, want 1", got)
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM monitor_resource_snapshots WHERE run_id=?", "run_ret_mysql_old"); got != 0 {
		t.Fatalf("old snapshot rows after prune = %d, want 0", got)
	}

	// Three further post-run triggers stay inside the throttle and must announce
	// their deferral exactly once, the INT-42 contract, now on MySQL.
	for i := 1; i <= 3; i++ {
		skip := store.PruneMonitorRetention(now.Add(time.Duration(i)*time.Minute), retentionTestPolicy(30, 30, 1), false)
		if !skip.SkippedThrottled {
			t.Fatalf("post-run attempt %d must be throttled, got %s", i, skip.summary())
		}
		if skip.ThrottledFor <= 0 || skip.ThrottleWindow != timeouts.MonitorRetentionMinInterval {
			t.Fatalf("attempt %d must explain the throttle, got for=%s window=%s", i, skip.ThrottledFor, skip.ThrottleWindow)
		}
	}
	if got := strings.Count(logs.String(), "monitor retention deferred"); got != 1 {
		t.Fatalf("throttled skips must be announced once, got %d in %q", got, logs.String())
	}
	if !strings.Contains(logs.String(), "artifacts=30d") || !strings.Contains(logs.String(), "metrics=30d") {
		t.Fatalf("deferral line must name the pending windows, got %q", logs.String())
	}

	// Audit retention shares the store but not the monitor throttle. The MySQL
	// completed-audit statement has to delete only the expired row and leave the
	// in-window row and the purge receipt in place.
	purged, err := store.PurgeExpiredAudit(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("purge expired audit on MySQL: %v", err)
	}
	if purged.Deleted != 1 {
		t.Fatalf("audit purge deleted %d rows, want exactly the one expired row (result=%+v)", purged.Deleted, purged)
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM audit_events WHERE id=?", oldAudit); got != 0 {
		t.Fatalf("expired audit row %d still present after purge", oldAudit)
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM audit_events WHERE id=?", freshAudit); got != 1 {
		t.Fatalf("in-window audit row %d was deleted by the purge", freshAudit)
	}
	if got := monitorRetentionMySQLCount(t, store, "SELECT COUNT(*) FROM audit_events WHERE action='audit:purge' AND status='success'"); got != 1 {
		t.Fatalf("audit purge receipts = %d, want exactly one completed receipt", got)
	}
}
