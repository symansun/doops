import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import {
  buildSparklineGeometry,
  CORE_TARGET_INSPECTION_SKILLS,
  formatAssetMonitorError,
  humanBytesHint,
  matchMonitorAssetFocus,
  monitorDeviceLabel,
  resolveFleetInspectionSkills,
  resolveTargetInspectionSkills,
  resolveTrendSkillOptions,
  visibleMetricsForAsset,
} from "./agent-monitor.ts"

test("Given target capabilities and skill registry, When resolving one-click inspection skills, Then explicit enabled skills are returned without empty runs", () => {
  // Default fallback without registry omits inspect-k8s-cluster when k8s unavailable.
  assert.deepEqual(
    resolveTargetInspectionSkills({ kubernetes_available: true }),
    [...CORE_TARGET_INSPECTION_SKILLS],
  )
  assert.deepEqual(
    resolveTargetInspectionSkills({ kubernetes_available: false }),
    ["inspect-vm-host", "inspect-container-runtime", "inspect-app-web"],
  )
  // Filters out disabled skills from registry.
  const registry = [
    { slug: "inspect-vm-host", enabled: true },
    { slug: "inspect-container-runtime", enabled: false },
    { slug: "inspect-app-web", enabled: true },
    { slug: "inspect-k8s-cluster", enabled: true },
  ]
  assert.deepEqual(
    resolveTargetInspectionSkills({ kubernetes_available: true }, registry),
    ["inspect-vm-host", "inspect-app-web", "inspect-k8s-cluster"],
  )
  // Fleet resolution omits inspect-k8s-cluster only when every target reports kubernetes_available=false.
  assert.deepEqual(
    resolveFleetInspectionSkills([{ kubernetes_available: false }, { kubernetes_available: true }], registry),
    ["inspect-vm-host", "inspect-app-web", "inspect-k8s-cluster"],
  )
  assert.deepEqual(
    resolveFleetInspectionSkills([{ kubernetes_available: false }], registry),
    ["inspect-vm-host", "inspect-app-web"],
  )
  // Focus matching resolves exact alert/drawer targets first, then falls back cleanly.
  const catalog = [
    { type: "machine", asset_id: "vm-1", namespace: "", origin: "agent_host" },
    { type: "container", asset_id: "nginx", namespace: "", origin: "container_stats" },
    { type: "app", asset_id: "api", namespace: "prod", origin: "k8s_top" },
  ]
  assert.equal(
    matchMonitorAssetFocus(catalog, { type: "app", assetId: "api", namespace: "prod", origin: "k8s_top" })?.asset_id,
    "api",
  )
  assert.equal(
    matchMonitorAssetFocus(catalog, { type: "container", assetId: "missing" })?.asset_id,
    "nginx",
  )
})

test("Given mixed inspection, security, and custom skills, When resolving Skill trend options, Then all enabled skills are exposed in order", () => {
  const registry = [
    { slug: "inspect-vm-host", name: "主机巡检", category: "inspection", enabled: true },
    { slug: "inspect-k8s-cluster", name: "K8s 巡检", category: "inspection", enabled: true },
    { slug: "inspect-crypto-mining", name: "挖矿检测", category: "security", enabled: true },
    { slug: "inspect-package-vuln", name: "漏洞基线", category: "security", enabled: true },
    { slug: "custom-gpu-health", name: "自定义 GPU 巡检", category: "ops", enabled: true },
    { slug: "disabled-skill", name: "已禁用", category: "security", enabled: false },
  ]
  const withK8s = resolveTrendSkillOptions({ kubernetes_available: true }, registry)
  assert.deepEqual(
    withK8s.map((o) => o.slug),
    ["inspect-vm-host", "inspect-k8s-cluster", "inspect-crypto-mining", "inspect-package-vuln", "custom-gpu-health"],
  )
  assert.equal(withK8s.find((o) => o.slug === "inspect-crypto-mining")?.category, "security")

  const noK8s = resolveTrendSkillOptions({ kubernetes_available: false }, registry)
  assert.deepEqual(
    noK8s.map((o) => o.slug),
    ["inspect-vm-host", "inspect-crypto-mining", "inspect-package-vuln", "custom-gpu-health"],
  )
})

