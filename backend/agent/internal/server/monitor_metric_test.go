package server

// SF-04 contract: inspection artifacts stay the source of truth on disk while
// their numeric metrics are projected into inspection_metric_points, standard
// keys bridge into monitor_resource_snapshots, GET /v1/monitor/metrics serves
// the series, and GET /v1/monitor/runs/<id> attaches a cross-run diff.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

const metricTestSkillSlug = "web-inspector"

func openMetricTestStore(t *testing.T) (*GatewayStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func metricTestAdminAuth(t *testing.T, store *GatewayStore) TokenAuth {
	t.Helper()
	user, err := store.CreateUser("metric-admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.SetUserAdmin(user.ID, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	return TokenAuth{UserID: user.ID, Kind: TokenKindUser}
}

// persistMetricTestRun writes one terminal skill inspection run whose artifact
// JSON carries metrics. Artifact files land next to the SQLite database.
func persistMetricTestRun(t *testing.T, store *GatewayStore, runID string, startedAtMs int64, checks []api.MonitorInspectCheck, artifactJSON string, snapshots []monitorResourceSnapshot) monitorRunRecord {
	t.Helper()
	ctx := context.Background()
	run := monitorRunRecord{
		ID:           runID,
		TriggerKind:  "manual",
		TriggeredBy:  "metric-admin",
		Status:       "running",
		StartedAtMs:  startedAtMs,
		FinishedAtMs: startedAtMs + 1000,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("create monitor run %s: %v", runID, err)
	}
	item := monitorRunItemRecord{
		RunID:           runID,
		Cluster:         "local",
		Instance:        "host-a",
		AssetID:         metricTestSkillSlug,
		AssetName:       "web host-a",
		AssetKind:       "skill",
		Strategy:        "skill:" + metricTestSkillSlug,
		CollectorMethod: "skill",
		StartedAtMs:     startedAtMs,
		FinishedAtMs:    startedAtMs + 900,
		Status:          "pass",
		Checks:          checks,
		ArtifactJSON:    json.RawMessage(artifactJSON),
		Evidence:        map[string]any{"skill_slug": metricTestSkillSlug},
	}
	run.Status = "success"
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, snapshots, nil); err != nil {
		t.Fatalf("persist monitor run %s: %v", runID, err)
	}
	return run
}

func metricValuesByKey(t *testing.T, store *GatewayStore, runID string) map[string]float64 {
	t.Helper()
	rows, err := store.db.Query(`SELECT metric_key, value FROM inspection_metric_points WHERE run_id=?`, runID)
	if err != nil {
		t.Fatalf("query metric rows: %v", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("scan metric row: %v", err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate metric rows: %v", err)
	}
	return out
}

func readItemEvidence(t *testing.T, store *GatewayStore, runID string) map[string]any {
	t.Helper()
	var raw string
	if err := store.db.QueryRow(`SELECT evidence_json FROM monitor_run_items WHERE run_id=?`, runID).Scan(&raw); err != nil {
		t.Fatalf("read item evidence: %v", err)
	}
	var evidence map[string]any
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
			t.Fatalf("decode item evidence: %v", err)
		}
	}
	return evidence
}

type metricSnapshotRow struct {
	CPUTotalCores  sql.NullFloat64
	CPUPercent     sql.NullFloat64
	MemoryPercent  sql.NullFloat64
	DiskPercent    sql.NullFloat64
	Load1m         sql.NullFloat64
	UptimeSeconds  sql.NullFloat64
	NetBytesPerSec sql.NullFloat64
	DiskLatencyMs  sql.NullFloat64
	GPUPercent     sql.NullFloat64
	OSInfo         string
	KernelInfo     string
	Details        string
}

func readMetricSnapshot(t *testing.T, store *GatewayStore, runID string) metricSnapshotRow {
	t.Helper()
	var row metricSnapshotRow
	err := store.db.QueryRow(`SELECT cpu_total_cores, cpu_percent, memory_percent, disk_percent, load_1m, uptime_seconds,
		net_bytes_per_sec, disk_latency_ms, gpu_percent, os_info, kernel_info, details_json
		FROM monitor_resource_snapshots WHERE run_id=?`, runID).Scan(
		&row.CPUTotalCores, &row.CPUPercent, &row.MemoryPercent, &row.DiskPercent, &row.Load1m, &row.UptimeSeconds,
		&row.NetBytesPerSec, &row.DiskLatencyMs, &row.GPUPercent, &row.OSInfo, &row.KernelInfo, &row.Details)
	if err != nil {
		t.Fatalf("read bridged snapshot: %v", err)
	}
	return row
}

func TestInspectionMetricPersistenceAndSnapshotBridge(t *testing.T) {
	store, _ := openMetricTestStore(t)
	artifact := `{"status":"pass","summary":"host ok","host":{"os":"Ubuntu 24.04 LTS","kernel":"Linux 6.8.0"},"metrics":[
		{"key":"cpu_total_cores","value":32,"unit":""},
		{"key":"cpu_percent","value":42.5,"unit":"%","label":"CPU 使用率"},
		{"key":"memory_percent","value":61.2,"unit":"%"},
		{"key":"disk_percent","value":71,"unit":"%"},
		{"key":"load_1m","value":1.75},
		{"key":"uptime_seconds","value":86400},
		{"key":"net_bytes_per_sec","value":204800,"unit":"B/s"},
		{"key":"disk_io_latency_ms","value":4.2,"unit":"ms"},
		{"key":"gpu_percent","value":88.5,"unit":"%"},
		{"key":"app_latency_ms","value":12.5,"unit":"ms"},
		{"key":"bad key","value":1},
		{"key":"cpu_percent","value":99},
		{"key":"skipped_text","value":"n/a"}
	]}`
	run := persistMetricTestRun(t, store, "run_metric_json", time.Now().Add(-time.Hour).UnixMilli(), nil, artifact, nil)

	values := metricValuesByKey(t, store, run.ID)
	if len(values) != 10 {
		t.Fatalf("want 10 metric rows, got %d: %v", len(values), values)
	}
	want := map[string]float64{
		"cpu_total_cores":    32,
		"cpu_percent":        42.5,
		"memory_percent":     61.2,
		"disk_percent":       71,
		"load_1m":            1.75,
		"uptime_seconds":     86400,
		"net_bytes_per_sec":  204800,
		"disk_io_latency_ms": 4.2,
		"gpu_percent":        88.5,
		"app_latency_ms":     12.5,
	}
	for key, expected := range want {
		if got, ok := values[key]; !ok || got != expected {
			t.Errorf("metric %s = %v (present=%v), want %v", key, got, ok, expected)
		}
	}

	evidence := readItemEvidence(t, store, run.ID)
	extraction, _ := evidence["inspection_metrics"].(map[string]any)
	if extraction == nil {
		t.Fatalf("expected inspection_metrics summary on evidence, got %v", evidence)
	}
	if got := extraction["extracted"]; got != float64(10) {
		t.Errorf("evidence extracted = %v, want 10", got)
	}
	if got := extraction["skipped"]; got != float64(3) {
		t.Errorf("evidence skipped = %v, want 3 (invalid key, duplicate key, non-numeric value)", got)
	}

	snapshot := readMetricSnapshot(t, store, run.ID)
	for name, field := range map[string]struct {
		value sql.NullFloat64
		want  float64
	}{
		"cpu_total_cores":   {snapshot.CPUTotalCores, 32},
		"cpu_percent":       {snapshot.CPUPercent, 42.5},
		"memory_percent":    {snapshot.MemoryPercent, 61.2},
		"disk_percent":      {snapshot.DiskPercent, 71},
		"load_1m":           {snapshot.Load1m, 1.75},
		"uptime_seconds":    {snapshot.UptimeSeconds, 86400},
		"net_bytes_per_sec": {snapshot.NetBytesPerSec, 204800},
		"disk_latency_ms":   {snapshot.DiskLatencyMs, 4.2},
		"gpu_percent":       {snapshot.GPUPercent, 88.5},
	} {
		if !field.value.Valid || field.value.Float64 != field.want {
			t.Errorf("bridged snapshot %s = %v (valid=%v), want %v", name, field.value.Float64, field.value.Valid, field.want)
		}
	}
	if snapshot.OSInfo != "Ubuntu 24.04 LTS" || snapshot.KernelInfo != "Linux 6.8.0" {
		t.Errorf("bridged snapshot os/kernel = (%q, %q), want (Ubuntu 24.04 LTS, Linux 6.8.0)", snapshot.OSInfo, snapshot.KernelInfo)
	}
	if !strings.Contains(snapshot.Details, `"source":"inspection_metrics"`) {
		t.Errorf("bridged snapshot details must mark the inspection bridge, got %q", snapshot.Details)
	}

	// Flat numeric top-level fields are the documented fallback when the
	// optional metrics[] array is absent (spec §4.1).
	flat := `{"status":"pass","summary":"flat envelope","cpu_percent":12.5,"load_1m":0.5,"note":"text is not a metric"}`
	flatRun := persistMetricTestRun(t, store, "run_metric_flat", time.Now().Add(-30*time.Minute).UnixMilli(), nil, flat, nil)
	flatValues := metricValuesByKey(t, store, flatRun.ID)
	if len(flatValues) != 2 || flatValues["cpu_percent"] != 12.5 || flatValues["load_1m"] != 0.5 {
		t.Fatalf("flat fallback metrics = %v, want cpu_percent=12.5 load_1m=0.5 only", flatValues)
	}
}

func TestInspectionMetricBridgeSkipsExistingSnapshot(t *testing.T) {
	store, _ := openMetricTestStore(t)
	startedAtMs := time.Now().Add(-time.Hour).UnixMilli()
	provided := monitorResourceSnapshot{
		Cluster:         "local",
		Instance:        "host-a",
		AssetID:         metricTestSkillSlug,
		AssetName:       "web host-a",
		AssetKind:       "skill",
		Strategy:        "skill:" + metricTestSkillSlug,
		CollectorMethod: "skill",
		CollectedAtMs:   startedAtMs + 500,
		Status:          "pass",
	}
	run := persistMetricTestRun(t, store, "run_metric_snap", startedAtMs, nil,
		`{"metrics":[{"key":"cpu_percent","value":10}]}`, []monitorResourceSnapshot{provided})

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM monitor_resource_snapshots WHERE run_id=?`, run.ID).Scan(&count); err != nil {
		t.Fatalf("count snapshot rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("bridge must not add a second snapshot when the strategy already collected one, got %d rows", count)
	}
	var details string
	if err := store.db.QueryRow(`SELECT details_json FROM monitor_resource_snapshots WHERE run_id=?`, run.ID).Scan(&details); err != nil {
		t.Fatalf("read snapshot details: %v", err)
	}
	if strings.Contains(details, "inspection_metrics") {
		t.Fatalf("existing snapshot row was overwritten by the bridge: %q", details)
	}
}

// The optional top-level "series_points" array in an artifact JSON bridges into
// asset_monitor_series/asset_monitor_samples so skill inspections (e.g. the
// dosql-backed database skill) feed the same metric timeline as collectors.
func TestInspectionSeriesPointsBridgeToAssetMonitor(t *testing.T) {
	store, _ := openMetricTestStore(t)
	ddl, err := os.ReadFile("../../../docs/migrations/asset_monitor.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(string(ddl)); err != nil {
		t.Fatalf("apply asset monitor migration: %v", err)
	}
	startedAtMs := time.Now().Add(-time.Hour).UnixMilli()
	atMs := startedAtMs + 500
	artifact := `{"status":"pass","series_points":[
		{"type":"database","asset_id":"prod-mysql","origin":"exporter","metric":"db_tps","unit":"tx/s","at_ms":` + json.Number(fmt.Sprintf("%d", atMs)).String() + `,"status":"ok","value":128.5},
		{"type":"database","asset_id":"prod-mysql","origin":"exporter","metric":"db_lock_waits","unit":"waits","at_ms":` + json.Number(fmt.Sprintf("%d", atMs)).String() + `,"status":"ok","value":2},
		{"type":"machine","asset_id":"prod-mysql","origin":"exporter","metric":"db_tps","unit":"tx/s","at_ms":` + json.Number(fmt.Sprintf("%d", atMs)).String() + `,"status":"ok","value":9},
		{"type":"database","asset_id":"prod-mysql","origin":"exporter","metric":"db_tps","unit":"wrong","at_ms":` + json.Number(fmt.Sprintf("%d", atMs)).String() + `,"status":"ok","value":9},
		{"type":"database","asset_id":"prod-mysql","origin":"injected","metric":"db_tps","unit":"tx/s","at_ms":` + json.Number(fmt.Sprintf("%d", atMs)).String() + `,"status":"ok","value":9}
	]}`
	run := persistMetricTestRun(t, store, "run_series_bridge", startedAtMs, nil, artifact, nil)

	var seriesCount, sampleCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM asset_monitor_series WHERE metric GLOB 'db_*'`).Scan(&seriesCount); err != nil {
		t.Fatalf("count db series: %v", err)
	}
	if seriesCount != 2 {
		t.Fatalf("want 2 bridged db series (tps + lock_waits), got %d", seriesCount)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM asset_monitor_samples s
		JOIN asset_monitor_series r ON r.id=s.series_id
		WHERE r.metric GLOB 'db_*' AND s.status='ok'`).Scan(&sampleCount); err != nil {
		t.Fatalf("count db samples: %v", err)
	}
	if sampleCount != 2 {
		t.Fatalf("want 2 db samples, got %d", sampleCount)
	}
	var totalSeries int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM asset_monitor_series`).Scan(&totalSeries); err != nil {
		t.Fatalf("count all series: %v", err)
	}
	if totalSeries != 2 {
		t.Fatalf("type-mismatched or invalid points must be dropped, got %d series", totalSeries)
	}
	_ = run
}

