package server

// Inspection metric time series + cross-run diff (SKILL_FIRST_INSPECTION_SPEC.md
// §4.1-§4.3). Artifact JSON stays the source of truth on disk; this file only
// projects the numeric metrics it carries into an indexed table so historical
// trends and run-to-run diffs stay cheap to query.

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/user/doops/agent/api"
)

const (
	// maxInspectionMetricPointsPerItem bounds how many metric points one
	// inspection artifact can contribute, so a hostile or buggy payload cannot
	// flood the series table.
	maxInspectionMetricPointsPerItem = 200
	// monitorMetricSeriesMaxRows caps the rows scanned for one trend query and
	// monitorMetricSeriesMaxPointsPerKey keeps the newest points per series.
	monitorMetricSeriesMaxRows         = 4000
	monitorMetricSeriesMaxPointsPerKey = 720
)

var monitorMetricKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// fallbackTopLevelMetricKeys are protocol/control fields of the optional
// artifact JSON envelope (see spec §4.1). They are never treated as metrics.
var fallbackTopLevelMetricKeys = map[string]bool{
	"status":        true,
	"summary":       true,
	"metrics":       true,
	"checks":        true,
	"charts":        true,
	"details":       true,
	"evidence":      true,
	"series_points": true,
	"run_id":        true,
	"generated_at":  true,
	"timestamp":     true,
}

type monitorMetricPoint struct {
	Key    string
	Label  string
	Unit   string
	Status string
	Value  float64
}

// monitorMetricExtraction is the per-item result of parsing an artifact JSON
// payload: the numeric points to persist plus the count of entries rejected as
// non-numeric/malformed (recorded on the item evidence for observability).
type monitorMetricExtraction struct {
	Points        []monitorMetricPoint
	Skipped       int
	SkillSlug     string
	CollectedAtMs int64
}

type monitorMetricSeriesPoint struct {
	AtMs   int64   `json:"at_ms"`
	Value  float64 `json:"value"`
	Status string  `json:"status,omitempty"`
	RunID  string  `json:"run_id,omitempty"`
}

type monitorMetricSeries struct {
	Key    string                     `json:"key"`
	Label  string                     `json:"label,omitempty"`
	Unit   string                     `json:"unit,omitempty"`
	Points []monitorMetricSeriesPoint `json:"points"`
}

type monitorMetricSeriesResponse struct {
	Skill    string                `json:"skill"`
	Cluster  string                `json:"cluster"`
	Instance string                `json:"instance"`
	Range    string                `json:"range"`
	FromMs   int64                 `json:"from_ms"`
	ToMs     int64                 `json:"to_ms"`
	Series   []monitorMetricSeries `json:"series"`
}

// monitorMetricRangeDuration supports the trend ranges from spec §4.2, which
// extend the dashboard ranges with 30d.
func monitorMetricRangeDuration(raw string) (string, time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "1h":
		return "1h", time.Hour, true
	case "6h":
		return "6h", 6 * time.Hour, true
	case "24h":
		return "24h", 24 * time.Hour, true
	case "7d":
		return "7d", 7 * 24 * time.Hour, true
	case "30d":
		return "30d", 30 * 24 * time.Hour, true
	default:
		return "", 0, false
	}
}

// monitorItemSkillSlug recovers the producing skill slug from the item. Skill
// runs are recorded as strategy "skill:<slug>" with the slug as asset id, so
// this stays consistent even for items persisted before this table existed.
func monitorItemSkillSlug(item monitorRunItemRecord) string {
	if item.Evidence != nil {
		if raw, ok := item.Evidence["skill_slug"].(string); ok {
			slug := strings.ToLower(strings.TrimSpace(raw))
			if skillSlugPattern.MatchString(slug) {
				return slug
			}
		}
	}
	if strings.HasPrefix(item.Strategy, "skill:") {
		slug := strings.TrimPrefix(item.Strategy, "skill:")
		if skillSlugPattern.MatchString(slug) {
			return slug
		}
	}
	if item.AssetKind == "skill" && skillSlugPattern.MatchString(item.AssetID) {
		return item.AssetID
	}
	return ""
}

