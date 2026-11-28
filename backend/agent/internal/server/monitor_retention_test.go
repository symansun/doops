package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// --- INT-34: the batched metric delete must remove the same rows on both backends ---

// metricPruneParityExpired is larger than two retention batches, so one batch is
// never the whole table: LIMIT has to select rows, not merely permit the
// statement.
const metricPruneParityExpired = 620

type metricPruneParityRow struct {
	collectedAtMs int64
	metricKey     string
}

// metricPruneParityFixture lays identical data into both backends. Timestamps do
// not follow insertion order and ten rows share each timestamp, so a statement
// that ordered by collected_at_ms alone, or that let the engine pick arbitrary
// rows, would answer differently on the two servers.
func metricPruneParityFixture(cutoffMs int64) []metricPruneParityRow {
	rows := make([]metricPruneParityRow, 0, metricPruneParityExpired+5)
	for i := 0; i < metricPruneParityExpired; i++ {
		bucket := int64(i % 62)
		rows = append(rows, metricPruneParityRow{
			collectedAtMs: cutoffMs - 1000 - bucket*1000,
			metricKey:     fmt.Sprintf("expired.%03d", i),
		})
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, metricPruneParityRow{
			collectedAtMs: cutoffMs + int64(i+1)*1000,
			metricKey:     fmt.Sprintf("fresh.%d", i),
		})
	}
	return rows
}

func insertMetricPruneParityRows(t *testing.T, db *sql.DB, rows []metricPruneParityRow) {
	t.Helper()
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO inspection_metric_points(
			collected_at_ms, run_id, schedule_id, cluster, instance, skill_slug,
			metric_key, metric_label, value, unit, status
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			row.collectedAtMs, "run_parity", "sched_parity", "local", "host_a",
			"inspect-vm-host", row.metricKey, row.metricKey, 1.0, "count", "ok",
		); err != nil {
			t.Fatalf("insert metric point %s: %v", row.metricKey, err)
		}
	}
}

func metricPruneParityIDs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query("SELECT id FROM inspection_metric_points ORDER BY id")
	if err != nil {
		t.Fatalf("list metric point ids: %v", err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan metric point id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list metric point ids: %v", err)
	}
	return ids
}

func metricPruneDeletedIDs(before, after []int64) []int64 {
	kept := make(map[int64]bool, len(after))
	for _, id := range after {
		kept[id] = true
	}
	deleted := []int64{}
	for _, id := range before {
		if !kept[id] {
			deleted = append(deleted, id)
		}
	}
	return deleted
}

func sameIDSet(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[int64]int, len(left))
	for _, id := range left {
		counts[id]++
	}
	for _, id := range right {
		counts[id]--
		if counts[id] < 0 {
			return false
		}
	}
	return true
}

