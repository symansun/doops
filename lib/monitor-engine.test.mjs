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
import * as inspectionRunGroups from "./inspection-run-groups.ts"
import {
  TASK_GROUP_KIND_LABELS,
  describeInspectionRunTitle,
  filterInspectionRuns,
  filterInspectionTaskGroups,
  groupInspectionRuns,
  mergeInspectionRuns,
  statusDotToneClass,
} from "./inspection-run-groups.ts"
import * as automationHelpers from "./automation.ts"
import * as monitorEngine from "./monitor-engine.ts"
import { buttonNamed, componentHarness, componentStubs, elementText } from "./component-test-harness.mjs"
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

  // 接线锁：共享执行详情页的 viewer 调用必须把映射铺开，删掉即红。
  const panelSource = readFileSync(new URL("../components/monitor-run-detail.tsx", import.meta.url), "utf8")
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

function makeRun(overrides = {}) {
  return {
    id: "run_default",
    schedule_id: "",
    schedule_name: "",
    trigger_kind: "manual",
    triggered_by: "admin",
    status: "success",
    skills: [],
    prompt: "",
    command: "",
    total_targets: 2,
    total_assets: 2,
    passed_checks: 6,
    warning_checks: 0,
    failed_checks: 0,
    skipped_checks: 0,
    offline_targets: 0,
    metrics_written: 12,
    started_at_ms: 1_700_000_000_000,
    finished_at_ms: 1_700_000_010_000,
    duration_ms: 10_000,
    summary: "",
    ...overrides,
  }
}

function makeSchedule(overrides = {}) {
  return {
    id: "sched_default",
    name: "默认巡检计划",
    description: "",
    enabled: true,
    scope_mode: "all_online",
    targets: [],
    tags: [],
    skills: [],
    prompt: "",
    command: "",
    thresholds: {},
    interval_seconds: 3600,
    next_run_at_ms: 1_700_003_600_000,
    last_run_at_ms: 1_700_000_000_000,
    last_run_id: "",
    last_status: "",
    created_at: "2026-11-26T00:00:00Z",
    updated_at: "2026-11-26T00:00:00Z",
    ...overrides,
  }
}