test("Given sparkline samples with gaps and non-ok status, When building geometry, Then segments split cleanly", () => {
  const empty = buildSparklineGeometry([], 0, 1000, 300)
  assert.equal(empty.hasData, false)

  const g = buildSparklineGeometry(
    [
      { at_ms: 100, value: 10, status: "ok" },
      { at_ms: 200, value: 20, status: "ok" },
      { at_ms: 800, value: 30, status: "ok" }, // gap > 300 -> isolated dot
      { at_ms: 900, value: null, status: "missing" },
    ],
    0,
    1000,
    300,
  )
  assert.equal(g.hasData, true)
  assert.equal(g.polylines.length, 1)
  assert.equal(g.dots.length, 1)
})

test("Given monitor formatting helpers, When formatting bytes, devices, and visible metrics, Then outputs match contract", () => {
  assert.equal(humanBytesHint("bytes", 2048), "2.00 KiB")
  assert.equal(humanBytesHint("B/s", 1048576), "1.00 MiB/s")
  assert.equal(humanBytesHint("%", 88), "")
  assert.equal(monitorDeviceLabel("storage", "/host/raid5"), "挂载点 /host/raid5")
  assert.equal(monitorDeviceLabel("network", "eth0"), "网卡 eth0")
  assert.equal(monitorDeviceLabel("disk_read_iops", "sda"), "块设备 sda")
  assert.deepEqual(
    visibleMetricsForAsset({ type: "container", origin: "container_stats" }, ["cpu", "memory"]),
    ["cpu", "memory", "memory_limit"],
  )
})

test("Given /api/assets/monitor HTTP errors, When formatting messages, Then 503 distinguishes target_offline from asset_monitor_unavailable without misleading operators", () => {
  assert.equal(formatAssetMonitorError(401, {}), "登录已失效，请重新登录。")
  assert.equal(formatAssetMonitorError(403, {}), "无权访问该监控来源。")
  assert.equal(formatAssetMonitorError(404, {}), "监控对象或序列不存在。")
  assert.match(formatAssetMonitorError(422, { code: "monitor_window_too_dense" }), /采样点过多/)

  const offlineMsg = formatAssetMonitorError(503, { code: "target_offline", error: '{"code":"target_offline"}' })
  assert.match(offlineMsg, /目标 Agent 离线/)

  const containerMsg = formatAssetMonitorError(503, { code: "container_runtime_unavailable" })
  assert.match(containerMsg, /容器运行时/)
  assert.doesNotMatch(containerMsg, /Agent 离线/)

  const unavailableMsg = formatAssetMonitorError(503, {
    code: "asset_monitor_unavailable",
    error: '{"code":"asset_monitor_unavailable"}',
    message: "监控数据暂不可用，请检查 Gateway 监控存储或查看服务日志",
  })
  assert.match(unavailableMsg, /监控数据暂不可用/)
  assert.doesNotMatch(unavailableMsg, /Agent 离线/)

  const rawJsonCodeMsg = formatAssetMonitorError(503, { error: '{"code":"asset_monitor_unavailable"}' })
  assert.match(rawJsonCodeMsg, /监控数据暂不可用/)
  assert.doesNotMatch(rawJsonCodeMsg, /Agent 离线/)

  const generic503 = formatAssetMonitorError(503, {})
  assert.doesNotMatch(generic503, /Agent 离线/)
  assert.match(generic503, /监控服务暂不可用/)
})

