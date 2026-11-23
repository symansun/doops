package server

// SF-04 contract: inspection artifacts stay the source of truth on disk while
// their numeric metrics are projected into inspection_metric_points, standard
// keys bridge into monitor_resource_snapshots, GET /v1/monitor/metrics serves
// the series, and GET /v1/monitor/runs/<id> attaches a cross-run diff.

import (
	"context"
	"database/sql"
	"encoding/json"
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
	CPUPercent    sql.NullFloat64
	MemoryPercent sql.NullFloat64
	DiskPercent   sql.NullFloat64
	Load1m        sql.NullFloat64
	UptimeSeconds sql.NullFloat64
	Details       string
}

func readMetricSnapshot(t *testing.T, store *GatewayStore, runID string) metricSnapshotRow {
	t.Helper()
	var row metricSnapshotRow
	err := store.db.QueryRow(`SELECT cpu_percent, memory_percent, disk_percent, load_1m, uptime_seconds, details_json
		FROM monitor_resource_snapshots WHERE run_id=?`, runID).Scan(
		&row.CPUPercent, &row.MemoryPercent, &row.DiskPercent, &row.Load1m, &row.UptimeSeconds, &row.Details)
	if err != nil {
		t.Fatalf("read bridged snapshot: %v", err)
	}
	return row
}

func TestInspectionMetricPersistenceAndSnapshotBridge(t *testing.T) {
	store, _ := openMetricTestStore(t)
	artifact := `{"status":"pass","summary":"host ok","metrics":[
		{"key":"cpu_percent","value":42.5,"unit":"%","label":"CPU 使用率"},
		{"key":"memory_percent","value":61.2,"unit":"%"},
		{"key":"disk_percent","value":71,"unit":"%"},
		{"key":"load_1m","value":1.75},
		{"key":"uptime_seconds","value":86400},
		{"key":"app_latency_ms","value":12.5,"unit":"ms"},
		{"key":"bad key","value":1},
		{"key":"cpu_percent","value":99},
		{"key":"skipped_text","value":"n/a"}
	]}`
	run := persistMetricTestRun(t, store, "run_metric_json", time.Now().Add(-time.Hour).UnixMilli(), nil, artifact, nil)

	values := metricValuesByKey(t, store, run.ID)
	if len(values) != 6 {
		t.Fatalf("want 6 metric rows, got %d: %v", len(values), values)
	}
	want := map[string]float64{
		"cpu_percent":    42.5,
		"memory_percent": 61.2,
		"disk_percent":   71,
		"load_1m":        1.75,
		"uptime_seconds": 86400,
		"app_latency_ms": 12.5,
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
	if got := extraction["extracted"]; got != float64(6) {
		t.Errorf("evidence extracted = %v, want 6", got)
	}
	if got := extraction["skipped"]; got != float64(3) {
		t.Errorf("evidence skipped = %v, want 3 (invalid key, duplicate key, non-numeric value)", got)
	}

	snapshot := readMetricSnapshot(t, store, run.ID)
	for name, field := range map[string]struct {
		value sql.NullFloat64
		want  float64
	}{
		"cpu_percent":    {snapshot.CPUPercent, 42.5},
		"memory_percent": {snapshot.MemoryPercent, 61.2},
		"disk_percent":   {snapshot.DiskPercent, 71},
		"load_1m":        {snapshot.Load1m, 1.75},
		"uptime_seconds": {snapshot.UptimeSeconds, 86400},
	} {
		if !field.value.Valid || field.value.Float64 != field.want {
			t.Errorf("bridged snapshot %s = %v (valid=%v), want %v", name, field.value.Float64, field.value.Valid, field.want)
		}
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
