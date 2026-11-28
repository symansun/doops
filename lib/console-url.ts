import {
  ADMIN_TABS,
  CONSOLE_HOME,
  DEPLOY_SECTIONS,
  PLUGIN_TAB_PATTERN,
  TARGET_TABS,
  assetKinds,
  type AdminTab,
  type AgentOverviewSubTab,
  type AssetArea,
  type AutomationSubTab,
  type CatalogKind,
  type ConsoleView,
  type DeploySection,
  type PluginCenterSubTab,
  type ResourceMonitorSubTab,
  type TargetTab,
} from "./console-views.ts"
import type { AssetPage } from "@/components/asset-inventory"

export interface ConsoleRouteState {
  view: ConsoleView
  tab: TargetTab
  overviewSub?: AgentOverviewSubTab
  tasksSub?: AutomationSubTab
  tasksRunId?: string
  adminTab?: AdminTab
  assetPage: AssetPage
  deploySection: DeploySection
  catalogKind: CatalogKind
  pluginId?: string
}

const VALID_VIEWS = new Set<ConsoleView>([CONSOLE_HOME, "console", "assets", "deploy", "admin", "catalog", "plugin"])
const VALID_TABS = new Set<string>(TARGET_TABS)
const VALID_ADMIN_TABS = new Set<string>(ADMIN_TABS.map((item) => item.id))
export const DEFAULT_ADMIN_TAB: AdminTab = "instances"
const VALID_AREAS = new Set<AssetArea>(["plugins", "situation", "monitor", "inspection", "skills", "mcp", "board", "machines", "clusters", "systems"])
const VALID_PLUGIN_SUBS = new Set<PluginCenterSubTab>(["scenarios", "skills", "mcp"])
const VALID_MONITOR_SUBS = new Set<ResourceMonitorSubTab>(["fleet", "target"])
const VALID_OVERVIEW_SUBS = new Set<AgentOverviewSubTab>(["summary", "manifest", "workspace"])
const VALID_AUTOMATION_SUBS = new Set<AutomationSubTab>(["schedules", "runs", "bg"])
const VALID_DEPLOY = new Set<DeploySection>(DEPLOY_SECTIONS.map((item) => item.id))
const VALID_CATALOG = new Set<string>(Object.keys(assetKinds))
const SAFE_ID = /^[A-Za-z0-9._:-]{1,128}$/
const SAFE_TARGET_SEG = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/
const SAFE_PLUGIN_ID = /^(doops\.[a-z][a-z0-9-]{1,30}|skill\.[a-z0-9][a-z0-9-]{0,62})$/

function isSafeTargetKey(value: string): boolean {
  const parts = value.split("/")
  return parts.length === 2 && SAFE_TARGET_SEG.test(parts[0]) && SAFE_TARGET_SEG.test(parts[1])
}

export const DEFAULT_CONSOLE_ROUTE: ConsoleRouteState = {
  view: CONSOLE_HOME,
  tab: "overview",
  assetPage: { section: "board" },
  deploySection: "onboard",
  catalogKind: "database",
}