func TestMonitorMetricsHTTP(t *testing.T) {
	f := monitorEngineFixture(t)
	now := time.Now().UTC()
	firstAt := now.Add(-10 * time.Minute).UnixMilli()
	secondAt := now.Add(-5 * time.Minute).UnixMilli()
	persistMetricTestRun(t, f.store, "run_http_metrics_a", firstAt, nil,
		`{"metrics":[{"key":"cpu_percent","value":40,"unit":"%"},{"key":"errors","value":0}]}`, nil)
	persistMetricTestRun(t, f.store, "run_http_metrics_b", secondAt, nil,
		`{"metrics":[{"key":"cpu_percent","value":60,"unit":"%"},{"key":"errors","value":2}]}`, nil)

	data := f.http(t, f.admin, "GET", "/v1/monitor/metrics?skill=web-inspector&target=local/host-a&range=1h", "", 200)
	var resp monitorMetricSeriesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("decode metrics response: %v", err)
	}
	if resp.Skill != "web-inspector" || resp.Cluster != "local" || resp.Instance != "host-a" || resp.Range != "1h" {
		t.Fatalf("unexpected metrics envelope: %+v", resp)
	}
	if len(resp.Series) != 2 {
		t.Fatalf("want 2 metric series, got %d: %+v", len(resp.Series), resp.Series)
	}
	if resp.Series[0].Key != "cpu_percent" || resp.Series[1].Key != "errors" {
		t.Fatalf("series must be grouped and sorted by key, got %q, %q", resp.Series[0].Key, resp.Series[1].Key)
	}
	cpu := resp.Series[0]
	if cpu.Unit != "%" || len(cpu.Points) != 2 {
		t.Fatalf("cpu series = %+v", cpu)
	}
	if cpu.Points[0].Value != 40 || cpu.Points[1].Value != 60 {
		t.Fatalf("cpu points must be oldest-first, got %+v", cpu.Points)
	}
	if cpu.Points[0].AtMs > cpu.Points[1].AtMs {
		t.Fatalf("points must be ascending by time: %+v", cpu.Points)
	}
	if cpu.Points[0].RunID != "run_http_metrics_a" || cpu.Points[1].RunID != "run_http_metrics_b" {
		t.Fatalf("points must carry their run id: %+v", cpu.Points)
	}

	f.http(t, f.admin, "GET", "/v1/monitor/metrics?skill=web-inspector&target=local/host-a&range=30d", "", 200)

	badRange := f.http(t, f.admin, "GET", "/v1/monitor/metrics?skill=web-inspector&target=local/host-a&range=2h", "", 400)
	if !strings.Contains(string(badRange), "invalid_monitor_metrics_range") {
		t.Fatalf("bad range error = %s", badRange)
	}
	badSkill := f.http(t, f.admin, "GET", "/v1/monitor/metrics?skill=BAD%25&target=local/host-a", "", 400)
	if !strings.Contains(string(badSkill), "invalid_monitor_metrics_query") {
		t.Fatalf("bad skill error = %s", badSkill)
	}
	badTarget := f.http(t, f.admin, "GET", "/v1/monitor/metrics?skill=web-inspector&target=local", "", 400)
	if !strings.Contains(string(badTarget), "invalid_monitor_metrics_query") {
		t.Fatalf("bad target error = %s", badTarget)
	}
	f.http(t, f.admin, "POST", "/v1/monitor/metrics?skill=web-inspector&target=local/host-a", "", 405)

	viewer := f.user(t, "metric-viewer", ScopeGrant{Cluster: "prod", Instance: "*"})
	forbidden := f.http(t, viewer, "GET", "/v1/monitor/metrics?skill=web-inspector&target=local/host-a", "", 403)
	if !strings.Contains(string(forbidden), "monitor_metrics_forbidden") {
		t.Fatalf("forbidden error = %s", forbidden)
	}
}

