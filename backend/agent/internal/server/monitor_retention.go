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

	// monitorRetentionMinInterval throttles the post-run trigger. The hourly
	// fallback path always scans (force=true), so throttling can never switch
	// the cleanup off.
	monitorRetentionMinInterval = 15 * time.Minute
	// monitorRetentionRunsPerScan bounds one scan so a large backlog drains over
	// several passes instead of one long write lock.
	monitorRetentionRunsPerScan = 50
	// monitorRetentionMetricBatch / monitorRetentionMetricBatches bound the
	// metric prune.
	monitorRetentionMetricBatch   = 500
	monitorRetentionMetricBatches = 20
)

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
}

func (r monitorRetentionResult) changed() bool {
	return r.RunsDeleted > 0 || r.ItemsDeleted > 0 || r.MetricsDeleted > 0 || r.DirsRemoved > 0
}

// summary is the human-readable form used by tests and logs.
func (r monitorRetentionResult) summary() string {
	return fmt.Sprintf("runs=%d items=%d metrics=%d dirs=%d dir_failures=%d throttled=%t busy=%t",
		r.RunsDeleted, r.ItemsDeleted, r.MetricsDeleted, r.DirsRemoved, r.DirFailures, r.SkippedThrottled, r.SkippedBusy)
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
	} else {
		last := monitorRetentionGate.lastMs.Load()
		if last != 0 && nowMs-last < monitorRetentionMinInterval.Milliseconds() {
			result.SkippedThrottled = true
			return result
		}
		monitorRetentionGate.lastMs.Store(nowMs)
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
	var total int64
	for batch := 0; batch < monitorRetentionMetricBatches; batch++ {
		res, err := s.db.Exec("DELETE FROM inspection_metric_points WHERE id IN (SELECT id FROM inspection_metric_points WHERE collected_at_ms < ? LIMIT ?)",
			cutoffMs, monitorRetentionMetricBatch)
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
