import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import { ALL_CONSOLE_NAV_ENTRIES, CONSOLE_HOME, consoleNavEntries, targetTabLabel } from "../console-views.ts"
import { buildConsoleSearch, DEFAULT_CONSOLE_ROUTE, parseConsoleSearch } from "../console-url.ts"
import { BUILTIN_PLUGIN_CATALOG } from "./types.ts"
import {
  activeOverrideCount,
  buildDefaultPluginState,
  canNavigateDestination,
  findBuiltinPluginMeta,
  formatPluginName,
  isCoreEntryId,
  isDeclarativePluginVisibleInNav,
  isPluginEnabled,
  pluginOwningNavEntry,
  pluginOwningRoute,
  resolveConsoleNavItems,
  resolveDeclarativeSidebarPlugins,
  resolveDeclarativeTargetTabPlugins,
  resolveHomeWidgetPlugins,
} from "./resolver.ts"
import {
  buildMonitorLatestMetricPairsPath,
  buildMonitorLatestMetricsPath,
  chunkMonitorMetricPairs,
  fetchMonitorLatestMetrics,
  fetchMonitorLatestMetricsForWidgets,
} from "../monitor-metrics.ts"
import {
  clearCachedPluginState,
  fetchPluginState,
  mutatePluginState,
  readCachedPluginState,
  resetDemoPluginStore,
  writeCachedPluginState,
} from "./client.ts"

test("Given builtin-catalog.json, When compared with UI registry and console-views, Then every plugin and nav entry aligns with zero drift", () => {
  const registrySrc = readFileSync(new URL("../../components/plugins/registry.tsx", import.meta.url), "utf8")
  for (const plugin of BUILTIN_PLUGIN_CATALOG.plugins) {
    assert.ok(registrySrc.includes(`"${plugin.id}"`), `components/plugins/registry.tsx missing plugin ID ${plugin.id}`)
  }

  // Every console nav entry must be either explicitly listed in core_ids or owned by one builtin plugin.
  for (const entry of ALL_CONSOLE_NAV_ENTRIES) {
    const owner = pluginOwningNavEntry(entry.id)
    const isCore = isCoreEntryId(entry.id)
    assert.ok(isCore !== Boolean(owner), `nav entry ${entry.id} must be exclusively Core (${isCore}) or Plugin-owned (${owner?.id})`)
  }
})

test("Given the 5 scenario presets, When resolving nav entries, Then enabled and hidden modules match each scenario contract", () => {
  const hostSec = buildDefaultPluginState({ scenarioId: "host-security-ops" })
  const hostItems = resolveConsoleNavItems({ isAdmin: true, hasTarget: true, kubernetesAvailable: true, pluginState: hostSec })
  const hostIds = hostItems.map((i) => i.entry.id)
  assert.ok(hostIds.includes("home"))
  assert.ok(hostIds.includes("kb"), "kb stays in Core Shell")
  assert.ok(hostIds.includes("deploy-upgrade"), "deploy-upgrade stays in Core Shell")
  assert.ok(hostIds.includes("plugin-center"), "plugin-center stays in Core Shell")
  assert.ok(hostIds.includes("tasks"), "unified automation & inspection workbench stays enabled")
  assert.ok(!hostIds.includes("smart-inspection"), "duplicate smart-inspection sidebar entry is consolidated into tasks")
  assert.ok(hostIds.includes("ledger-machines"))
  assert.ok(!hostIds.includes("k8s"), "host-security-ops hides Kubernetes")
  assert.ok(!hostIds.includes("deploy-workload"), "host-security-ops hides workload deploy")
  assert.ok(!hostIds.includes("image"), "host-security-ops hides image")
  assert.ok(!hostIds.includes("website"), "host-security-ops hides website")

  const minimal = buildDefaultPluginState({ scenarioId: "minimal-workbench" })
  const minItems = resolveConsoleNavItems({ isAdmin: false, hasTarget: true, kubernetesAvailable: true, pluginState: minimal })
  assert.equal(minItems.length, 10, "minimal-workbench for non-admin keeps exactly the 10 non-admin Core entries")
  assert.ok(!minItems.some((i) => i.entry.id === "platform-admin"))

  // Cold boot before plugin state loads (pluginsReady=false) shows only Core entries (zero flash).
  const coldItems = resolveConsoleNavItems({ isAdmin: true, hasTarget: true, pluginsReady: false })
  assert.equal(coldItems.length, 11)
  assert.ok(coldItems.every((i) => i.pluginId === null))
})