func monitorMetricCollectedAtMs(run monitorRunRecord, item monitorRunItemRecord) int64 {
	if item.FinishedAtMs > 0 {
		return item.FinishedAtMs
	}
	if item.StartedAtMs > 0 {
		return item.StartedAtMs
	}
	if run.FinishedAtMs > 0 {
		return run.FinishedAtMs
	}
	if run.StartedAtMs > 0 {
		return run.StartedAtMs
	}
	return time.Now().UnixMilli()
}

func extractInspectionMetricForItem(run monitorRunRecord, item monitorRunItemRecord) monitorMetricExtraction {
	points, skipped := extractMonitorArtifactMetrics(item.ArtifactJSON, item.Status)
	return monitorMetricExtraction{
		Points:        points,
		Skipped:       skipped,
		SkillSlug:     monitorItemSkillSlug(item),
		CollectedAtMs: monitorMetricCollectedAtMs(run, item),
	}
}

// extractMonitorArtifactMetrics implements the optional protocol from spec
// §4.1: prefer the metrics[] array, otherwise accept flat numeric top-level
// fields. Non-numeric values are skipped and counted.
func extractMonitorArtifactMetrics(raw json.RawMessage, fallbackStatus string) ([]monitorMetricPoint, int) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, 0
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, 0
	}
	if metricsRaw, ok := top["metrics"]; ok {
		var entries []json.RawMessage
		if json.Unmarshal(metricsRaw, &entries) == nil {
			points := make([]monitorMetricPoint, 0, len(entries))
			seen := make(map[string]bool, len(entries))
			skipped := 0
			for _, entry := range entries {
				var rawEntry struct {
					Key    string `json:"key"`
					Label  string `json:"label"`
					Value  any    `json:"value"`
					Unit   string `json:"unit"`
					Status string `json:"status"`
				}
				if json.Unmarshal(entry, &rawEntry) != nil {
					skipped++
					continue
				}
				key := strings.TrimSpace(rawEntry.Key)
				if !monitorMetricKeyPattern.MatchString(key) || seen[key] || len(points) >= maxInspectionMetricPointsPerItem {
					skipped++
					continue
				}
				value, ok := monitorNumericValue(rawEntry.Value)
				if !ok {
					skipped++
					continue
				}
				seen[key] = true
				status := strings.TrimSpace(rawEntry.Status)
				if status == "" {
					status = fallbackStatus
				}
				points = append(points, monitorMetricPoint{
					Key:    key,
					Label:  monitorMetricClampText(rawEntry.Label, 120),
					Unit:   monitorMetricClampText(rawEntry.Unit, 32),
					Status: monitorMetricClampText(status, 32),
					Value:  value,
				})
			}
			return points, skipped
		}
	}
	points := make([]monitorMetricPoint, 0, len(top))
	for key, valueRaw := range top {
		if fallbackTopLevelMetricKeys[key] || strings.HasPrefix(key, "_") || !monitorMetricKeyPattern.MatchString(key) {
			continue
		}
		var decoded any
		if json.Unmarshal(valueRaw, &decoded) != nil {
			continue
		}
		value, ok := monitorNumericValue(decoded)
		if !ok {
			continue
		}
		points = append(points, monitorMetricPoint{
			Key:    key,
			Status: monitorMetricClampText(fallbackStatus, 32),
			Value:  value,
		})
		if len(points) >= maxInspectionMetricPointsPerItem {
			break
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Key < points[j].Key })
	return points, 0
}

