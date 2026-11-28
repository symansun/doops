package server

// Inspection retention (SKILL_FIRST_INSPECTION_SPEC.md 3.5).
//
// Artifacts and metric points are append-only without this unit: a scheduled
// inspection that runs eight Skill packages over dozens of targets every hour
// fills the agent host and slows the trend queries forever. The rules below
// keep the store bounded without losing the recent history an operator needs.
//
// Two invariants matter more than the exact numbers:
//   - files are removed before the index rows that name them, because the
//     on-disk inventory is derived from those rows (deleting the rows first
//     would orphan payload directories nothing can find again);
//   - a directory that cannot be removed is logged, and its row is dropped
//     anyway, so one read-only mount cannot make every prune retry forever.

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// monitorArtifactRetentionDaysEnv keeps artifacts for this long by default.
	monitorArtifactRetentionDaysEnv = "DOOPS_MONITOR_ARTIFACT_RETENTION_DAYS"
	// monitorArtifactKeepRunsEnv is the per-schedule floor: the newest N runs of
	// every schedule survive regardless of age.
	monitorArtifactKeepRunsEnv = "DOOPS_MONITOR_ARTIFACT_KEEP_RUNS"
	// monitorMetricRetentionDaysEnv keeps trend points longer than artifacts so
	// the charts stay continuous across artifact expiry.
	monitorMetricRetentionDaysEnv = "DOOPS_MONITOR_METRIC_RETENTION_DAYS"

	defaultMonitorArtifactRetentionDays = 30
	defaultMonitorArtifactKeepRuns      = 50
	defaultMonitorMetricRetentionDays   = 90

	// The post-run trigger is throttled by timeouts.MonitorRetentionMinInterval.
	// The hourly fallback path always scans (force=true), so throttling can never
	// switch the cleanup off.
	// monitorRetentionRunsPerScan bounds one scan so a large backlog drains over
	// several passes instead of one long write lock.
	monitorRetentionRunsPerScan = 50
	// monitorRetentionMetricBatch / monitorRetentionMetricBatches bound the
	// metric prune.
	monitorRetentionMetricBatch   = 500
	monitorRetentionMetricBatches = 20
)

// The metric prune is the one retention statement whose spelling differs per
// backend, so both renderings live here as full literals and
// gatewayMetricPruneBatchFor only picks one. The literals stay in this file on
// purpose: the Gateway runtime SQL audit reads every SQL string outside the
// dialect layer, and pinning the SQLite form here keeps the remaining
// LIMIT-in-IN-subquery site attributed to the retention code that owns it
// instead of hiding it in a renderer file the audit skips.
const (
	// monitorRetentionMetricPruneSQLiteSQL deletes one metric batch on SQLite.
	// SQLite accepts LIMIT inside an IN subquery.
	monitorRetentionMetricPruneSQLiteSQL = "DELETE FROM inspection_metric_points WHERE id IN (SELECT id FROM inspection_metric_points WHERE collected_at_ms < ? ORDER BY collected_at_ms, id LIMIT ?)"

	// monitorRetentionMetricPruneMySQLSQL deletes the same batch on MySQL 8.4,
	// which raises ERROR 1235 for LIMIT inside IN/ALL/ANY/SOME. The id batch
	// moves into a derived table that the multi-table DELETE joins; because the
	// derived table carries ORDER BY ... LIMIT MySQL materializes it, so ERROR
	// 1093 ("You can't specify target table ... for update in FROM clause")
	// does not fire. Both forms order by (collected_at_ms, id) so they delete the
	// same rows; neither drops LIMIT, which is what caps a batch at
	// monitorRetentionMetricBatch for the caller's bounded loop.
	monitorRetentionMetricPruneMySQLSQL = "DELETE target FROM inspection_metric_points AS target JOIN (SELECT id FROM inspection_metric_points WHERE collected_at_ms < ? ORDER BY collected_at_ms, id LIMIT ?) AS batch ON batch.id = target.id"
)

// gatewayMetricPruneBatchSQL renders one metric prune batch for the process
// dialect. SQLite keeps the single-statement DELETE ... WHERE id IN (SELECT ...
// LIMIT): SQLite accepts LIMIT in an IN subquery. MySQL 8.4 does not — the same
// statement fails with ERROR 1235 (42000) "This version of MySQL doesn't yet
// support 'LIMIT & IN/ALL/ANY/SOME subquery'" — which is the INT-34 failure that
// made every metric prune tick fail on a MySQL Gateway while the rows kept
// growing.
//
// MySQL names the target table a second time, and that is where ERROR 1093
// ("You can't specify target table ... for update in FROM clause") waits: the
// multi-table DELETE reads the id batch through a derived table, which MySQL
// cannot merge into the outer statement because it carries ORDER BY ... LIMIT,
// so it materializes the batch and deletes against that snapshot. A bare merged
// subquery is rejected, which is why the batch is a derived table.
//
// Dropping LIMIT is not the equivalent of this statement: LIMIT caps one batch
// at monitorRetentionMetricBatch (500) rows so the caller's 20-batch loop bounds
// lock time, undo growth and WAL churn; without it one delete takes the whole
// expired window in a single transaction. Both spellings order by
// (collected_at_ms, id) inside the LIMIT so the batch is deterministic and the
// two backends delete the same rows; SQLite's answer used to depend on rowid
// scan order, which happened to agree but was never pinned.
func gatewayMetricPruneBatchSQL() string {
	return gatewayMetricPruneBatchFor(gatewayDialect())
}

