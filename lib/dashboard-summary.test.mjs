import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import {
  buildAttentionItems,
  buildClusterGroups,
  buildNodeRows,
  buildTargetModeBreakdown,
  formatDashboardStatusLine,
  formatTagChipLabel,
  indexHostTelemetry,
  indexMachineTags,
  partitionHomeWidgets,
  resolveDefaultActivityTab,
} from "./dashboard-summary.ts"
import { buildDefaultPluginState, resolveHomeWidgetPlugins } from "./plugins/resolver.ts"

function makeTarget(overrides = {}) {
  return {
    key: "prod-cn/web-01",
    cluster: "prod-cn",
    instance: "web-01",
    mode: "kubernetes",
    kubernetes_available: true,
    status: "online",
    sessions: [],
    active_ops: 0,
    queued_ops: 0,
    busy: false,
    ...overrides,
  }
}

function makeWidgetPlugin(slug, opts = {}) {
  return {
    id: `skill.${slug}`,
    tier: "declarative",
    skill_slug: slug,
    scope: "system",
    owner_is_me: false,
    skill_enabled: true,
    name: opts.name ?? slug,
    description: "desc",
    category: opts.category ?? "observability",
    effective: true,
    scenario_default: true,
    override: null,
    dependencies: { skills: [], mcp: [] },
    manifest: {
      api_version: "doops.sh/v1alpha1",
      kind: "SkillPlugin",
      plugin: { id: `skill.${slug}`, name: opts.name ?? slug, category: opts.category ?? "observability", icon: "cpu", scenarios: [] },
      capabilities: { target_modes: ["kubernetes", "linux"] },
      ui: {
        slots: {
          sidebar: { enabled: true, label: opts.name ?? slug, group: "extensions", order: opts.order ?? 10 },
          home_widget: {
            enabled: true,
            title: opts.name ?? slug,
            span: 1,
            metrics: opts.metrics ?? [{ key: "m1", label: "指标 1", unit: "%", aggregate: "max" }],
          },
        },
        inputs: [],
        presets: [],
        views: [],
      },
    },
  }
}

test("Given online targets across modes, When computing mode breakdown, Then K8s/Linux/Docker counts stay distinct from Agent cluster grouping", () => {
  const breakdown = buildTargetModeBreakdown([
    makeTarget({ key: "a/1", mode: "kubernetes", kubernetes_available: true }),
    makeTarget({ key: "b/1", mode: "linux", kubernetes_available: false }),
    makeTarget({ key: "c/1", mode: "linux", kubernetes_available: false }),
    makeTarget({ key: "d/1", mode: "docker", kubernetes_available: false }),
  ])
  assert.equal(breakdown.k8s, 1)
  assert.equal(breakdown.linux, 2)
  assert.equal(breakdown.docker, 1)
  assert.equal(breakdown.label, "K8s 1 · Linux 2 · Docker 1")
  assert.equal(buildTargetModeBreakdown([]).label, "暂无在线节点")
})

test("Given CMDB machines with tags, When indexing tags, Then workload/app/db tags sort ahead of env/rack metadata and coverage counts stay exact", () => {
  const summary = indexMachineTags([
    { asset_id: "m1", cluster: "prod", instance: "web-01", tags: ["env=prod", "rack=A1", "app=教务门户", "runtime=java-17"] },
    { asset_id: "m2", cluster: "prod", instance: "web-01", tags: ["db=mysql-8.0", "app=教务门户"] },
    { asset_id: "m3", cluster: "edge", instance: "gw-01", tags: [] },
  ])
  assert.equal(summary.totalMachines, 3)
  assert.equal(summary.taggedMachines, 2)
  assert.equal(summary.untaggedMachines, 1)
  const webTags = summary.byTarget.get("prod/web-01") || []
  assert.deepEqual(webTags.slice(0, 3), ["app=教务门户", "db=mysql-8.0", "runtime=java-17"])
  assert.equal(formatTagChipLabel("app=教务门户"), "教务门户")
  assert.equal(formatTagChipLabel("env=prod"), "env=prod")
})

