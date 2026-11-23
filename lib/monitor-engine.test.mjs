import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import {
  buildRunReportCSV,
  describeMonitorStatus,
  fetchMonitorRunDetail,
  fetchMonitorSchedules,
  formatBytesCompact,
  formatIntervalLabel,
  formatPercent,
  formatRateBytes,
  mutateMonitorSchedule,
  triggerMonitorRun,
} from "./monitor-engine.ts"
import { toInspectionViewerProps } from "./monitor-metrics.ts"
import { analyzeInspectionWithAi, buildCustomReportMarkdown } from "./inspection-report.ts"
import {
  deletePlatformSkill,
  fetchPlatformSkillDetail,
  fetchPlatformSkills,
  parseSkillFrontmatter,
  resetSystemSkill,
  slugifySkillName,
  syncSkillsToAgents,
  togglePlatformSkill,
  upsertPlatformSkill,
} from "./skills-registry.ts"

test("Given monitor engine statuses, When described, Then labels and badge variants map cleanly", () => {
  assert.deepEqual(describeMonitorStatus("pass"), { label: "正常", variant: "success" })
  assert.deepEqual(describeMonitorStatus("warning"), { label: "告警", variant: "warning" })
  assert.deepEqual(describeMonitorStatus("partial"), { label: "部分完成", variant: "warning" })
  assert.deepEqual(describeMonitorStatus("critical"), { label: "严重", variant: "danger" })
  assert.deepEqual(describeMonitorStatus("offline"), { label: "离线", variant: "default" })
  assert.deepEqual(describeMonitorStatus("skipped"), { label: "跳过", variant: "outline" })
})

test("Given intervals, bytes, rates and percentages, When formatted, Then human-readable values are deterministic", () => {
  assert.equal(formatIntervalLabel(300), "每 5 分钟")
  assert.equal(formatIntervalLabel(3600), "每 1 小时")
  assert.equal(formatIntervalLabel(86400), "每天")
  assert.equal(formatIntervalLabel(604800), "每周")
  assert.equal(formatBytesCompact(0), "0 B")
  assert.equal(formatBytesCompact(2048), "2.00 KiB")
  assert.equal(formatRateBytes(1024), "1.00 KiB/s")
  assert.equal(formatPercent(42.34), "42.3%")
  assert.equal(formatPercent(null), "—")
})

test("Given a run detail payload, When exported as CSV, Then BOM, check rows and offline rows are serialized safely", () => {
  const csv = buildRunReportCSV({
    run: {
      id: "run_1",
      trigger_kind: "scheduled",
      status: "partial",
      total_targets: 2,
      total_assets: 2,
      passed_checks: 1,
      warning_checks: 0,
      failed_checks: 1,
      skipped_checks: 0,
      offline_targets: 1,
      metrics_written: 10,
      started_at_ms: 1,
      finished_at_ms: 2,
      duration_ms: 1,
    },
    items: [
      {
        id: 1,
        run_id: "run_1",
        cluster: "prod",
        instance: "web-01",
        asset_id: "srv-1",
        asset_name: "Web 01",
        asset_kind: "vm",
        strategy: "vm_host",
        collector_method: "agent_local",
        status: "pass",
        started_at_ms: 1,
        finished_at_ms: 2,
        duration_ms: 1,
        metrics: {},
        checks: [{ id: "host.uptime", name: "运行与重启", category: "availability", status: "pass", value: "3600s", threshold: ">=300s", detail: 'ok "quoted"' }],
      },
      {
        id: 2,
        run_id: "run_1",
        cluster: "prod",
        instance: "off-02",
        asset_id: "off-02",
        asset_name: "Off 02",
        asset_kind: "vm",
        strategy: "vm_host",
        collector_method: "agent_local",
        status: "offline",
        reason: "target_offline",
        started_at_ms: 1,
        finished_at_ms: 2,
        duration_ms: 1,
        metrics: {},
        checks: [],
      },
    ],
  })
  assert.ok(csv.startsWith("\uFEFFrun_id,"))
  assert.ok(csv.includes('"host.uptime"'))
  assert.ok(csv.includes('"ok ""quoted"""'))
  assert.ok(csv.includes('"target_offline"'))
})