test("Given target capability kubernetes_available=false, When resolving nav and cross-page navigation, Then K8s entries are disabled with clear reason", () => {
  const allInOne = buildDefaultPluginState({ scenarioId: "all-in-one" })
  const items = resolveConsoleNavItems({
    isAdmin: true,
    hasTarget: true,
    kubernetesAvailable: false,
    pluginState: allInOne,
  })
  const k8sItem = items.find((i) => i.entry.id === "k8s")
  const workloadItem = items.find((i) => i.entry.id === "deploy-workload")
  assert.ok(k8sItem?.disabled)
  assert.equal(k8sItem?.disabledReason, "当前目标不支持 Kubernetes")
  assert.ok(workloadItem?.disabled)
  assert.equal(workloadItem?.disabledReason, "当前目标不支持 Kubernetes")

  // Undefined kubernetes_available (unreported) stays available.
  const unreported = canNavigateDestination({ view: "console", tab: "k8s" }, allInOne, { kubernetesAvailable: undefined })
  assert.equal(unreported.ok, true)

  const blockedCap = canNavigateDestination({ view: "console", tab: "k8s" }, allInOne, { kubernetesAvailable: false })
  assert.equal(blockedCap.ok, false)
  assert.equal(blockedCap.reason, "当前目标不支持 Kubernetes")
})

test("Given disabled plugins, When checking routes and cross-page navigation, Then disabled plugins are guarded and labels never throw", () => {
  const state = buildDefaultPluginState({ scenarioId: "minimal-workbench" })
  assert.equal(isPluginEnabled(state, "doops.k8s"), false)
  assert.equal(isPluginEnabled(state, "doops.database"), false)

  const k8sRoute = parseConsoleSearch("?view=assets&area=clusters")
  const owner = pluginOwningRoute(k8sRoute)
  assert.equal(owner?.id, "doops.k8s")

  const monitorRoute = parseConsoleSearch("?view=assets&area=monitor")
  assert.equal(pluginOwningRoute(monitorRoute)?.id, "doops.inspection")
  const monitorJump = canNavigateDestination({ view: "assets", area: "monitor" }, state)
  assert.equal(monitorJump.ok, false)
  assert.ok(monitorJump.reason.includes("态势感知与监控"))

  const legacyInspectionRoute = parseConsoleSearch("?view=assets&area=inspection&run=run_1")
  assert.equal(pluginOwningRoute(legacyInspectionRoute)?.id, "doops.automation")

  // Duplicate sidebar entries (assets / monitor / smart-inspection) are consolidated.
  const navIds = ALL_CONSOLE_NAV_ENTRIES.map((e) => e.id)
  assert.ok(navIds.includes("resource-monitor"))
  assert.ok(navIds.includes("tasks"))
  assert.ok(!navIds.includes("assets"))
  assert.ok(!navIds.includes("monitor"))
  assert.ok(!navIds.includes("smart-inspection"))

  const dbJump = canNavigateDestination({ view: "console", tab: "database" }, state)
  assert.equal(dbJump.ok, false)
  assert.ok(dbJump.reason.includes("数据库治理"))

  // Core destinations remain always allowed even in minimal-workbench.
  for (const dest of [
    { view: CONSOLE_HOME },
    { view: "console", tab: "ask" },
    { view: "console", tab: "assets" },
    { view: "console", tab: "kb" },
    { view: "assets", area: "plugins" },
    { view: "assets", area: "skills" },
    { view: "assets", area: "mcp" },
    { view: "deploy", section: "upgrade" },
    { view: "catalog", kind: "credentials" },
  ]) {
    assert.equal(canNavigateDestination(dest, state).ok, true, `Core dest ${JSON.stringify(dest)} must stay allowed`)
  }

  // targetTabLabel never crashes even on declarative plugin tabs.
  assert.equal(targetTabLabel("plugin:skill.gpu-governance"), "skill.gpu-governance")
  assert.equal(targetTabLabel("database"), "数据库")
  assert.ok(findBuiltinPluginMeta("doops.inspection"))
  assert.ok(consoleNavEntries(true).some((e) => e.id === "plugin-center"))
})

