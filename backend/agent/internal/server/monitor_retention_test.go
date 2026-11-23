package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SF-15 contract: artifacts and metric points have a lifecycle. These tests pin
// the rules in SKILL_FIRST_INSPECTION_SPEC.md 3.5: age windows, the per-schedule
// keep-runs floor, running-run protection, files-before-rows ordering, the
// metric window outliving the artifact window, the forced hourly fallback, and
// the "log and drop the row anyway" failure mode.

// resetMonitorRetentionGate clears the process-wide throttle so one test cannot
// leak its window into the next.
func resetMonitorRetentionGate() {
	monitorRetentionGate.lastMs.Store(0)
}

func retentionTestPolicy(artifactDays, metricDays, keepRuns int) monitorRetentionPolicy {
	return monitorRetentionPolicy{ArtifactDays: artifactDays, MetricDays: metricDays, KeepRuns: keepRuns}
}

// seedMonitorRetentionRun inserts one run plus an optional file-backed item and
// returns the absolute item directory ("" when no artifact was requested).
func seedMonitorRetentionRun(t *testing.T, store *GatewayStore, runID, scheduleID string, startedAtMs int64, status string, withArtifact bool) string {
	t.Helper()
	ctx := context.Background()
	run := monitorRunRecord{
		ID:          runID,
		ScheduleID:  scheduleID,
		TriggerKind: "scheduled",
		TriggeredBy: "retention-test",
		Status:      "running",
		StartedAtMs: startedAtMs,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("create run %s: %v", runID, err)
	}
	item := monitorRunItemRecord{
		Cluster:         "local",
		Instance:        "host-retention",
		AssetID:         "inspect-vm-host",
		AssetKind:       "skill",
		Strategy:        "skill:inspect-vm-host",
		CollectorMethod: "skill_script",
		Status:          "pass",
	}
	if withArtifact {
		item.ArtifactFormat = "markdown"
		item.ArtifactMarkdown = "### retention " + runID
	}
	run.Status = status
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, nil, nil); err != nil {
		t.Fatalf("persist run %s: %v", runID, err)
	}
	if withArtifact {
		row := readPersistedArtifactRow(t, store, runID)
		return filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(row.dir))
	}
	return ""
}

func monitorRetentionRunExists(t *testing.T, store *GatewayStore, runID string) bool {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM monitor_runs WHERE id=?", runID).Scan(&count); err != nil {
		t.Fatalf("count run %s: %v", runID, err)
	}
	return count > 0
}

func TestMonitorRetentionArtifactWindowKeepsRecentRuns(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	oldDir := seedMonitorRetentionRun(t, store, "run_ret_old", "sched_ret_a", now.Add(-40*24*time.Hour).UnixMilli(), "success", true)
	recentDir := seedMonitorRetentionRun(t, store, "run_ret_new", "sched_ret_a", now.Add(-2*time.Hour).UnixMilli(), "success", true)

	result := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), true)
	if result.RunsDeleted != 1 || result.DirsRemoved != 1 {
		t.Fatalf("expected the expired run and its directory to go, got %s", result.summary())
	}
	if monitorRetentionRunExists(t, store, "run_ret_old") {
		t.Fatal("expired run must be deleted")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expired artifact directory must be removed, stat err=%v", err)
	}
	if !monitorRetentionRunExists(t, store, "run_ret_new") {
		t.Fatal("run inside the window must survive")
	}
	if _, err := os.Stat(recentDir); err != nil {
		t.Fatalf("recent artifact directory must survive: %v", err)
	}
}

