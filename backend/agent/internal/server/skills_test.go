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
		if sk.Slug == "inspect-vm-host" && sk.Scope == "system" {
			foundVM = true
			if sk.CanEdit || sk.CanDelete {
				t.Fatalf("non-admin should not have edit/delete permission on system skill: %+v", sk)
			}
		}
	}
	if !foundVM {
		t.Fatalf("missing seeded inspect-vm-host system skill")
	}

	// System Skill detail is package-driven: it exposes the derived entrypoint /
	// exec_mode and never a builtin_strategy / strategy_id side channel (D9).
	vmDetailRaw := f.http(t, operator, "GET", "/v1/skills/inspect-vm-host", "", 200)
	if strings.Contains(string(vmDetailRaw), "driver_mode") || strings.Contains(string(vmDetailRaw), "strategy_id") {
		t.Fatalf("system skill detail still exposes dead strategy fields: %s", string(vmDetailRaw))
	}
	var vmDetail struct {
		Skill platformSkillRecord `json:"skill"`
	}
	if err := json.Unmarshal(vmDetailRaw, &vmDetail); err != nil {
		t.Fatalf("decode vm-host detail: %v", err)
	}
	if vmDetail.Skill.Entrypoint != "scripts/inspect.sh" || vmDetail.Skill.ExecMode != skillExecModeAuto {
		t.Fatalf("expected package-derived entrypoint/exec_mode, got %+v", vmDetail.Skill)
	}

	// 2. Non-admin cannot modify or toggle a system skill (403).
	f.http(t, operator, "POST", "/v1/skills", `{"action":"toggle","slug":"inspect-vm-host","enabled":false}`, 403)
	f.http(t, operator, "POST", "/v1/skills", `{"action":"upsert","slug":"inspect-vm-host","skill_md":"# hack"}`, 403)

	// 3. Non-admin CAN upload a custom skill (including attached script file) and edit/delete their own.
	customPayload := `{
		"action": "upsert",
		"slug": "custom-nginx-audit",
		"category": "inspection",
		"skill_md": "---\nname: Nginx 专项巡检\ndescription: 检查 Nginx 配置文件与 worker 进程\nentrypoint: scripts/check.sh\n---\n\n# Nginx Audit\n只读检查配置。",
		"files": [{"path": "scripts/check.sh", "content": "#!/bin/sh\necho ok\n"}]
	}`
	upRaw := f.http(t, operator, "POST", "/v1/skills", customPayload, 200)
	var upResp struct {
		Skill platformSkillRecord `json:"skill"`
	}
	if err := json.Unmarshal(upRaw, &upResp); err != nil {
		t.Fatalf("decode upsert: %v", err)
	}
	if upResp.Skill.Scope != "custom" || upResp.Skill.Name != "Nginx 专项巡检" || upResp.Skill.FileCount != 1 || upResp.Skill.Entrypoint != "scripts/check.sh" {
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

	// 5. Skill-driven inspection dispatches purely by the package contract: both
	// the builtin script Skill and the custom script Skill run their entrypoint.
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
		switch it.Strategy {
		case "skill:inspect-vm-host":
			sawVM = true
			if it.CollectorMethod != "skill_script" || it.Evidence["entrypoint"] != "scripts/inspect.sh" || it.Evidence["synced"] != true {
				t.Fatalf("expected vm-host script dispatch, got method=%s evidence=%+v", it.CollectorMethod, it.Evidence)
			}
		case "skill:custom-nginx-audit":
			sawCustom = true
			if it.CollectorMethod != "skill_script" || it.Evidence["entrypoint"] != "scripts/check.sh" || it.Evidence["synced"] != true {
				t.Fatalf("expected custom skill script dispatch, got method=%s evidence=%+v", it.CollectorMethod, it.Evidence)
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

func TestInspectionPromptCommandAndArbitraryArtifacts(t *testing.T) {
	// 1. Unit tests for extractInspectionArtifact with arbitrary schemas.
	pureJSON := extractInspectionArtifact(`{"host":"node-1","open_ports":[{"port":443,"proto":"tcp"}],"status":"ok"}`, "端口检查", "network")
	if pureJSON.Format != "json" || pureJSON.Status != "pass" || !strings.Contains(string(pureJSON.JSON), `"open_ports"`) {
		t.Fatalf("unexpected pureJSON parse: %+v", pureJSON)
	}

	mdAndJSON := extractInspectionArtifact("# 巡检结论\n\n发现 1 个异常端口。\n\n```json\n{\"status\":\"warning\",\"summary\":\"发现调试端口\",\"ports\":[{\"port\":8080,\"state\":\"LISTEN\"}]}\n```\n", "自定义检查", "ops")
	if mdAndJSON.Format != "markdown+json" || mdAndJSON.Status != "warning" || mdAndJSON.Summary != "发现调试端口" || !strings.Contains(string(mdAndJSON.JSON), `"port":8080`) {
		t.Fatalf("unexpected markdown+json parse: %+v", mdAndJSON)
	}

	// 2. End-to-end Gateway -> Agent direct command inspection & schedule persistence.
	f := monitorEngineFixture(t)
	operator := f.user(t, "cmd-op", ScopeGrant{Cluster: "dev", Instance: "local"})
	inventoryTestAgent(t, f, "local")

	skillsRoot := t.TempDir()
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", skillsRoot)

	// Run an arbitrary shell command producing Markdown + custom JSON payload.
	cmdBody := "printf '## 环境巡检摘要\\n\\n一切正常。\\n\\n```json\\n{\"status\":\"pass\",\"summary\":\"自定义命令采集成功\",\"custom_score\":98,\"rows\":[{\"item\":\"kernel\",\"ok\":true}]}\\n```\\n'"
	reqJSON, _ := json.Marshal(map[string]any{
		"targets": []map[string]string{{"cluster": "dev", "instance": "local"}},
		"prompt":  "检查主机环境与自定义结构化指标",
		"command": cmdBody,
	})
	runRaw := f.http(t, operator, "POST", "/v1/monitor/runs", string(reqJSON), 200)
	var runResp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(runRaw, &runResp); err != nil {
		t.Fatalf("decode command run: %v", err)
	}
	if runResp.Run.Prompt != "检查主机环境与自定义结构化指标" || runResp.Run.Command == "" {
		t.Fatalf("expected prompt and command persisted on run, got %+v", runResp.Run)
	}
	if len(runResp.Items) != 1 {
		t.Fatalf("expected exactly 1 item (a command run must not fan out into Skill items), got %d: %+v", len(runResp.Items), runResp.Items)
	}
	it := runResp.Items[0]
	if it.CollectorMethod != "shell_command" || it.ArtifactFormat != "markdown+json" {
		t.Fatalf("expected shell_command markdown+json artifact, got method=%s format=%s raw=%q", it.CollectorMethod, it.ArtifactFormat, it.RawOutput)
	}
	if !strings.Contains(string(it.ArtifactJSON), `"custom_score":98`) || !strings.Contains(it.ArtifactMarkdown, "环境巡检摘要") {
		t.Fatalf("expected arbitrary JSON and Markdown preserved, got json=%s md=%q", string(it.ArtifactJSON), it.ArtifactMarkdown)
	}

	// Verify GET /v1/monitor/runs/<id> returns persisted prompt, command, and artifacts.
	detailRaw := f.http(t, operator, "GET", "/v1/monitor/runs/"+runResp.Run.ID, "", 200)
	var detailResp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(detailRaw, &detailResp); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detailResp.Run.Prompt != "检查主机环境与自定义结构化指标" || len(detailResp.Items) != 1 || detailResp.Items[0].ArtifactFormat != "markdown+json" {
		t.Fatalf("persisted detail mismatch: run=%+v items=%+v", detailResp.Run, detailResp.Items)
	}

	// 3. Schedule with Prompt + Command persists cleanly and does not attach Skill items.
	schedRaw := f.http(t, f.admin, "POST", "/v1/monitor/schedules", `{
		"action": "create",
		"name": "自定义命令与提示词巡检",
		"scope_mode": "targets",
		"targets": [{"cluster": "dev", "instance": "local"}],
		"prompt": "每日检查服务状态并输出 JSON",
		"command": "printf '{\"status\":\"ok\",\"service\":\"demo\"}'",
		"interval_seconds": 300
	}`, 200)
	var schedResp struct {
		Schedule monitorScheduleRecord `json:"schedule"`
	}
	if err := json.Unmarshal(schedRaw, &schedResp); err != nil {
		t.Fatalf("decode schedule: %v", err)
	}
	if schedResp.Schedule.Prompt != "每日检查服务状态并输出 JSON" || schedResp.Schedule.Command == "" || len(schedResp.Schedule.Skills) != 0 {
		t.Fatalf("unexpected schedule saved: %+v", schedResp.Schedule)
	}

	// Triggering that schedule executes the saved command and stores pure JSON artifact.
	sRunRaw := f.http(t, operator, "POST", "/v1/monitor/runs", `{"schedule_id":"`+schedResp.Schedule.ID+`"}`, 200)
	var sRunResp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(sRunRaw, &sRunResp); err != nil {
		t.Fatalf("decode scheduled run: %v", err)
	}
	if len(sRunResp.Items) != 1 || sRunResp.Items[0].ArtifactFormat != "json" || !strings.Contains(string(sRunResp.Items[0].ArtifactJSON), `"service":"demo"`) {
		t.Fatalf("expected pure JSON artifact from scheduled command, got %+v", sRunResp.Items)
	}
}