test("Given monitor snapshots, When indexing host telemetry, Then vm/physical snapshots win over app rows and critical wins ties", () => {
  const map = indexHostTelemetry([
    {
      cluster: "prod",
      instance: "node-1",
      asset_id: "app-1",
      asset_name: "app",
      asset_kind: "app",
      strategy: "app_service",
      collector_method: "app_probe",
      collected_at_ms: 2000,
      status: "ok",
      online: true,
      stale: false,
      cpu_percent: 5,
      tags: [],
    },
    {
      cluster: "prod",
      instance: "node-1",
      asset_id: "vm-1",
      asset_name: "host",
      asset_kind: "vm",
      strategy: "vm_host",
      collector_method: "agent_local",
      collected_at_ms: 1000,
      status: "warning",
      online: true,
      stale: false,
      cpu_percent: 88.4,
      memory_percent: 76.2,
      disk_percent: 61.0,
      tags: [],
    },
  ])
  const hit = map.get("prod/node-1")
  assert.ok(hit)
  assert.equal(hit.status, "warning")
  assert.equal(hit.cpuPercent, 88.4)
  assert.equal(hit.memoryPercent, 76.2)
  assert.equal(hit.diskPercent, 61.0)
})

test("Given online targets, telemetry, and machine tags, When building dense node rows and cluster groups, Then critical/busy nodes surface first and tag search matches", () => {
  const targets = [
    makeTarget({ key: "alpha/n1", cluster: "alpha", instance: "n1", mode: "linux", kubernetes_available: false }),
    makeTarget({ key: "zeta/k1", cluster: "zeta", instance: "k1", mode: "kubernetes", kubernetes_available: true, active_ops: 2 }),
    makeTarget({ key: "mid/h1", cluster: "mid", instance: "h1", mode: "docker", kubernetes_available: false }),
  ]
  const telemetry = indexHostTelemetry([
    {
      cluster: "mid",
      instance: "h1",
      asset_id: "m1",
      asset_name: "mid-host",
      asset_kind: "physical",
      strategy: "vm_host",
      collector_method: "agent_local",
      collected_at_ms: 5000,
      status: "critical",
      online: true,
      stale: false,
      cpu_percent: 96,
      tags: [],
    },
  ])
  const tags = indexMachineTags([
    { asset_id: "m-alpha", cluster: "alpha", instance: "n1", tags: ["db=mysql-8.0"] },
  ]).byTarget

  const rows = buildNodeRows(targets, telemetry, tags, "all", "")
  assert.deepEqual(rows.map((r) => r.target.key), ["mid/h1", "zeta/k1", "alpha/n1"])
  assert.deepEqual(rows[2].tags, ["db=mysql-8.0"])

  const tagSearchRows = buildNodeRows(targets, telemetry, tags, "all", "mysql-8.0")
  assert.deepEqual(tagSearchRows.map((r) => r.target.key), ["alpha/n1"])

  const busyRows = buildNodeRows(targets, telemetry, tags, "busy", "")
  assert.deepEqual(busyRows.map((r) => r.target.key), ["mid/h1", "zeta/k1"])

  const groups = buildClusterGroups(targets, telemetry, "all", "", tags)
  assert.deepEqual(groups.map((g) => g.name), ["mid", "zeta", "alpha"])
  assert.equal(groups[0].criticalCount, 1)

  const k8sOnly = buildClusterGroups(targets, telemetry, "k8s", "", tags)
  assert.deepEqual(k8sOnly.map((g) => g.name), ["zeta"])
})

