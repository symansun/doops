package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

func (s *GatewayStore) tableExists(ctx context.Context, name string) bool {
	var ok bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&ok)
	return ok
}

// ---------------- Schedules CRUD ----------------

func (s *GatewayStore) ListMonitorSchedules(ctx context.Context) ([]monitorScheduleRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, enabled, scope_mode, targets_json, tags_json,
		strategies_json, skills_json, prompt, command, thresholds_json, interval_seconds, next_run_at_ms, last_run_at_ms, last_run_id, last_status,
		created_by, created_at, updated_at
		FROM monitor_schedules ORDER BY enabled DESC, created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []monitorScheduleRecord{}
	for rows.Next() {
		rec, err := scanMonitorSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *GatewayStore) GetMonitorSchedule(ctx context.Context, id string) (monitorScheduleRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, description, enabled, scope_mode, targets_json, tags_json,
		strategies_json, skills_json, prompt, command, thresholds_json, interval_seconds, next_run_at_ms, last_run_at_ms, last_run_id, last_status,
		created_by, created_at, updated_at
		FROM monitor_schedules WHERE id=?`, id)
	return scanMonitorSchedule(row)
}

func scanMonitorSchedule(row interface{ Scan(...any) error }) (monitorScheduleRecord, error) {
	var rec monitorScheduleRecord
	var enabledInt int
	var targetsRaw, tagsRaw, skillsRaw, thresholdsRaw string
	// strategies_json is a retired column: it stays in the table so rows written
	// before SF-09 keep loading, but it no longer selects any execution path.
	var legacyStrategiesRaw string
	err := row.Scan(
		&rec.ID, &rec.Name, &rec.Description, &enabledInt, &rec.ScopeMode,
		&targetsRaw, &tagsRaw, &legacyStrategiesRaw, &skillsRaw, &rec.Prompt, &rec.Command, &thresholdsRaw,
		&rec.IntervalSeconds, &rec.NextRunAtMs, &rec.LastRunAtMs,
		&rec.LastRunID, &rec.LastStatus, &rec.CreatedBy, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		return rec, err
	}
	rec.Enabled = enabledInt != 0
	rec.Targets = []monitorTargetRef{}
	rec.Tags = []string{}
	rec.Skills = []string{}
	_ = json.Unmarshal([]byte(targetsRaw), &rec.Targets)
	_ = json.Unmarshal([]byte(tagsRaw), &rec.Tags)
	_ = json.Unmarshal([]byte(skillsRaw), &rec.Skills)
	_ = json.Unmarshal([]byte(thresholdsRaw), &rec.Thresholds)
	rec.Skills = normalizeSkillSlugs(rec.Skills)
	rec.Prompt = strings.TrimSpace(rec.Prompt)
	rec.Command = strings.TrimSpace(rec.Command)
	rec.Thresholds = normalizeMonitorThresholds(rec.Thresholds)
	return rec, nil
}

func (s *GatewayStore) SaveMonitorSchedule(ctx context.Context, rec monitorScheduleRecord, isUpdate bool) (monitorScheduleRecord, error) {
	now := time.Now().UTC()
	nowStr := formatTime(now)
	rec.Skills = normalizeSkillSlugs(rec.Skills)
	rec.Prompt = strings.TrimSpace(rec.Prompt)
	rec.Command = strings.TrimSpace(rec.Command)
	rec.Thresholds = normalizeMonitorThresholds(rec.Thresholds)
	if rec.Targets == nil {
		rec.Targets = []monitorTargetRef{}
	}
	if rec.Tags == nil {
		rec.Tags = []string{}
	}
	targetsJSON, _ := json.Marshal(rec.Targets)
	tagsJSON, _ := json.Marshal(rec.Tags)
	skillsJSON, _ := json.Marshal(rec.Skills)
	thresholdsJSON, _ := json.Marshal(rec.Thresholds)
	enabledInt := 0
	if rec.Enabled {
		enabledInt = 1
	}

	if !isUpdate {
		if rec.ID == "" {
			rec.ID = "sched_" + randomHex(8)
		}
		rec.CreatedAt = nowStr
		rec.UpdatedAt = nowStr
		if rec.Enabled && rec.NextRunAtMs <= 0 {
			rec.NextRunAtMs = now.Add(time.Duration(rec.IntervalSeconds) * time.Second).UnixMilli()
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO monitor_schedules(
			id, name, description, enabled, scope_mode, targets_json, tags_json,
			strategies_json, skills_json, prompt, command, thresholds_json, interval_seconds, next_run_at_ms,
			last_run_at_ms, last_run_id, last_status, created_by, created_at, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			rec.ID, rec.Name, rec.Description, enabledInt, rec.ScopeMode,
			string(targetsJSON), string(tagsJSON), "[]", string(skillsJSON), rec.Prompt, rec.Command, string(thresholdsJSON),
			rec.IntervalSeconds, rec.NextRunAtMs, rec.LastRunAtMs, rec.LastRunID, rec.LastStatus,
			rec.CreatedBy, rec.CreatedAt, rec.UpdatedAt,
		)
		if err != nil {
			return rec, err
		}
		return s.GetMonitorSchedule(ctx, rec.ID)
	}

	rec.UpdatedAt = nowStr
	nextRun := rec.NextRunAtMs
	if rec.Enabled && nextRun <= 0 {
		nextRun = now.Add(time.Duration(rec.IntervalSeconds) * time.Second).UnixMilli()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE monitor_schedules SET
		name=?, description=?, enabled=?, scope_mode=?, targets_json=?, tags_json=?,
		strategies_json=?, skills_json=?, prompt=?, command=?, thresholds_json=?, interval_seconds=?, next_run_at_ms=?, updated_at=?
		WHERE id=?`,
		rec.Name, rec.Description, enabledInt, rec.ScopeMode,
		string(targetsJSON), string(tagsJSON), "[]", string(skillsJSON), rec.Prompt, rec.Command, string(thresholdsJSON),
		rec.IntervalSeconds, nextRun, rec.UpdatedAt, rec.ID,
	)
	if err != nil {
		return rec, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return rec, sql.ErrNoRows
	}
	return s.GetMonitorSchedule(ctx, rec.ID)
}