func TestMonitorRunDetailDiff(t *testing.T) {
	f := monitorEngineFixture(t)
	base := time.Now().UTC().Add(-time.Hour).UnixMilli()
	checksA := []api.MonitorInspectCheck{
		{ID: "disk_usage", Name: "磁盘容量", Category: "resource", Status: "pass"},
		{ID: "mem_pressure", Name: "内存压力", Category: "resource", Status: "warning"},
		{ID: "tls_cert", Name: "证书有效期", Category: "security", Status: "pass"},
	}
	checksB := []api.MonitorInspectCheck{
		{ID: "disk_usage", Name: "磁盘容量", Category: "resource", Status: "critical"},
		{ID: "mem_pressure", Name: "内存压力", Category: "resource", Status: "pass"},
		{ID: "cpu_load", Name: "CPU 负载", Category: "resource", Status: "pass"},
	}
	persistMetricTestRun(t, f.store, "run_diff_a", base, checksA,
		`{"metrics":[{"key":"cpu_percent","value":40,"unit":"%"},{"key":"errors","value":0}]}`, nil)
	persistMetricTestRun(t, f.store, "run_diff_b", base+600000, checksB,
		`{"metrics":[{"key":"cpu_percent","value":60,"unit":"%"},{"key":"errors","value":2}]}`, nil)

	var detail struct {
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(f.http(t, f.admin, "GET", "/v1/monitor/runs/run_diff_b", "", 200), &detail); err != nil {
		t.Fatalf("decode run detail: %v", err)
	}
	if len(detail.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(detail.Items))
	}
	diff := detail.Items[0].Diff
	if diff == nil {
		t.Fatalf("expected diff against run_diff_a, got nil")
	}
	if diff.PreviousRunID != "run_diff_a" || diff.PreviousStatus != "success" || diff.StatusChanged {
		t.Fatalf("unexpected diff header: %+v", diff)
	}
	deltas := map[string]monitorMetricDelta{}
	for _, delta := range diff.MetricDeltas {
		deltas[delta.Key] = delta
	}
	if len(deltas) != 2 {
		t.Fatalf("want 2 metric deltas, got %+v", diff.MetricDeltas)
	}
	cpu := deltas["cpu_percent"]
	if cpu.Previous != 40 || cpu.Current != 60 || cpu.Delta != 20 || cpu.DeltaPct == nil || *cpu.DeltaPct != 50 {
		t.Fatalf("cpu delta = %+v, want previous=40 current=60 delta=20 delta_pct=50", cpu)
	}
	errors := deltas["errors"]
	if errors.Delta != 2 || errors.DeltaPct != nil {
		t.Fatalf("errors delta = %+v, want delta=2 delta_pct=nil when previous is 0", errors)
	}
	if len(diff.AddedChecks) != 1 || diff.AddedChecks[0].ID != "cpu_load" {
		t.Fatalf("added checks = %+v, want cpu_load", diff.AddedChecks)
	}
	if len(diff.ResolvedChecks) != 1 || diff.ResolvedChecks[0].ID != "tls_cert" {
		t.Fatalf("resolved checks = %+v, want tls_cert", diff.ResolvedChecks)
	}
	if len(diff.ChangedChecks) != 2 {
		t.Fatalf("changed checks = %+v, want disk_usage and mem_pressure", diff.ChangedChecks)
	}
	changed := map[string]monitorRunCheckChange{}
	for _, change := range diff.ChangedChecks {
		changed[change.ID] = change
	}
	if change := changed["disk_usage"]; change.PreviousStatus != "pass" || change.CurrentStatus != "critical" {
		t.Errorf("disk_usage change = %+v", change)
	}
	if change := changed["mem_pressure"]; change.PreviousStatus != "warning" || change.CurrentStatus != "pass" {
		t.Errorf("mem_pressure change = %+v", change)
	}

	var firstDetail struct {
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(f.http(t, f.admin, "GET", "/v1/monitor/runs/run_diff_a", "", 200), &firstDetail); err != nil {
		t.Fatalf("decode first run detail: %v", err)
	}
	if len(firstDetail.Items) != 1 || firstDetail.Items[0].Diff != nil {
		t.Fatalf("first terminal run must have diff=null, got %+v", firstDetail.Items)
	}
}

func TestInspectionMetricSchemaUpgrade(t *testing.T) {
	store, path := openMetricTestStore(t)
	auth := metricTestAdminAuth(t, store)
	startedAtMs := time.Now().Add(-2 * time.Hour).UnixMilli()
	persistMetricTestRun(t, store, "run_upgrade_old", startedAtMs, []api.MonitorInspectCheck{
		{ID: "disk_usage", Name: "磁盘容量", Category: "resource", Status: "pass"},
	}, `{"metrics":[{"key":"cpu_percent","value":33}]}`, nil)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Simulate a database created before SF-04: the new table and index are
	// absent when the upgraded binary opens it.
	legacy, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatalf("reopen legacy store: %v", err)
	}
	if _, err := legacy.db.Exec(`DROP TABLE inspection_metric_points`); err != nil {
		t.Fatalf("drop metric table: %v", err)
	}
	if _, err := legacy.db.Exec(`DROP INDEX idx_monitor_run_items_series`); err != nil {
		t.Fatalf("drop series index: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	upgraded, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatalf("open upgraded store: %v", err)
	}
	defer func() { _ = upgraded.Close() }()
	var tableName string
	if err := upgraded.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='inspection_metric_points'").Scan(&tableName); err != nil {
		t.Fatalf("metric table was not recreated by migrate: %v", err)
	}
	var indexCount int
	if err := upgraded.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_inspection_metric_points_%'").Scan(&indexCount); err != nil {
		t.Fatalf("count metric indexes: %v", err)
	}
	if indexCount != 3 {
		t.Fatalf("want 3 metric series indexes, got %d", indexCount)
	}
	var seriesIndex string
	if err := upgraded.db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_monitor_run_items_series'").Scan(&seriesIndex); err != nil {
		t.Fatalf("series lookup index was not recreated: %v", err)
	}

	// Historical runs keep reading after the upgrade, including diff lookup.
	run, items, err := upgraded.GetMonitorRunDetail(context.Background(), auth, "run_upgrade_old")
	if err != nil {
		t.Fatalf("read historical run after upgrade: %v", err)
	}
	if run.ID != "run_upgrade_old" || len(items) != 1 {
		t.Fatalf("unexpected historical detail: run=%+v items=%d", run, len(items))
	}
	if len(items[0].Checks) != 1 || items[0].Checks[0].ID != "disk_usage" {
		t.Fatalf("historical checks lost after upgrade: %+v", items[0].Checks)
	}

	// Writing through the recreated table succeeds again.
	persistMetricTestRun(t, upgraded, "run_upgrade_new", time.Now().Add(-time.Hour).UnixMilli(), nil,
		`{"metrics":[{"key":"cpu_percent","value":55}]}`, nil)
	if values := metricValuesByKey(t, upgraded, "run_upgrade_new"); values["cpu_percent"] != 55 {
		t.Fatalf("metric write after upgrade = %v", values)
	}
}