test("Given heterogeneous inspection runs and schedules, When grouped by task, Then schedules, skills, commands, prompts and unloaded history aggregate accurately", () => {
  const schedules = [
    makeSchedule({
      id: "sched_mining",
      name: "全集群挖矿行为定时安全巡检",
      skills: ["inspect-crypto-mining"],
      interval_seconds: 21600,
      last_run_id: "run_m3",
      last_run_at_ms: 3000,
      last_status: "partial",
    }),
    makeSchedule({
      id: "sched_pushed_out",
      name: "每日本地基线巡检",
      skills: ["inspect-vm-host"],
      interval_seconds: 86400,
      last_run_id: "run_old_1",
      last_run_at_ms: 1500,
      last_status: "critical",
    }),
    makeSchedule({
      id: "sched_never_ran",
      name: "尚未执行过的计划",
      last_run_id: "",
      last_run_at_ms: 0,
      last_status: "",
    }),
  ]

  const runs = [
    // Renamed schedule: old run still has old schedule_name, new runs have new name; mixed triggers + running run
    makeRun({ id: "run_m1", schedule_id: "sched_mining", schedule_name: "旧名挖矿巡检", trigger_kind: "scheduled", status: "success", duration_ms: 20_000, started_at_ms: 1000 }),
    makeRun({ id: "run_m2", schedule_id: "sched_mining", schedule_name: "全集群挖矿行为定时安全巡检", trigger_kind: "manual", status: "critical", failed_checks: 4, duration_ms: 40_000, started_at_ms: 2000 }),
    makeRun({ id: "run_m3", schedule_id: "sched_mining", schedule_name: "全集群挖矿行为定时安全巡检", trigger_kind: "scheduled", status: "partial", failed_checks: 2, offline_targets: 1, duration_ms: 60_000, started_at_ms: 3000 }),
    makeRun({ id: "run_m4", schedule_id: "sched_mining", schedule_name: "全集群挖矿行为定时安全巡检", trigger_kind: "retest", status: "running", duration_ms: 0, started_at_ms: 3500 }),
    // Deleted schedule
    makeRun({ id: "run_del1", schedule_id: "sched_gone", schedule_name: "已删除的夜间巡检", trigger_kind: "scheduled", status: "warning", duration_ms: 5_000, started_at_ms: 2500 }),
    // Ad-hoc skill runs (with and without prompt) fold into the same skill group
    makeRun({ id: "run_sk1", skills: ["inspect-crypto-mining"], prompt: "检查 GPU 进程", status: "success", duration_ms: 12_000, started_at_ms: 2800 }),
    makeRun({ id: "run_sk2", skills: ["inspect-crypto-mining"], prompt: "", status: "warning", duration_ms: 18_000, started_at_ms: 2900 }),
    // Direct command runs normalize whitespace
    makeRun({ id: "run_cmd1", command: "df   -h", status: "success", duration_ms: 1_000, started_at_ms: 1800 }),
    makeRun({ id: "run_cmd2", command: "df -h\n", status: "failed", duration_ms: 3_000, started_at_ms: 1900 }),
    // Pure prompt runs group by first line
    makeRun({ id: "run_p1", prompt: "检查 443 端口证书\n输出 JSON", status: "success", duration_ms: 4_000, started_at_ms: 1700 }),
    makeRun({ id: "run_p2", prompt: "检查 443 端口证书\n附带摘要", status: "success", duration_ms: 6_000, started_at_ms: 1750 }),
    // Duplicate run ID should be deduplicated
    makeRun({ id: "run_p2", prompt: "检查 443 端口证书\n附带摘要", status: "success", duration_ms: 6_000, started_at_ms: 1750 }),
  ]

  const skillNameBySlug = new Map([["inspect-crypto-mining", "集群与主机挖矿行为安全巡检 Skill"]])
  const groups = groupInspectionRuns(runs, { schedules, skillNameBySlug })

  assert.equal(groups.length, 6, "never-ran schedule is omitted; 5 active buckets + 1 pushed-out schedule remain")
  assert.deepEqual(
    groups.map((g) => g.key),
    [
      "schedule:sched_mining",
      "skill:inspect-crypto-mining",
      "schedule:sched_gone",
      "command:df -h",
      "prompt:检查 443 端口证书",
      "schedule:sched_pushed_out",
    ],
    "groups are ordered by latestStartedAtMs descending",
  )

  const mining = groups[0]
  assert.equal(mining.kind, "schedule")
  assert.equal(mining.title, "全集群挖矿行为定时安全巡检", "renamed schedule uses current schedule name")
  assert.equal(mining.scheduleDeleted, false)
  assert.equal(mining.runCount, 4)
  assert.deepEqual(mining.runs.map((r) => r.id), ["run_m4", "run_m3", "run_m2", "run_m1"])
  assert.deepEqual(mining.recentTimeline.map((r) => r.id), ["run_m1", "run_m2", "run_m3", "run_m4"], "timeline is oldest to newest")
  assert.equal(mining.latestStatus, "running")
  assert.equal(mining.statusCounts.running, 1)
  assert.equal(mining.statusCounts.success, 1)
  assert.equal(mining.statusCounts.critical, 1)
  assert.equal(mining.statusCounts.partial, 1)
  assert.equal(mining.abnormalCount, 2)
  assert.equal(mining.successRatePct, 33, "success rate divides by finished runs (1/3 = 33%), excluding running")
  assert.equal(mining.avgDurationMs, 40_000, "average duration excludes duration_ms=0 running entries")
  assert.deepEqual(mining.triggerCounts, { scheduled: 2, manual: 1, retest: 1, other: 0 })

  const skillGroup = groups[1]
  assert.equal(skillGroup.kind, "skill")
  assert.equal(skillGroup.title, "集群与主机挖矿行为安全巡检 Skill")
  assert.equal(skillGroup.runCount, 2)
  assert.equal(skillGroup.successRatePct, 50)
  assert.equal(skillGroup.avgDurationMs, 15_000)

  const deletedSched = groups[2]
  assert.equal(deletedSched.kind, "schedule")
  assert.equal(deletedSched.scheduleDeleted, true)
  assert.equal(deletedSched.title, "已删除的夜间巡检")

  const pushedOut = groups[5]
  assert.equal(pushedOut.hasUnloadedHistory, true)
  assert.equal(pushedOut.runCount, 0)
  assert.equal(pushedOut.latestRunId, "run_old_1")
  assert.equal(pushedOut.latestStatus, "critical")

  // Status filter applies consistently to both loaded runs and unloaded schedule shells
  const criticalOnly = groupInspectionRuns(runs, { schedules, skillNameBySlug, statusFilter: "critical" })
  assert.deepEqual(criticalOnly.map((g) => g.key), ["schedule:sched_mining", "schedule:sched_pushed_out"])
  assert.equal(criticalOnly[0].runCount, 1)
  assert.equal(criticalOnly[0].runs[0].id, "run_m2")

  // Search filters work in both grouped and flat modes
  assert.deepEqual(
    filterInspectionTaskGroups(groups, "挖矿", skillNameBySlug).map((g) => g.key),
    ["schedule:sched_mining", "skill:inspect-crypto-mining"],
  )
  assert.deepEqual(filterInspectionTaskGroups(groups, "run_cmd2", skillNameBySlug).map((g) => g.key), ["command:df -h"])
  assert.equal(filterInspectionRuns(runs, "443").length, 3)

  // Merging global + per-schedule history deduplicates and keeps fresher record
  const merged = mergeInspectionRuns(
    [makeRun({ id: "run_1", status: "running", finished_at_ms: 0, started_at_ms: 100 })],
    [[makeRun({ id: "run_1", status: "success", finished_at_ms: 200, started_at_ms: 100 }), makeRun({ id: "run_0", status: "warning", started_at_ms: 50 })]],
  )
  assert.deepEqual(merged.map((r) => [r.id, r.status]), [["run_1", "success"], ["run_0", "warning"]])

  // Recent timeline caps at 10 runs oldest -> newest
  const manyRuns = Array.from({ length: 14 }, (_, idx) =>
    makeRun({ id: `run_${String(idx + 1).padStart(2, "0")}`, schedule_id: "sched_mining", started_at_ms: (idx + 1) * 1000 }),
  )
  const capped = groupInspectionRuns(manyRuns, { schedules })[0]
  assert.equal(capped.recentTimeline.length, 10)
  assert.equal(capped.recentTimeline[0].id, "run_05")
  assert.equal(capped.recentTimeline[9].id, "run_14")

  // Semantic token guard: status dots use only design tokens
  for (const s of ["success", "warning", "partial", "critical", "failed", "running", "offline"]) {
    const cls = statusDotToneClass(s)
    assert.ok(!/(amber|emerald|red|slate|green|yellow)-/.test(cls), `statusDotToneClass(${s}) must not use raw palette class: ${cls}`)
  }
  assert.equal(TASK_GROUP_KIND_LABELS.schedule, "定时任务")
  assert.equal(describeInspectionRunTitle(runs[0], skillNameBySlug, new Map([[schedules[0].id, schedules[0]]])), "全集群挖矿行为定时安全巡检")

  // Unified task view: includeUnrunSchedules keeps never-run configured schedules in the same list,
  // hides them when statusFilter is active, and sorts enabled before paused.
  const unrunSchedules = [
    ...schedules,
    makeSchedule({ id: "sched_new_paused", name: "暂停未跑任务", enabled: false, last_run_id: "", last_run_at_ms: 0, last_status: "" }),
    makeSchedule({ id: "sched_new_active", name: "新建待跑任务", enabled: true, last_run_id: "", last_run_at_ms: 0, last_status: "" }),
  ]
  const unifiedGroups = groupInspectionRuns(runs, { schedules: unrunSchedules, skillNameBySlug, includeUnrunSchedules: true })
  const unrunTail = unifiedGroups.filter((g) => g.runCount === 0 && !g.hasUnloadedHistory)
  assert.deepEqual(unrunTail.map((g) => g.scheduleId), ["sched_never_ran", "sched_new_active", "sched_new_paused"])
  assert.equal(unrunTail[0].latestStatus, "")
  const filteredUnified = groupInspectionRuns(runs, { schedules: unrunSchedules, skillNameBySlug, statusFilter: "critical", includeUnrunSchedules: true })
  assert.equal(filteredUnified.some((g) => g.scheduleId === "sched_new_active"), false)

  // onlyConfiguredSchedules excludes ad-hoc/deleted-schedule runs from Tab 1 「定时任务」 so counts stay 1:1 with configured schedules.
  const onlySched = groupInspectionRuns(runs, {
    schedules: unrunSchedules,
    skillNameBySlug,
    includeUnrunSchedules: true,
    onlyConfiguredSchedules: true,
  })
  assert.equal(onlySched.length, unrunSchedules.length)
  assert.ok(onlySched.every((g) => g.kind === "schedule" && Boolean(g.schedule)))

  // Run source filter & labels cleanly separate scheduled, ad-hoc, and retest runs for Tab 2 「执行记录」.
  const { describeRunSourceLabel, filterInspectionRunsBySource } = inspectionRunGroups
  assert.equal(describeRunSourceLabel({ schedule_id: "sched_1", trigger_kind: "scheduled" }), "定时调度")
  assert.equal(describeRunSourceLabel({ schedule_id: "sched_1", trigger_kind: "manual" }), "定时任务（手动）")
  assert.equal(describeRunSourceLabel({ schedule_id: "", trigger_kind: "manual" }), "即时执行")
  assert.equal(describeRunSourceLabel({ schedule_id: "sched_1", trigger_kind: "retest" }), "异常复检")
  const sourceSample = [
    makeRun({ id: "r_sched", schedule_id: "sched_1", trigger_kind: "scheduled" }),
    makeRun({ id: "r_adhoc", schedule_id: "", trigger_kind: "manual" }),
    makeRun({ id: "r_retest", schedule_id: "sched_1", trigger_kind: "retest" }),
  ]
  assert.deepEqual(filterInspectionRunsBySource(sourceSample, "all").map((r) => r.id), ["r_sched", "r_adhoc", "r_retest"])
  assert.deepEqual(filterInspectionRunsBySource(sourceSample, "scheduled").map((r) => r.id), ["r_sched"])
  assert.deepEqual(filterInspectionRunsBySource(sourceSample, "adhoc").map((r) => r.id), ["r_adhoc"])
  assert.deepEqual(filterInspectionRunsBySource(sourceSample, "retest").map((r) => r.id), ["r_retest"])
})