func TestMonitorRetentionKeepRunsProtectsScheduleHistory(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	// A schedule that has not run for weeks: every run is outside the window,
	// but the newest two must survive so a woken schedule keeps its history.
	seedMonitorRetentionRun(t, store, "run_ret_keep_newest", "sched_ret_keep", now.Add(-40*24*time.Hour).UnixMilli(), "success", false)
	seedMonitorRetentionRun(t, store, "run_ret_keep_second", "sched_ret_keep", now.Add(-41*24*time.Hour).UnixMilli(), "success", false)
	seedMonitorRetentionRun(t, store, "run_ret_keep_third", "sched_ret_keep", now.Add(-42*24*time.Hour).UnixMilli(), "success", false)
	// Manual runs ("" schedule group) keep their own newest run.
	seedMonitorRetentionRun(t, store, "run_ret_manual", "", now.Add(-45*24*time.Hour).UnixMilli(), "success", false)

	result := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 2), true)
	if result.RunsDeleted != 1 {
		t.Fatalf("only the third-oldest scheduled run may expire, got %s", result.summary())
	}
	for _, keep := range []string{"run_ret_keep_newest", "run_ret_keep_second", "run_ret_manual"} {
		if !monitorRetentionRunExists(t, store, keep) {
			t.Fatalf("run %s must be protected by the keep-runs floor", keep)
		}
	}
	if monitorRetentionRunExists(t, store, "run_ret_keep_third") {
		t.Fatal("the run beyond the keep-runs floor must expire")
	}
}

func TestMonitorRetentionSkipsRunningRuns(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	dir := seedMonitorRetentionRun(t, store, "run_ret_running", "sched_ret_run", now.Add(-40*24*time.Hour).UnixMilli(), "running", true)
	// A newer run takes the keep-runs slot so only the running protection can
	// save the expired one.
	seedMonitorRetentionRun(t, store, "run_ret_running_newer", "sched_ret_run", now.Add(-time.Hour).UnixMilli(), "success", false)

	result := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), true)
	if result.RunsDeleted != 0 {
		t.Fatalf("a running run must never be deleted, got %s", result.summary())
	}
	if !monitorRetentionRunExists(t, store, "run_ret_running") {
		t.Fatal("running run row must survive the scan")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("running run artifacts must survive: %v", err)
	}
}

func TestMonitorRetentionMetricsOutliveArtifactRows(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	collectedAtMs := now.Add(-40 * 24 * time.Hour).UnixMilli()
	seedMonitorRetentionRun(t, store, "run_ret_metrics", "sched_ret_metrics", collectedAtMs, "success", false)
	seedMonitorRetentionRun(t, store, "run_ret_metrics_newer", "sched_ret_metrics", now.Add(-time.Hour).UnixMilli(), "success", false)
	if _, err := store.db.Exec("INSERT INTO inspection_metric_points(collected_at_ms, run_id, schedule_id, cluster, instance, skill_slug, metric_key, value) VALUES(?,?,?,?,?,?,?,?)",
		collectedAtMs, "run_ret_metrics", "sched_ret_metrics", "local", "host-retention", "inspect-vm-host", "host.cpu_percent", 42.5); err != nil {
		t.Fatalf("insert metric point: %v", err)
	}

	store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), true)
	if monitorRetentionRunExists(t, store, "run_ret_metrics") {
		t.Fatal("artifact rows must expire after 30 days")
	}
	var points int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM inspection_metric_points WHERE run_id=?", "run_ret_metrics").Scan(&points); err != nil {
		t.Fatalf("count metric points: %v", err)
	}
	if points != 1 {
		t.Fatalf("metric points must outlive the artifact window (90d), got %d rows", points)
	}
	store.PruneMonitorRetention(now, retentionTestPolicy(30, 30, 1), true)
	if err := store.db.QueryRow("SELECT COUNT(*) FROM inspection_metric_points WHERE run_id=?", "run_ret_metrics").Scan(&points); err != nil {
		t.Fatalf("count metric points after metric window: %v", err)
	}
	if points != 0 {
		t.Fatalf("metric points past the 30d metric window must be deleted, got %d rows", points)
	}
}

