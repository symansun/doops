package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A request that names a Skill the registry does not know (or has disabled) is
// refused at request time: the run cannot execute anything, so accepting it would
// only produce a vacuous pass (SKILL_FIRST_INSPECTION_SPEC.md 2.5).
func TestMonitorRunRejectsUnknownSkill(t *testing.T) {
	f := monitorEngineFixture(t)
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	inventoryTestAgent(t, f, "local")

	unknownRaw := f.http(t, f.admin, "POST", "/v1/monitor/runs",
		`{"scope_mode":"targets","targets":[{"cluster":"dev","instance":"local"}],"skills":["no-such-skill"]}`, 400)
	if !strings.Contains(string(unknownRaw), "unknown_skill") || !strings.Contains(string(unknownRaw), "no-such-skill") {
		t.Fatalf("unknown skill response = %s, want unknown_skill naming no-such-skill", unknownRaw)
	}

	mixedRaw := f.http(t, f.admin, "POST", "/v1/monitor/runs",
		`{"scope_mode":"targets","targets":[{"cluster":"dev","instance":"local"}],"skills":["inspect-vm-host","ghost-one","ghost-two"]}`, 400)
	mixed := string(mixedRaw)
	if !strings.Contains(mixed, "ghost-one") || !strings.Contains(mixed, "ghost-two") {
		t.Fatalf("mixed selection response = %s, want both unknown slugs listed", mixed)
	}
	if strings.Contains(mixed, "inspect-vm-host") {
		t.Fatalf("mixed selection response = %s, want only the unknown slugs listed", mixed)
	}

	okRaw := f.http(t, f.admin, "POST", "/v1/monitor/runs",
		`{"scope_mode":"targets","targets":[{"cluster":"dev","instance":"local"}],"skills":["inspect-vm-host"]}`, 200)
	var okResp struct {
		Run monitorRunRecord `json:"run"`
	}
	if err := json.Unmarshal(okRaw, &okResp); err != nil || okResp.Run.ID == "" {
		t.Fatalf("known skill run = %s", okRaw)
	}

	badSched := f.http(t, f.admin, "POST", "/v1/monitor/schedules",
		`{"action":"create","name":"未知 Skill 计划","scope_mode":"targets","targets":[{"cluster":"dev","instance":"local"}],"skills":["no-such-skill"]}`, 400)
	if !strings.Contains(string(badSched), "unknown_skill") || !strings.Contains(string(badSched), "no-such-skill") {
		t.Fatalf("unknown skill schedule = %s, want unknown_skill naming no-such-skill", badSched)
	}
	f.http(t, f.admin, "POST", "/v1/monitor/schedules",
		`{"action":"create","name":"有效 Skill 计划","scope_mode":"targets","targets":[{"cluster":"dev","instance":"local"}],"skills":["inspect-vm-host"]}`, 200)
}

// A run over an online target with nothing executable must fail with a decidable
// reason, never fall through to the "全部 0 项巡检通过" default.
func TestMonitorRunWithoutExecutableItemsFails(t *testing.T) {
	f := monitorEngineFixture(t)
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	inventoryTestAgent(t, f, "local")

	raw := f.http(t, f.admin, "POST", "/v1/monitor/runs",
		`{"scope_mode":"targets","targets":[{"cluster":"dev","instance":"local"}]}`, 200)
	var resp struct {
		Run monitorRunRecord `json:"run"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode empty run: %s", raw)
	}
	run := resp.Run
	if run.TotalTargets != 1 || run.OfflineTargets != 0 || run.TotalAssets != 0 {
		t.Fatalf("fixture expected one online target and zero assets, got %+v", run)
	}
	if run.Status != "failed" {
		t.Fatalf("empty run status = %q, want failed (summary %q)", run.Status, run.Summary)
	}
	if !strings.Contains(run.Summary, "未执行任何巡检项") {
		t.Fatalf("empty run summary = %q, want it to state 未执行任何巡检项", run.Summary)
	}
	if !strings.Contains(run.Summary, "请求未指定任何可执行的 Skill") {
		t.Fatalf("empty run summary = %q, want the decidable cause", run.Summary)
	}
	if strings.Contains(run.Summary, "全部 0 项巡检通过") {
		t.Fatalf("empty run summary = %q, still claims a green pass", run.Summary)
	}

	// The runner-level path keeps its own reason when a stored plan (for example a
	// schedule written before request-time validation) selects an unknown Skill.
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	direct, _, err := f.hub.ExecuteMonitorRun(context.Background(), monitorRunPlan{
		TriggerKind: "manual",
		Auth:        auth,
		ScopeMode:   "targets",
		Targets:     []monitorTargetRef{{Cluster: "dev", Instance: "local"}},
		Skills:      []string{"no-such-skill"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.Status != "failed" || !strings.Contains(direct.Summary, "请求的 Skill no-such-skill 未解析到可执行包") {
		t.Fatalf("unresolved-skill run = %+v, want failed naming the unresolved Skill", direct)
	}
}