func gatewayMetricPruneBatchFor(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return monitorRetentionMetricPruneMySQLSQL
	}
	return monitorRetentionMetricPruneSQLiteSQL
}

// monitorRetentionPolicy is the resolved (env or default) retention window.
type monitorRetentionPolicy struct {
	ArtifactDays int
	MetricDays   int
	KeepRuns     int
}

// monitorRetentionResult reports what one scan did. It is logged when something
// was removed and asserted by tests.
type monitorRetentionResult struct {
	RunsDeleted      int
	ItemsDeleted     int64
	MetricsDeleted   int64
	DirsRemoved      int
	DirFailures      int
	SkippedThrottled bool
	SkippedBusy      bool
	// ThrottledFor and ThrottleWindow explain a throttled skip: how long ago
	// the last scan ran and how long the caller must wait for the next one.
	ThrottledFor   time.Duration
	ThrottleWindow time.Duration
}

func (r monitorRetentionResult) changed() bool {
	return r.RunsDeleted > 0 || r.ItemsDeleted > 0 || r.MetricsDeleted > 0 || r.DirsRemoved > 0
}

// summary is the human-readable form used by tests and logs.
func (r monitorRetentionResult) summary() string {
	return fmt.Sprintf("runs=%d items=%d metrics=%d dirs=%d dir_failures=%d throttled=%t busy=%t",
		r.RunsDeleted, r.ItemsDeleted, r.MetricsDeleted, r.DirsRemoved, r.DirFailures, r.SkippedThrottled, r.SkippedBusy)
}

// monitorRetentionWindowLabel renders a retention window for logs. Zero means
// the window is disabled and nothing in it expires.
func monitorRetentionWindowLabel(days int) string {
	if days <= 0 {
		return "kept forever"
	}
	return fmt.Sprintf("%dd", days)
}

// monitorRetentionEnvInt reads a positive integer override, falling back to the
// default for missing, non-numeric, or non-positive values.
func monitorRetentionEnvInt(raw string, def int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return def
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value <= 0 {
		log.Printf("[gateway] ignoring invalid retention override %q; using %d", raw, def)
		return def
	}
	return value
}

// monitorRetentionPolicyFromEnv resolves the retention policy for this process.
func monitorRetentionPolicyFromEnv() monitorRetentionPolicy {
	return monitorRetentionPolicy{
		ArtifactDays: monitorRetentionEnvInt(os.Getenv(monitorArtifactRetentionDaysEnv), defaultMonitorArtifactRetentionDays),
		MetricDays:   monitorRetentionEnvInt(os.Getenv(monitorMetricRetentionDaysEnv), defaultMonitorMetricRetentionDays),
		KeepRuns:     monitorRetentionEnvInt(os.Getenv(monitorArtifactKeepRunsEnv), defaultMonitorArtifactKeepRuns),
	}
}

// artifactCutoffMs is the oldest artifact timestamp still kept. Zero disables
// the window (keep everything).
func (p monitorRetentionPolicy) artifactCutoffMs(now time.Time) int64 {
	if p.ArtifactDays <= 0 {
		return 0
	}
	return now.UTC().Add(-time.Duration(p.ArtifactDays) * 24 * time.Hour).UnixMilli()
}

// metricCutoffMs is the oldest metric sample still kept. Zero disables the
// window (keep everything).
func (p monitorRetentionPolicy) metricCutoffMs(now time.Time) int64 {
	if p.MetricDays <= 0 {
		return 0
	}
	return now.UTC().Add(-time.Duration(p.MetricDays) * 24 * time.Hour).UnixMilli()
}

// monitorRetentionGate is process-wide: scheduled runs and manual HTTP runs can
// finish at the same moment, and two prunes deleting the same directories would
// only produce misleading errors.
var monitorRetentionGate struct {
	mu     sync.Mutex
	lastMs atomic.Int64
	// skipAnnounced makes the throttle observable: the post-run trigger is
	// silent by design, so an operator who sets DOOPS_MONITOR_*_RETENTION_DAYS
	// and watches a run saw nothing and could not tell "throttled" from
	// "nothing expired". One line per throttle window keeps that answer without
	// logging every run.
	skipAnnounced atomic.Bool
}