// SF-12: the cross-run diff used to sort every candidate row of a series
// (SF-04 观察项 D1). The run is now selected through idx_monitor_runs_started;
// this pins that plan so a future rewrite cannot reintroduce the temp B-tree.
func explainMonitorPlan(t *testing.T, store *GatewayStore, query string, args ...any) string {
	t.Helper()
	rows, err := store.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("explain scan: %v", err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
	return strings.Join(lines, "\n")
}

func TestMonitorRunDiffQueryPlanAvoidsTempBTree(t *testing.T) {
	store, _ := openMetricTestStore(t)
	series := "skill:" + metricTestSkillSlug
	startPlan := explainMonitorPlan(t, store, monitorRunDiffPrevStartSQL,
		"local", "host-a", series, "run_current", int64(1000))
	if strings.Contains(startPlan, "TEMP B-TREE") {
		t.Fatalf("diff run-clock query still sorts candidates:\n%s", startPlan)
	}
	if !strings.Contains(startPlan, "idx_monitor_run_items_series") {
		t.Fatalf("diff run-clock query does not read idx_monitor_run_items_series:\n%s", startPlan)
	}
	itemPlan := explainMonitorPlan(t, store, monitorRunDiffPrevItemSQL,
		"local", "host-a", series, "run_current", int64(1000))
	if strings.Contains(itemPlan, "TEMP B-TREE") {
		t.Fatalf("diff item query still sorts candidates:\n%s", itemPlan)
	}
}

func persistMetricTestSeriesRun(t *testing.T, store *GatewayStore, runID string, runStartMs, itemStartMs int64, artifactJSON, status string) (monitorRunRecord, monitorRunItemRecord) {
	t.Helper()
	ctx := context.Background()
	run := monitorRunRecord{
		ID:           runID,
		TriggerKind:  "manual",
		TriggeredBy:  "metric-admin",
		Status:       "running",
		StartedAtMs:  runStartMs,
		FinishedAtMs: runStartMs + 1000,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("create run %s: %v", runID, err)
	}
	item := monitorRunItemRecord{
		RunID:           runID,
		Cluster:         "local",
		Instance:        "host-a",
		AssetID:         metricTestSkillSlug,
		AssetName:       "web host-a",
		AssetKind:       "skill",
		Strategy:        "skill:" + metricTestSkillSlug,
		CollectorMethod: "skill",
		StartedAtMs:     itemStartMs,
		FinishedAtMs:    itemStartMs + 900,
		Status:          status,
		ArtifactJSON:    json.RawMessage(artifactJSON),
		Evidence:        map[string]any{"skill_slug": metricTestSkillSlug},
	}
	run.Status = "success"
	run.FinishedAtMs = itemStartMs + 1000
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, nil, nil); err != nil {
		t.Fatalf("persist run %s: %v", runID, err)
	}
	return run, item
}