test("Given threshold alerts, unhealthy snapshots, failed runs, and offline targets, When building attention items, Then critical alerts merge live telemetry and deduplicate same-target rows", () => {
  const healthy = buildAttentionItems({
    onlineTargets: [makeTarget()],
    directoryTargets: [makeTarget()],
    monitorOverview: null,
  })
  assert.equal(healthy.total, 0)
  assert.deepEqual(healthy.items, [])

  const tOnline = makeTarget({ key: "prod/web-01", cluster: "prod", instance: "web-01" })
  const tOffline = makeTarget({ key: "edge/box-09", cluster: "edge", instance: "box-09", status: "offline" })
  const out = buildAttentionItems({
    onlineTargets: [tOnline],
    directoryTargets: [tOnline, tOffline],
    monitorOverview: {
      scope: "global",
      range: "24h",
      from_ms: 0,
      to_ms: 1,
      summary: {
        total_assets: 2,
        online_agents: 1,
        offline_agents: 1,
        covered_assets: 1,
        probe_coverage_pct: 50,
        healthy_assets: 0,
        warning_assets: 0,
        critical_assets: 1,
        offline_assets: 1,
        avg_cpu_percent: 50,
        avg_memory_percent: 60,
        avg_disk_percent: 40,
        total_cpu_cores: 8,
        used_cpu_cores: 4,
        total_memory_bytes: 1,
        used_memory_bytes: 1,
        total_disk_bytes: 1,
        used_disk_bytes: 1,
        active_schedules: 1,
        total_runs: 2,
        open_threshold_alerts: 1,
        last_collected_at_ms: 1000,
      },
      by_kind: {},
      resources: [
        {
          cluster: "prod",
          instance: "web-01",
          asset_id: "vm-1",
          asset_name: "web-01",
          asset_kind: "vm",
          strategy: "vm_host",
          collector_method: "agent_local",
          collected_at_ms: 1000,
          status: "critical",
          online: true,
          stale: false,
          cpu_percent: 93.2,
          memory_percent: 84.5,
          tags: [],
        },
      ],
      top_cpu: [],
      top_memory: [],
      top_disk: [],
      trends: [],
      threshold_alerts: [
        {
          cluster: "prod",
          instance: "web-01",
          type: "host",
          asset_id: "vm-1",
          origin: "rule",
          metric: "cpu_percent",
          threshold: 85,
          duration_seconds: 300,
          opened_ms: 1000,
        },
      ],
      recent_runs: [
        {
          id: "run_fail_1",
          schedule_name: "主机安全巡检",
          trigger_kind: "scheduled",
          status: "failed",
          total_targets: 1,
          total_assets: 1,
          passed_checks: 0,
          warning_checks: 0,
          failed_checks: 2,
          skipped_checks: 0,
          offline_targets: 0,
          metrics_written: 0,
          started_at_ms: 1000,
          finished_at_ms: 2000,
          duration_ms: 1000,
        },
      ],
    },
  })

  // Threshold alert merges live telemetry and suppresses duplicate resource snapshot row on same target.
  assert.equal(out.total, 3)
  assert.equal(out.items[0].kind, "threshold_alert")
  assert.equal(out.items[0].severity, "critical")
  assert.match(out.items[0].detail, /CPU 93\.2%/)
  assert.ok(out.items[0].target, "online target should attach for one-click AI/terminal jump")
  assert.equal(out.items[1].kind, "failed_run")
  assert.equal(out.items[1].runId, "run_fail_1")
  assert.equal(out.items[2].kind, "offline_target")
  assert.equal(out.items[2].severity, "offline")
})