test("Given unified MonitorTaskWorkspace, When rendered in schedules and runs tabs, Then Tab 1 shows only configured schedules without ad-hoc duplicates, Tab 2 lists chronological runs with source filtering, and target filter works in-place", async () => {
  const targetA = { key: "prod-cn/node-a", cluster: "prod-cn", instance: "node-a", status: "online", sessions: [] }
  const targetB = { key: "prod-cn/node-b", cluster: "prod-cn", instance: "node-b", status: "online", sessions: [] }
  const schedulesData = [
    makeSchedule({
      id: "sched_mining",
      name: "全集群挖矿行为定时安全巡检",
      scope_mode: "all_online",
      skills: ["inspect-crypto-mining"],
      interval_seconds: 21600,
      last_run_id: "run_m2",
      last_run_at_ms: 2000,
      last_status: "critical",
    }),
    makeSchedule({
      id: "sched_archive",
      name: "低频周度合规巡检",
      scope_mode: "targets",
      targets: [{ cluster: "prod-cn", instance: "node-b" }],
      skills: ["inspect-vm-host"],
      interval_seconds: 604800,
      last_run_id: "run_arch_1",
      last_run_at_ms: 1000,
      last_status: "success",
    }),
    makeSchedule({
      id: "sched_nginx",
      name: "节点 A Nginx 每日配置检查",
      scope_mode: "targets",
      targets: [{ cluster: "prod-cn", instance: "node-a" }],
      skills: [],
      command: "nginx -t",
      interval_seconds: 86400,
      last_run_id: "",
      last_run_at_ms: 0,
      last_status: "",
    }),
  ]
  const globalRuns = [
    makeRun({ id: "run_m2", schedule_id: "sched_mining", schedule_name: "全集群挖矿行为定时安全巡检", trigger_kind: "scheduled", status: "critical", failed_checks: 3, started_at_ms: 2000 }),
    makeRun({ id: "run_m1", schedule_id: "sched_mining", schedule_name: "全集群挖矿行为定时安全巡检", trigger_kind: "manual", status: "success", started_at_ms: 1500 }),
    makeRun({ id: "run_sk1", skills: ["inspect-crypto-mining"], trigger_kind: "manual", status: "warning", warning_checks: 1, started_at_ms: 1800 }),
  ]
  const archiveRuns = [
    makeRun({ id: "run_arch_1", schedule_id: "sched_archive", schedule_name: "低频周度合规巡检", trigger_kind: "scheduled", status: "success", started_at_ms: 1000 }),
  ]

  const fetchCalls = []
  const selectedRuns = []

  const deps = {
    "lucide-react": componentStubs,
    "@/lib/client": { sessionIsAdmin: () => true },
    "@/lib/automation": automationHelpers,
    "@/lib/inspection-run-groups": inspectionRunGroups,
    "@/lib/monitor-engine": {
      ...monitorEngine,
      fetchMonitorSchedules: async () => ({ schedules: schedulesData, defaults: {} }),
      fetchMonitorRuns: async (_session, opts = {}) => {
        fetchCalls.push(opts)
        if (opts.scheduleId === "sched_archive") return { runs: archiveRuns, target_filtered: Boolean(opts.cluster) }
        if (opts.scheduleId === "sched_mining") return { runs: globalRuns.filter((r) => r.schedule_id === "sched_mining"), target_filtered: Boolean(opts.cluster) }
        if (opts.cluster === "prod-cn" && opts.instance === "node-a") {
          return { runs: globalRuns.filter((r) => r.schedule_id === "sched_mining"), target_filtered: true }
        }
        return { runs: globalRuns }
      },
    },
    "@/lib/skills-registry": {
      fetchPlatformSkills: async () => ({
        skills: [
          { slug: "inspect-crypto-mining", name: "集群与主机挖矿行为安全巡检 Skill", enabled: true, scope: "system" },
          { slug: "inspect-vm-host", name: "虚机与物理机系统巡检 Skill", enabled: true, scope: "system" },
        ],
      }),
    },
    "./icons": componentStubs,
    "./monitor-run-detail": { MonitorRunDetailView: "MonitorRunDetailView" },
    "./monitor-schedule-editor": { MonitorScheduleEditor: "MonitorScheduleEditor" },
    "./ui": componentStubs,
  }

  const session = { gateway: "https://doops.example", token: "synthetic", isAdmin: true, username: "admin" }

  // 1) Tab 1 (「定时任务」, no selected target): renders strictly the 3 configured schedules, excluding ad-hoc run_sk1 duplicate card
  const schedView = componentHarness(
    "../components/monitor-task-workspace.tsx",
    "MonitorTaskWorkspace",
    {
      session,
      targets: [targetA, targetB],
      activeTab: "schedules",
      onSelectRun: (id) => selectedRuns.push(id),
    },
    deps,
  )
  await schedView.flush()

  assert.equal(fetchCalls[0].limit, 200)
  assert.equal(fetchCalls[0].cluster, undefined)
  // Exactly 3 configured schedules — ad-hoc run_sk1 does NOT appear as a 4th duplicate card or inflate header count!
  const groupToggles = schedView.nodes((n) => n.type === "button" && typeof n.props["aria-expanded"] === "boolean")
  assert.equal(groupToggles.length, 3)
  assert.ok(schedView.nodes((n) => n.type === "span" && elementText(n).includes("3 个定时任务 · 2 次执行")).length === 1, "header count must match configured schedules 1:1 without ad-hoc run inflation")
  assert.ok(groupToggles.every((btn) => btn.props["aria-expanded"] === false))
  assert.ok(elementText(groupToggles[0]).includes("全集群挖矿行为定时安全巡检"))
  assert.ok(elementText(groupToggles[0]).includes("已执行 2 次"))
  assert.ok(elementText(groupToggles[1]).includes("历史待加载"))
  assert.ok(elementText(groupToggles[2]).includes("节点 A Nginx 每日配置检查"))
  assert.ok(elementText(groupToggles[2]).includes("尚未运行"))

  // Expand first schedule -> displays its 2 historical runs inline
  groupToggles[0].props.onClick()
  await schedView.flush()
  assert.equal(schedView.nodes((n) => n.type === "button" && n.props["aria-expanded"] === true).length, 1)
  assert.ok(schedView.nodes((n) => n.type === "TableRow").length >= 3, "header + 2 run rows should render")

  // Expand pushed-out schedule -> auto-fetches schedule-specific runs and renders #run_arch_1
  const archiveToggle = schedView.one((n) => n.type === "button" && typeof n.props["aria-expanded"] === "boolean" && elementText(n).includes("低频周度合规巡检"))
  archiveToggle.props.onClick()
  await schedView.flush()
  assert.ok(fetchCalls.some((c) => c.scheduleId === "sched_archive"), "expanding unloaded schedule must fetch by scheduleId")
  assert.ok(schedView.nodes((n) => n.type === "TableRow" && elementText(n).includes("#run_arch_1")).length === 1)

  // Search filters schedules cleanly
  const searchInput = schedView.one((n) => n.type === "Input" && n.props["aria-label"] === "搜索定时任务")
  searchInput.props.onChange({ target: { value: "低频" } })
  await schedView.flush()
  assert.ok(schedView.nodes((n) => n.type === "span" && elementText(n).includes("1 个定时任务 · 1 次执行")).length === 1)
  searchInput.props.onChange({ target: { value: "" } })
  await schedView.flush()

  // Clicking a timeline dot opens MonitorRunDetailView in-place and notifies onSelectRun
  const timelineBtn = schedView.one((n) => n.type === "button" && typeof n.props["aria-label"] === "string" && n.props["aria-label"].includes("run_m1"))
  timelineBtn.props.onClick()
  await schedView.flush()
  assert.equal(selectedRuns.at(-1), "run_m1")
  const detailNode = schedView.one((n) => n.type === "MonitorRunDetailView")
  assert.equal(detailNode.props.runId, "run_m1")
  detailNode.props.onBack()
  await schedView.flush()

  // 2) Tab 2 (「执行记录」): chronological table includes both scheduled and ad-hoc runs, with source filter
  const runsView = componentHarness(
    "../components/monitor-task-workspace.tsx",
    "MonitorTaskWorkspace",
    {
      session,
      targets: [targetA, targetB],
      activeTab: "runs",
      onSelectRun: (id) => selectedRuns.push(id),
    },
    deps,
  )
  await runsView.flush()
  assert.equal(runsView.nodes((n) => n.type === "TableRow").length, 4, "1 header + 3 chronological run rows (including ad-hoc run_sk1)")
  const sourceSelect = runsView.one((n) => n.type === "Select" && n.props["aria-label"] === "按来源筛选")
  sourceSelect.props.onChange({ target: { value: "adhoc" } })
  await runsView.flush()
  assert.equal(runsView.nodes((n) => n.type === "TableRow").length, 2, "1 header + 1 ad-hoc run row")

  // 3) Target-scoped Tab 1: sends cluster/instance filter, shows only target-matched schedules, and supports scope toggle & focusedScheduleId
  fetchCalls.length = 0
  const autoView = componentHarness(
    "../components/monitor-task-workspace.tsx",
    "MonitorTaskWorkspace",
    {
      session,
      targets: [targetA, targetB],
      selectedTarget: targetA,
      activeTab: "schedules",
    },
    deps,
  )
  await autoView.flush()
  assert.equal(fetchCalls[0].cluster, "prod-cn")
  assert.equal(fetchCalls[0].instance, "node-a")
  const autoGroups = autoView.nodes((n) => n.type === "button" && typeof n.props["aria-expanded"] === "boolean")
  assert.equal(autoGroups.length, 2)
  assert.ok(elementText(autoGroups[0]).includes("全集群挖矿行为定时安全巡检"))
  assert.ok(elementText(autoGroups[1]).includes("节点 A Nginx 每日配置检查"))

  // Expand sched_mining and open run_m2 in-place with focusTarget
  autoGroups[0].props.onClick()
  await autoView.flush()
  const runM2Row = autoView.one((n) => n.type === "TableRow" && elementText(n).includes("#run_m2"))
  const detailBtn = autoView.nodes((n) => n.type === "Button" && elementText(n).trim() === "查看详情")[0]
  assert.ok(runM2Row && detailBtn)
  detailBtn.props.onClick()
  await autoView.flush()
  const autoDetail = autoView.one((n) => n.type === "MonitorRunDetailView")
  assert.equal(autoDetail.props.runId, "run_m2")
  assert.equal(autoDetail.props.focusTarget.key, "prod-cn/node-a")
  autoDetail.props.onBack()
  await autoView.flush()

  // Switch to 「全部目标」 drops cluster/instance params and reveals all 3 configured schedules (still zero ad-hoc duplicates)
  autoView.one((n) => n.type === "button" && elementText(n).includes("全部目标")).props.onClick()
  await autoView.flush()
  assert.equal(fetchCalls.at(-1).cluster, undefined)
  assert.equal(autoView.nodes((n) => n.type === "button" && typeof n.props["aria-expanded"] === "boolean").length, 3)

  // Focused schedule targeting a schedule outside node-a auto-expands & switches scope
  const focusedAuto = componentHarness(
    "../components/monitor-task-workspace.tsx",
    "MonitorTaskWorkspace",
    {
      session,
      targets: [targetA, targetB],
      selectedTarget: targetA,
      activeTab: "schedules",
      focusedScheduleId: "sched_archive",
    },
    deps,
  )
  await focusedAuto.flush()
  const autoExpanded = focusedAuto.one((n) => n.type === "button" && n.props["aria-expanded"] === true)
  assert.ok(elementText(autoExpanded).includes("低频周度合规巡检"))
})