// TestMonitorRunItemDiffKeepsRunOrderWhenItemsOverlap proves the diff orders by
// the run clock, not the item clock: run B starts after run A but A dispatches
// its item late, so item-level started_at_ms would pick A as the predecessor.
func TestMonitorRunItemDiffKeepsRunOrderWhenItemsOverlap(t *testing.T) {
	store, _ := openMetricTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
	persistMetricTestSeriesRun(t, store, "run_overlap_a", at, at+50_000,
		`{"metrics":[{"key":"cpu_percent","value":10,"unit":"%"}]}`, "pass")
	persistMetricTestSeriesRun(t, store, "run_overlap_b", at+60_000, at+61_000,
		`{"metrics":[{"key":"cpu_percent","value":20,"unit":"%"}]}`, "pass")
	runC, itemC := persistMetricTestSeriesRun(t, store, "run_overlap_c", at+120_000, at+120_500,
		`{"metrics":[{"key":"cpu_percent","value":30,"unit":"%"}]}`, "pass")

	diff := store.buildMonitorRunItemDiff(ctx, runC, itemC)
	if diff == nil {
		t.Fatal("diff = nil, want comparison against run_overlap_b")
	}
	if diff.PreviousRunID != "run_overlap_b" {
		t.Fatalf("previous run = %s, want run_overlap_b (run order, not item order)", diff.PreviousRunID)
	}
	if diff.PreviousAtMs != at+60_000 {
		t.Fatalf("previous at = %d, want %d", diff.PreviousAtMs, at+60_000)
	}
	if len(diff.MetricDeltas) != 1 || diff.MetricDeltas[0].Key != "cpu_percent" {
		t.Fatalf("metric deltas = %+v, want one cpu_percent delta", diff.MetricDeltas)
	}
	if delta := diff.MetricDeltas[0]; delta.Previous != 20 || delta.Current != 30 || delta.Delta != 10 {
		t.Fatalf("cpu delta = %+v, want previous=20 current=30 delta=10", delta)
	}
}

// SF-12 D2: skill_slug stays a residual predicate behind
// idx_inspection_metric_points_target, and the bounded scan must still return
// exactly the requested target's points (no cross-target leakage or truncation).
func TestInspectionMetricSeriesStaysScopedToTarget(t *testing.T) {
	store, _ := openMetricTestStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).UnixMilli()
	targets := [][2]string{{"local", "host-a"}, {"local", "host-b"}, {"prod", "host-c"}}
	const pointsPerTarget = 40
	insert := func(target [2]string, skill, key string, atMs int64, value float64) {
		t.Helper()
		if _, err := store.db.Exec("INSERT INTO inspection_metric_points(collected_at_ms, run_id, cluster, instance, skill_slug, metric_key, metric_label, value, unit, status) "+
			"VALUES(?,?,?,?,?,?,?,?,?,?)",
			atMs, "run_"+target[1], target[0], target[1], skill, key, "CPU 使用率", value, "%", "pass"); err != nil {
			t.Fatalf("seed metric point: %v", err)
		}
	}
	for ti, target := range targets {
		for p := 0; p < pointsPerTarget; p++ {
			insert(target, metricTestSkillSlug, "cpu_percent", base+int64(p)*1000, float64(ti*1000+p))
		}
		// Another skill on the same target must not leak into the series.
		insert(target, "other-skill", "cpu_percent", base, 999)
	}
	for ti, target := range targets {
		series, err := store.ListInspectionMetricSeries(ctx, target[0], target[1], metricTestSkillSlug, base-1, base+pointsPerTarget*1000)
		if err != nil {
			t.Fatalf("list series %s/%s: %v", target[0], target[1], err)
		}
		if len(series) != 1 || series[0].Key != "cpu_percent" {
			t.Fatalf("target %s/%s series = %+v, want one cpu_percent series", target[0], target[1], series)
		}
		if got := len(series[0].Points); got != pointsPerTarget {
			t.Fatalf("target %s/%s points = %d, want %d", target[0], target[1], got, pointsPerTarget)
		}
		for p, point := range series[0].Points {
			want := float64(ti*1000 + p)
			if point.Value != want {
				t.Fatalf("target %s/%s point %d value = %v, want %v", target[0], target[1], p, point.Value, want)
			}
		}
	}
}

func TestInspectVMHostGPUShimAndHostViewScope(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required")
	}
	scriptPath := filepath.Join("skills_builtin", "inspect-vm-host", "scripts", "inspect.sh")

	shimDir := t.TempDir()
	shimPath := filepath.Join(shimDir, "nvidia-smi")
	shimBody := "#!/bin/sh\nprintf '0, NVIDIA RTX 4090 D, 80, 18000, 24576, 86, 320.5\\n1, NVIDIA RTX 4090 D, 40, 6576, 24576, 62, 180.0\\n'\n"
	if err := os.WriteFile(shimPath, []byte(shimBody), 0o755); err != nil {
		t.Fatalf("write nvidia-smi shim: %v", err)
	}

	cmd := exec.Command("sh", scriptPath)
	cmd.Env = append(os.Environ(),
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOOPS_TARGET_CLUSTER=gpu-test",
		"DOOPS_TARGET_INSTANCE=node-01",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect-vm-host failed: %v\n%s", err, string(out))
	}
	parsed := extractInspectionArtifact(string(out), "inspect-vm-host", "inspection")
	if parsed.Format != "markdown+json" || len(parsed.JSON) == 0 {
		t.Fatalf("expected markdown+json artifact, got format=%q len=%d", parsed.Format, len(parsed.JSON))
	}
	var payload struct {
		Host struct {
			ViewScope string `json:"view_scope"`
			GPUCount  int    `json:"gpu_count"`
		} `json:"host"`
		GPUs    []map[string]any          `json:"gpus"`
		Metrics []map[string]any          `json:"metrics"`
		Checks  []api.MonitorInspectCheck `json:"checks"`
		Charts  []struct {
			Series []struct {
				Label string `json:"label"`
			} `json:"series"`
		} `json:"charts"`
	}
	if err := json.Unmarshal(parsed.JSON, &payload); err != nil {
		t.Fatalf("unmarshal artifact json: %v", err)
	}
	if payload.Host.ViewScope == "" {
		t.Fatalf("expected non-empty host.view_scope")
	}
	if payload.Host.GPUCount != 2 || len(payload.GPUs) != 2 {
		t.Fatalf("expected 2 GPUs from shim, got host.gpu_count=%d len(gpus)=%d", payload.Host.GPUCount, len(payload.GPUs))
	}
	metricMap := map[string]float64{}
	for _, m := range payload.Metrics {
		k, _ := m["key"].(string)
		if v, ok := m["value"].(float64); ok {
			metricMap[k] = v
		}
	}
	if got := metricMap["gpu_percent"]; got != 60 {
		t.Errorf("gpu_percent = %v, want 60", got)
	}
	if got := metricMap["gpu_temp_max_c"]; got != 86 {
		t.Errorf("gpu_temp_max_c = %v, want 86", got)
	}
	if got := metricMap["gpu_count"]; got != 2 {
		t.Errorf("gpu_count = %v, want 2", got)
	}
	if got := metricMap["cpu_total_cores"]; got <= 0 {
		t.Errorf("cpu_total_cores = %v, want > 0", got)
	}
	var gpuCheck *api.MonitorInspectCheck
	for i := range payload.Checks {
		if payload.Checks[i].ID == "host.gpu" {
			gpuCheck = &payload.Checks[i]
			break
		}
	}
	if gpuCheck == nil {
		t.Fatalf("missing host.gpu check")
	}
	if gpuCheck.Status != "warning" {
		t.Errorf("host.gpu status = %q, want warning (86C >= 85C threshold)", gpuCheck.Status)
	}
	if len(payload.Charts) == 0 || len(payload.Charts[0].Series) < 5 {
		t.Fatalf("expected GPU bars appended to resource chart, got %+v", payload.Charts)
	}
}