func TestMonitorRetentionRemovesFilesBeforeIndexRows(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	dir := seedMonitorRetentionRun(t, store, "run_ret_order", "sched_ret_order", now.Add(-40*24*time.Hour).UnixMilli(), "success", true)
	seedMonitorRetentionRun(t, store, "run_ret_order_newer", "sched_ret_order", now.Add(-time.Hour).UnixMilli(), "success", false)

	result := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), true)
	if result.DirsRemoved != 1 {
		t.Fatalf("the artifact directory must be removed by the same scan, got %s", result.summary())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("artifact directory must be gone, stat err=%v", err)
	}
	// A successful scan may not leave any index row pointing at a directory that
	// no longer exists: that is exactly the state the files-before-rows order
	// prevents (a failing index delete is covered by
	// TestMonitorRetentionFilesGoBeforeRowsEvenWhenRowDeleteFails).
	rows, err := store.db.Query("SELECT run_id, artifact_dir FROM monitor_run_items WHERE artifact_dir != ''")
	if err != nil {
		t.Fatalf("list artifact rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID, rel string
		if err := rows.Scan(&runID, &rel); err != nil {
			t.Fatalf("scan artifact row: %v", err)
		}
		if _, err := os.Stat(filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(rel))); err != nil {
			t.Fatalf("index row run=%s dir=%s dangles: %v", runID, rel, err)
		}
	}
}

// TestMonitorRetentionFilesGoBeforeRowsEvenWhenRowDeleteFails pins the deletion
// order directly: the payload directory is already gone while the index delete
// is still failing, which is the state a crash between the two steps produces.
func TestMonitorRetentionFilesGoBeforeRowsEvenWhenRowDeleteFails(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	dir := seedMonitorRetentionRun(t, store, "run_ret_order_fail", "sched_ret_order_fail", now.Add(-40*24*time.Hour).UnixMilli(), "success", true)
	seedMonitorRetentionRun(t, store, "run_ret_order_fail_newer", "sched_ret_order_fail", now.Add(-time.Hour).UnixMilli(), "success", false)
	if _, err := store.db.Exec("CREATE TRIGGER ret_block_item_delete BEFORE DELETE ON monitor_run_items BEGIN SELECT RAISE(ABORT, 'blocked by retention ordering test'); END;"); err != nil {
		t.Fatalf("create delete-blocking trigger: %v", err)
	}

	result := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), true)
	if result.DirsRemoved != 1 {
		t.Fatalf("the payload must be removed before the index delete is attempted, got %s", result.summary())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("payload directory must be gone even though the row delete failed, stat err=%v", err)
	}
	if !monitorRetentionRunExists(t, store, "run_ret_order_fail") {
		t.Fatal("the blocked index delete must leave the run row behind, otherwise the ordering is not observable")
	}
	if result.RunsDeleted != 0 {
		t.Fatalf("no run may be reported deleted when the row delete failed, got %s", result.summary())
	}
}

func TestMonitorRetentionThrottlesPostRunTriggerButNotFallback(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	seedMonitorRetentionRun(t, store, "run_ret_first", "sched_ret_throttle", now.Add(-40*24*time.Hour).UnixMilli(), "success", false)
	seedMonitorRetentionRun(t, store, "run_ret_throttle_anchor", "sched_ret_throttle", now.Add(-time.Hour).UnixMilli(), "success", false)

	first := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), false)
	if first.SkippedThrottled || first.RunsDeleted != 1 {
		t.Fatalf("the first post-run trigger must scan, got %s", first.summary())
	}

	seedMonitorRetentionRun(t, store, "run_ret_second", "sched_ret_throttle", now.Add(-41*24*time.Hour).UnixMilli(), "success", false)
	second := store.PruneMonitorRetention(now.Add(2*time.Minute), retentionTestPolicy(30, 90, 1), false)
	if !second.SkippedThrottled {
		t.Fatalf("a post-run trigger inside the window must be throttled, got %s", second.summary())
	}
	if !monitorRetentionRunExists(t, store, "run_ret_second") {
		t.Fatal("a throttled scan must not delete anything")
	}

	// The hourly fallback always scans: throttling can never switch cleanup off.
	fallback := store.PruneMonitorRetention(now.Add(3*time.Minute), retentionTestPolicy(30, 90, 1), true)
	if fallback.SkippedThrottled || fallback.RunsDeleted != 1 {
		t.Fatalf("the hourly fallback must ignore the throttle, got %s", fallback.summary())
	}
	// PruneMonitorEngineHistory is that fallback and must stay forced even when
	// the throttle window is open.
	seedMonitorRetentionRun(t, store, "run_ret_third", "sched_ret_throttle", now.Add(-42*24*time.Hour).UnixMilli(), "success", false)
	t.Setenv(monitorArtifactKeepRunsEnv, "1")
	store.PruneMonitorEngineHistory(now.Add(4 * time.Minute))
	if monitorRetentionRunExists(t, store, "run_ret_third") {
		t.Fatal("PruneMonitorEngineHistory must keep its force=true semantics")
	}
}