func monitorNumericValue(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func monitorMetricClampText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// persistInspectionMetricExtractions writes the extracted points and bridges
// the standard host/container keys into monitor_resource_snapshots so the
// situational-awareness view keeps receiving real snapshots (spec §4.2).
func persistInspectionMetricExtractions(ctx context.Context, tx *sql.Tx, run monitorRunRecord, items []monitorRunItemRecord, extractions []monitorMetricExtraction, snapshots []monitorResourceSnapshot) error {
	if len(items) != len(extractions) {
		return nil
	}
	var hasMetricTable bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='inspection_metric_points')`).Scan(&hasMetricTable); err != nil {
		return err
	}
	if !hasMetricTable {
		return nil
	}
	providedSnapshotTargets := make(map[string]bool, len(snapshots))
	for _, snap := range snapshots {
		providedSnapshotTargets[tunnelKey(snap.Cluster, snap.Instance)] = true
	}
	for i, item := range items {
		extraction := extractions[i]
		if len(extraction.Points) == 0 {
			continue
		}
		for _, point := range extraction.Points {
			if _, err := tx.ExecContext(ctx, `INSERT INTO inspection_metric_points(
				collected_at_ms, run_id, schedule_id, cluster, instance, skill_slug,
				metric_key, metric_label, value, unit, status
			) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
				extraction.CollectedAtMs, run.ID, run.ScheduleID, item.Cluster, item.Instance, extraction.SkillSlug,
				point.Key, point.Label, point.Value, point.Unit, point.Status,
			); err != nil {
				return err
			}
		}
		if providedSnapshotTargets[tunnelKey(item.Cluster, item.Instance)] {
			// The strategy already produced a richer snapshot for this target;
			// bridging again would duplicate the same point.
			continue
		}
		if err := bridgeInspectionMetricSnapshot(ctx, tx, run, item, extraction); err != nil {
			return err
		}
	}
	return nil
}

func bridgeInspectionMetricSnapshot(ctx context.Context, tx *sql.Tx, run monitorRunRecord, item monitorRunItemRecord, extraction monitorMetricExtraction) error {
	snap := monitorResourceSnapshot{
		Cluster:         item.Cluster,
		Instance:        item.Instance,
		AssetID:         item.AssetID,
		AssetName:       item.AssetName,
		AssetKind:       item.AssetKind,
		Strategy:        item.Strategy,
		CollectorMethod: item.CollectorMethod,
		CollectedAtMs:   extraction.CollectedAtMs,
		RunID:           run.ID,
		Status:          item.Status,
		Details:         map[string]any{"source": "inspection_metrics", "skill_slug": extraction.SkillSlug},
	}
	matched := false
	for _, point := range extraction.Points {
		value := point.Value
		switch point.Key {
		case "cpu_percent":
			snap.CPUPercent = &value
		case "memory_percent":
			snap.MemoryPercent = &value
		case "disk_percent":
			snap.DiskPercent = &value
		case "load_1m":
			snap.Load1m = &value
		case "uptime_seconds":
			snap.UptimeSeconds = &value
		case "memory_used_bytes":
			snap.MemoryUsedBytes = &value
		case "memory_total_bytes":
			snap.MemoryTotalBytes = &value
		case "disk_used_bytes":
			snap.DiskUsedBytes = &value
		case "disk_total_bytes":
			snap.DiskTotalBytes = &value
		default:
			continue
		}
		matched = true
	}
	if !matched {
		return nil
	}
	return insertMonitorResourceSnapshot(ctx, tx, run.ID, snap)
}

// ListInspectionMetricSeries returns the trend points for one skill on one
// target, grouped by metric_key and ordered oldest to newest.
func (s *GatewayStore) ListInspectionMetricSeries(ctx context.Context, cluster, instance, skill string, fromMs, toMs int64) ([]monitorMetricSeries, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT metric_key, metric_label, unit, collected_at_ms, value, status, run_id
		FROM inspection_metric_points
		WHERE skill_slug=? AND cluster=? AND instance=? AND collected_at_ms>=? AND collected_at_ms<=?
		ORDER BY collected_at_ms DESC, id DESC
		LIMIT ?`,
		skill, cluster, instance, fromMs, toMs, monitorMetricSeriesMaxRows,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byKey := make(map[string]*monitorMetricSeries)
	for rows.Next() {
		var key, label, unit, status, runID string
		var atMs int64
		var value float64
		if err := rows.Scan(&key, &label, &unit, &atMs, &value, &status, &runID); err != nil {
			return nil, err
		}
		series, ok := byKey[key]
		if !ok {
			series = &monitorMetricSeries{Key: key}
			byKey[key] = series
		}
		if series.Label == "" && label != "" {
			series.Label = label
		}
		if series.Unit == "" && unit != "" {
			series.Unit = unit
		}
		series.Points = append(series.Points, monitorMetricSeriesPoint{AtMs: atMs, Value: value, Status: status, RunID: runID})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]monitorMetricSeries, 0, len(byKey))
	for _, series := range byKey {
		sort.Slice(series.Points, func(i, j int) bool {
			if series.Points[i].AtMs != series.Points[j].AtMs {
				return series.Points[i].AtMs < series.Points[j].AtMs
			}
			return series.Points[i].RunID < series.Points[j].RunID
		})
		if len(series.Points) > monitorMetricSeriesMaxPointsPerKey {
			series.Points = series.Points[len(series.Points)-monitorMetricSeriesMaxPointsPerKey:]
		}
		out = append(out, *series)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

type monitorMetricDelta struct {
	Key      string   `json:"key"`
	Label    string   `json:"label,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Previous float64  `json:"previous"`
	Current  float64  `json:"current"`
	Delta    float64  `json:"delta"`
	DeltaPct *float64 `json:"delta_pct"`
}