export function parseConsoleSearch(search: string): ConsoleRouteState {
  const params = new URLSearchParams(search)
  const rawView = params.get("view") as ConsoleView | null
  const view: ConsoleView = rawView && VALID_VIEWS.has(rawView) ? rawView : CONSOLE_HOME

  const rawTab = params.get("tab")
  const tab: TargetTab =
    rawTab && (VALID_TABS.has(rawTab) || PLUGIN_TAB_PATTERN.test(rawTab))
      ? (rawTab as TargetTab)
      : "overview"
  const adminTab: AdminTab | undefined =
    view === "admin" && rawTab && VALID_ADMIN_TABS.has(rawTab)
      ? (rawTab as AdminTab)
      : undefined
  const rawOverviewSub = params.get("sub") as AgentOverviewSubTab | null
  const overviewSub: AgentOverviewSubTab | undefined =
    view === "console" && (tab === "overview" || tab === "assets")
      ? (rawOverviewSub && VALID_OVERVIEW_SUBS.has(rawOverviewSub)
          ? rawOverviewSub
          : tab === "assets"
            ? "manifest"
            : undefined)
      : undefined

  const rawTasksSub = params.get("sub") as AutomationSubTab | null
  const rawTasksRun = params.get("run") ?? ""
  const tasksRunId =
    view === "console" && tab === "tasks" && SAFE_ID.test(rawTasksRun) ? rawTasksRun : undefined
  const tasksSub: AutomationSubTab | undefined =
    view === "console" && tab === "tasks"
      ? (rawTasksSub && VALID_AUTOMATION_SUBS.has(rawTasksSub)
          ? rawTasksSub
          : tasksRunId
            ? "runs"
            : undefined)
      : undefined

  const rawDeploy = params.get("section") as DeploySection | null
  const deploySection: DeploySection = rawDeploy && VALID_DEPLOY.has(rawDeploy) ? rawDeploy : "onboard"

  const rawKind = params.get("kind")
  const catalogKind: CatalogKind =
    rawKind && VALID_CATALOG.has(rawKind) && rawKind !== "k8s" ? (rawKind as CatalogKind) : "database"

  const rawPluginId = params.get("plugin") ?? ""
  const pluginId = SAFE_PLUGIN_ID.test(rawPluginId) ? rawPluginId : undefined

  const rawArea = params.get("area") as AssetArea | null
  const area: AssetArea = rawArea && VALID_AREAS.has(rawArea) ? rawArea : "board"
  let assetPage: AssetPage
  if (area === "plugins") {
    const rawSub = params.get("sub") as PluginCenterSubTab | null
    const sub: PluginCenterSubTab = rawSub && VALID_PLUGIN_SUBS.has(rawSub) ? rawSub : "scenarios"
    const slug = params.get("skill") ?? ""
    assetPage = sub === "skills" && SAFE_ID.test(slug)
      ? { section: "plugins", sub, slug }
      : { section: "plugins", sub }
  } else if (area === "situation") {
    const clusterId = params.get("cluster") ?? ""
    assetPage = SAFE_ID.test(clusterId) ? { section: "situation", clusterId } : { section: "situation" }
  } else if (area === "monitor") {
    const rawSub = params.get("sub") as ResourceMonitorSubTab | null
    const sub: ResourceMonitorSubTab = rawSub && VALID_MONITOR_SUBS.has(rawSub) ? rawSub : "fleet"
    const targetKey = params.get("target") ?? ""
    const clusterId = params.get("cluster") ?? ""
    assetPage = {
      section: "monitor",
      sub,
      ...(isSafeTargetKey(targetKey) ? { targetKey } : {}),
      ...(SAFE_ID.test(clusterId) ? { clusterId } : {}),
    }
  } else if (area === "inspection") {
    const runId = params.get("run") ?? ""
    assetPage = SAFE_ID.test(runId) ? { section: "inspection", runId } : { section: "inspection" }
  } else if (area === "skills") {
    const slug = params.get("skill") ?? ""
    assetPage = SAFE_ID.test(slug) ? { section: "skills", slug } : { section: "skills" }
  } else if (area === "mcp") {
    assetPage = { section: "mcp" }
  } else if (area === "board") {
    const clusterId = params.get("cluster") ?? ""
    assetPage = SAFE_ID.test(clusterId) ? { section: "board", clusterId } : { section: "board" }
  } else if (area === "machines") {
    const machineId = params.get("machine") ?? ""
    assetPage = SAFE_ID.test(machineId) ? { section: "machines", machineId } : { section: "machines" }
  } else if (area === "clusters") {
    assetPage = { section: "clusters", query: "", expanded: [] }
  } else {
    assetPage = { section: "systems" }
  }

  if (view === "plugin" && !pluginId) {
    return { view: CONSOLE_HOME, tab, assetPage, deploySection, catalogKind }
  }

  return {
    view,
    tab,
    ...(overviewSub && overviewSub !== "summary" ? { overviewSub } : {}),
    ...(tasksSub && tasksSub !== "schedules" ? { tasksSub } : {}),
    ...(tasksRunId ? { tasksRunId } : {}),
    ...(adminTab && adminTab !== DEFAULT_ADMIN_TAB ? { adminTab } : {}),
    assetPage,
    deploySection,
    catalogKind,
    ...(pluginId ? { pluginId } : {}),
  }
}