// TestMonitorRetentionMetricPruneParityAcrossDialects is the INT-34 guard: the
// same fixture must lose the same rows on SQLite and MySQL 8.4. It compares the
// deleted primary-key sets per batch and after a full retention scan, and it
// pins why the dialect split exists by making MySQL reject the SQLite shape with
// error 1235.
func TestMonitorRetentionMetricPruneParityAcrossDialects(t *testing.T) {
	resetMonitorRetentionGate()
	now := time.Now().UTC()
	cutoffMs := now.Add(-24 * time.Hour).UnixMilli()
	fixture := metricPruneParityFixture(cutoffMs)

	sqliteStore := openMonitorArtifactStore(t)
	mysqlDB := gatewayMySQLTestOpenEmpty(t)
	if _, err := installGatewayMySQLSchema(context.Background(), mysqlDB); err != nil {
		t.Fatalf("install MySQL schema: %v", err)
	}
	mysqlStore := &GatewayStore{db: mysqlDB, dialect: gatewayDialectMySQL, backend: "mysql"}

	insertMetricPruneParityRows(t, sqliteStore.db, fixture)
	insertMetricPruneParityRows(t, mysqlDB, fixture)
	seededSQLite := metricPruneParityIDs(t, sqliteStore.db)
	seededMySQL := metricPruneParityIDs(t, mysqlDB)
	if !sameIDSet(seededSQLite, seededMySQL) {
		t.Fatalf("fixture ids differ before pruning: sqlite=%v mysql=%v", seededSQLite, seededMySQL)
	}

	// Expected batch: the 500 rows with the smallest (collected_at_ms, id),
	// computed from the fixture rather than from either server's answer.
	type collectedID struct{ id, collected int64 }
	ordered := make([]collectedID, 0, len(fixture))
	for i, row := range fixture {
		ordered = append(ordered, collectedID{id: int64(i + 1), collected: row.collectedAtMs})
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].collected != ordered[right].collected {
			return ordered[left].collected < ordered[right].collected
		}
		return ordered[left].id < ordered[right].id
	})
	expectedBatch := make([]int64, 0, monitorRetentionMetricBatch)
	for _, entry := range ordered[:monitorRetentionMetricBatch] {
		expectedBatch = append(expectedBatch, entry.id)
	}

	if _, err := sqliteStore.db.Exec(gatewayMetricPruneBatchFor(gatewayDialectSQLite), cutoffMs, monitorRetentionMetricBatch); err != nil {
		t.Fatalf("sqlite metric batch: %v", err)
	}
	if _, err := mysqlDB.Exec(gatewayMetricPruneBatchFor(gatewayDialectMySQL), cutoffMs, monitorRetentionMetricBatch); err != nil {
		t.Fatalf("mysql metric batch: %v", err)
	}
	deletedSQLite := metricPruneDeletedIDs(seededSQLite, metricPruneParityIDs(t, sqliteStore.db))
	deletedMySQL := metricPruneDeletedIDs(seededMySQL, metricPruneParityIDs(t, mysqlDB))
	if !sameIDSet(deletedSQLite, deletedMySQL) {
		t.Fatalf("one batch deleted different rows: sqlite=%v mysql=%v", deletedSQLite, deletedMySQL)
	}
	if !sameIDSet(deletedSQLite, expectedBatch) {
		t.Fatalf("batch deleted %d rows, want the %d oldest by (collected_at_ms, id): deleted=%v expected=%v",
			len(deletedSQLite), len(expectedBatch), deletedSQLite, expectedBatch)
	}

	// MySQL must still reject the SQLite spelling. If a future MySQL accepts it,
	// this test fails and the split can be retired deliberately instead of
	// silently.
	if _, err := mysqlDB.Exec(gatewayMetricPruneBatchFor(gatewayDialectSQLite), cutoffMs, monitorRetentionMetricBatch); err == nil {
		t.Fatal("MySQL accepted the SQLite LIMIT-subquery delete; the dialect split no longer needs to exist")
	} else {
		var mysqlErr *mysql.MySQLError
		if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1235 {
			t.Fatalf("SQLite shape on MySQL = %v, want MySQL error 1235 (LIMIT & IN subquery)", err)
		}
	}

	// A full scan on the same fixture, including the run-level rules: keep_runs
	// protects the newest runs, running runs are never deleted, other expired runs
	// go, and every expired metric point goes while the in-window points stay.
	for _, db := range []*sql.DB{sqliteStore.db, mysqlDB} {
		if _, err := db.Exec("DELETE FROM inspection_metric_points"); err != nil {
			t.Fatalf("reset metric points: %v", err)
		}
	}
	insertMetricPruneParityRows(t, sqliteStore.db, fixture)
	insertMetricPruneParityRows(t, mysqlDB, fixture)

	seedRun := func(store *GatewayStore, runID string, startedAt time.Time, status string) {
		if err := store.CreateMonitorRun(context.Background(), monitorRunRecord{
			ID:          runID,
			ScheduleID:  "sched_parity",
			TriggerKind: "schedule",
			Status:      status,
			StartedAtMs: startedAt.UnixMilli(),
			Summary:     runID,
		}); err != nil {
			t.Fatalf("seed run %s: %v", runID, err)
		}
	}
	for _, store := range []*GatewayStore{sqliteStore, mysqlStore} {
		seedRun(store, "run_parity_newest", now.Add(-40*24*time.Hour), "passed")
		seedRun(store, "run_parity_second", now.Add(-41*24*time.Hour), "passed")
		seedRun(store, "run_parity_expired_a", now.Add(-42*24*time.Hour), "failed")
		seedRun(store, "run_parity_expired_b", now.Add(-43*24*time.Hour), "failed")
		seedRun(store, "run_parity_running", now.Add(-44*24*time.Hour), "running")
	}

	policy := retentionTestPolicy(30, 1, 2)
	resetMonitorRetentionGate()
	sqliteResult := sqliteStore.PruneMonitorRetention(now, policy, true)
	resetMonitorRetentionGate()
	mysqlResult := mysqlStore.PruneMonitorRetention(now, policy, true)

	if sqliteResult.RunsDeleted != 2 || mysqlResult.RunsDeleted != 2 {
		t.Fatalf("runs deleted: sqlite=%d mysql=%d, want 2 on both (keep_runs=2 plus the running run survive)",
			sqliteResult.RunsDeleted, mysqlResult.RunsDeleted)
	}
	if sqliteResult.MetricsDeleted != metricPruneParityExpired || mysqlResult.MetricsDeleted != metricPruneParityExpired {
		t.Fatalf("metrics deleted: sqlite=%d mysql=%d, want %d on both",
			sqliteResult.MetricsDeleted, mysqlResult.MetricsDeleted, metricPruneParityExpired)
	}
	for runID, want := range map[string]bool{
		"run_parity_newest":    true,
		"run_parity_second":    true,
		"run_parity_running":   true,
		"run_parity_expired_a": false,
		"run_parity_expired_b": false,
	} {
		if got := monitorRetentionRunExists(t, sqliteStore, runID); got != want {
			t.Fatalf("sqlite run %s exists=%v, want %v", runID, got, want)
		}
		if got := monitorRetentionRunExists(t, mysqlStore, runID); got != want {
			t.Fatalf("mysql run %s exists=%v, want %v", runID, got, want)
		}
	}
	remainingSQLite := metricPruneParityIDs(t, sqliteStore.db)
	remainingMySQL := metricPruneParityIDs(t, mysqlDB)
	if !sameIDSet(remainingSQLite, remainingMySQL) {
		t.Fatalf("remaining metric points differ: sqlite=%v mysql=%v", remainingSQLite, remainingMySQL)
	}
	if len(remainingSQLite) != 5 {
		t.Fatalf("remaining metric points = %v, want the 5 in-window points", remainingSQLite)
	}
}