func TestMonitorRetentionDropsRowsWhenDirectoryRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only directory used to force a removal failure")
	}
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	dir := seedMonitorRetentionRun(t, store, "run_ret_locked", "sched_ret_locked", now.Add(-40*24*time.Hour).UnixMilli(), "success", true)
	seedMonitorRetentionRun(t, store, "run_ret_locked_newer", "sched_ret_locked", now.Add(-time.Hour).UnixMilli(), "success", false)
	// Removing the payload requires write permission on the item directory
	// itself, so a read-only item directory is exactly the "read-only mount"
	// failure the retention path must survive.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod artifact item dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	result := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), true)
	if result.DirFailures == 0 {
		t.Fatalf("a read-only payload directory must be reported as a failure, got %s", result.summary())
	}
	if monitorRetentionRunExists(t, store, "run_ret_locked") {
		t.Fatal("the index row must be dropped even when the payload directory survives")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the read-only payload must still exist, otherwise the failure was not exercised: %v", err)
	}
}

func TestMonitorRetentionPolicyFromEnv(t *testing.T) {
	defaults := monitorRetentionPolicyFromEnv()
	if defaults.ArtifactDays != defaultMonitorArtifactRetentionDays || defaults.MetricDays != defaultMonitorMetricRetentionDays || defaults.KeepRuns != defaultMonitorArtifactKeepRuns {
		t.Fatalf("unset env must resolve to defaults, got %+v", defaults)
	}
	t.Setenv(monitorArtifactRetentionDaysEnv, "7")
	t.Setenv(monitorMetricRetentionDaysEnv, "180")
	t.Setenv(monitorArtifactKeepRunsEnv, "5")
	overridden := monitorRetentionPolicyFromEnv()
	if overridden.ArtifactDays != 7 || overridden.MetricDays != 180 || overridden.KeepRuns != 5 {
		t.Fatalf("valid overrides must win, got %+v", overridden)
	}
	for name, value := range map[string]string{
		monitorArtifactRetentionDaysEnv: "thirty",
		monitorMetricRetentionDaysEnv:   "0",
		monitorArtifactKeepRunsEnv:      "-3",
	} {
		t.Setenv(name, value)
	}
	invalid := monitorRetentionPolicyFromEnv()
	if invalid.ArtifactDays != defaultMonitorArtifactRetentionDays || invalid.MetricDays != defaultMonitorMetricRetentionDays || invalid.KeepRuns != defaultMonitorArtifactKeepRuns {
		t.Fatalf("invalid overrides must fall back to defaults, got %+v", invalid)
	}
}

func TestMonitorRetentionZeroDaysKeepsEverything(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	dir := seedMonitorRetentionRun(t, store, "run_ret_forever", "sched_ret_forever", now.Add(-400*24*time.Hour).UnixMilli(), "success", true)
	result := store.PruneMonitorRetention(now, retentionTestPolicy(0, 0, 50), true)
	if result.RunsDeleted != 0 || result.MetricsDeleted != 0 || result.DirsRemoved != 0 {
		t.Fatalf("a disabled window must keep everything, got %s", result.summary())
	}
	if !monitorRetentionRunExists(t, store, "run_ret_forever") {
		t.Fatal("disabled retention must not delete the run")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("disabled retention must not delete artifacts: %v", err)
	}
	if !strings.Contains(result.summary(), "throttled=false") {
		t.Fatalf("summary must stay readable, got %q", result.summary())
	}
}
