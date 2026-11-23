package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformSkillsCRUDPermissionsSyncAndInspectionDriver(t *testing.T) {
	f := monitorEngineFixture(t)
	operator := f.user(t, "skill-op", ScopeGrant{Cluster: "dev", Instance: "local"})

	skillsRoot := t.TempDir()
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", skillsRoot)

	// 1. Built-in system skills are seeded automatically and readable by all users.
	listRaw := f.http(t, operator, "GET", "/v1/skills", "", 200)
	var listResp struct {
		Skills []platformSkillRecord `json:"skills"`
	}
	if err := json.Unmarshal(listRaw, &listResp); err != nil {
		t.Fatalf("decode skills list: %v", err)
	}
	if len(listResp.Skills) < 8 {
		t.Fatalf("expected >= 8 seeded builtin system skills, got %d", len(listResp.Skills))
	}
	var foundVM bool
	for _, sk := range listResp.Skills {
		if sk.Slug == "inspect-vm-host" && sk.Scope == "system" && sk.DriverMode == "builtin_strategy" && sk.StrategyID == "vm_host" {
			foundVM = true
			if sk.CanEdit || sk.CanDelete {
				t.Fatalf("non-admin should not have edit/delete permission on system skill: %+v", sk)
			}
		}
	}
	if !foundVM {
		t.Fatalf("missing seeded inspect-vm-host system skill")
	}

	// 2. Non-admin cannot modify or toggle a system skill (403).
	f.http(t, operator, "POST", "/v1/skills", `{"action":"toggle","slug":"inspect-vm-host","enabled":false}`, 403)
	f.http(t, operator, "POST", "/v1/skills", `{"action":"upsert","slug":"inspect-vm-host","skill_md":"# hack"}`, 403)

	// 3. Non-admin CAN upload a custom skill (including attached script file) and edit/delete their own.
	customPayload := `{
		"action": "upsert",
		"slug": "custom-nginx-audit",
		"category": "inspection",
		"skill_md": "---\nname: Nginx 专项巡检\ndescription: 检查 Nginx 配置文件与 worker 进程\n---\n\n# Nginx Audit\n只读检查配置。",
		"files": [{"path": "scripts/check.sh", "content": "#!/bin/sh\necho ok\n"}]
	}`
	upRaw := f.http(t, operator, "POST", "/v1/skills", customPayload, 200)
	var upResp struct {
		Skill platformSkillRecord `json:"skill"`
	}
	if err := json.Unmarshal(upRaw, &upResp); err != nil {
		t.Fatalf("decode upsert: %v", err)
	}
	if upResp.Skill.Scope != "custom" || upResp.Skill.Name != "Nginx 专项巡检" || upResp.Skill.FileCount != 1 {
		t.Fatalf("unexpected custom skill saved: %+v", upResp.Skill)
	}

	// Path traversal attachment must be rejected.
	f.http(t, operator, "POST", "/v1/skills", `{
		"action": "upsert",
		"slug": "bad-path-skill",
		"skill_md": "# test",
		"files": [{"path": "../escape.sh", "content": "nope"}]
	}`, 400)

	// Another operator cannot overwrite or delete skill-op's custom skill (403).
	otherOp := f.user(t, "other-op", ScopeGrant{Cluster: "dev", Instance: "local"})
	f.http(t, otherOp, "POST", "/v1/skills", `{"action":"delete","slug":"custom-nginx-audit"}`, 403)

	// Admin CAN edit a system skill and reset it back to builtin defaults.
	f.http(t, f.admin, "POST", "/v1/skills", `{"action":"upsert","slug":"inspect-vm-host","skill_md":"---\nname: 定制虚机巡检\ndescription: 管理员定制\n---\n# Custom VM"}`, 200)
	detailRaw := f.http(t, f.admin, "GET", "/v1/skills/inspect-vm-host", "", 200)
	if !strings.Contains(string(detailRaw), "定制虚机巡检") {
		t.Fatalf("expected edited system skill name, got: %s", string(detailRaw))
	}
	f.http(t, f.admin, "POST", "/v1/skills", `{"action":"reset_system","slug":"inspect-vm-host"}`, 200)
	resetRaw := f.http(t, f.admin, "GET", "/v1/skills/inspect-vm-host", "", 200)
	if !strings.Contains(string(resetRaw), "虚机与物理机系统巡检 Skill") {
		t.Fatalf("expected reset system skill name, got: %s", string(resetRaw))
	}

	// 4. Register an online Agent and test doops_skill_sync + Skill-driven inspection!
	inventoryTestAgent(t, f, "local")

	// Sync custom skill to online Agent and assert files materialized safely under skillsRoot.
	syncRaw := f.http(t, operator, "POST", "/v1/skills", `{"action":"sync","slug":"custom-nginx-audit","targets":[{"cluster":"dev","instance":"local"}]}`, 200)
	if !strings.Contains(string(syncRaw), `"status":"ok"`) {
		t.Fatalf("expected ok skill sync result, got: %s", string(syncRaw))
	}
	gotMD, err := os.ReadFile(filepath.Join(skillsRoot, "custom-nginx-audit", "SKILL.md"))
	if err != nil || !strings.Contains(string(gotMD), "Nginx 专项巡检") {
		t.Fatalf("expected SKILL.md materialized on agent, err=%v content=%q", err, string(gotMD))
	}
	gotScript, err := os.ReadFile(filepath.Join(skillsRoot, "custom-nginx-audit", "scripts", "check.sh"))
	if err != nil || !strings.Contains(string(gotScript), "echo ok") {
		t.Fatalf("expected attached script materialized on agent, err=%v", err)
	}

	// 5. Trigger Skill-driven inspection combining a system builtin_strategy skill AND the custom skill!
	runRaw := f.http(t, operator, "POST", "/v1/monitor/runs", `{
		"targets": [{"cluster": "dev", "instance": "local"}],
		"skills": ["inspect-vm-host", "custom-nginx-audit"]
	}`, 200)
	var runResp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(runRaw, &runResp); err != nil {
		t.Fatalf("decode skill-driven run: %v", err)
	}
	if runResp.Run.TotalAssets < 2 {
		t.Fatalf("expected both builtin vm skill and custom skill results, got %d items: %+v", len(runResp.Items), runResp.Items)
	}
	var sawVM, sawCustom bool
	for _, it := range runResp.Items {
		if it.Strategy == "vm_host" {
			sawVM = true
			if it.Evidence["skill_slug"] != "inspect-vm-host" {
				t.Fatalf("expected vm_host evidence skill_slug=inspect-vm-host, got %+v", it.Evidence)
			}
		}
		if it.Strategy == "skill:custom-nginx-audit" && it.CollectorMethod == "agent_skill" {
			sawCustom = true
			if it.Evidence["synced"] != true {
				t.Fatalf("expected custom skill synced=true, got %+v", it.Evidence)
			}
		}
	}
	if !sawVM || !sawCustom {
		t.Fatalf("expected both sawVM=%v and sawCustom=%v in items: %+v", sawVM, sawCustom, runResp.Items)
	}

	// 6. System skill cannot be deleted (409); owner can delete custom skill (200).
	f.http(t, f.admin, "POST", "/v1/skills", `{"action":"delete","slug":"inspect-vm-host"}`, 409)
	f.http(t, operator, "POST", "/v1/skills", `{"action":"delete","slug":"custom-nginx-audit"}`, 200)
}