test("Given Plugin Center and declarative URL routes, When parsed and serialized, Then old skills/mcp URLs and new plugins URLs both round-trip", () => {
  const center = parseConsoleSearch("?view=assets&area=plugins&sub=mcp")
  assert.deepEqual(center.assetPage, { section: "plugins", sub: "mcp" })
  assert.equal(buildConsoleSearch("", center), "?view=assets&area=plugins&sub=mcp")

  const defaultCenter = parseConsoleSearch("?view=assets&area=plugins")
  assert.deepEqual(defaultCenter.assetPage, { section: "plugins", sub: "scenarios" })
  assert.equal(buildConsoleSearch("", defaultCenter), "?view=assets&area=plugins")

  // Backward-compatible old bookmarks still parse & round-trip cleanly.
  const oldSkills = parseConsoleSearch("?view=assets&area=skills&skill=inspect-vm-host")
  assert.deepEqual(oldSkills.assetPage, { section: "skills", slug: "inspect-vm-host" })
  assert.equal(buildConsoleSearch("", oldSkills), "?view=assets&area=skills&skill=inspect-vm-host")

  // Declarative plugin tab & view validation.
  const declTab = parseConsoleSearch("?view=console&tab=plugin:skill.gpu-governance")
  assert.equal(declTab.tab, "plugin:skill.gpu-governance")
  const badDeclTab = parseConsoleSearch("?view=console&tab=plugin:../evil")
  assert.equal(badDeclTab.tab, "overview")
  const badPluginView = parseConsoleSearch("?view=plugin&plugin=../../etc")
  assert.equal(badPluginView.view, DEFAULT_CONSOLE_ROUTE.view)
})

test("Given sessionStorage cache and Demo mode mutations, When exercising lifecycle, Then cache isolation, 403 non-admin, dry_run, overrides and 409 conflict hold", async () => {
  const mem = new Map()
  const storage = {
    getItem: (k) => mem.get(k) ?? null,
    setItem: (k, v) => { mem.set(k, v) },
    removeItem: (k) => { mem.delete(k) },
  }
  const liveSession = { gateway: "https://gw.example", userId: "usr_1", userName: "alice", isAdmin: true, demo: false }
  const state = buildDefaultPluginState({ scenarioId: "ai-compute-cluster", overrides: { "doops.database": true }, revision: 3 })
  assert.equal(activeOverrideCount(state), 1)
  writeCachedPluginState(storage, liveSession, state, '"etag-r3"')
  const restored = readCachedPluginState(storage, liveSession)
  assert.equal(restored?.etag, '"etag-r3"')
  assert.equal(restored?.state.scenario_id, "ai-compute-cluster")
  clearCachedPluginState(storage, liveSession)
  assert.equal(readCachedPluginState(storage, liveSession), null)

  // Demo mode mutations
  resetDemoPluginStore()
  const demoAdmin = { gateway: "demo://local", userId: "demo-admin", userName: "演示管理员", isAdmin: true, demo: true }
  const demoUser = { gateway: "demo://local", userId: "demo-user", userName: "演示成员", isAdmin: false, demo: true }

  // Demo mode includes only the 12 Core builtin L2+ plugins (DNS & Email are external installable plugins).
  const initialDemo = await fetchPluginState(demoAdmin)
  assert.equal(initialDemo.kind, "ok")
  assert.equal(initialDemo.state.plugins.filter((p) => p.tier === "declarative").length, 12)
  assert.ok(!initialDemo.state.plugins.some((p) => p.id === "skill.dns-governance" || p.id === "skill.mail-governance"))

  const forbidden = await mutatePluginState(demoUser, { action: "apply_scenario", scenario_id: "minimal-workbench", expected_revision: 0 })
  assert.equal(forbidden.ok, false)
  assert.equal(forbidden.status, 403)

  const preview = await mutatePluginState(demoAdmin, { action: "apply_scenario", scenario_id: "host-security-ops", expected_revision: 0, dry_run: true })
  assert.equal(preview.ok, true)
  assert.equal(preview.data.dry_run, true)
  assert.equal(preview.data.revision, 0)

  const applied = await mutatePluginState(demoAdmin, { action: "apply_scenario", scenario_id: "ai-compute-cluster", expected_revision: 0 })
  assert.equal(applied.ok, true)
  assert.equal(applied.data.revision, 1)

  const conflict = await mutatePluginState(demoAdmin, { action: "reset", expected_revision: 0 })
  assert.equal(conflict.ok, false)
  assert.equal(conflict.conflict, true)

  const fetched = await fetchPluginState(demoAdmin)
  assert.equal(fetched.kind, "ok")
  assert.equal(fetched.state.scenario_id, "ai-compute-cluster")
  assert.equal(fetched.state.configured, true)
  // Demo mode includes all 12 Core builtin L2+ scenario plugins (ai-compute-cluster enables 5, hides 7).
  assert.equal(fetched.state.plugins.filter((p) => p.tier === "declarative").length, 12)
  for (const id of [
    "skill.inspect-vm-host",
    "skill.machine-workload-discovery",
    "skill.gpu-governance",
    "skill.inspect-k8s-cluster",
    "skill.host-security",
  ]) {
    assert.ok(fetched.state.plugins.some((p) => p.id === id && p.effective), `expected ${id} enabled in ai-compute-cluster`)
  }
  for (const id of [
    "skill.inspect-container-runtime",
    "skill.inspect-app-web",
    "skill.attack-surface-scan",
    "skill.inspect-database",
    "skill.inspect-ssh-host",
    "skill.inspect-package-vuln",
    "skill.inspect-proxy-evasion",
  ]) {
    assert.ok(fetched.state.plugins.some((p) => p.id === id && !p.effective), `expected ${id} hidden in ai-compute-cluster`)
  }
  // Admin can override a hidden L2 plugin ON and verify override flag.
  const enableSurface = await mutatePluginState(demoAdmin, { action: "set_plugin", plugin_id: "skill.attack-surface-scan", enabled: true, expected_revision: 1 })
  assert.equal(enableSurface.ok, true)
  const afterSurface = await fetchPluginState(demoAdmin)
  assert.equal(afterSurface.kind, "ok")
  assert.ok(afterSurface.state.plugins.some((p) => p.id === "skill.attack-surface-scan" && p.effective && p.override === true))
  resetDemoPluginStore()
})