// Preserves runtime flags (`demo`, `role`) while serializing only non-sensitive UI view state.
export function buildConsoleSearch(currentSearch: string, state: ConsoleRouteState): string {
  const current = new URLSearchParams(currentSearch)
  const next = new URLSearchParams()

  const demo = current.get("demo")
  const role = current.get("role")
  if (demo !== null) next.set("demo", demo)
  if (role !== null) next.set("role", role)

  if (state.view === "console") {
    next.set("view", "console")
    if (state.tab !== "overview") next.set("tab", state.tab)
    if (state.tab === "overview" && state.overviewSub && state.overviewSub !== "summary") {
      next.set("sub", state.overviewSub)
    }
    if (state.tab === "tasks") {
      if (state.tasksSub && state.tasksSub !== "schedules") {
        next.set("sub", state.tasksSub)
      }
      if (state.tasksRunId) {
        next.set("run", state.tasksRunId)
      }
    }
  } else if (state.view === "catalog") {
    next.set("view", "catalog")
    next.set("kind", state.catalogKind)
  } else if (state.view === "deploy") {
    next.set("view", "deploy")
    next.set("section", state.deploySection)
  } else if (state.view === "admin") {
    next.set("view", "admin")
    if (state.adminTab && state.adminTab !== DEFAULT_ADMIN_TAB) {
      next.set("tab", state.adminTab)
    }
  } else if (state.view === "plugin" && state.pluginId) {
    next.set("view", "plugin")
    next.set("plugin", state.pluginId)
  } else if (state.view === "assets") {
    next.set("view", "assets")
    next.set("area", state.assetPage.section)
    if (state.assetPage.section === "plugins") {
      if (state.assetPage.sub && state.assetPage.sub !== "scenarios") {
        next.set("sub", state.assetPage.sub)
      }
      if (state.assetPage.sub === "skills" && state.assetPage.slug) {
        next.set("skill", state.assetPage.slug)
      }
    } else if (state.assetPage.section === "monitor") {
      if (state.assetPage.sub && state.assetPage.sub !== "fleet") {
        next.set("sub", state.assetPage.sub)
      }
      if (state.assetPage.targetKey) {
        next.set("target", state.assetPage.targetKey)
      }
      if (state.assetPage.clusterId) {
        next.set("cluster", state.assetPage.clusterId)
      }
    } else if ((state.assetPage.section === "board" || state.assetPage.section === "situation") && state.assetPage.clusterId) {
      next.set("cluster", state.assetPage.clusterId)
    } else if (state.assetPage.section === "inspection" && state.assetPage.runId) {
      next.set("run", state.assetPage.runId)
    } else if (state.assetPage.section === "skills" && state.assetPage.slug) {
      next.set("skill", state.assetPage.slug)
    } else if (state.assetPage.section === "machines" && "machineId" in state.assetPage && state.assetPage.machineId) {
      next.set("machine", state.assetPage.machineId)
    }
  }

  const query = next.toString()
  return query ? `?${query}` : ""
}

export function parseAdminTab(search: string): AdminTab {
  const raw = new URLSearchParams(search).get("tab")
  return raw && VALID_ADMIN_TABS.has(raw) ? (raw as AdminTab) : DEFAULT_ADMIN_TAB
}

// 只保留运行时标记（demo、role），序列化管理页签状态。
export function buildAdminSearch(currentSearch: string, tab: AdminTab): string {
  const current = new URLSearchParams(currentSearch)
  const next = new URLSearchParams()

  const demo = current.get("demo")
  const role = current.get("role")
  if (demo !== null) next.set("demo", demo)
  if (role !== null) next.set("role", role)

  if (tab !== DEFAULT_ADMIN_TAB) next.set("tab", tab)

  const query = next.toString()
  return query ? `?${query}` : ""
}

export function buildAdminRedirectUrl(currentSearch: string, adminTab?: AdminTab): string {
  const resolvedTab = adminTab ?? parseAdminTab(currentSearch)
  return `/console/admin${buildAdminSearch(currentSearch, resolvedTab)}`
}