test("Wiring lock: AgentMonitor and AgentAssets converge shared helpers and expose rule delete + Skill trends + manifest", () => {
  const monitorSrc = readFileSync(new URL("../components/asset-monitor-panel.tsx", import.meta.url), "utf8")
  assert.match(monitorSrc, /from "@\/lib\/agent-monitor"/, "AgentMonitor must import lib/agent-monitor")
  assert.match(monitorSrc, /formatAssetMonitorError\(/, "AgentMonitor must map HTTP errors through formatAssetMonitorError")
  assert.doesNotMatch(monitorSrc, /503:\s*"目标 Agent 离线或监控暂不可用。"/, "AgentMonitor must not hardcode all 503 responses as Agent offline")
  assert.match(monitorSrc, /resolveTargetInspectionSkills\(/, "One-click inspect must resolve explicit skills instead of empty payload")
  assert.match(monitorSrc, /resolveTrendSkillOptions\(/, "Skill trends must resolve all enabled skills including security and custom skills")
  assert.match(monitorSrc, /fetchPlatformSkills\(session, \{\},/, "AgentMonitor must load skills across all categories instead of only inspection")
  assert.match(monitorSrc, /action: "delete_rule"/, "AgentMonitor must support deleting alert rules")
  assert.match(monitorSrc, /fetchMonitorMetrics\(/, "AgentMonitor must expose Skill-First /api/monitor/metrics trends")
  assert.match(monitorSrc, /extractManifestNamespaces\(/, "K8s Pod collector must offer discovered namespace suggestions")

  const assetsSrc = readFileSync(new URL("../components/agent-asset-scope.tsx", import.meta.url), "utf8")
  assert.match(assetsSrc, /TargetManifestCard/, "AgentAssets must integrate TargetManifestCard")
  assert.match(assetsSrc, /filterWorkspaceAssets\(/, "AgentAssets must filter workspace scope via shared helper")
  assert.match(assetsSrc, /onOpenMonitor/, "AgentAssets must support jumping directly to Agent Monitor")

  // Dead AssetMonitorPanel wrapper must stay deleted after ResourceMonitorHub consolidation.
  assert.doesNotMatch(monitorSrc, /export function AssetMonitorPanel\b/, "Dead AssetMonitorPanel wrapper must not be reintroduced")

  const hubSrc = readFileSync(new URL("../components/resource-monitor-hub.tsx", import.meta.url), "utf8")
  assert.match(hubSrc, /SystemResourceMonitor/, "ResourceMonitorHub must integrate SystemResourceMonitor fleet view")
  assert.match(hubSrc, /AgentMonitor/, "ResourceMonitorHub must integrate AgentMonitor per-target view")
  assert.match(hubSrc, /initialFocus=\{activeFocus\}/, "ResourceMonitorHub must forward alert/drawer focus into AgentMonitor")

  const fleetSrc = readFileSync(new URL("../components/system-resource-monitor.tsx", import.meta.url), "utf8")
  assert.match(fleetSrc, /resolveFleetInspectionSkills\(/, "SystemResourceMonitor fleet collect must resolve explicit enabled skills")
  assert.match(fleetSrc, /skills:\s*fleetSkills/, "SystemResourceMonitor must never send an empty skills payload to triggerMonitorRun")

  const overviewSrc = readFileSync(new URL("../components/agent-overview-panel.tsx", import.meta.url), "utf8")
  assert.match(overviewSrc, /TargetManifestCard/, "AgentOverviewPanel must integrate TargetManifestCard")
  assert.match(overviewSrc, /WorkspaceScopeSection/, "AgentOverviewPanel must integrate WorkspaceScopeSection")

  const situationSrc = readFileSync(new URL("../components/situational-dashboard-panel.tsx", import.meta.url), "utf8")
  assert.doesNotMatch(situationSrc, /<SystemResourceMonitor\b/, "SituationalDashboardPanel must delegate fleet resource monitoring to ResourceMonitorHub")
  assert.match(situationSrc, /onOpenResourceMonitor/, "SituationalDashboardPanel must link directly to ResourceMonitorHub")

  const routeSrc = readFileSync(new URL("../app/api/monitor/[...path]/route.ts", import.meta.url), "utf8")
  assert.match(routeSrc, /segments\[0\] === "metrics"/, "Monitor BFF route must proxy /v1/monitor/metrics")
})