type monitorRunCheckChange struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	Category       string `json:"category,omitempty"`
	PreviousStatus string `json:"previous_status"`
	CurrentStatus  string `json:"current_status"`
}

type monitorRunDiff struct {
	PreviousRunID  string                    `json:"previous_run_id"`
	PreviousAtMs   int64                     `json:"previous_at_ms"`
	PreviousStatus string                    `json:"previous_status"`
	StatusChanged  bool                      `json:"status_changed"`
	MetricDeltas   []monitorMetricDelta      `json:"metric_deltas"`
	AddedChecks    []api.MonitorInspectCheck `json:"added_checks"`
	ResolvedChecks []api.MonitorInspectCheck `json:"resolved_checks"`
	ChangedChecks  []monitorRunCheckChange   `json:"changed_checks"`
}

// attachMonitorRunItemDiffs fills item.Diff by comparing each item against the
// previous terminal run of the same (cluster, instance, strategy) series.
func (s *GatewayStore) attachMonitorRunItemDiffs(ctx context.Context, run monitorRunRecord, items []monitorRunItemRecord) {
	for i := range items {
		items[i].Diff = s.buildMonitorRunItemDiff(ctx, run, items[i])
	}
}

func (s *GatewayStore) buildMonitorRunItemDiff(ctx context.Context, run monitorRunRecord, item monitorRunItemRecord) *monitorRunDiff {
	var prevRunID, prevStatus, prevChecksRaw, prevArtifactDir, prevArtifactJSONRaw string
	var prevAtMs int64
	var prevItemID int64
	if run.StartedAtMs <= 0 {
		return nil
	}
	err := s.readDB().QueryRowContext(ctx, `SELECT r.id, r.started_at_ms, r.status, i.id, i.checks_json, i.artifact_dir, i.artifact_json
		FROM monitor_run_items i JOIN monitor_runs r ON r.id=i.run_id
		WHERE i.cluster=? AND i.instance=? AND i.strategy=? AND r.id<>? AND r.status<>'' AND r.status<>'running' AND r.started_at_ms<?
		ORDER BY r.started_at_ms DESC, i.id DESC LIMIT 1`,
		item.Cluster, item.Instance, item.Strategy, run.ID, run.StartedAtMs,
	).Scan(&prevRunID, &prevAtMs, &prevStatus, &prevItemID, &prevChecksRaw, &prevArtifactDir, &prevArtifactJSONRaw)
	if err != nil {
		return nil
	}
	_ = prevItemID
	diff := &monitorRunDiff{
		PreviousRunID:  prevRunID,
		PreviousAtMs:   prevAtMs,
		PreviousStatus: prevStatus,
		StatusChanged:  run.Status != "" && prevStatus != run.Status,
		MetricDeltas:   []monitorMetricDelta{},
		AddedChecks:    []api.MonitorInspectCheck{},
		ResolvedChecks: []api.MonitorInspectCheck{},
		ChangedChecks:  []monitorRunCheckChange{},
	}

	prevJSON := []byte(prevArtifactJSONRaw)
	if prevArtifactDir != "" {
		if content, ok := s.readMonitorArtifactFiles(prevArtifactDir); ok && len(content.JSON) > 0 {
			prevJSON = content.JSON
		}
	}
	prevPoints, _ := extractMonitorArtifactMetrics(json.RawMessage(prevJSON), prevStatus)
	currPoints, _ := extractMonitorArtifactMetrics(item.ArtifactJSON, item.Status)
	diff.MetricDeltas = buildMonitorMetricDeltas(prevPoints, currPoints)

	prevChecks := []api.MonitorInspectCheck{}
	_ = json.Unmarshal([]byte(prevChecksRaw), &prevChecks)
	diff.AddedChecks, diff.ResolvedChecks, diff.ChangedChecks = buildMonitorCheckDiff(prevChecks, item.Checks)
	return diff
}