test("Given declarative L2+ Skill plugins, When resolving sidebar, target tabs, home widgets, and cross-user isolation, Then owner_is_me, target_modes, and 12-widget cap hold", async () => {
  const makeL2 = (slug, opts = {}) => ({
    id: `skill.${slug}`,
    tier: "declarative",
    skill_slug: slug,
    scope: opts.scope ?? "system",
    owner_is_me: opts.ownerIsMe ?? false,
    skill_enabled: opts.skillEnabled ?? true,
    name: opts.name ?? slug,
    description: "desc",
    category: "ops",
    effective: opts.effective ?? true,
    scenario_default: true,
    override: null,
    dependencies: { skills: [], mcp: [] },
    manifest: {
      api_version: "doops.sh/v1alpha1",
      kind: "SkillPlugin",
      plugin: { id: `skill.${slug}`, name: opts.name ?? slug, category: "ops", icon: "cpu", scenarios: [] },
      capabilities: { target_modes: opts.targetModes ?? ["kubernetes", "linux"] },
      ui: {
        slots: {
          sidebar: { enabled: true, label: opts.name ?? slug, group: "extensions", order: opts.order ?? 10 },
          target_tab: { enabled: true, label: opts.name ?? slug, order: opts.order ?? 10 },
          home_widget: { enabled: opts.homeWidget ?? true, title: opts.name ?? slug, span: 1, metrics: [{ key: "m1", label: "M1" }] },
        },
        inputs: [],
        presets: [],
        views: [],
      },
    },
  })

  const state = buildDefaultPluginState({
    scenarioId: "ai-compute-cluster",
    extraPlugins: [
      makeL2("gpu-governance", { name: "GPU 算力治理", order: 10, targetModes: ["kubernetes", "linux"] }),
      makeL2("my-custom", { scope: "custom", ownerIsMe: true, name: "我的自定义插件", order: 20 }),
      makeL2("other-user-custom", { scope: "custom", ownerIsMe: false, name: "他人自定义插件", order: 30 }),
      makeL2("disabled-l2", { scope: "system", effective: false, name: "已停用插件", order: 40 }),
      makeL2("k8s-only-l2", { scope: "system", targetModes: ["kubernetes"], name: "仅K8s插件", order: 50 }),
      makeL2("w6", { order: 60 }),
      makeL2("w7", { order: 70 }),
      makeL2("w8", { order: 80 }),
      makeL2("w9", { order: 90 }),
      makeL2("w10", { order: 100 }),
      makeL2("w11", { order: 110 }),
      makeL2("w12", { order: 120 }),
      makeL2("w13", { order: 130 }),
      makeL2("w14", { order: 140 }),
      makeL2("w15", { order: 150 }),
    ],
  })

  // §3.2 #4: Other user's custom L2 plugin MUST NOT appear in sidebar / target tabs / home widgets!
  const other = state.plugins.find((p) => p.id === "skill.other-user-custom")
  assert.equal(isDeclarativePluginVisibleInNav(other), false)

  const sidebarL2 = resolveDeclarativeSidebarPlugins(state)
  assert.ok(sidebarL2.some((p) => p.id === "skill.gpu-governance"))
  assert.ok(sidebarL2.some((p) => p.id === "skill.my-custom"))
  assert.ok(!sidebarL2.some((p) => p.id === "skill.other-user-custom"), "other user custom L2 must never pollute sidebar")
  assert.ok(!sidebarL2.some((p) => p.id === "skill.disabled-l2"))

  // Sidebar items mount L2 into extensions view=plugin destination.
  const navItems = resolveConsoleNavItems({ isAdmin: true, hasTarget: true, pluginState: state })
  const gpuNav = navItems.find((item) => item.entry.id === "skill.gpu-governance")
  assert.deepEqual(gpuNav?.entry.destination, { view: "plugin", pluginId: "skill.gpu-governance" })
  assert.equal(gpuNav?.icon, "cpu")

  // Target mode filter hides k8s-only L2 on linux target, shows on kubernetes target.
  const linuxTabs = resolveDeclarativeTargetTabPlugins(state, "linux")
  assert.ok(!linuxTabs.some((p) => p.id === "skill.k8s-only-l2"))
  const k8sTabs = resolveDeclarativeTargetTabPlugins(state, "kubernetes")
  assert.ok(k8sTabs.some((p) => p.id === "skill.k8s-only-l2"))

  // Unbounded pluggable home widgets: zero silent 6/12 truncation — all enabled visible widgets are preserved.
  const homeWidgets = resolveHomeWidgetPlugins(state)
  assert.ok(homeWidgets.length > 12, `expected >12 visible widgets preserved without silent truncation, got ${homeWidgets.length}`)
  assert.ok(!homeWidgets.some((p) => p.id === "skill.other-user-custom"), "other user custom L2 must never enter home widgets")
  assert.ok(!homeWidgets.some((p) => p.id === "skill.disabled-l2"))

  // Route & navigation guard for disabled L2 plugin.
  const disabledNav = canNavigateDestination({ view: "plugin", pluginId: "skill.disabled-l2" }, state)
  assert.equal(disabledNav.ok, false)
  assert.ok(disabledNav.reason.includes("已停用插件"))
  assert.equal(formatPluginName("skill.gpu-governance", state), "GPU 算力治理")
  const ownerRoute = pluginOwningRoute(parseConsoleSearch("?view=plugin&plugin=skill.gpu-governance"), state)
  assert.equal(ownerRoute?.id, "skill.gpu-governance")
  const ownerTab = pluginOwningRoute(parseConsoleSearch("?view=console&tab=plugin:skill.gpu-governance"), state)
  assert.equal(ownerTab?.id, "skill.gpu-governance")

  // Latest metrics path, auto-chunking batches, and multi-plugin demo fetch.
  assert.equal(
    buildMonitorLatestMetricsPath(["gpu-governance", "host-security"], ["gpu_count", "sec_risk_total"]),
    "metrics/latest?skills=gpu-governance%2Chost-security&keys=gpu_count%2Csec_risk_total",
  )
  assert.equal(
    buildMonitorLatestMetricPairsPath([
      { skill: "gpu-governance", key: "gpu_util_max_pct" },
      { skill: "attack-surface-scan", key: "surface_risk_total" },
    ]),
    "metrics/latest?pairs=gpu-governance%3Agpu_util_max_pct%2Cattack-surface-scan%3Asurface_risk_total",
  )
  const manyPairs = Array.from({ length: 70 }, (_, idx) => ({ skill: `skill-${idx % 18}`, key: `metric_${idx}` }))
  const chunks = chunkMonitorMetricPairs(manyPairs, 32)
  assert.deepEqual(chunks.map((c) => c.length), [32, 32, 6])

  const demoLatest = await fetchMonitorLatestMetrics(
    { gateway: "demo://local", token: "demo", username: "demo", demo: true, isAdmin: true },
    { skills: ["gpu-governance"], keys: ["gpu_count"] },
  )
  assert.equal(demoLatest.items.length, 1)
  assert.equal(demoLatest.items[0].skill, "gpu-governance")

  const demoBatched = await fetchMonitorLatestMetricsForWidgets(
    { gateway: "demo://local", token: "demo", username: "demo", demo: true, isAdmin: true },
    [
      { skill: "inspect-vm-host", key: "cpu_percent" },
      { skill: "gpu-governance", key: "gpu_count" },
      { skill: "inspect-k8s-cluster", key: "k8s_pods_abnormal" },
      { skill: "host-security", key: "sec_risk_total" },
      { skill: "inspect-container-runtime", key: "containers_running" },
      { skill: "inspect-app-web", key: "tcp_listen_ports" },
      { skill: "attack-surface-scan", key: "surface_missing_csp" },
      { skill: "inspect-database", key: "active_connections" },
    ],
  )
  assert.equal(demoBatched.items.length, 8)
  assert.equal(demoBatched.partialErrors.length, 0)
})
