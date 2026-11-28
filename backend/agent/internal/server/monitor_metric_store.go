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
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
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

const (
	maxLatestMonitorMetricSkills    = 24
	maxLatestMonitorMetricKeys      = 96
	maxLatestMonitorMetricPairs     = 64
	latestMonitorMetricsRowsPerPair = 500
)

type monitorLatestMetricPair struct {
	Skill string
	Key   string
}

type monitorLatestMetricsBatchMeta struct {
	RequestedPairs int `json:"requested_pairs"`
	ReturnedItems  int `json:"returned_items"`
}

// monitorLatestMetricItem is the fleet-wide aggregated latest value for one
// (skill, key) pair across all targets the caller is authorized to view.
type monitorLatestMetricItem struct {
	Skill            string  `json:"skill"`
	Key              string  `json:"key"`
	Label            string  `json:"label,omitempty"`
	Unit             string  `json:"unit,omitempty"`
	Max              float64 `json:"max"`
	Avg              float64 `json:"avg"`
	Latest           float64 `json:"latest"`
	LatestAtMs       int64   `json:"latest_at_ms"`
	Status           string  `json:"status,omitempty"`
	ReportingTargets int     `json:"reporting_targets"`
}

type monitorLatestMetricsResponse struct {
	Items []monitorLatestMetricItem      `json:"items"`
	Batch *monitorLatestMetricsBatchMeta `json:"batch,omitempty"`
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

// maxArtifactSeriesPointsPerItem bounds how many bridged series points one
// artifact can contribute, mirroring the metrics extraction cap.
const maxArtifactSeriesPointsPerItem = 200

// extractArtifactSeriesPoints reads the optional top-level "series_points"
// array from an artifact JSON payload. Each entry follows the
// api.MonitorInspectSeriesPoint contract; malformed entries are skipped so a
// partial payload still contributes its valid points. The caller validates
// each point again through monitorInput.valid before persisting.
func extractArtifactSeriesPoints(raw json.RawMessage) []api.MonitorInspectSeriesPoint {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	var top struct {
		SeriesPoints []api.MonitorInspectSeriesPoint `json:"series_points"`
	}
	if err := json.Unmarshal(raw, &top); err != nil || len(top.SeriesPoints) == 0 {
		return nil
	}
	if len(top.SeriesPoints) > maxArtifactSeriesPointsPerItem {
		top.SeriesPoints = top.SeriesPoints[:maxArtifactSeriesPointsPerItem]
	}
	return top.SeriesPoints
}

var artifactByteSizePattern = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(b|kib|mib|gib|tib|kb|mb|gb|tb|k|m|g|t)?\s*$`)

func parseArtifactByteSize(raw string) (float64, bool) {
	m := artifactByteSizePattern.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return 0, false
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
		return 0, false
	}
	switch strings.ToLower(m[2]) {
	case "", "b":
		return val, true
	case "k", "kb", "kib":
		return val * 1024, true
	case "m", "mb", "mib":
		return val * 1024 * 1024, true
	case "g", "gb", "gib":
		return val * 1024 * 1024 * 1024, true
	case "t", "tb", "tib":
		return val * 1024 * 1024 * 1024 * 1024, true
	default:
		return 0, false
	}
}

func sanitizeDerivedAssetID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 64 {
			break
		}
	}
	id := strings.Trim(b.String(), "-._")
	if monitorID(id) {
		return id
	}
	return ""
}

// deriveArtifactSeriesPoints projects structured physical measurements from
// builtin Skill-First inspection artifacts into asset_monitor series points so
// periodic Skill inspections automatically populate Agent Monitor curves and
// evaluate threshold alert rules without duplicating collection pipelines.
//
// Only real physical-unit quantities are projected (never percentage-only
// placeholders), keeping synthetic unit fixtures unaffected.
func deriveArtifactSeriesPoints(item monitorRunItemRecord, collectedAtMs int64, hostAssetID string) []api.MonitorInspectSeriesPoint {
	if len(item.ArtifactJSON) == 0 || !json.Valid(item.ArtifactJSON) || collectedAtMs <= 0 {
		return nil
	}
	slug := monitorItemSkillSlug(item)
	hostID := strings.TrimSpace(hostAssetID)
	if !monitorID(hostID) {
		hostID = sanitizeDerivedAssetID(item.Instance)
	}

	var parsed struct {
		Metrics []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"metrics"`
		Mounts []struct {
			Mount        string   `json:"mount"`
			TotalGiB     *float64 `json:"total_gib"`
			UsedGiB      *float64 `json:"used_gib"`
			SharedVolume bool     `json:"shared_volume"`
		} `json:"mounts"`
		NetworkInterfaces []struct {
			Interface   string   `json:"interface"`
			RxBytesPerS *float64 `json:"rx_bytes_per_sec"`
			TxBytesPerS *float64 `json:"tx_bytes_per_sec"`
			Virtual     bool     `json:"virtual"`
		} `json:"network_interfaces"`
		GPUs []struct {
			UUID    string   `json:"uuid"`
			UtilPct *float64 `json:"util_pct"`
		} `json:"gpus"`
		Containers []struct {
			Name          string   `json:"name"`
			CPUPercent    *float64 `json:"cpu_percent"`
			MemPercent    *float64 `json:"mem_percent"`
			MemUsage      string   `json:"mem_usage"`
			MemLimitBytes *float64 `json:"mem_limit_bytes"`
		} `json:"containers"`
		NodeUsage []struct {
			Node          string   `json:"node"`
			CPUMillicores *float64 `json:"cpu_millicores"`
			MemoryBytes   *float64 `json:"memory_bytes"`
		} `json:"node_usage"`
		PodUsage []struct {
			Namespace     string   `json:"namespace"`
			Pod           string   `json:"pod"`
			CPUMillicores *float64 `json:"cpu_millicores"`
			MemoryBytes   *float64 `json:"memory_bytes"`
		} `json:"pod_usage"`
		Endpoints []struct {
			URL        string   `json:"url"`
			StatusCode int      `json:"status_code"`
			LatencyMs  *float64 `json:"latency_ms"`
			Error      string   `json:"error"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(item.ArtifactJSON, &parsed); err != nil {
		return nil
	}

	metricVals := make(map[string]float64, len(parsed.Metrics))
	for _, m := range parsed.Metrics {
		if v, ok := monitorNumericValue(m.Value); ok {
			metricVals[strings.TrimSpace(m.Key)] = v
		}
	}

	out := make([]api.MonitorInspectSeriesPoint, 0, 16)
	appendOK := func(assetType, assetID, ns, origin, metric, deviceID string, val float64) {
		if len(out) >= maxArtifactSeriesPointsPerItem {
			return
		}
		unit := monitorUnits[metric]
		if unit == "" || !monitorValidValue(metric, val) || !validMonitorDevice(metric, deviceID) {
			return
		}
		if !monitorValidAsset(monitorAsset{Type: assetType, AssetID: assetID, Namespace: ns}) {
			return
		}
		v := val
		out = append(out, api.MonitorInspectSeriesPoint{
			Type:      assetType,
			AssetID:   assetID,
			Namespace: ns,
			Origin:    origin,
			Metric:    metric,
			DeviceID:  deviceID,
			Unit:      unit,
			AtMs:      collectedAtMs,
			Status:    "ok",
			Value:     &v,
		})
	}

	// 1) Host metrics (inspect-vm-host or any artifact with host physical units).
	if hostID != "" && (slug == "inspect-vm-host" || len(parsed.Mounts) > 0 || len(parsed.NetworkInterfaces) > 0 || len(parsed.GPUs) > 0) {
		if cores, ok := metricVals["cpu_cores"]; ok && cores >= 0 {
			appendOK("machine", hostID, "", "agent_host", "cpu", "", cores)
		} else if totalCores, okT := metricVals["cpu_total_cores"]; okT && totalCores > 0 {
			if cpuPct, okP := metricVals["cpu_percent"]; okP && cpuPct >= 0 {
				appendOK("machine", hostID, "", "agent_host", "cpu", "", math.Round(totalCores*cpuPct)/100.0)
			}
		}
		if memUsed, ok := metricVals["memory_used_bytes"]; ok && memUsed >= 0 {
			appendOK("machine", hostID, "", "agent_host", "memory", "", memUsed)
		}
		wroteNIC := false
		for _, nic := range parsed.NetworkInterfaces {
			if nic.Virtual || !monitorInterface.MatchString(nic.Interface) {
				continue
			}
			var bps float64
			if nic.RxBytesPerS != nil && *nic.RxBytesPerS >= 0 {
				bps += *nic.RxBytesPerS
			}
			if nic.TxBytesPerS != nil && *nic.TxBytesPerS >= 0 {
				bps += *nic.TxBytesPerS
			}
			if nic.RxBytesPerS != nil || nic.TxBytesPerS != nil {
				appendOK("machine", hostID, "", "agent_host", "network", nic.Interface, bps)
				wroteNIC = true
			}
		}
		if !wroteNIC {
			if netBps, ok := metricVals["net_bytes_per_sec"]; ok && netBps >= 0 {
				appendOK("machine", hostID, "", "agent_host", "network", "", netBps)
			}
		}
		const gib = 1024.0 * 1024.0 * 1024.0
		seenMounts := map[string]bool{}
		for _, m := range parsed.Mounts {
			if m.SharedVolume || m.TotalGiB == nil || *m.TotalGiB <= 0 {
				continue
			}
			cleanMount := path.Clean(strings.TrimSpace(m.Mount))
			if !strings.HasPrefix(cleanMount, "/") || seenMounts[cleanMount] {
				continue
			}
			seenMounts[cleanMount] = true
			totalBytes := math.Round(*m.TotalGiB * gib)
			appendOK("machine", hostID, "", "agent_host", "storage_total", cleanMount, totalBytes)
			if m.UsedGiB != nil && *m.UsedGiB >= 0 {
				usedBytes := math.Round(*m.UsedGiB * gib)
				availBytes := totalBytes - usedBytes
				if availBytes < 0 {
					availBytes = 0
				}
				appendOK("machine", hostID, "", "agent_host", "storage", cleanMount, usedBytes)
				appendOK("machine", hostID, "", "agent_host", "storage_available", cleanMount, availBytes)
			}
		}
		for _, g := range parsed.GPUs {
			if g.UtilPct != nil && monitorGPUUUID.MatchString(strings.TrimSpace(g.UUID)) {
				appendOK("machine", hostID, "", "agent_host", "gpu", strings.TrimSpace(g.UUID), *g.UtilPct)
			}
		}
	}

	// 2) Container runtime metrics (inspect-container-runtime).
	seenContainers := map[string]bool{}
	for _, c := range parsed.Containers {
		cid := sanitizeDerivedAssetID(c.Name)
		if cid == "" || seenContainers[cid] {
			continue
		}
		seenContainers[cid] = true
		if c.CPUPercent != nil && *c.CPUPercent >= 0 {
			appendOK("container", cid, "", "container_stats", "cpu", "", *c.CPUPercent/100.0)
		}
		var usedBytes *float64
		if left, _, ok := strings.Cut(c.MemUsage, "/"); ok {
			if b, okB := parseArtifactByteSize(left); okB {
				usedBytes = &b
			}
		}
		if usedBytes == nil && c.MemPercent != nil && c.MemLimitBytes != nil && *c.MemLimitBytes > 0 && *c.MemPercent >= 0 {
			b := (*c.MemPercent / 100.0) * (*c.MemLimitBytes)
			usedBytes = &b
		}
		if usedBytes != nil {
			appendOK("container", cid, "", "container_stats", "memory", "", *usedBytes)
		}
		if c.MemLimitBytes != nil && *c.MemLimitBytes >= 0 {
			appendOK("container", cid, "", "container_stats", "memory_limit", "", *c.MemLimitBytes)
		}
	}

	// 3) K8s node usage (inspect-k8s-cluster).
	seenNodes := map[string]bool{}
	for _, n := range parsed.NodeUsage {
		nid := sanitizeDerivedAssetID(n.Node)
		if nid == "" || seenNodes[nid] {
			continue
		}
		seenNodes[nid] = true
		if n.CPUMillicores != nil && *n.CPUMillicores >= 0 {
			appendOK("machine", nid, "", "k8s_top", "cpu", "", *n.CPUMillicores/1000.0)
		}
		if n.MemoryBytes != nil && *n.MemoryBytes >= 0 {
			appendOK("machine", nid, "", "k8s_top", "memory", "", *n.MemoryBytes)
		}
	}

	// 3b) K8s Pod usage (inspect-k8s-cluster).
	seenPods := map[string]bool{}
	for _, p := range parsed.PodUsage {
		ns := strings.TrimSpace(p.Namespace)
		pid := sanitizeDerivedAssetID(p.Pod)
		if ns == "" || !inventoryNamespacePattern.MatchString(ns) || pid == "" {
			continue
		}
		key := ns + "/" + pid
		if seenPods[key] {
			continue
		}
		seenPods[key] = true
		if p.CPUMillicores != nil && *p.CPUMillicores >= 0 {
			appendOK("app", pid, ns, "k8s_top", "cpu", "", *p.CPUMillicores/1000.0)
		}
		if p.MemoryBytes != nil && *p.MemoryBytes >= 0 {
			appendOK("app", pid, ns, "k8s_top", "memory", "", *p.MemoryBytes)
		}
	}

	// 4) Web/app HTTP probes (inspect-app-web).
	seenApps := map[string]bool{}
	for _, ep := range parsed.Endpoints {
		u, err := url.Parse(strings.TrimSpace(ep.URL))
		if err != nil || u.Host == "" {
			continue
		}
		aid := sanitizeDerivedAssetID(u.Host)
		if aid == "" || seenApps[aid] {
			continue
		}
		seenApps[aid] = true
		errRate := 0.0
		if strings.TrimSpace(ep.Error) != "" || ep.StatusCode >= 400 || ep.StatusCode == 0 {
			errRate = 100.0
		}
		appendOK("app", aid, "", "app_probe", "error_rate", "", errRate)
		if ep.LatencyMs != nil && *ep.LatencyMs >= 0 && strings.TrimSpace(ep.Error) == "" && ep.StatusCode > 0 {
			appendOK("app", aid, "", "app_probe", "response_time_ms", "", *ep.LatencyMs)
		}
	}

	return out
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
//
// It returns how many metric rows it wrote (one per inspection_metric_points
// row plus one per bridged snapshot), because the run's metrics_written counter
// promises "how many metrics this run wrote" and Skill-driven runs write their
// metrics here, not through the legacy strategy snapshots.
func persistInspectionMetricExtractions(ctx context.Context, tx *sql.Tx, run monitorRunRecord, items []monitorRunItemRecord, extractions []monitorMetricExtraction, snapshots []monitorResourceSnapshot) (int, error) {
	if len(items) != len(extractions) {
		return 0, nil
	}
	written := 0
	var hasMetricTable bool
	if err := tx.QueryRowContext(ctx, gatewayTableExistsSQL(gatewayDialect()), "inspection_metric_points").Scan(&hasMetricTable); err != nil {
		return 0, err
	}
	if !hasMetricTable {
		return 0, nil
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
		const metricInsertBatchSize = 200
		const metricTuple = "(?,?,?,?,?,?,?,?,?,?,?)"
		for start := 0; start < len(extraction.Points); start += metricInsertBatchSize {
			end := start + metricInsertBatchSize
			if end > len(extraction.Points) {
				end = len(extraction.Points)
			}
			chunk := extraction.Points[start:end]
			var q strings.Builder
			q.Grow(180 + len(chunk)*(len(metricTuple)+1))
			q.WriteString(`INSERT INTO inspection_metric_points(
				collected_at_ms, run_id, schedule_id, cluster, instance, skill_slug,
				metric_key, metric_label, value, unit, status
			) VALUES `)
			args := make([]any, 0, len(chunk)*11)
			for idx, point := range chunk {
				if idx > 0 {
					q.WriteByte(',')
				}
				q.WriteString(metricTuple)
				args = append(args,
					extraction.CollectedAtMs, run.ID, run.ScheduleID, item.Cluster, item.Instance, extraction.SkillSlug,
					point.Key, point.Label, point.Value, point.Unit, point.Status,
				)
			}
			if _, err := tx.ExecContext(ctx, q.String(), args...); err != nil {
				return 0, err
			}
			written += len(chunk)
		}
		if providedSnapshotTargets[tunnelKey(item.Cluster, item.Instance)] {
			// The strategy already produced a richer snapshot for this target;
			// bridging again would duplicate the same point.
			continue
		}
		if err := bridgeInspectionMetricSnapshot(ctx, tx, run, item, extraction); err != nil {
			return 0, err
		}
		written++
	}
	return written, nil
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
		case "cpu_total_cores":
			snap.CPUTotalCores = &value
		case "cpu_cores":
			snap.CPUCores = &value
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
		case "net_bytes_per_sec":
			snap.NetBytesPerSec = &value
		case "disk_io_latency_ms", "disk_latency_ms":
			snap.DiskLatencyMs = &value
		case "gpu_percent":
			snap.GPUPercent = &value
		default:
			continue
		}
		matched = true
	}
	if !matched {
		return nil
	}
	if snap.CPUCores == nil && snap.CPUTotalCores != nil && snap.CPUPercent != nil {
		used := math.Round((*snap.CPUTotalCores)*(*snap.CPUPercent)) / 100.0
		snap.CPUCores = &used
	}
	if len(item.ArtifactJSON) > 0 && json.Valid(item.ArtifactJSON) {
		var meta struct {
			Host struct {
				OS     string `json:"os"`
				Kernel string `json:"kernel"`
			} `json:"host"`
		}
		if json.Unmarshal(item.ArtifactJSON, &meta) == nil {
			if snap.OSInfo == "" {
				snap.OSInfo = monitorMetricClampText(meta.Host.OS, 128)
			}
			if snap.KernelInfo == "" {
				snap.KernelInfo = monitorMetricClampText(meta.Host.Kernel, 128)
			}
		}
	}
	return insertMonitorResourceSnapshot(ctx, tx, run.ID, snap)
}

// ListInspectionMetricSeries returns the trend points for one skill on one
// target, grouped by metric_key and ordered oldest to newest.
//
// The scan is served by idx_inspection_metric_points_target (cluster, instance,
// collected_at_ms) and skill_slug stays a residual predicate: putting it into
// the index prefix would need a fourth column on that index, which is a schema
// change this task rules out (SF-04 观察项 D2). monitorMetricSeriesMaxRows
// bounds the work and counts *matching* rows — SQLite applies LIMIT after the
// WHERE filter — so a target with a long history cannot truncate another
// target's series or silently drop rows of its own; the only price is extra
// index probes when two targets share a skill on one host.
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

// ListLatestInspectionMetrics aggregates the newest recorded point per
// (cluster, instance, skill_slug, metric_key) across authorized targets for the
// requested skills and metric keys. Delegates to per-pair queries so a
// high-frequency skill can never starve low-frequency plugins.
func (s *GatewayStore) ListLatestInspectionMetrics(
	ctx context.Context,
	skills []string,
	keys []string,
	allowTarget func(cluster, instance string) bool,
) ([]monitorLatestMetricItem, error) {
	if len(skills) == 0 || len(keys) == 0 {
		return []monitorLatestMetricItem{}, nil
	}
	pairs := make([]monitorLatestMetricPair, 0, len(skills)*len(keys))
	for _, sk := range skills {
		for _, k := range keys {
			pairs = append(pairs, monitorLatestMetricPair{Skill: sk, Key: k})
		}
	}
	return s.ListLatestInspectionMetricPairs(ctx, pairs, allowTarget)
}

// ListLatestInspectionMetricPairs queries each exact (skill_slug, metric_key)
// pair independently with a per-pair row cap, eliminating cross-skill row
// starvation while remaining 100% compatible with SQLite and MySQL 5.7/8.0/8.4.
func (s *GatewayStore) ListLatestInspectionMetricPairs(
	ctx context.Context,
	pairs []monitorLatestMetricPair,
	allowTarget func(cluster, instance string) bool,
) ([]monitorLatestMetricItem, error) {
	if len(pairs) == 0 {
		return []monitorLatestMetricItem{}, nil
	}
	type aggBucket struct {
		item monitorLatestMetricItem
		sum  float64
	}
	out := make([]monitorLatestMetricItem, 0, len(pairs))
	seenPairs := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		pairKey := pair.Skill + "\x00" + pair.Key
		if seenPairs[pairKey] {
			continue
		}
		seenPairs[pairKey] = true

		rows, err := s.readDB().QueryContext(ctx,
			`SELECT metric_label, unit, cluster, instance, collected_at_ms, value, status
			FROM inspection_metric_points
			WHERE skill_slug = ? AND metric_key = ?
			ORDER BY collected_at_ms DESC, id DESC
			LIMIT ?`,
			pair.Skill, pair.Key, latestMonitorMetricsRowsPerPair,
		)
		if err != nil {
			return nil, err
		}

		seenTarget := make(map[string]bool)
		var b *aggBucket
		for rows.Next() {
			var label, unit, cluster, instance, status string
			var atMs int64
			var value float64
			if err := rows.Scan(&label, &unit, &cluster, &instance, &atMs, &value, &status); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if allowTarget != nil && !allowTarget(cluster, instance) {
				continue
			}
			targetKey := cluster + "\x00" + instance
			if seenTarget[targetKey] {
				continue
			}
			seenTarget[targetKey] = true

			if b == nil {
				b = &aggBucket{
					item: monitorLatestMetricItem{
						Skill:            pair.Skill,
						Key:              pair.Key,
						Label:            label,
						Unit:             unit,
						Max:              value,
						Latest:           value,
						LatestAtMs:       atMs,
						Status:           status,
						ReportingTargets: 1,
					},
					sum: value,
				}
				continue
			}
			b.item.ReportingTargets++
			b.sum += value
			if value > b.item.Max {
				b.item.Max = value
			}
			if atMs > b.item.LatestAtMs {
				b.item.LatestAtMs = atMs
				b.item.Latest = value
				if status != "" {
					b.item.Status = status
				}
			}
			if b.item.Label == "" && label != "" {
				b.item.Label = label
			}
			if b.item.Unit == "" && unit != "" {
				b.item.Unit = unit
			}
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return nil, rowErr
		}
		if b != nil {
			if b.item.ReportingTargets > 0 {
				b.item.Avg = math.Round((b.sum/float64(b.item.ReportingTargets))*10000) / 10000
			}
			out = append(out, b.item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Skill != out[j].Skill {
			return out[i].Skill < out[j].Skill
		}
		return out[i].Key < out[j].Key
	})
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

// monitorRunDiffPrevStartSQL returns the newest run clock that has an item for
// one (cluster, instance, strategy) series. MAX() over the series index range
// needs no sort, so the join no longer materialises a temp B-tree.
const monitorRunDiffPrevStartSQL = `SELECT MAX(r.started_at_ms)
	FROM monitor_run_items i JOIN monitor_runs r ON r.id=i.run_id
	WHERE i.cluster=? AND i.instance=? AND i.strategy=? AND r.id<>? AND r.status<>'' AND r.status<>'running' AND r.started_at_ms<?`

// monitorRunDiffPrevItemSQL returns the last item of the runs that started at
// the newest run clock. Ordering by item id with the series prefix is served by
// idx_monitor_run_items_series (entries are rowid-ordered), so it needs no sort
// either; ties on started_at_ms still resolve by item id like the old query.
const monitorRunDiffPrevItemSQL = `SELECT i.run_id, r.status, i.id, i.checks_json, i.artifact_dir, i.artifact_json
	FROM monitor_run_items i JOIN monitor_runs r ON r.id=i.run_id
	WHERE i.cluster=? AND i.instance=? AND i.strategy=? AND r.id<>? AND r.status<>'' AND r.status<>'running' AND r.started_at_ms=?
	ORDER BY i.id DESC LIMIT 1`

type monitorRunDiffPrevious struct {
	RunID        string
	AtMs         int64
	Status       string
	ChecksJSON   string
	ArtifactDir  string
	ArtifactJSON string
}

// findPreviousMonitorRunItem returns the item the diff compares against.
//
// The comparison order is (run.started_at_ms DESC, item.id DESC): the newest
// terminal run of the series wins and inside it the last persisted item. Item
// started_at_ms is not interchangeable with run started_at_ms — an item starts
// when its target is dispatched, after the run started
// (monitor_engine_runner.go newSkillRunItem / newCommandRunItem) — so ordering
// by the item column would pick another run whenever two runs overlap. The old
// join therefore had to sort every candidate row (SF-04 观察项 D1:
// USE TEMP B-TREE FOR ORDER BY). Both statements below walk only the series'
// index range: the first aggregates MAX(run started_at_ms) and the second picks
// the last item inside that run-clock group, so the answer is unchanged and no
// temp B-tree is built. Keeping the EXISTS-probe-per-run variant instead would
// have degraded to runs×series index visits whenever recent runs of other
// targets sit between two runs of this series.
func (s *GatewayStore) findPreviousMonitorRunItem(ctx context.Context, run monitorRunRecord, item monitorRunItemRecord) (monitorRunDiffPrevious, bool) {
	var topStart sql.NullInt64
	if err := s.readDB().QueryRowContext(ctx, monitorRunDiffPrevStartSQL,
		item.Cluster, item.Instance, item.Strategy, run.ID, run.StartedAtMs,
	).Scan(&topStart); err != nil || !topStart.Valid {
		return monitorRunDiffPrevious{}, false
	}
	var (
		prev   monitorRunDiffPrevious
		itemID int64
	)
	prev.AtMs = topStart.Int64
	if err := s.readDB().QueryRowContext(ctx, monitorRunDiffPrevItemSQL,
		item.Cluster, item.Instance, item.Strategy, run.ID, prev.AtMs,
	).Scan(&prev.RunID, &prev.Status, &itemID, &prev.ChecksJSON, &prev.ArtifactDir, &prev.ArtifactJSON); err != nil {
		return monitorRunDiffPrevious{}, false
	}
	return prev, true
}

// attachMonitorRunItemDiffs fills item.Diff by comparing each item against the
// previous terminal run of the same (cluster, instance, strategy) series.
func (s *GatewayStore) attachMonitorRunItemDiffs(ctx context.Context, run monitorRunRecord, items []monitorRunItemRecord) {
	for i := range items {
		items[i].Diff = s.buildMonitorRunItemDiff(ctx, run, items[i])
	}
}

func (s *GatewayStore) buildMonitorRunItemDiff(ctx context.Context, run monitorRunRecord, item monitorRunItemRecord) *monitorRunDiff {
	if run.StartedAtMs <= 0 {
		return nil
	}
	prev, ok := s.findPreviousMonitorRunItem(ctx, run, item)
	if !ok {
		return nil
	}
	diff := &monitorRunDiff{
		PreviousRunID:  prev.RunID,
		PreviousAtMs:   prev.AtMs,
		PreviousStatus: prev.Status,
		StatusChanged:  run.Status != "" && prev.Status != run.Status,
		MetricDeltas:   []monitorMetricDelta{},
		AddedChecks:    []api.MonitorInspectCheck{},
		ResolvedChecks: []api.MonitorInspectCheck{},
		ChangedChecks:  []monitorRunCheckChange{},
	}

	prevJSON := []byte(prev.ArtifactJSON)
	if prev.ArtifactDir != "" {
		if content, ok := s.readMonitorArtifactFiles(prev.ArtifactDir); ok && len(content.JSON) > 0 {
			prevJSON = content.JSON
		}
	}
	prevPoints, _ := extractMonitorArtifactMetrics(json.RawMessage(prevJSON), prev.Status)
	currPoints, _ := extractMonitorArtifactMetrics(item.ArtifactJSON, item.Status)
	diff.MetricDeltas = buildMonitorMetricDeltas(prevPoints, currPoints)

	prevChecks := []api.MonitorInspectCheck{}
	_ = json.Unmarshal([]byte(prev.ChecksJSON), &prevChecks)
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