func buildMonitorMetricDeltas(previous, current []monitorMetricPoint) []monitorMetricDelta {
	type pair struct {
		prev, curr *monitorMetricPoint
	}
	pairs := make(map[string]*pair, len(previous)+len(current))
	for i := range previous {
		point := previous[i]
		pairs[point.Key] = &pair{prev: &point}
	}
	for i := range current {
		point := current[i]
		entry, ok := pairs[point.Key]
		if !ok {
			entry = &pair{}
			pairs[point.Key] = entry
		}
		entry.curr = &point
	}
	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	deltas := []monitorMetricDelta{}
	for _, key := range keys {
		entry := pairs[key]
		prevValue, currValue := 0.0, 0.0
		label, unit := "", ""
		if entry.prev != nil {
			prevValue = entry.prev.Value
			label, unit = entry.prev.Label, entry.prev.Unit
		}
		if entry.curr != nil {
			currValue = entry.curr.Value
			if entry.curr.Label != "" {
				label = entry.curr.Label
			}
			if entry.curr.Unit != "" {
				unit = entry.curr.Unit
			}
		}
		delta := roundMonitorMetricValue(currValue - prevValue)
		if math.Abs(delta) < 1e-9 {
			continue
		}
		deltaPct := (*float64)(nil)
		if prevValue != 0 {
			pct := math.Round(delta/prevValue*10000) / 100
			deltaPct = &pct
		}
		deltas = append(deltas, monitorMetricDelta{
			Key:      key,
			Label:    label,
			Unit:     unit,
			Previous: roundMonitorMetricValue(prevValue),
			Current:  roundMonitorMetricValue(currValue),
			Delta:    delta,
			DeltaPct: deltaPct,
		})
	}
	return deltas
}

func roundMonitorMetricValue(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func monitorCheckIdentity(check api.MonitorInspectCheck) string {
	if id := strings.TrimSpace(check.ID); id != "" {
		return "id:" + id
	}
	return "name:" + strings.TrimSpace(check.Name)
}

func buildMonitorCheckDiff(previous, current []api.MonitorInspectCheck) (added, resolved []api.MonitorInspectCheck, changed []monitorRunCheckChange) {
	added = []api.MonitorInspectCheck{}
	resolved = []api.MonitorInspectCheck{}
	changed = []monitorRunCheckChange{}
	prevByKey := make(map[string]api.MonitorInspectCheck, len(previous))
	for _, check := range previous {
		key := monitorCheckIdentity(check)
		if key == "name:" {
			continue
		}
		prevByKey[key] = check
	}
	seen := make(map[string]bool, len(current))
	for _, check := range current {
		key := monitorCheckIdentity(check)
		if key == "name:" {
			continue
		}
		seen[key] = true
		prev, ok := prevByKey[key]
		if !ok {
			added = append(added, check)
			continue
		}
		if prev.Status != check.Status {
			changed = append(changed, monitorRunCheckChange{
				ID:             check.ID,
				Name:           check.Name,
				Category:       check.Category,
				PreviousStatus: prev.Status,
				CurrentStatus:  check.Status,
			})
		}
	}
	for _, check := range previous {
		key := monitorCheckIdentity(check)
		if key == "name:" || seen[key] {
			continue
		}
		resolved = append(resolved, check)
	}
	return added, resolved, changed
}