test("Given home widgets with mixed telemetry, When partitioning widgets, Then uncollected widgets collapse to pending and active widgets sort by severity without truncation", () => {
  const state = buildDefaultPluginState({
    scenarioId: "all-in-one",
    extraPlugins: [
      makeWidgetPlugin("vm-ok", { name: "主机体检", order: 1 }),
      makeWidgetPlugin("gpu-empty", { name: "GPU 算力", order: 2 }),
      makeWidgetPlugin("sec-crit", { name: "主机安全", order: 3 }),
      makeWidgetPlugin("web-warn", { name: "Web 巡检", order: 4 }),
      makeWidgetPlugin("db-empty", { name: "数据库巡检", order: 5 }),
    ],
  })
  const all = resolveHomeWidgetPlugins(state)
  assert.equal(all.length, 5)

  const emptyPart = partitionHomeWidgets(all, new Map())
  assert.equal(emptyPart.active.length, 0)
  assert.equal(emptyPart.pending.length, 5)

  const bySkillKey = new Map([
    ["vm-ok:m1", { skill: "vm-ok", key: "m1", max: 32, avg: 28, latest: 30, latest_at_ms: 1, status: "ok", reporting_targets: 3 }],
    ["sec-crit:m1", { skill: "sec-crit", key: "m1", max: 4, avg: 2, latest: 4, latest_at_ms: 1, status: "critical", reporting_targets: 2 }],
    ["web-warn:m1", { skill: "web-warn", key: "m1", max: 1, avg: 1, latest: 1, latest_at_ms: 1, status: "warning", reporting_targets: 1 }],
  ])
  const mixed = partitionHomeWidgets(all, bySkillKey)
  assert.deepEqual(
    mixed.active.map((w) => [w.plugin.skill_slug, w.severity]),
    [
      ["sec-crit", "critical"],
      ["web-warn", "warning"],
      ["vm-ok", "ok"],
    ],
  )
  assert.deepEqual(
    mixed.pending.map((w) => w.plugin.skill_slug),
    ["gpu-empty", "db-empty"],
  )
})

test("Given non-admin audit restriction and status formatting, When resolving home defaults, Then non-admins land on inspection runs and status pill stays concise", () => {
  assert.equal(resolveDefaultActivityTab({ auditDenied: true, hasInspectionRuns: true }), "runs")
  assert.equal(resolveDefaultActivityTab({ auditDenied: true, hasInspectionRuns: false }), "audit")
  assert.equal(resolveDefaultActivityTab({ auditDenied: false, hasInspectionRuns: true }), "audit")

  assert.equal(
    formatDashboardStatusLine({ onlineAgents: 11, activeOps: 1, issueCount: 0 }),
    "11 台在线 · 运行正常 · 1 项执行中",
  )
  assert.equal(
    formatDashboardStatusLine({ onlineAgents: 11, activeOps: 0, issueCount: 3 }),
    "11 台在线 · 3 项异常待处理",
  )
  assert.equal(
    formatDashboardStatusLine({ onlineAgents: 0, activeOps: 0, issueCount: 0 }),
    "暂无在线 Agent",
  )
})

test("Given the refined Console Home source, When inspecting components, Then onboarding noise and duplicate right-rail sidebar links stay permanently absent", () => {
  const dashboardSrc = readFileSync(new URL("../components/dashboard-panel.tsx", import.meta.url), "utf8")
  assert.doesNotMatch(dashboardSrc, /按使用场景精简你的工作台/, "scenario onboarding nag banner must be removed")
  assert.doesNotMatch(dashboardSrc, /默认兼容态/, "compatibility-mode marketing copy must be removed")
  assert.doesNotMatch(dashboardSrc, /选择场景包/, "scenario picker nag CTA must be removed from Home")
  assert.doesNotMatch(dashboardSrc, /ActivePluginsHomeStrip/, "duplicate plugin shortcut strip must be removed from Home")
  assert.doesNotMatch(dashboardSrc, /FIRST_GUIDE_DISMISSED_KEY/, "obsolete dismissed-guide storage key must not be used in Home")
  assert.ok(dashboardSrc.includes("buildNodeRows"), "Home must use high-density node table by default")
  assert.ok(dashboardSrc.includes("triggerMonitorRun"), "Home must support direct one-click fleet health check")
  assert.ok(dashboardSrc.includes("buildTargetModeBreakdown"), "KPI #1 must show runtime mode breakdown without conflating Agent grouping with K8s clusters")

  const widgetSrc = readFileSync(new URL("../components/plugins/home-widget-grid.tsx", import.meta.url), "utf8")
  assert.ok(widgetSrc.includes("partitionHomeWidgets"), "HomePluginWidgetGrid must partition active vs pending widgets")
})