// SF-15 contract: artifacts and metric points have a lifecycle. These tests pin
// the rules in SKILL_FIRST_INSPECTION_SPEC.md 3.5: age windows, the per-schedule
// keep-runs floor, running-run protection, files-before-rows ordering, the
// metric window outliving the artifact window, the forced hourly fallback, and
// the "log and drop the row anyway" failure mode.

// resetMonitorRetentionGate clears the process-wide throttle so one test cannot
// leak its window into the next.
func resetMonitorRetentionGate() {
	monitorRetentionGate.lastMs.Store(0)
	monitorRetentionGate.skipAnnounced.Store(false)
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

// TestMonitorRetentionAnnouncesThrottledDeferralOnce pins the observability
// contract of the throttle: a skipped post-run trigger must say so exactly once
// per window, because otherwise an operator who lowers the retention window and
// watches a run cannot tell "throttled" from "retention is broken".
func TestMonitorRetentionAnnouncesThrottledDeferralOnce(t *testing.T) {
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	seedMonitorRetentionRun(t, store, "run_ret_announce_anchor", "sched_ret_announce", now.Add(-time.Hour).UnixMilli(), "success", false)

	var logs strings.Builder
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	ran := store.PruneMonitorRetention(now, retentionTestPolicy(30, 90, 1), false)
	if ran.SkippedThrottled {
		t.Fatalf("the first post-run trigger must scan, got %s", ran.summary())
	}
	if strings.Contains(logs.String(), "monitor retention deferred") {
		t.Fatalf("a scan that actually ran must not announce a deferral, got %q", logs.String())
	}

	for i := 1; i <= 3; i++ {
		skip := store.PruneMonitorRetention(now.Add(time.Duration(i)*time.Minute), retentionTestPolicy(30, 90, 1), false)
		if !skip.SkippedThrottled {
			t.Fatalf("attempt %d must be throttled, got %s", i, skip.summary())
		}
		if skip.ThrottledFor <= 0 || skip.ThrottleWindow != timeouts.MonitorRetentionMinInterval {
			t.Fatalf("a throttled skip must explain itself, got for=%s window=%s", skip.ThrottledFor, skip.ThrottleWindow)
		}
	}
	if got := strings.Count(logs.String(), "monitor retention deferred"); got != 1 {
		t.Fatalf("throttled skips must be announced once per window, got %d in %q", got, logs.String())
	}
	if !strings.Contains(logs.String(), "artifacts=30d") || !strings.Contains(logs.String(), "metrics=90d") {
		t.Fatalf("the deferral line must name the pending windows, got %q", logs.String())
	}

	// The hourly fallback re-arms the announcement: the next window explains
	// itself again instead of staying silent for the life of the process.
	forced := store.PruneMonitorRetention(now.Add(20*time.Minute), retentionTestPolicy(30, 90, 1), true)
	if forced.SkippedThrottled {
		t.Fatalf("the fallback always scans, got %s", forced.summary())
	}
	again := store.PruneMonitorRetention(now.Add(21*time.Minute), retentionTestPolicy(30, 90, 1), false)
	if !again.SkippedThrottled {
		t.Fatalf("a trigger inside the new window must be throttled, got %s", again.summary())
	}
	if got := strings.Count(logs.String(), "monitor retention deferred"); got != 2 {
		t.Fatalf("each window must announce its own deferral, got %d in %q", got, logs.String())
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