test("Given the retired strategies selector, When a schedule and a run are created from skills, Then requests and responses carry skills only", async () => {
  const session = { gateway: "demo", token: "demo", demo: true, isAdmin: true, username: "admin" }
  const created = await mutateMonitorSchedule(session, {
    action: "create",
    name: "Skill-first 巡检",
    skills: ["inspect-vm-host"],
    interval_seconds: 300,
  })
  assert.ok(created.schedule)
  assert.deepEqual(created.schedule.skills, ["inspect-vm-host"])
  assert.equal(Object.prototype.hasOwnProperty.call(created.schedule, "strategies"), false)

  const schedules = await fetchMonitorSchedules(session)
  assert.equal(schedules.schedules.some((s) => Object.prototype.hasOwnProperty.call(s, "strategies")), false)

  const run = await triggerMonitorRun(session, { scope_mode: "all_online", skills: ["inspect-vm-host"] })
  assert.ok(run.items.length > 0)
  assert.ok(run.items.every((i) => i.evidence?.skill_slug === "inspect-vm-host" || String(i.strategy).startsWith("skill:")))
  assert.equal(Object.prototype.hasOwnProperty.call(run, "strategies"), false)
})

test("Given platform skills (system + custom), When uploaded, edited, synced and driving inspection, Then lifecycle and permissions hold end-to-end", async () => {
  assert.equal(slugifySkillName("My_Custom Skill.zip"), "my-custom-skill")
  assert.deepEqual(
    parseSkillFrontmatter("---\nname: nginx-audit\ndescription: \"Check nginx\"\n---\n# Body"),
    { name: "nginx-audit", description: "Check nginx" },
  )

  const adminSession = { gateway: "demo", token: "demo", demo: true, isAdmin: true, username: "admin" }
  const userSession = { gateway: "demo", token: "demo", demo: true, isAdmin: false, username: "ops-zhang" }

  const initial = await fetchPlatformSkills(adminSession)
  assert.ok(initial.skills.length >= 8, "should pre-seed at least 8 built-in system skills")
  assert.ok(initial.skills.some((s) => s.slug === "inspect-vm-host" && s.scope === "system"))

  // Ordinary user cannot delete or edit a system skill
  await assert.rejects(() => deletePlatformSkill(userSession, "inspect-vm-host"))
  await assert.rejects(() =>
    upsertPlatformSkill(userSession, { slug: "inspect-vm-host", skill_md: "# hacked" }),
  )

  // Admin can customize a system skill and reset it back to default
  const editedSys = await upsertPlatformSkill(adminSession, {
    slug: "inspect-vm-host",
    name: "自定义主机系统巡检 Skill",
    skill_md: "---\nname: inspect-vm-host\ndescription: customized\n---\n# Custom",
  })
  assert.equal(editedSys.skill.name, "自定义主机系统巡检 Skill")
  assert.equal(editedSys.skill.scope, "system")
  const resetSys = await resetSystemSkill(adminSession, "inspect-vm-host")
  assert.equal(resetSys.skill.name, "虚机与物理机系统巡检 Skill")

  // Ordinary user can upload their own custom skill with attached scripts
  const uploaded = await upsertPlatformSkill(userSession, {
    slug: "custom-nginx-audit",
    name: "Nginx 配置安全巡检",
    description: "只读检查 nginx 配置与安全响应头",
    category: "security",
    skill_md: "---\nname: custom-nginx-audit\ndescription: nginx\n---\n## 语法核查\n## TLS 证书",
    files: [{ path: "scripts/check.sh", content: "#!/usr/bin/env sh\nnginx -t\n" }],
  })
  assert.equal(uploaded.skill.scope, "custom")
  assert.equal(uploaded.skill.file_count, 1)

  const detail = await fetchPlatformSkillDetail(userSession, "custom-nginx-audit")
  assert.equal(detail.skill.files?.length, 1)

  // Sync custom skill to online agents
  const synced = await syncSkillsToAgents(userSession, { slug: "custom-nginx-audit" })
  assert.ok(synced.results.some((r) => r.status === "ok" && r.synced_count === 1))

  // Trigger Skill-driven inspection combining a built-in system skill + the uploaded custom skill
  const skillRun = await triggerMonitorRun(userSession, {
    scope_mode: "all_online",
    skills: ["inspect-vm-host", "custom-nginx-audit"],
  })
  const runDetail = await fetchMonitorRunDetail(userSession, skillRun.run.id)
  assert.ok(runDetail.items.some((i) => i.evidence?.skill_slug === "inspect-vm-host"))
  assert.ok(runDetail.items.some((i) => i.strategy === "skill:custom-nginx-audit" && i.collector_method === "agent_skill"))

  // Toggle and delete custom skill cleanly
  const disabled = await togglePlatformSkill(userSession, "custom-nginx-audit", false)
  assert.equal(disabled.skill.enabled, false)
  const deleted = await deletePlatformSkill(userSession, "custom-nginx-audit")
  assert.equal(deleted.deleted, "custom-nginx-audit")
})