// monitorRetentionProtectedRuns returns every run id that must survive a scan:
// the newest keepRuns runs of each schedule (the manual "" group included, so
// every schedule and the manual history keep their latest runs) plus every run
// still marked running.
func (s *GatewayStore) monitorRetentionProtectedRuns(keepRuns int) (map[string]bool, error) {
	protected := map[string]bool{}
	rows, err := s.db.Query("SELECT DISTINCT schedule_id FROM monitor_runs WHERE status != 'running'")
	if err != nil {
		return nil, err
	}
	schedules := []string{}
	for rows.Next() {
		var scheduleID string
		if err := rows.Scan(&scheduleID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		schedules = append(schedules, scheduleID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if keepRuns <= 0 {
		keepRuns = defaultMonitorArtifactKeepRuns
	}
	for _, scheduleID := range schedules {
		runRows, err := s.db.Query("SELECT id FROM monitor_runs WHERE schedule_id=? ORDER BY started_at_ms DESC, id DESC LIMIT ?", scheduleID, keepRuns)
		if err != nil {
			return nil, err
		}
		for runRows.Next() {
			var runID string
			if err := runRows.Scan(&runID); err != nil {
				_ = runRows.Close()
				return nil, err
			}
			protected[runID] = true
		}
		if err := runRows.Err(); err != nil {
			_ = runRows.Close()
			return nil, err
		}
		_ = runRows.Close()
	}
	runningRows, err := s.db.Query("SELECT id FROM monitor_runs WHERE status = 'running'")
	if err != nil {
		return nil, err
	}
	defer runningRows.Close()
	for runningRows.Next() {
		var runID string
		if err := runningRows.Scan(&runID); err != nil {
			return nil, err
		}
		protected[runID] = true
	}
	return protected, runningRows.Err()
}

// monitorRetentionRunDirs lists the artifact directories a run still points at.
func (s *GatewayStore) monitorRetentionRunDirs(runID string) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT artifact_dir FROM monitor_run_items WHERE run_id=? AND artifact_dir != ''", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dirs := []string{}
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		if strings.TrimSpace(dir) != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs, rows.Err()
}

// monitorRetentionExpiredRunIDs picks the expired candidates of one scan,
// newest first so a large backlog drains predictably.
func (s *GatewayStore) monitorRetentionExpiredRunIDs(cutoffMs int64, limit int) ([]string, error) {
	if cutoffMs <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query("SELECT id FROM monitor_runs WHERE started_at_ms < ? AND status != 'running' ORDER BY started_at_ms DESC, id DESC LIMIT ?", cutoffMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, err
		}
		ids = append(ids, runID)
	}
	return ids, rows.Err()
}

// PruneMonitorRetention applies the retention policy once.
//
// force=false is the post-run trigger: it returns immediately unless the
// throttle window has elapsed. force=true is the hourly fallback and always
// scans. Both share one mutex so concurrent callers never prune in parallel.
func (s *GatewayStore) PruneMonitorRetention(now time.Time, policy monitorRetentionPolicy, force bool) monitorRetentionResult {
	var result monitorRetentionResult
	if s == nil || s.db == nil {
		return result
	}
	if !monitorRetentionGate.mu.TryLock() {
		result.SkippedBusy = true
		return result
	}
	defer monitorRetentionGate.mu.Unlock()
	nowMs := now.UTC().UnixMilli()
	if force {
		monitorRetentionGate.lastMs.Store(nowMs)
		monitorRetentionGate.skipAnnounced.Store(false)
	} else {
		last := monitorRetentionGate.lastMs.Load()
		throttle := timeouts.MonitorRetentionMinInterval
		if last != 0 && nowMs-last < throttle.Milliseconds() {
			result.SkippedThrottled = true
			result.ThrottledFor = time.Duration(nowMs-last) * time.Millisecond
			result.ThrottleWindow = throttle
			// Once per window: the first skipped run explains itself, the rest
			// stay quiet.
			if monitorRetentionGate.skipAnnounced.CompareAndSwap(false, true) {
				log.Printf("[gateway] monitor retention deferred: last scan %s ago, throttle %s (artifacts=%s metrics=%s before the next scan)",
					result.ThrottledFor.Round(time.Second), throttle,
					monitorRetentionWindowLabel(policy.ArtifactDays), monitorRetentionWindowLabel(policy.MetricDays))
			}
			return result
		}
		monitorRetentionGate.lastMs.Store(nowMs)
		monitorRetentionGate.skipAnnounced.Store(false)
	}
	return s.pruneMonitorRetentionLocked(now, policy)
}

func (s *GatewayStore) pruneMonitorRetentionLocked(now time.Time, policy monitorRetentionPolicy) monitorRetentionResult {
	var result monitorRetentionResult
	protected, err := s.monitorRetentionProtectedRuns(policy.KeepRuns)
	if err != nil {
		log.Printf("[gateway] monitor retention skipped: protected-run lookup failed: %v", err)
		return result
	}
	expired, err := s.monitorRetentionExpiredRunIDs(policy.artifactCutoffMs(now), monitorRetentionRunsPerScan)
	if err != nil {
		log.Printf("[gateway] monitor retention skipped: expired-run lookup failed: %v", err)
		return result
	}
	for _, runID := range expired {
		if protected[runID] {
			continue
		}
		dirs, err := s.monitorRetentionRunDirs(runID)
		if err != nil {
			log.Printf("[gateway] monitor retention run %s: artifact lookup failed: %v", runID, err)
			continue
		}
		// Files first: the directory inventory is derived from these rows, so
		// deleting the rows first would leak payload directories forever.
		for _, dir := range dirs {
			if err := s.removeMonitorArtifactDirErr(dir); err != nil {
				// A directory we cannot remove must not block index cleanup:
				// keeping the row would retry the same failure on every scan.
				result.DirFailures++
				log.Printf("[gateway] monitor retention run %s: remove artifact dir %s failed: %v (index row dropped anyway)", runID, dir, err)
				continue
			}
			result.DirsRemoved++
		}
		tx, err := s.db.Begin()
		if err != nil {
			log.Printf("[gateway] monitor retention run %s: begin failed: %v", runID, err)
			continue
		}
		itemsRes, err := tx.Exec("DELETE FROM monitor_run_items WHERE run_id=?", runID)
		if err != nil {
			_ = tx.Rollback()
			log.Printf("[gateway] monitor retention run %s: item delete failed: %v", runID, err)
			continue
		}
		runRes, err := tx.Exec("DELETE FROM monitor_runs WHERE id=? AND status != 'running'", runID)
		if err != nil {
			_ = tx.Rollback()
			log.Printf("[gateway] monitor retention run %s: run delete failed: %v", runID, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			log.Printf("[gateway] monitor retention run %s: commit failed: %v", runID, err)
			continue
		}
		if n, err := itemsRes.RowsAffected(); err == nil {
			result.ItemsDeleted += n
		}
		if n, err := runRes.RowsAffected(); err == nil && n > 0 {
			result.RunsDeleted++
		}
	}
	// Resource snapshots follow the artifact window: they are the per-run
	// inventory the artifact rows point at.
	if cutoffMs := policy.artifactCutoffMs(now); cutoffMs > 0 {
		if _, err := s.db.Exec("DELETE FROM monitor_resource_snapshots WHERE collected_at_ms < ?", cutoffMs); err != nil {
			log.Printf("[gateway] monitor retention: snapshot prune failed: %v", err)
		}
	}
	result.MetricsDeleted = s.pruneMonitorRetentionMetrics(policy.metricCutoffMs(now))
	if result.changed() {
		log.Printf("[gateway] monitor retention removed runs=%d items=%d metrics=%d dirs=%d dir_failures=%d (artifact=%dd metric=%dd keep_runs=%d)",
			result.RunsDeleted, result.ItemsDeleted, result.MetricsDeleted, result.DirsRemoved, result.DirFailures,
			policy.ArtifactDays, policy.MetricDays, policy.KeepRuns)
	}
	return result
}

// pruneMonitorRetentionMetrics drops trend points older than the metric window.
// Points outlive their run on purpose: the charts stay continuous after the
// artifact rows expire, so this delete must not be tied to run deletion.
func (s *GatewayStore) pruneMonitorRetentionMetrics(cutoffMs int64) int64 {
	if cutoffMs <= 0 {
		return 0
	}
	dialect := s.dialect
	if dialect == "" {
		// Stores opened by tests carry no dialect field; fall back to the
		// process value the rest of the store statements render for.
		dialect = gatewayDialect()
	}
	var total int64
	for batch := 0; batch < monitorRetentionMetricBatches; batch++ {
		res, err := s.db.Exec(gatewayMetricPruneBatchFor(dialect), cutoffMs, monitorRetentionMetricBatch)
		if err != nil {
			log.Printf("[gateway] monitor retention: metric prune failed: %v", err)
			return total
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total
		}
		total += n
		if n < int64(monitorRetentionMetricBatch) {
			break
		}
	}
	return total
}

// maybePruneMonitorRetention is the post-run trigger. It is throttled and
// returns silently inside the window.
func (h *GatewayHub) maybePruneMonitorRetention() {
	if h == nil || h.store == nil {
		return
	}
	h.store.PruneMonitorRetention(time.Now().UTC(), monitorRetentionPolicyFromEnv(), false)
}