func TestBuildMonitorOverviewAggregatesSkillHostSnapshots(t *testing.T) {
	store, _ := openMetricTestStore(t)
	auth := metricTestAdminAuth(t, store)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()

	run := monitorRunRecord{
		ID:           "run_overview_skill_vm",
		TriggerKind:  "scheduled",
		TriggeredBy:  "system",
		Status:       "running",
		StartedAtMs:  now - 5_000,
		FinishedAtMs: now - 1_000,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	artifact := `{"status":"pass","summary":"host ok","host":{"os":"Ubuntu 24.04","kernel":"Linux 6.8"},"metrics":[
		{"key":"cpu_total_cores","value":32,"unit":""},
		{"key":"cpu_percent","value":25.0,"unit":"%"},
		{"key":"memory_percent","value":50.0,"unit":"%"},
		{"key":"memory_total_bytes","value":68719476736,"unit":"B"},
		{"key":"memory_used_bytes","value":34359738368,"unit":"B"},
		{"key":"disk_percent","value":60.0,"unit":"%"},
		{"key":"disk_total_bytes","value":1099511627776,"unit":"B"},
		{"key":"disk_used_bytes","value":659706976665,"unit":"B"}
	]}`
	item := monitorRunItemRecord{
		RunID:           run.ID,
		Cluster:         "prod",
		Instance:        "gpu-01",
		AssetID:         "inspect-vm-host",
		AssetName:       "gpu-01",
		AssetKind:       "skill",
		Strategy:        "skill:inspect-vm-host",
		CollectorMethod: "skill_script",
		StartedAtMs:     now - 5_000,
		FinishedAtMs:    now - 1_000,
		Status:          "pass",
		ArtifactJSON:    json.RawMessage(artifact),
		Evidence:        map[string]any{"skill_slug": "inspect-vm-host"},
	}
	run.Status = "success"
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, nil, nil); err != nil {
		t.Fatalf("persist run: %v", err)
	}

	ov, err := hub.BuildMonitorOverview(ctx, auth, "1h", "", "", "")
	if err != nil {
		t.Fatalf("BuildMonitorOverview: %v", err)
	}
	if ov.Summary.AvgCPUPercent != 25.0 {
		t.Errorf("AvgCPUPercent = %v, want 25.0", ov.Summary.AvgCPUPercent)
	}
	if ov.Summary.AvgMemoryPercent != 50.0 {
		t.Errorf("AvgMemoryPercent = %v, want 50.0", ov.Summary.AvgMemoryPercent)
	}
	if ov.Summary.AvgDiskPercent != 60.0 {
		t.Errorf("AvgDiskPercent = %v, want 60.0", ov.Summary.AvgDiskPercent)
	}
	if ov.Summary.TotalCPUCores != 32 || ov.Summary.UsedCPUCores != 8 {
		t.Errorf("CPU cores total/used = %v/%v, want 32/8", ov.Summary.TotalCPUCores, ov.Summary.UsedCPUCores)
	}
	if ov.Summary.TotalMemoryBytes != 68719476736 || ov.Summary.UsedMemoryBytes != 34359738368 {
		t.Errorf("Memory bytes total/used = %v/%v", ov.Summary.TotalMemoryBytes, ov.Summary.UsedMemoryBytes)
	}
	if ov.Summary.TotalDiskBytes != 1099511627776 {
		t.Errorf("TotalDiskBytes = %v, want 1099511627776", ov.Summary.TotalDiskBytes)
	}
	samples := 0
	for _, b := range ov.Trends {
		samples += b.Samples
	}
	if samples == 0 {
		t.Errorf("expected non-zero trend bucket samples for skill:inspect-vm-host snapshots")
	}
}