func (s *GatewayStore) ToggleMonitorSchedule(ctx context.Context, id string, enabled bool) (monitorScheduleRecord, error) {
	cur, err := s.GetMonitorSchedule(ctx, id)
	if err != nil {
		return cur, err
	}
	now := time.Now().UTC()
	nextRun := cur.NextRunAtMs
	if enabled && nextRun <= now.UnixMilli() {
		nextRun = now.Add(time.Duration(cur.IntervalSeconds) * time.Second).UnixMilli()
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err = s.db.ExecContext(ctx, `UPDATE monitor_schedules SET enabled=?, next_run_at_ms=?, updated_at=? WHERE id=?`,
		enabledInt, nextRun, formatTime(now), id)
	if err != nil {
		return cur, err
	}
	return s.GetMonitorSchedule(ctx, id)
}

func (s *GatewayStore) DeleteMonitorSchedule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM monitor_schedules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ClaimDueMonitorSchedules atomically picks up to limit due schedules and advances their next_run_at_ms.
func (s *GatewayStore) ClaimDueMonitorSchedules(ctx context.Context, now time.Time, limit int) ([]monitorScheduleRecord, error) {
	if limit <= 0 {
		limit = 4
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	nowMs := now.UnixMilli()
	rows, err := tx.QueryContext(ctx, `SELECT id, name, description, enabled, scope_mode, targets_json, tags_json,
		strategies_json, skills_json, prompt, command, thresholds_json, interval_seconds, next_run_at_ms, last_run_at_ms, last_run_id, last_status,
		created_by, created_at, updated_at
		FROM monitor_schedules WHERE enabled=1 AND next_run_at_ms>0 AND next_run_at_ms<=?
		ORDER BY next_run_at_ms ASC, id ASC LIMIT ?`, nowMs, limit)
	if err != nil {
		return nil, err
	}
	var claimed []monitorScheduleRecord
	for rows.Next() {
		rec, err := scanMonitorSchedule(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		claimed = append(claimed, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range claimed {
		stepMs := int64(claimed[i].IntervalSeconds) * 1000
		if stepMs < 60_000 {
			stepMs = 60_000
		}
		nextMs := nowMs + stepMs
		if _, err := tx.ExecContext(ctx, `UPDATE monitor_schedules SET next_run_at_ms=?, updated_at=? WHERE id=?`,
			nextMs, formatTime(now.UTC()), claimed[i].ID); err != nil {
			return nil, err
		}
		claimed[i].NextRunAtMs = nextMs
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

// ---------------- Runs, Run Items & Snapshots Persistence ----------------

func (s *GatewayStore) CreateMonitorRun(ctx context.Context, run monitorRunRecord) error {
	skills := normalizeSkillSlugs(run.Skills)
	skillsJSON, _ := json.Marshal(skills)
	_, err := s.db.ExecContext(ctx, `INSERT INTO monitor_runs(
		id, schedule_id, schedule_name, trigger_kind, triggered_by, status,
		skills_json, prompt, command,
		total_targets, total_assets, passed_checks, warning_checks, failed_checks, skipped_checks,
		offline_targets, metrics_written, started_at_ms, finished_at_ms, duration_ms, summary
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.ScheduleID, run.ScheduleName, run.TriggerKind, run.TriggeredBy, run.Status,
		string(skillsJSON), run.Prompt, run.Command,
		run.TotalTargets, run.TotalAssets, run.PassedChecks, run.WarningChecks, run.FailedChecks, run.SkippedChecks,
		run.OfflineTargets, run.MetricsWritten, run.StartedAtMs, run.FinishedAtMs, run.DurationMs, run.Summary,
	)
	return err
}

func (s *GatewayStore) PersistMonitorRunResults(
	ctx context.Context,
	run monitorRunRecord,
	items []monitorRunItemRecord,
	snapshots []monitorResourceSnapshot,
	seriesPoints []monitorSeriesBridgeEntry,
) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Extract trend metrics once; the same extraction both feeds
	// inspection_metric_points and records the skipped-entry count on evidence.
	extractions := make([]monitorMetricExtraction, len(items))
	for i := range items {
		extractions[i] = extractInspectionMetricForItem(run, items[i])
	}
	for i, item := range items {
		metricsJSON, _ := json.Marshal(item.Metrics)
		checksJSON, _ := json.Marshal(item.Checks)
		evidence := sanitizeMonitorEvidence(item.Evidence)
		if extraction := extractions[i]; len(extraction.Points) > 0 || extraction.Skipped > 0 {
			if evidence == nil {
				evidence = map[string]any{}
			}
			evidence["inspection_metrics"] = map[string]any{
				"extracted": len(extraction.Points),
				"skipped":   extraction.Skipped,
			}
		}
		evidenceJSON, _ := json.Marshal(evidence)
		artifactJSONStr := ""
		if len(item.ArtifactJSON) > 0 && json.Valid(item.ArtifactJSON) {
			candidate := redactSecretText(string(item.ArtifactJSON))
			if json.Valid([]byte(candidate)) {
				artifactJSONStr = candidate
			}
		}
		// The row id names the on-disk item directory, so insert the light
		// columns first and attach either the file index or the legacy text
		// columns right after.
		res, err := tx.ExecContext(ctx, `INSERT INTO monitor_run_items(
			run_id, cluster, instance, asset_id, asset_name, asset_kind, strategy, collector_method,
			status, reason, started_at_ms, finished_at_ms, duration_ms, metrics_json, checks_json, evidence_json,
			artifact_format, artifact_markdown, artifact_json, raw_output,
			artifact_dir, artifact_bytes, artifact_sha256, artifact_truncated
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.ID, item.Cluster, item.Instance, item.AssetID, item.AssetName, item.AssetKind, item.Strategy, item.CollectorMethod,
			item.Status, item.Reason, item.StartedAtMs, item.FinishedAtMs, item.DurationMs,
			string(metricsJSON), string(checksJSON), string(evidenceJSON),
			item.ArtifactFormat, "", "", "", "", 0, "", 0,
		)
		if err != nil {
			return 0, err
		}
		if itemID, idErr := res.LastInsertId(); idErr == nil && itemID > 0 {
			if err := s.persistMonitorRunItemArtifact(ctx, tx, run.ID, itemID, item, artifactJSONStr); err != nil {
				return 0, err
			}
		}
	}

	for _, snapshot := range snapshots {
		if err := insertMonitorResourceSnapshot(ctx, tx, run.ID, snapshot); err != nil {
			return 0, err
		}
	}

	if err := persistInspectionMetricExtractions(ctx, tx, run, items, extractions, snapshots); err != nil {
		return 0, err
	}

	// Optionally bridge granular series into asset_monitor_series / asset_monitor_samples when migrated.
	bridged := 0
	var hasAssetMonitor bool
	_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='asset_monitor_series')`).Scan(&hasAssetMonitor)
	if hasAssetMonitor && len(seriesPoints) > 0 {
		now := time.Now()
		for _, entry := range seriesPoints {
			pt := entry.Point
			origin := pt.Origin
			if !monitorOrigins[origin] {
				continue
			}
			in := monitorInput{
				DeviceID:     pt.DeviceID,
				monitorAsset: monitorAsset{Type: pt.Type, AssetID: pt.AssetID, Namespace: pt.Namespace},
				Metric:       pt.Metric,
				Unit:         pt.Unit,
				monitorPoint: monitorPoint{At: pt.AtMs, Status: pt.Status, Value: pt.Value},
			}
			if !in.valid(now) {
				continue
			}
			req := monitorRequest{Cluster: entry.Cluster, Instance: entry.Instance}
			if err := storeMonitorPoint(tx, req, in, origin); err == nil {
				bridged++
			}
		}
	}

	run.MetricsWritten = bridged + len(snapshots)
	if _, err := tx.ExecContext(ctx, `UPDATE monitor_runs SET
		status=?, total_targets=?, total_assets=?, passed_checks=?, warning_checks=?, failed_checks=?, skipped_checks=?,
		offline_targets=?, metrics_written=?, finished_at_ms=?, duration_ms=?, summary=?
		WHERE id=?`,
		run.Status, run.TotalTargets, run.TotalAssets, run.PassedChecks, run.WarningChecks, run.FailedChecks, run.SkippedChecks,
		run.OfflineTargets, run.MetricsWritten, run.FinishedAtMs, run.DurationMs, run.Summary, run.ID,
	); err != nil {
		return 0, err
	}

	if run.ScheduleID != "" {
		_, _ = tx.ExecContext(ctx, `UPDATE monitor_schedules SET last_run_at_ms=?, last_run_id=?, last_status=?, updated_at=? WHERE id=?`,
			run.StartedAtMs, run.ID, run.Status, formatTime(time.Now().UTC()), run.ScheduleID)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return run.MetricsWritten, nil
}

// insertMonitorResourceSnapshot appends one resource snapshot row. Strategy-
// provided snapshots and the inspection-metric bridge (spec §4.2) share it so
// there stays exactly one canonical snapshot table.
func insertMonitorResourceSnapshot(ctx context.Context, tx *sql.Tx, runID string, snapshot monitorResourceSnapshot) error {
	tagsJSON, _ := json.Marshal(snapshot.Tags)
	detailsJSON, _ := json.Marshal(sanitizeMonitorEvidence(snapshot.Details))
	_, err := tx.ExecContext(ctx, `INSERT INTO monitor_resource_snapshots(
		cluster, instance, asset_id, asset_name, asset_kind, strategy, collector_method,
		collected_at_ms, run_id, status,
		cpu_cores, cpu_total_cores, cpu_percent,
		memory_used_bytes, memory_total_bytes, memory_percent,
		disk_used_bytes, disk_total_bytes, disk_percent,
		net_bytes_per_sec, disk_iops, disk_latency_ms, gpu_percent,
		response_time_ms, error_rate, active_connections, requests_per_sec,
		uptime_seconds, load_1m, os_info, kernel_info, tags_json, details_json
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		snapshot.Cluster, snapshot.Instance, snapshot.AssetID, snapshot.AssetName, snapshot.AssetKind, snapshot.Strategy, snapshot.CollectorMethod,
		snapshot.CollectedAtMs, runID, snapshot.Status,
		snapshot.CPUCores, snapshot.CPUTotalCores, snapshot.CPUPercent,
		snapshot.MemoryUsedBytes, snapshot.MemoryTotalBytes, snapshot.MemoryPercent,
		snapshot.DiskUsedBytes, snapshot.DiskTotalBytes, snapshot.DiskPercent,
		snapshot.NetBytesPerSec, snapshot.DiskIOPS, snapshot.DiskLatencyMs, snapshot.GPUPercent,
		snapshot.ResponseTimeMs, snapshot.ErrorRate, snapshot.ActiveConnections, snapshot.RequestsPerSec,
		snapshot.UptimeSeconds, snapshot.Load1m, snapshot.OSInfo, snapshot.KernelInfo, string(tagsJSON), string(detailsJSON),
	)
	return err
}

// persistMonitorRunItemArtifact writes the sanitized payload to
// <dbDir>/artifacts (file-first) and stores only the index columns. When file
// storage is unavailable — or a write fails — the sanitized payload stays in
// the SQLite text columns so no inspection evidence is lost. Legacy rows keep
// reading those columns (see GetMonitorRunDetail).
func (s *GatewayStore) persistMonitorRunItemArtifact(ctx context.Context, tx *sql.Tx, runID string, itemID int64, item monitorRunItemRecord, artifactJSONStr string) error {
	atMs := item.StartedAtMs
	if atMs <= 0 {
		atMs = time.Now().UnixMilli()
	}
	content, truncated := prepareMonitorArtifactForItem(item)
	if !monitorArtifactContentEmpty(content) {
		dir, size, digest, err := s.writeMonitorArtifactFiles(runID, itemID, atMs, content)
		if err == nil && dir != "" {
			truncatedValue := 0
			if truncated {
				truncatedValue = 1
			}
			_, err = tx.ExecContext(ctx, `UPDATE monitor_run_items
				SET artifact_dir=?, artifact_bytes=?, artifact_sha256=?, artifact_truncated=? WHERE id=?`,
				dir, size, digest, truncatedValue, itemID)
			return err
		}
		if err != nil && !errors.Is(err, errMonitorArtifactStorageDisabled) {
			// Keep the run usable: fall back to the SQLite text columns below
			// instead of failing the whole persistence.
			_ = err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE monitor_run_items
		SET artifact_markdown=?, artifact_json=?, raw_output=? WHERE id=?`,
		sanitizeAndCapArtifact(item.ArtifactMarkdown), artifactJSONStr, sanitizeAndCapArtifact(item.RawOutput), itemID)
	return err
}

// ---------------- Queries (Target-Grant Scoped) ----------------

func (s *GatewayStore) ListMonitorRuns(ctx context.Context, auth TokenAuth, scheduleID, statusFilter string, limit int) ([]monitorRunRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	isAdmin := s.UserIsPlatformAdmin(auth.UserID)
	where := []string{"1=1"}
	args := []any{}
	if scheduleID != "" {
		where = append(where, "r.schedule_id=?")
		args = append(args, scheduleID)
	}
	if statusFilter != "" {
		where = append(where, "r.status=?")
		args = append(args, statusFilter)
	}
	if !isAdmin {
		where = append(where, `(r.triggered_by=? OR EXISTS(
			SELECT 1 FROM monitor_run_items i JOIN grants g ON g.user_id=?
			WHERE i.run_id=r.id AND g.cluster IN ('*', i.cluster) AND g.instance IN ('*', i.instance)
		))`)
		args = append(args, auth.UserID, auth.UserID)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.schedule_id, r.schedule_name, r.trigger_kind, r.triggered_by, r.status,
		r.skills_json, r.prompt, r.command,
		r.total_targets, r.total_assets, r.passed_checks, r.warning_checks, r.failed_checks, r.skipped_checks,
		r.offline_targets, r.metrics_written, r.started_at_ms, r.finished_at_ms, r.duration_ms, r.summary
		FROM monitor_runs r WHERE `+strings.Join(where, " AND ")+` ORDER BY r.started_at_ms DESC, r.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []monitorRunRecord{}
	for rows.Next() {
		var rec monitorRunRecord
		var skillsRaw string
		if err := rows.Scan(
			&rec.ID, &rec.ScheduleID, &rec.ScheduleName, &rec.TriggerKind, &rec.TriggeredBy, &rec.Status,
			&skillsRaw, &rec.Prompt, &rec.Command,
			&rec.TotalTargets, &rec.TotalAssets, &rec.PassedChecks, &rec.WarningChecks, &rec.FailedChecks, &rec.SkippedChecks,
			&rec.OfflineTargets, &rec.MetricsWritten, &rec.StartedAtMs, &rec.FinishedAtMs, &rec.DurationMs, &rec.Summary,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(skillsRaw), &rec.Skills)
		rec.Skills = normalizeSkillSlugs(rec.Skills)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *GatewayStore) GetMonitorRunDetail(ctx context.Context, auth TokenAuth, runID string) (monitorRunRecord, []monitorRunItemRecord, error) {
	var run monitorRunRecord
	var skillsRaw string
	err := s.db.QueryRowContext(ctx, `SELECT id, schedule_id, schedule_name, trigger_kind, triggered_by, status,
		skills_json, prompt, command,
		total_targets, total_assets, passed_checks, warning_checks, failed_checks, skipped_checks,
		offline_targets, metrics_written, started_at_ms, finished_at_ms, duration_ms, summary
		FROM monitor_runs WHERE id=?`, runID).Scan(
		&run.ID, &run.ScheduleID, &run.ScheduleName, &run.TriggerKind, &run.TriggeredBy, &run.Status,
		&skillsRaw, &run.Prompt, &run.Command,
		&run.TotalTargets, &run.TotalAssets, &run.PassedChecks, &run.WarningChecks, &run.FailedChecks, &run.SkippedChecks,
		&run.OfflineTargets, &run.MetricsWritten, &run.StartedAtMs, &run.FinishedAtMs, &run.DurationMs, &run.Summary,
	)
	if err != nil {
		return run, nil, err
	}
	_ = json.Unmarshal([]byte(skillsRaw), &run.Skills)
	run.Skills = normalizeSkillSlugs(run.Skills)
	isAdmin := s.UserIsPlatformAdmin(auth.UserID)
	query := `SELECT i.id, i.run_id, i.cluster, i.instance, i.asset_id, i.asset_name, i.asset_kind, i.strategy, i.collector_method,
		i.status, i.reason, i.started_at_ms, i.finished_at_ms, i.duration_ms, i.metrics_json, i.checks_json, i.evidence_json,
		i.artifact_format, i.artifact_markdown, i.artifact_json, i.raw_output,
		i.artifact_dir, i.artifact_bytes, i.artifact_sha256, i.artifact_truncated
		FROM monitor_run_items i WHERE i.run_id=?`
	args := []any{runID}
	if !isAdmin {
		query += ` AND EXISTS(SELECT 1 FROM grants g WHERE g.user_id=? AND g.cluster IN ('*', i.cluster) AND g.instance IN ('*', i.instance))`
		args = append(args, auth.UserID)
	}
	query += ` ORDER BY i.id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return run, nil, err
	}
	defer rows.Close()
	items := []monitorRunItemRecord{}
	for rows.Next() {
		var item monitorRunItemRecord
		var metricsRaw, checksRaw, evidenceRaw, artifactJSONRaw string
		var artifactTruncated int
		if err := rows.Scan(
			&item.ID, &item.RunID, &item.Cluster, &item.Instance, &item.AssetID, &item.AssetName, &item.AssetKind,
			&item.Strategy, &item.CollectorMethod, &item.Status, &item.Reason,
			&item.StartedAtMs, &item.FinishedAtMs, &item.DurationMs, &metricsRaw, &checksRaw, &evidenceRaw,
			&item.ArtifactFormat, &item.ArtifactMarkdown, &artifactJSONRaw, &item.RawOutput,
			&item.ArtifactDir, &item.ArtifactBytes, &item.ArtifactSHA256, &artifactTruncated,
		); err != nil {
			return run, nil, err
		}
		item.ArtifactTruncated = artifactTruncated != 0
		item.Checks = []api.MonitorInspectCheck{}
		_ = json.Unmarshal([]byte(metricsRaw), &item.Metrics)
		_ = json.Unmarshal([]byte(checksRaw), &item.Checks)
		_ = json.Unmarshal([]byte(evidenceRaw), &item.Evidence)
		// File-first: a row with an artifact directory reads the full payload
		// back from disk. Legacy rows without one keep their SQLite text
		// columns, so upgrades stay lossless.
		if item.ArtifactDir != "" {
			if content, ok := s.readMonitorArtifactFiles(item.ArtifactDir); ok {
				item.ArtifactMarkdown = content.Markdown
				item.ArtifactJSON = content.JSON
				item.RawOutput = content.Raw
			}
		}
		if len(item.ArtifactJSON) == 0 {
			if compact, ok := compactValidJSON(artifactJSONRaw); ok {
				item.ArtifactJSON = compact
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return run, nil, err
	}
	if !isAdmin && len(items) == 0 && run.TriggeredBy != auth.UserID {
		return run, nil, sql.ErrNoRows
	}
	// Close the item cursor before the diff lookups so the single write
	// connection is free (SQLite store runs with MaxOpenConns=1).
	_ = rows.Close()
	s.attachMonitorRunItemDiffs(ctx, run, items)
	return run, items, nil
}

func scanResourceSnapshot(row interface{ Scan(...any) error }) (monitorResourceSnapshot, error) {
	var snap monitorResourceSnapshot
	var tagsRaw, detailsRaw string
	err := row.Scan(
		&snap.ID, &snap.Cluster, &snap.Instance, &snap.AssetID, &snap.AssetName, &snap.AssetKind,
		&snap.Strategy, &snap.CollectorMethod, &snap.CollectedAtMs, &snap.RunID, &snap.Status,
		&snap.CPUCores, &snap.CPUTotalCores, &snap.CPUPercent,
		&snap.MemoryUsedBytes, &snap.MemoryTotalBytes, &snap.MemoryPercent,
		&snap.DiskUsedBytes, &snap.DiskTotalBytes, &snap.DiskPercent,
		&snap.NetBytesPerSec, &snap.DiskIOPS, &snap.DiskLatencyMs, &snap.GPUPercent,
		&snap.ResponseTimeMs, &snap.ErrorRate, &snap.ActiveConnections, &snap.RequestsPerSec,
		&snap.UptimeSeconds, &snap.Load1m, &snap.OSInfo, &snap.KernelInfo, &tagsRaw, &detailsRaw,
	)
	if err != nil {
		return snap, err
	}
	snap.Tags = []string{}
	_ = json.Unmarshal([]byte(tagsRaw), &snap.Tags)
	_ = json.Unmarshal([]byte(detailsRaw), &snap.Details)
	return snap, nil
}

func (h *GatewayHub) BuildMonitorOverview(ctx context.Context, auth TokenAuth, rangeKey, clusterFilter, kindFilter, tagFilter string) (monitorOverviewResponse, error) {
	rName, window, bucketCount, ok := monitorEngineRangeDuration(rangeKey)
	if !ok {
		return monitorOverviewResponse{}, errors.New("invalid_monitor_range")
	}
	now := time.Now().UTC()
	toMs := now.UnixMilli()
	fromMs := now.Add(-window).UnixMilli()
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
	scope := "self"
	if isAdmin {
		scope = "global"
	}

	// Build live target status map.
	liveTargets := make(map[string]bool)
	for _, t := range h.ListTargets() {
		if isAdmin || h.store.UserCanUseTarget(auth.UserID, t.Cluster, t.Instance) {
			if clusterFilter == "" || t.Cluster == clusterFilter {
				liveTargets[tunnelKey(t.Cluster, t.Instance)] = true
			}
		}
	}
	statuses, _ := h.store.ListAgentStatus()
	knownTargets := make(map[string]bool)
	for _, st := range statuses {
		if meaningfulInstanceTime(st.ArchivedAt) {
			continue
		}
		if !isAdmin && !h.store.UserCanUseTarget(auth.UserID, st.Cluster, st.Instance) {
			continue
		}
		if clusterFilter != "" && st.Cluster != clusterFilter {
			continue
		}
		knownTargets[tunnelKey(st.Cluster, st.Instance)] = true
	}
	for k := range liveTargets {
		knownTargets[k] = true
	}

	// Query latest snapshot per (cluster, instance, asset_kind, asset_id).
	where := []string{"1=1"}
	args := []any{}
	if clusterFilter != "" {
		where = append(where, "s.cluster=?")
		args = append(args, clusterFilter)
	}
	if kindFilter != "" {
		where = append(where, "s.asset_kind=?")
		args = append(args, kindFilter)
	}
	if !isAdmin {
		where = append(where, `EXISTS(SELECT 1 FROM grants g WHERE g.user_id=? AND g.cluster IN ('*', s.cluster) AND g.instance IN ('*', s.instance))`)
		args = append(args, auth.UserID)
	}

	latestSQL := `SELECT s.id, s.cluster, s.instance, s.asset_id, s.asset_name, s.asset_kind,
		s.strategy, s.collector_method, s.collected_at_ms, s.run_id, s.status,
		s.cpu_cores, s.cpu_total_cores, s.cpu_percent,
		s.memory_used_bytes, s.memory_total_bytes, s.memory_percent,
		s.disk_used_bytes, s.disk_total_bytes, s.disk_percent,
		s.net_bytes_per_sec, s.disk_iops, s.disk_latency_ms, s.gpu_percent,
		s.response_time_ms, s.error_rate, s.active_connections, s.requests_per_sec,
		s.uptime_seconds, s.load_1m, s.os_info, s.kernel_info, s.tags_json, s.details_json
		FROM monitor_resource_snapshots s
		JOIN (
			SELECT cluster, instance, asset_kind, asset_id, MAX(id) AS max_id
			FROM monitor_resource_snapshots
			GROUP BY cluster, instance, asset_kind, asset_id
		) latest ON latest.max_id = s.id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY s.cluster, s.instance, s.asset_kind, s.asset_id`

	rows, err := h.store.db.QueryContext(ctx, latestSQL, args...)
	if err != nil {
		return monitorOverviewResponse{}, err
	}
	defer rows.Close()
	resources := []monitorResourceSnapshot{}
	tagFilterLower := strings.ToLower(strings.TrimSpace(tagFilter))
	for rows.Next() {
		snap, err := scanResourceSnapshot(rows)
		if err != nil {
			return monitorOverviewResponse{}, err
		}
		if tagFilterLower != "" {
			matched := false
			for _, t := range snap.Tags {
				if strings.ToLower(t) == tagFilterLower {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		snap.Online = liveTargets[tunnelKey(snap.Cluster, snap.Instance)]
		snap.Stale = toMs-snap.CollectedAtMs > 15*60*1000
		resources = append(resources, snap)
	}
	if err := rows.Err(); err != nil {
		return monitorOverviewResponse{}, err
	}

	// Aggregate summary stats.
	var summary monitorOverviewSummary
	summary.OnlineAgents = len(liveTargets)
	summary.OfflineAgents = max(0, len(knownTargets)-len(liveTargets))
	summary.TotalAssets = len(resources)
	byKind := map[string]int{}
	var cpuPctSum, memPctSum, diskPctSum float64
	var cpuPctCnt, memPctCnt, diskPctCnt int
	coveredSet := map[string]bool{}
	for _, r := range resources {
		byKind[r.AssetKind]++
		if r.CollectedAtMs > summary.LastCollectedAtMs {
			summary.LastCollectedAtMs = r.CollectedAtMs
		}
		if r.Online && !r.Stale && r.Status != "offline" && r.Status != "error" {
			summary.CoveredAssets++
			coveredSet[tunnelKey(r.Cluster, r.Instance)] = true
		}
		switch {
		case !r.Online || r.Status == "offline":
			summary.OfflineAssets++
		case r.Status == "critical" || r.Status == "error":
			summary.CriticalAssets++
		case r.Status == "warning":
			summary.WarningAssets++
		default:
			summary.HealthyAssets++
		}
		// Avoid double-counting container/k8s totals on top of host totals in fleet capacity KPIs,
		// while still averaging all assets that report utilization percentages.
		if r.AssetKind == "vm" || r.AssetKind == "physical" || r.AssetKind == "k8s" {
			if r.CPUPercent != nil {
				cpuPctSum += *r.CPUPercent
				cpuPctCnt++
			}
			if r.MemoryPercent != nil {
				memPctSum += *r.MemoryPercent
				memPctCnt++
			}
			if r.DiskPercent != nil {
				diskPctSum += *r.DiskPercent
				diskPctCnt++
			}
		}
		if r.AssetKind == "vm" || r.AssetKind == "physical" {
			if r.CPUTotalCores != nil {
				summary.TotalCPUCores += *r.CPUTotalCores
			}
			if r.CPUCores != nil {
				summary.UsedCPUCores += *r.CPUCores
			}
			if r.MemoryTotalBytes != nil {
				summary.TotalMemoryBytes += *r.MemoryTotalBytes
			}
			if r.MemoryUsedBytes != nil {
				summary.UsedMemoryBytes += *r.MemoryUsedBytes
			}
			if r.DiskTotalBytes != nil {
				summary.TotalDiskBytes += *r.DiskTotalBytes
			}
			if r.DiskUsedBytes != nil {
				summary.UsedDiskBytes += *r.DiskUsedBytes
			}
		}
	}
	if cpuPctCnt > 0 {
		summary.AvgCPUPercent = math.Round((cpuPctSum/float64(cpuPctCnt))*10) / 10
	}
	if memPctCnt > 0 {
		summary.AvgMemoryPercent = math.Round((memPctSum/float64(memPctCnt))*10) / 10
	}
	if diskPctCnt > 0 {
		summary.AvgDiskPercent = math.Round((diskPctSum/float64(diskPctCnt))*10) / 10
	}
	denom := len(resources)
	if len(knownTargets) > denom {
		denom = len(knownTargets)
	}
	if denom > 0 {
		summary.ProbeCoveragePct = math.Round((float64(summary.CoveredAssets)/float64(denom))*1000) / 10
	}

	_ = h.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_schedules WHERE enabled=1`).Scan(&summary.ActiveSchedules)
	_ = h.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_runs`).Scan(&summary.TotalRuns)

	thresholdAlerts := h.store.readOpenThresholdAlerts(ctx, auth, isAdmin, clusterFilter)
	summary.OpenThresholdAlerts = len(thresholdAlerts)

	// Top consumers (up to 5 each).
	topCPU := topResourcesBy(resources, func(r monitorResourceSnapshot) (float64, bool) {
		if r.CPUPercent == nil {
			return 0, false
		}
		return *r.CPUPercent, true
	}, 5)
	topMem := topResourcesBy(resources, func(r monitorResourceSnapshot) (float64, bool) {
		if r.MemoryPercent == nil {
			return 0, false
		}
		return *r.MemoryPercent, true
	}, 5)
	topDisk := topResourcesBy(resources, func(r monitorResourceSnapshot) (float64, bool) {
		if r.DiskPercent == nil {
			return 0, false
		}
		return *r.DiskPercent, true
	}, 5)

	// Fleet utilization trend buckets over [fromMs, toMs].
	trends, err := h.store.readMonitorTrendBuckets(ctx, auth, isAdmin, fromMs, toMs, bucketCount, clusterFilter, kindFilter)
	if err != nil {
		return monitorOverviewResponse{}, err
	}

	recentRuns, err := h.store.ListMonitorRuns(ctx, auth, "", "", 10)
	if err != nil {
		return monitorOverviewResponse{}, err
	}

	return monitorOverviewResponse{
		Scope:           scope,
		Range:           rName,
		FromMs:          fromMs,
		ToMs:            toMs,
		Summary:         summary,
		ByKind:          byKind,
		Resources:       resources,
		TopCPU:          topCPU,
		TopMemory:       topMem,
		TopDisk:         topDisk,
		Trends:          trends,
		ThresholdAlerts: thresholdAlerts,
		RecentRuns:      recentRuns,
	}, nil
}

func (s *GatewayStore) readOpenThresholdAlerts(ctx context.Context, auth TokenAuth, isAdmin bool, clusterFilter string) []monitorOpenThresholdAlert {
	out := []monitorOpenThresholdAlert{}
	if !s.tableExists(ctx, "asset_monitor_alerts") || !s.tableExists(ctx, "asset_monitor_series") {
		return out
	}
	where := []string{"a.state='open'"}
	args := []any{}
	if clusterFilter != "" {
		where = append(where, "s.cluster=?")
		args = append(args, clusterFilter)
	}
	if !isAdmin {
		where = append(where, `EXISTS(SELECT 1 FROM grants g WHERE g.user_id=? AND g.cluster IN ('*', s.cluster) AND g.instance IN ('*', s.instance))`)
		args = append(args, auth.UserID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.cluster, s.instance, s.type, s.asset_id, s.namespace, s.origin,
		s.metric, s.device_id, a.threshold, a.duration_seconds, a.opened_ms
		FROM asset_monitor_alerts a JOIN asset_monitor_series s ON s.id = a.series_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY a.opened_ms DESC LIMIT 50`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var item monitorOpenThresholdAlert
		if rows.Scan(&item.Cluster, &item.Instance, &item.AssetType, &item.AssetID, &item.Namespace, &item.Origin,
			&item.Metric, &item.DeviceID, &item.Threshold, &item.DurationSeconds, &item.OpenedMs) == nil {
			out = append(out, item)
		}
	}
	return out
}

func topResourcesBy(list []monitorResourceSnapshot, fn func(monitorResourceSnapshot) (float64, bool), limit int) []monitorResourceSnapshot {
	type scored struct {
		row monitorResourceSnapshot
		val float64
	}
	var items []scored
	for _, r := range list {
		if v, ok := fn(r); ok {
			items = append(items, scored{row: r, val: v})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].val != items[j].val {
			return items[i].val > items[j].val
		}
		return items[i].row.AssetID < items[j].row.AssetID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]monitorResourceSnapshot, 0, len(items))
	for _, it := range items {
		out = append(out, it.row)
	}
	return out
}

func (s *GatewayStore) readMonitorTrendBuckets(
	ctx context.Context, auth TokenAuth, isAdmin bool, fromMs, toMs int64, bucketCount int, clusterFilter, kindFilter string,
) ([]monitorTrendBucket, error) {
	out := make([]monitorTrendBucket, bucketCount)
	span := toMs - fromMs
	if span <= 0 {
		span = 1
	}
	for i := 0; i < bucketCount; i++ {
		out[i] = monitorTrendBucket{
			StartMs: fromMs + (span*int64(i))/int64(bucketCount),
			EndMs:   fromMs + (span*int64(i+1))/int64(bucketCount),
		}
	}
	where := []string{"s.collected_at_ms >= ?", "s.collected_at_ms <= ?"}
	args := []any{fromMs, toMs}
	if clusterFilter != "" {
		where = append(where, "s.cluster=?")
		args = append(args, clusterFilter)
	}
	if kindFilter != "" {
		where = append(where, "s.asset_kind=?")
		args = append(args, kindFilter)
	} else {
		where = append(where, "s.asset_kind IN ('vm','physical','k8s')")
	}
	if !isAdmin {
		where = append(where, `EXISTS(SELECT 1 FROM grants g WHERE g.user_id=? AND g.cluster IN ('*', s.cluster) AND g.instance IN ('*', s.instance))`)
		args = append(args, auth.UserID)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT s.collected_at_ms, s.cpu_percent, s.memory_percent, s.disk_percent
		FROM monitor_resource_snapshots s WHERE `+strings.Join(where, " AND ")+` ORDER BY s.collected_at_ms ASC LIMIT 10000`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	type acc struct {
		cpuSum, memSum, diskSum float64
		cpuCnt, memCnt, diskCnt int
		cpuMax, memMax          float64
	}
	accs := make([]acc, bucketCount)
	for rows.Next() {
		var ts int64
		var cpu, mem, disk sql.NullFloat64
		if err := rows.Scan(&ts, &cpu, &mem, &disk); err != nil {
			return out, err
		}
		idx := int(((ts - fromMs) * int64(bucketCount)) / span)
		if idx < 0 {
			idx = 0
		} else if idx >= bucketCount {
			idx = bucketCount - 1
		}
		out[idx].Samples++
		if cpu.Valid {
			accs[idx].cpuSum += cpu.Float64
			accs[idx].cpuCnt++
			if cpu.Float64 > accs[idx].cpuMax {
				accs[idx].cpuMax = cpu.Float64
			}
		}
		if mem.Valid {
			accs[idx].memSum += mem.Float64
			accs[idx].memCnt++
			if mem.Float64 > accs[idx].memMax {
				accs[idx].memMax = mem.Float64
			}
		}
		if disk.Valid {
			accs[idx].diskSum += disk.Float64
			accs[idx].diskCnt++
		}
	}
	for i := range out {
		if accs[i].cpuCnt > 0 {
			out[i].AvgCPUPercent = monitorFloatPtr(accs[i].cpuSum / float64(accs[i].cpuCnt))
			out[i].MaxCPUPercent = monitorFloatPtr(accs[i].cpuMax)
		}
		if accs[i].memCnt > 0 {
			out[i].AvgMemoryPercent = monitorFloatPtr(accs[i].memSum / float64(accs[i].memCnt))
			out[i].MaxMemoryPercent = monitorFloatPtr(accs[i].memMax)
		}
		if accs[i].diskCnt > 0 {
			out[i].AvgDiskPercent = monitorFloatPtr(accs[i].diskSum / float64(accs[i].diskCnt))
		}
	}
	return out, rows.Err()
}

func (h *GatewayHub) ReadMonitorResourceHistory(ctx context.Context, auth TokenAuth, cluster, instance, assetKind, assetID, rangeKey string) (monitorHistoryResponse, error) {
	if !monitorID(cluster) || !monitorID(instance) || !monitorID(assetID) {
		return monitorHistoryResponse{}, fmt.Errorf("invalid_monitor_target")
	}
	if !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
		return monitorHistoryResponse{}, ErrForbidden
	}
	rName, window, _, ok := monitorEngineRangeDuration(rangeKey)
	if !ok {
		return monitorHistoryResponse{}, fmt.Errorf("invalid_monitor_range")
	}
	now := time.Now().UTC()
	toMs := now.UnixMilli()
	fromMs := now.Add(-window).UnixMilli()

	query := `SELECT id, cluster, instance, asset_id, asset_name, asset_kind,
		strategy, collector_method, collected_at_ms, run_id, status,
		cpu_cores, cpu_total_cores, cpu_percent,
		memory_used_bytes, memory_total_bytes, memory_percent,
		disk_used_bytes, disk_total_bytes, disk_percent,
		net_bytes_per_sec, disk_iops, disk_latency_ms, gpu_percent,
		response_time_ms, error_rate, active_connections, requests_per_sec,
		uptime_seconds, load_1m, os_info, kernel_info, tags_json, details_json
		FROM monitor_resource_snapshots
		WHERE cluster=? AND instance=? AND asset_id=? AND (?='' OR asset_kind=?)
			AND collected_at_ms BETWEEN ? AND ?
		ORDER BY collected_at_ms ASC LIMIT 1000`
	rows, err := h.store.db.QueryContext(ctx, query, cluster, instance, assetID, assetKind, assetKind, fromMs, toMs)
	if err != nil {
		return monitorHistoryResponse{}, err
	}
	defer rows.Close()
	points := []monitorResourceSnapshot{}
	online := h.getAgent(cluster, instance) != nil
	for rows.Next() {
		snap, err := scanResourceSnapshot(rows)
		if err != nil {
			return monitorHistoryResponse{}, err
		}
		snap.Online = online
		snap.Stale = toMs-snap.CollectedAtMs > 15*60*1000
		points = append(points, snap)
	}
	if err := rows.Err(); err != nil {
		return monitorHistoryResponse{}, err
	}
	resp := monitorHistoryResponse{
		Cluster:   cluster,
		Instance:  instance,
		AssetKind: assetKind,
		AssetID:   assetID,
		Range:     rName,
		FromMs:    fromMs,
		ToMs:      toMs,
		Points:    points,
	}
	if len(points) > 0 {
		last := points[len(points)-1]
		resp.Latest = &last
	}
	return resp, nil
}

// PruneMonitorEngineHistory is the hourly fallback entrypoint. It always scans:
// the throttle only applies to the post-run trigger, so a quiet gateway still
// reclaims space (see monitor_retention.go).
func (s *GatewayStore) PruneMonitorEngineHistory(now time.Time) {
	s.PruneMonitorRetention(now, monitorRetentionPolicyFromEnv(), true)
}