test("Given real inspection failures and prompt/command runs, When summarized or reported, Then zero fake demo strings exist and Markdown+JSON artifacts persist", async () => {
  const realDetail = {
    run: {
      id: "run_real_1",
      trigger_kind: "manual",
      status: "critical",
      total_targets: 1,
      total_assets: 1,
      passed_checks: 0,
      warning_checks: 0,
      failed_checks: 1,
      skipped_checks: 0,
      offline_targets: 0,
      metrics_written: 1,
      started_at_ms: 1,
      finished_at_ms: 2,
      duration_ms: 1,
    },
    items: [
      {
        id: 1,
        run_id: "run_real_1",
        cluster: "local",
        instance: "dev-agent",
        asset_id: "local-k8s",
        asset_name: "local Kubernetes",
        asset_kind: "k8s",
        strategy: "k8s_cluster",
        collector_method: "k8s_api",
        status: "critical",
        started_at_ms: 1,
        finished_at_ms: 2,
        duration_ms: 1,
        metrics: {},
        checks: [
          {
            id: "k8s.nodes",
            name: "K8s 节点健康与压力",
            category: "availability",
            status: "critical",
            detail: "dial tcp 127.0.0.1:8080: connection refused",
          },
        ],
      },
    ],
  }
  const summary = analyzeInspectionWithAi(realDetail, null)
  assert.ok(!summary.model.includes("浙音大模型"))
  assert.ok(!summary.headline.includes("浙音大模型"))
  assert.ok(!summary.findings[0].rootCause.includes("83.8%"))
  assert.ok(!summary.findings[0].businessImpact.includes("全校教务管理系统"))
  assert.ok(summary.findings[0].rootCause.includes("connection refused"))

  const md = buildCustomReportMarkdown(
    { title: "真实态势报告", period: "7d", sections: ["inspection_vuln", "security_events", "ai_remediation"] },
    null,
    realDetail,
    summary,
  )
  assert.ok(!md.includes("浙音大模型"))
  assert.ok(!md.includes("安全巡检 MCP"))
  assert.ok(!md.includes("阻断 3 次"))

  // Prompt + Command run produces persisted Markdown + JSON artifact.
  const demoSession = { gateway: "demo", token: "demo", demo: true, isAdmin: true, username: "admin" }
  const cmdRun = await triggerMonitorRun(demoSession, {
    scope_mode: "all_online",
    prompt: "检查服务端口并输出 JSON 产物",
    command: "ss -tlnp",
  })
  const cmdDetail = await fetchMonitorRunDetail(demoSession, cmdRun.run.id)
  assert.equal(cmdDetail.run.prompt, "检查服务端口并输出 JSON 产物")
  assert.equal(cmdDetail.run.command, "ss -tlnp")
  assert.equal(cmdDetail.items.length, 1)
  assert.equal(cmdDetail.items[0].artifact_format, "markdown+json")
  assert.ok(cmdDetail.items[0].artifact_markdown?.includes("巡检产物摘要"))
  assert.ok(Array.isArray(cmdDetail.items[0].artifact_json?.open_ports))
})

