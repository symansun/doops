package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func monitorEngineFixture(t *testing.T) *executionLifecycleFixture {
	t.Helper()
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	mux := http.NewServeMux()
	f.hub.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.server = srv
	return f
}

// TestMonitorEngineScheduledSkillRun drives the scheduled path end to end after
// SF-09 retired the native strategy collectors: a schedule now selects Skill
// packages only, and every run item must come from a Skill package or a
// target-offline gap.
func TestMonitorEngineScheduledSkillRun(t *testing.T) {
	f := monitorEngineFixture(t)
	f.httpTimeout = 60 * time.Second
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())

	inventoryTestAgent(t, f, "local")
	if _, err := f.store.db.Exec("INSERT INTO agent_status(cluster,instance,status,updated_at,last_seen) VALUES('dev','offline-node','offline',?,?)",
		formatTime(time.Now().UTC()), formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	operator := f.user(t, "mon-op", ScopeGrant{Cluster: "dev", Instance: "local"})

	schedBody := `{"action":"create","name":"全校定时巡检","description":"按 Skill 包巡检","scope_mode":"targets",` +
		`"targets":[{"cluster":"dev","instance":"local"},{"cluster":"dev","instance":"offline-node"}],` +
		`"skills":["inspect-vm-host"],"interval_seconds":300}`
	f.http(t, operator, "POST", "/v1/monitor/schedules", schedBody, 403)
	schedRaw := f.http(t, f.admin, "POST", "/v1/monitor/schedules", schedBody, 200)
	if strings.Contains(string(schedRaw), "\"strategies\"") {
		t.Fatalf("schedule response still exposes retired strategies field: %s", schedRaw)
	}
	var schedResp struct {
		Schedule monitorScheduleRecord `json:"schedule"`
	}
	if err := json.Unmarshal(schedRaw, &schedResp); err != nil || schedResp.Schedule.ID == "" {
		t.Fatalf("create schedule: %s", schedRaw)
	}
	schedID := schedResp.Schedule.ID
	if len(schedResp.Schedule.Skills) != 1 || schedResp.Schedule.Skills[0] != "inspect-vm-host" {
		t.Fatalf("schedule did not keep skill selection: %+v", schedResp.Schedule)
	}

	pastMs := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := f.store.db.Exec("UPDATE monitor_schedules SET next_run_at_ms=? WHERE id=?", pastMs, schedID); err != nil {
		t.Fatal(err)
	}
	runs := f.hub.RunDueMonitorSchedules(context.Background(), time.Now().UTC())
	if len(runs) != 1 {
		t.Fatalf("expected 1 due scheduled run, got %d", len(runs))
	}
	run := runs[0]
	if run.TriggerKind != "scheduled" || run.OfflineTargets != 1 {
		t.Fatalf("unexpected scheduled run summary: %+v", run)
	}
	if run.TotalAssets < 2 {
		t.Fatalf("expected one item per target, got %+v", run)
	}
	if again := f.hub.RunDueMonitorSchedules(context.Background(), time.Now().UTC()); len(again) != 0 {
		t.Fatalf("expected 0 duplicate scheduled runs, got %d", len(again))
	}

	adminDetailRaw := f.http(t, f.admin, "GET", "/v1/monitor/runs/"+run.ID, "", 200)
	if strings.Contains(string(adminDetailRaw), "\"strategies\"") {
		t.Fatalf("run detail still exposes retired strategies field: %s", adminDetailRaw)
	}
	var adminDetail struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(adminDetailRaw, &adminDetail); err != nil {
		t.Fatal(err)
	}
	sawSkill, sawOffline := false, false
	for _, it := range adminDetail.Items {
		if it.Status == "offline" {
			if it.Instance == "offline-node" && it.Reason == "target_offline" && strings.HasPrefix(it.Strategy, "skill:") {
				sawOffline = true
				continue
			}
			t.Fatalf("unexpected offline item: %+v", it)
		}
		if !strings.HasPrefix(it.Strategy, "skill:") {
			t.Fatalf("run item is not skill-driven: %+v", it)
		}
		if it.Instance == "local" {
			sawSkill = true
			if it.AssetID != "inspect-vm-host" || it.AssetKind != "skill" {
				t.Fatalf("unexpected skill item identity: %+v", it)
			}
			if it.Evidence["skill_slug"] != "inspect-vm-host" || it.Evidence["synced"] != true {
				t.Fatalf("skill item missing sync evidence: %+v", it.Evidence)
			}
			if it.CollectorMethod != "skill_script" {
				t.Fatalf("expected skill_script collector for inspect-vm-host, got %+v", it)
			}
		}
	}
	if !sawSkill || !sawOffline {
		t.Fatalf("expected both a skill item and an offline gap item, run=%+v items=%+v", adminDetail.Run, adminDetail.Items)
	}

	opDetailRaw := f.http(t, operator, "GET", "/v1/monitor/runs/"+run.ID, "", 200)
	var opDetail struct {
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(opDetailRaw, &opDetail); err != nil {
		t.Fatal(err)
	}
	for _, it := range opDetail.Items {
		if it.Instance != "local" {
			t.Fatalf("operator saw unauthorized target item: %+v", it)
		}
	}

	ovRaw := f.http(t, f.admin, "GET", "/v1/monitor/overview?range=1h", "", 200)
	if strings.Contains(string(ovRaw), "\"strategies\"") {
		t.Fatalf("overview still exposes retired strategies field: %s", ovRaw)
	}
	var ov monitorOverviewResponse
	if err := json.Unmarshal(ovRaw, &ov); err != nil {
		t.Fatal(err)
	}
	if ov.Summary.TotalAssets < 1 {
		t.Fatalf("unexpected overview summary: %+v", ov.Summary)
	}

	f.http(t, operator, "POST", "/v1/monitor/runs", `{"targets":[{"cluster":"dev","instance":"offline-node"}]}`, 403)
	retestRaw := f.http(t, f.admin, "POST", "/v1/monitor/runs", fmt.Sprintf(`{"retest_run_id":"%s"}`, run.ID), 200)
	var retestResp struct {
		Run monitorRunRecord `json:"run"`
	}
	if err := json.Unmarshal(retestRaw, &retestResp); err != nil || retestResp.Run.TriggerKind != "retest" {
		t.Fatalf("unexpected retest response: %s", retestRaw)
	}

	f.http(t, f.admin, "POST", "/v1/monitor/schedules", fmt.Sprintf(`{"action":"toggle","id":"%s","enabled":false}`, schedID), 200)
	f.http(t, f.admin, "POST", "/v1/monitor/schedules", fmt.Sprintf(`{"action":"delete","id":"%s"}`, schedID), 200)
}

// TestMonitorRunRejectsLegacyStrategies locks the SF-09 removal: the retired
// native selector must fail loudly instead of silently selecting collectors.
func TestMonitorRunRejectsLegacyStrategies(t *testing.T) {
	f := monitorEngineFixture(t)
	admin := f.admin

	raw := f.http(t, admin, "POST", "/v1/monitor/runs", `{"targets":[{"cluster":"dev","instance":"local"}],"strategies":["vm_host"]}`, 400)
	if !strings.Contains(string(raw), "strategies_removed") {
		t.Fatalf("expected strategies_removed, got %s", raw)
	}
	raw = f.http(t, admin, "POST", "/v1/monitor/runs", `{"targets":[{"cluster":"dev","instance":"local"}],"ssh_hosts":[{"asset_id":"srv-remote"}]}`, 400)
	if !strings.Contains(string(raw), "ssh_hosts_removed") {
		t.Fatalf("expected ssh_hosts_removed, got %s", raw)
	}
	raw = f.http(t, admin, "POST", "/v1/monitor/schedules",
		`{"action":"create","name":"legacy","scope_mode":"all_online","strategies":["vm_host"],"interval_seconds":300}`, 400)
	if !strings.Contains(string(raw), "strategies_removed") {
		t.Fatalf("expected schedule create to reject strategies, got %s", raw)
	}
}

// TestMonitorRunLegacyStrategiesRecordReadOnly proves rows written before SF-09
// (item.Strategy = native strategy id, schedule strategies_json populated) stay
// readable and can be retested through the matching Skill package.
func TestMonitorRunLegacyStrategiesRecordReadOnly(t *testing.T) {
	f := monitorEngineFixture(t)
	f.httpTimeout = 60 * time.Second
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	inventoryTestAgent(t, f, "local")

	ctx := context.Background()
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := f.store.db.Exec(`INSERT INTO monitor_schedules(id,name,description,enabled,scope_mode,targets_json,tags_json,`+
		`strategies_json,skills_json,prompt,command,thresholds_json,interval_seconds,next_run_at_ms,created_by,created_at,updated_at)`+
		`VALUES('sched_legacy','legacy','',0,'targets','[{"cluster":"dev","instance":"local"}]','[]',`+
		`'["vm_host"]','[]','','','{}',300,0,'system',?,?)`, formatTime(time.Now().UTC()), formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	run := monitorRunRecord{
		ID: "run_legacy_sf09", ScheduleID: "sched_legacy", ScheduleName: "legacy", TriggerKind: "scheduled",
		TriggeredBy: "system", Status: "critical", TotalTargets: 1, TotalAssets: 1,
		StartedAtMs: nowMs - 1000, FinishedAtMs: nowMs, DurationMs: 1000, Summary: "legacy run",
	}
	if err := f.store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	item := monitorRunItemRecord{
		RunID: "run_legacy_sf09", Cluster: "dev", Instance: "local", AssetID: "srv-local", AssetName: "Local VM",
		AssetKind: "vm", Strategy: "vm_host", CollectorMethod: "agent_local", Status: "critical",
		Reason: "legacy_cpu_critical", StartedAtMs: nowMs - 900, FinishedAtMs: nowMs, DurationMs: 900,
		Evidence: map[string]any{"skill_slug": "inspect-vm-host"},
	}
	if _, err := f.store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, nil, nil); err != nil {
		t.Fatal(err)
	}

	detailRaw := f.http(t, f.admin, "GET", "/v1/monitor/runs/run_legacy_sf09", "", 200)
	if strings.Contains(string(detailRaw), "\"strategies\"") {
		t.Fatalf("legacy run detail must not expose retired strategies field: %s", detailRaw)
	}
	var detail struct {
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(detailRaw, &detail); err != nil || len(detail.Items) != 1 {
		t.Fatalf("legacy run detail unreadable: %s", detailRaw)
	}
	if detail.Items[0].Strategy != "vm_host" {
		t.Fatalf("legacy item strategy should be preserved verbatim, got %+v", detail.Items[0])
	}

	retestRaw := f.http(t, f.admin, "POST", "/v1/monitor/runs", `{"retest_run_id":"run_legacy_sf09"}`, 200)
	var retest struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(retestRaw, &retest); err != nil || retest.Run.TriggerKind != "retest" {
		t.Fatalf("unexpected retest response: %s", retestRaw)
	}
	sawReplayed := false
	for _, it := range retest.Items {
		if it.Strategy == "skill:inspect-vm-host" {
			sawReplayed = true
		}
		if it.CollectorMethod == "agent_local" || it.Strategy == "vm_host" {
			t.Fatalf("retest replayed the retired native collector: %+v", it)
		}
	}
	if !sawReplayed {
		t.Fatalf("expected retest to replay vm_host through inspect-vm-host skill, got %+v", retest.Items)
	}
}

// TestMonitorEngineRetiredInternalToolAbsent asserts the retired internal tool
// is no longer registered, so no caller can reach the old collector pipeline.
func TestMonitorEngineRetiredInternalToolAbsent(t *testing.T) {
	if _, ok := gatewayToolIndex["doops_monitor_inspect"]; ok {
		t.Fatal("doops_monitor_inspect must not be registered after SF-09")
	}
	if retiredStrategySkillSlugs["vm_host"] != "inspect-vm-host" {
		t.Fatalf("unexpected legacy mapping: %+v", retiredStrategySkillSlugs)
	}
	for legacy, slug := range retiredStrategySkillSlugs {
		got, ok := skillSlugForRetiredStrategy(legacy)
		if !ok || got != slug {
			t.Fatalf("legacy %s -> %q, want %q", legacy, got, slug)
		}
	}
	if slug, ok := skillSlugForRetiredStrategy("skill:inspect-app-web"); !ok || slug != "inspect-app-web" {
		t.Fatalf("skill: prefix should pass through, got %q %v", slug, ok)
	}
	if _, ok := skillSlugForRetiredStrategy("unknown_strategy"); ok {
		t.Fatal("unknown legacy strategy must not map to a skill")
	}
}