func TestDerivedSkillSeriesBridgeToAssetMonitorAndAlerts(t *testing.T) {
	store, _ := openMetricTestStore(t)
	ctx := context.Background()
	if _, err := store.db.Exec(`INSERT INTO agent_status(cluster,instance,status,updated_at) VALUES('prod','node-gpu','online','2026-11-27T12:00:00Z')`); err != nil {
		t.Fatalf("seed agent_status: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO inventory_machines(asset_id,name,kind,address,cluster,instance,revision,created_at) VALUES('phy-gpu-01','GPU Host','physical','10.0.0.10','prod','node-gpu',1,'2025-10-20T00:00:00Z')`); err != nil {
		t.Fatalf("seed machine: %v", err)
	}

	const gpuUUID = "GPU-11111111-2222-3333-4444-555555555555"
	vmArtifact := fmt.Sprintf(`{
		"status":"pass",
		"metrics":[
			{"key":"cpu_total_cores","value":32,"unit":""},
			{"key":"cpu_percent","value":25.0,"unit":"%%"},
			{"key":"memory_used_bytes","value":17179869184,"unit":"B"},
			{"key":"net_bytes_per_sec","value":500000,"unit":"B/s"}
		],
		"mounts":[
			{"mount":"/","total_gib":100,"used_gib":40,"shared_volume":false},
			{"mount":"/dup","total_gib":100,"used_gib":40,"shared_volume":true}
		],
		"network_interfaces":[
			{"interface":"eth0","rx_bytes_per_sec":120000,"tx_bytes_per_sec":80000,"virtual":false},
			{"interface":"docker0","rx_bytes_per_sec":999,"tx_bytes_per_sec":999,"virtual":true}
		],
		"gpus":[
			{"index":0,"name":"NVIDIA RTX 4090 D","uuid":%q,"util_pct":88.5},
			{"index":1,"name":"No UUID GPU","util_pct":50.0}
		]
	}`, gpuUUID)
	containerArtifact := `{
		"status":"pass",
		"containers":[
			{"name":"nginx-web","cpu_percent":150.0,"mem_percent":50.0,"mem_usage":"512MiB / 1GiB","mem_limit_bytes":1073741824}
		]
	}`
	k8sArtifact := `{
		"status":"pass",
		"node_usage":[
			{"node":"k8s-worker-1","cpu_millicores":2500,"memory_bytes":4294967296}
		],
		"pod_usage":[
			{"namespace":"apps","pod":"web-7f8b9","cpu_millicores":350,"memory_bytes":268435456},
			{"namespace":"INVALID_NS","pod":"bad-pod","cpu_millicores":100,"memory_bytes":1024}
		]
	}`
	appArtifact := `{
		"status":"warning",
		"endpoints":[
			{"url":"https://portal.example.edu/health","status_code":200,"latency_ms":42.5,"error":""},
			{"url":"https://broken.example.edu/api","status_code":502,"latency_ms":120.0,"error":""}
		]
	}`

	nowMs := time.Now().UnixMilli()
	run := monitorRunRecord{
		ID: "run_derived_bridge_1", TriggerKind: "manual", TriggeredBy: "admin",
		Status: "success", StartedAtMs: nowMs - 4000, FinishedAtMs: nowMs - 3000,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	items := []monitorRunItemRecord{
		{RunID: run.ID, Cluster: "prod", Instance: "node-gpu", AssetID: "inspect-vm-host", AssetKind: "skill", Strategy: "skill:inspect-vm-host", CollectorMethod: "skill_script", StartedAtMs: nowMs - 3900, FinishedAtMs: nowMs - 3500, Status: "pass", ArtifactJSON: json.RawMessage(vmArtifact), Evidence: map[string]any{"skill_slug": "inspect-vm-host"}},
		{RunID: run.ID, Cluster: "prod", Instance: "node-gpu", AssetID: "inspect-container-runtime", AssetKind: "skill", Strategy: "skill:inspect-container-runtime", CollectorMethod: "skill_script", StartedAtMs: nowMs - 3800, FinishedAtMs: nowMs - 3400, Status: "pass", ArtifactJSON: json.RawMessage(containerArtifact), Evidence: map[string]any{"skill_slug": "inspect-container-runtime"}},
		{RunID: run.ID, Cluster: "prod", Instance: "node-gpu", AssetID: "inspect-k8s-cluster", AssetKind: "skill", Strategy: "skill:inspect-k8s-cluster", CollectorMethod: "skill_script", StartedAtMs: nowMs - 3700, FinishedAtMs: nowMs - 3300, Status: "pass", ArtifactJSON: json.RawMessage(k8sArtifact), Evidence: map[string]any{"skill_slug": "inspect-k8s-cluster"}},
		{RunID: run.ID, Cluster: "prod", Instance: "node-gpu", AssetID: "inspect-app-web", AssetKind: "skill", Strategy: "skill:inspect-app-web", CollectorMethod: "skill_script", StartedAtMs: nowMs - 3600, FinishedAtMs: nowMs - 3200, Status: "warning", ArtifactJSON: json.RawMessage(appArtifact), Evidence: map[string]any{"skill_slug": "inspect-app-web"}},
	}
	if _, err := store.PersistMonitorRunResults(ctx, run, items, nil, nil); err != nil {
		t.Fatalf("persist run: %v", err)
	}

	assertSample := func(assetType, assetID, origin, metric, device string, want float64) {
		t.Helper()
		var got float64
		err := store.db.QueryRow(`SELECT p.value FROM asset_monitor_samples p
			JOIN asset_monitor_series s ON s.id=p.series_id
			WHERE s.cluster='prod' AND s.instance='node-gpu' AND s.type=? AND s.asset_id=? AND s.origin=? AND s.metric=? AND s.device_id=?
			ORDER BY p.at_ms DESC LIMIT 1`, assetType, assetID, origin, metric, device).Scan(&got)
		if err != nil {
			t.Fatalf("missing bridged sample %s/%s/%s/%s(%s): %v", assetType, assetID, origin, metric, device, err)
		}
		if math.Abs(got-want) > 0.01 {
			t.Fatalf("sample %s/%s/%s(%s) = %v, want %v", assetType, assetID, metric, device, got, want)
		}
	}

	// Host resolves bound inventory_machines asset_id 'phy-gpu-01', skips shared mount /dup, virtual docker0, and UUID-less GPU.
	assertSample("machine", "phy-gpu-01", "agent_host", "cpu", "", 8.0)
	assertSample("machine", "phy-gpu-01", "agent_host", "memory", "", 17179869184)
	assertSample("machine", "phy-gpu-01", "agent_host", "network", "eth0", 200000)
	assertSample("machine", "phy-gpu-01", "agent_host", "storage", "/", 40*1073741824)
	assertSample("machine", "phy-gpu-01", "agent_host", "gpu", gpuUUID, 88.5)
	// Container, K8s node, App probe bridged too.
	assertSample("container", "nginx-web", "container_stats", "cpu", "", 1.5)
	assertSample("container", "nginx-web", "container_stats", "memory", "", 512*1024*1024)
	assertSample("container", "nginx-web", "container_stats", "memory_limit", "", 1073741824)
	assertSample("machine", "k8s-worker-1", "k8s_top", "cpu", "", 2.5)
	assertSample("machine", "k8s-worker-1", "k8s_top", "memory", "", 4294967296)
	// K8s Pod usage bridged into type=app with namespace preserved.
	var podCPU, podMem float64
	if err := store.db.QueryRow(`SELECT p.value FROM asset_monitor_samples p
		JOIN asset_monitor_series s ON s.id=p.series_id
		WHERE s.cluster='prod' AND s.instance='node-gpu' AND s.type='app' AND s.namespace='apps' AND s.asset_id='web-7f8b9' AND s.origin='k8s_top' AND s.metric='cpu'
		ORDER BY p.at_ms DESC LIMIT 1`).Scan(&podCPU); err != nil {
		t.Fatalf("missing bridged k8s pod cpu sample: %v", err)
	}
	if math.Abs(podCPU-0.35) > 0.001 {
		t.Fatalf("pod cpu = %v, want 0.35", podCPU)
	}
	if err := store.db.QueryRow(`SELECT p.value FROM asset_monitor_samples p
		JOIN asset_monitor_series s ON s.id=p.series_id
		WHERE s.cluster='prod' AND s.instance='node-gpu' AND s.type='app' AND s.namespace='apps' AND s.asset_id='web-7f8b9' AND s.origin='k8s_top' AND s.metric='memory'
		ORDER BY p.at_ms DESC LIMIT 1`).Scan(&podMem); err != nil {
		t.Fatalf("missing bridged k8s pod memory sample: %v", err)
	}
	if math.Abs(podMem-268435456) > 1 {
		t.Fatalf("pod mem = %v, want 268435456", podMem)
	}
	assertSample("app", "portal.example.edu", "app_probe", "response_time_ms", "", 42.5)
	assertSample("app", "portal.example.edu", "app_probe", "error_rate", "", 0)
	assertSample("app", "broken.example.edu", "app_probe", "error_rate", "", 100)

	// Confirm virtual NIC, shared mount, and invalid K8s namespace pod were NOT bridged.
	var badCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM asset_monitor_series WHERE device_id IN ('docker0','/dup') OR asset_id='bad-pod'`).Scan(&badCount); err != nil {
		t.Fatal(err)
	}
	if badCount != 0 {
		t.Fatalf("virtual NIC, shared mount, or invalid-ns pod leaked into asset_monitor_series: %d", badCount)
	}

	// Configure a threshold rule on the bridged GPU series and verify a subsequent Skill run triggers an open alert!
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	thresh := 80.0
	if _, err := saveMonitorRule(tx, monitorRequest{
		Cluster: "prod", Instance: "node-gpu",
		monitorAsset: monitorAsset{Type: "machine", AssetID: "phy-gpu-01", Origin: "agent_host"},
		Metric:       "gpu", DeviceID: gpuUUID,
		monitorRule: monitorRule{Threshold: &thresh, Duration: 1},
	}, nowMs-2500, "admin"); err != nil {
		tx.Rollback()
		t.Fatalf("save GPU rule: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for idx, ts := range []int64{nowMs - 1500, nowMs - 200} {
		r2 := monitorRunRecord{
			ID: fmt.Sprintf("run_derived_alert_%d", idx), TriggerKind: "scheduled", TriggeredBy: "system",
			Status: "success", StartedAtMs: ts - 100, FinishedAtMs: ts,
		}
		if err := store.CreateMonitorRun(ctx, r2); err != nil {
			t.Fatal(err)
		}
		it2 := monitorRunItemRecord{
			RunID: r2.ID, Cluster: "prod", Instance: "node-gpu", AssetID: "inspect-vm-host", AssetKind: "skill",
			Strategy: "skill:inspect-vm-host", CollectorMethod: "skill_script", StartedAtMs: ts - 50, FinishedAtMs: ts,
			Status: "pass", ArtifactJSON: json.RawMessage(vmArtifact), Evidence: map[string]any{"skill_slug": "inspect-vm-host"},
		}
		if _, err := store.PersistMonitorRunResults(ctx, r2, []monitorRunItemRecord{it2}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	var openAlerts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM asset_monitor_alerts a
		JOIN asset_monitor_series s ON s.id=a.series_id
		WHERE s.cluster='prod' AND s.instance='node-gpu' AND s.metric='gpu' AND s.device_id=? AND a.state='open'`, gpuUUID).Scan(&openAlerts); err != nil {
		t.Fatal(err)
	}
	if openAlerts != 1 {
		t.Fatalf("expected 1 open alert triggered by bridged Skill run, got %d", openAlerts)
	}
}

func TestInspectK8sPodUsageShimAndAppWebCatalogURLInjection(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required")
	}

	// 1) Verify inspect-k8s-cluster emits sorted pod_usage from kubectl top pods -A.
	shimDir := t.TempDir()
	kubectlShim := filepath.Join(shimDir, "kubectl")
	shimBody := `#!/bin/sh
args="$*"
case "$args" in
  *"top nodes"*)
    printf 'node-a   1200m   30%%   4096Mi   50%%\n'
    ;;
  *"top pods"*)
    printf 'kube-system   coredns-1   50m   64Mi\n'
    printf 'apps   api-web-1   350m   512Mi\n'
    ;;
  *"get nodes"*)
    printf '{"items":[{"metadata":{"name":"node-a"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}\n'
    ;;
  *)
    printf '{"items":[]}\n'
    ;;
esac
`
	if err := os.WriteFile(kubectlShim, []byte(shimBody), 0o755); err != nil {
		t.Fatalf("write kubectl shim: %v", err)
	}
	scriptPath := filepath.Join("skills_builtin", "inspect-k8s-cluster", "scripts", "inspect.sh")
	cmd := exec.Command("sh", scriptPath)
	cmd.Env = append(os.Environ(),
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOOPS_TARGET_CLUSTER=k8s-test",
		"DOOPS_TARGET_INSTANCE=cluster-a",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect-k8s-cluster failed: %v\n%s", err, string(out))
	}
	parsed := extractInspectionArtifact(string(out), "inspect-k8s-cluster", "inspection")
	if parsed.Format != "markdown+json" || len(parsed.JSON) == 0 {
		t.Fatalf("expected markdown+json artifact, got format=%q", parsed.Format)
	}
	var payload struct {
		PodUsage []struct {
			Namespace     string `json:"namespace"`
			Pod           string `json:"pod"`
			CPUMillicores int    `json:"cpu_millicores"`
			MemoryBytes   int64  `json:"memory_bytes"`
		} `json:"pod_usage"`
	}
	if err := json.Unmarshal(parsed.JSON, &payload); err != nil {
		t.Fatalf("unmarshal k8s artifact: %v", err)
	}
	if len(payload.PodUsage) != 2 {
		t.Fatalf("expected 2 pod_usage rows, got %+v", payload.PodUsage)
	}
	if payload.PodUsage[0].Namespace != "apps" || payload.PodUsage[0].Pod != "api-web-1" || payload.PodUsage[0].CPUMillicores != 350 {
		t.Fatalf("expected top CPU pod apps/api-web-1 first, got %+v", payload.PodUsage[0])
	}
	derived := deriveArtifactSeriesPoints(monitorRunItemRecord{
		Cluster: "k8s-test", Instance: "cluster-a", Strategy: "skill:inspect-k8s-cluster",
		ArtifactJSON: parsed.JSON,
	}, time.Now().UnixMilli(), "")
	foundPod := false
	for _, pt := range derived {
		if pt.Type == "app" && pt.Namespace == "apps" && pt.AssetID == "api-web-1" && pt.Metric == "cpu" && pt.Value != nil && math.Abs(*pt.Value-0.35) < 0.001 {
			foundPod = true
		}
	}
	if !foundPod {
		t.Fatalf("expected derived app cpu point for apps/api-web-1, got %+v", derived)
	}

	// 2) Verify resolveTargetInspectURLs & skillScriptExtraEnv inject validated website catalog URLs into inspect-app-web only.
	store, _ := openMetricTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	ctx := context.Background()
	seedSite := func(name, cluster, instance, details string) {
		t.Helper()
		if _, err := store.db.Exec(
			`INSERT INTO inventory_resource_catalog(id,kind,cluster,instance,created_by,name,environment,details,identity_key,revision) VALUES(?,'website',?,?,'admin',?,'prod',?,?,1)`,
			"site-"+name, cluster, instance, name, details, name,
		); err != nil {
			t.Fatalf("seed website %s: %v", name, err)
		}
	}
	seedSite("portal", "prod", "web-01", `{"scheme":"https","domain":"portal.example.edu","path":"/health"}`)
	seedSite("portal-dup", "prod", "web-01", `{"scheme":"https","domain":"portal.example.edu","path":"/health"}`)
	seedSite("api", "prod", "web-01", `{"scheme":"http","domain":"api.example.edu","path":"/ready"}`)
	seedSite("bad-query", "prod", "web-01", `{"scheme":"https","domain":"bad.example.edu","path":"/health?token=secret"}`)
	seedSite("bad-comma", "prod", "web-01", `{"scheme":"https","domain":"comma.example.edu","path":"/a,b"}`)
	seedSite("other-target", "prod", "web-02", `{"scheme":"https","domain":"other.example.edu","path":"/"}`)

	tgt := monitorTargetRef{Cluster: "prod", Instance: "web-01"}
	env, count := hub.skillScriptExtraEnv(ctx, "inspect-app-web", tgt)
	wantURLs := "http://api.example.edu/ready,https://portal.example.edu/health"
	if count != 2 || env["DOOPS_INSPECT_URLS"] != wantURLs {
		t.Fatalf("skillScriptExtraEnv(inspect-app-web) = (%v, %d), want %q (2)", env, count, wantURLs)
	}
	cmdStr := skillScriptCommandWithEnv("inspect-app-web", "scripts/inspect.sh", tgt, "run_web_1", env)
	if !strings.Contains(cmdStr, "export DOOPS_INSPECT_URLS='"+wantURLs+"'") {
		t.Fatalf("expected single-quoted DOOPS_INSPECT_URLS in command, got: %s", cmdStr)
	}
	if otherEnv, otherCount := hub.skillScriptExtraEnv(ctx, "inspect-vm-host", tgt); otherCount != 0 || len(otherEnv) != 0 {
		t.Fatalf("non-app-web skill should not receive DOOPS_INSPECT_URLS, got (%v, %d)", otherEnv, otherCount)
	}
}