test("Given a run detail item, When mapped to the artifact viewer, Then diff and trend target are carried through", async () => {
  const demoSession = { gateway: "demo", token: "demo", demo: true, isAdmin: true, username: "admin" }
  const first = await triggerMonitorRun(demoSession, { skills: ["inspect-vm-host"] })
  const second = await triggerMonitorRun(demoSession, { skills: ["inspect-vm-host"] })
  const detail = await fetchMonitorRunDetail(demoSession, second.run.id)
  const item = detail.items.find((it) => it.strategy === "skill:inspect-vm-host")
  assert.ok(item, "demo 详情应包含 inspect-vm-host 的 skill item")
  assert.ok(item.diff, "详情接口应为第二次运行返回跨次 diff")
  assert.equal(item.diff.previous_run_id, first.run.id)
  assert.ok(item.diff.metric_deltas.length > 0, "跨次 diff 应包含指标增减")

  const props = toInspectionViewerProps(item, demoSession, "inspect-vm-host")
  assert.equal(props.diff, item.diff)
  assert.equal(props.session, demoSession)
  assert.equal(props.trendSkill, "inspect-vm-host")
  assert.equal(props.trendCluster, item.cluster)
  assert.equal(props.trendInstance, item.instance)

  // diff: null 表示后端明确回答"无前次基线"；缺 diff 键表示该响应没有 diff 概念（创建接口）。
  assert.equal(toInspectionViewerProps({ ...item, diff: null }, demoSession, "inspect-vm-host").diff, null)
  assert.equal(
    toInspectionViewerProps({ cluster: item.cluster, instance: item.instance }, demoSession, "inspect-vm-host").diff,
    undefined,
  )

  // 趋势取数目标不齐时不传 session/skill，viewer 据此不渲染趋势区块。
  const noTarget = toInspectionViewerProps(item, demoSession, null)
  assert.equal(noTarget.session, undefined)
  assert.equal(noTarget.trendSkill, undefined)
  assert.equal(toInspectionViewerProps(item, null, "inspect-vm-host").session, undefined)

  // 接线锁：执行记录详情页的 viewer 调用必须把映射铺开，删掉即红。
  const panelSource = readFileSync(new URL("../components/inspection-center-panel.tsx", import.meta.url), "utf8")
  const callIndex = panelSource.indexOf("<InspectionArtifactViewer")
  assert.ok(callIndex >= 0, "详情页应渲染 InspectionArtifactViewer")
  assert.ok(
    panelSource.slice(callIndex, callIndex + 1400).includes("{...toInspectionViewerProps(it, session, drivenSkillSlug)}"),
    "详情页的 InspectionArtifactViewer 必须铺开 toInspectionViewerProps(it, session, drivenSkillSlug)",
  )
})

test("Given item artifact_state, When mapped to the viewer props, Then present/missing is carried and legacy stays absent", () => {
  const demoSession = { gateway: "demo", token: "demo", demo: true, isAdmin: true, username: "admin" }
  const missing = toInspectionViewerProps(
    { cluster: "prod-cn", instance: "web-01", artifact_state: "missing", artifact_truncated: true },
    demoSession,
    "inspect-vm-host",
  )
  assert.equal(missing.artifactState, "missing")
  // missing 下后端不会给 truncated；即使硬塞也不提升，这里只验证透传。
  assert.equal(missing.artifactTruncated, true)

  const present = toInspectionViewerProps(
    { cluster: "prod-cn", instance: "web-01", artifact_state: "present", artifact_truncated: true },
    demoSession,
    "inspect-vm-host",
  )
  assert.equal(present.artifactState, "present")
  assert.equal(present.artifactTruncated, true)

  // 老响应没有该字段：映射保持 undefined，viewer 按 present 处理。
  const legacy = toInspectionViewerProps({ cluster: "prod-cn", instance: "web-01" }, demoSession, "inspect-vm-host")
  assert.equal(legacy.artifactState, undefined)
  assert.equal(legacy.artifactTruncated, undefined)

  // 趋势目标不齐的早退分支也必须带上产物状态，不能丢。
  const noTrend = toInspectionViewerProps({ artifact_state: "missing", artifact_truncated: false }, null, null)
  assert.equal(noTrend.artifactState, "missing")
})

test("Given the artifact viewer source, When inspected, Then artifact_state is decoded into the missing notice (wiring lock)", () => {
  const viewerSource = readFileSync(new URL("../components/inspection-artifact-viewer.tsx", import.meta.url), "utf8")
  assert.ok(viewerSource.includes("describeArtifactState"), "viewer 必须消费 describeArtifactState")
  assert.ok(
    viewerSource.includes("describeArtifactState(artifactState, artifactTruncated)"),
    "viewer 必须用 artifactState + artifactTruncated 计算展示模型",
  )
  assert.ok(viewerSource.includes("artifactDisplay.missing"), "viewer 必须按 missing 替换正文区")
  assert.ok(viewerSource.includes("artifactDisplay.notice"), "viewer 必须渲染 missing 提示文案")
})
