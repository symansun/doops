import {
  ADMIN_TABS,
  CONSOLE_HOME,
  DEPLOY_SECTIONS,
  TARGET_TABS,
  assetKinds,
  type AdminTab,
  type AssetArea,
  type CatalogKind,
  type ConsoleView,
  type DeploySection,
  type TargetTab,
} from "./console-views.ts"
import type { AssetPage } from "@/components/asset-inventory"

export interface ConsoleRouteState {
  view: ConsoleView
  tab: TargetTab
  assetPage: AssetPage
  deploySection: DeploySection
  catalogKind: CatalogKind
}

const VALID_VIEWS = new Set<ConsoleView>([CONSOLE_HOME, "console", "assets", "deploy", "catalog"])
const VALID_TABS = new Set<string>(TARGET_TABS)
const VALID_AREAS = new Set<AssetArea>(["board", "machines", "clusters", "systems", "scope"])
const VALID_DEPLOY = new Set<DeploySection>(DEPLOY_SECTIONS.map((item) => item.id))
const VALID_CATALOG = new Set<string>(Object.keys(assetKinds))
const SAFE_ID = /^[A-Za-z0-9._:-]{1,128}$/

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
  const tab: TargetTab = rawTab && VALID_TABS.has(rawTab) ? (rawTab as TargetTab) : "overview"

  const rawDeploy = params.get("section") as DeploySection | null
  const deploySection: DeploySection = rawDeploy && VALID_DEPLOY.has(rawDeploy) ? rawDeploy : "onboard"

  const rawKind = params.get("kind")
  const catalogKind: CatalogKind =
    rawKind && VALID_CATALOG.has(rawKind) && rawKind !== "k8s" ? (rawKind as CatalogKind) : "database"

  const rawArea = params.get("area") as AssetArea | null
  const area: AssetArea = rawArea && VALID_AREAS.has(rawArea) ? rawArea : "board"
  let assetPage: AssetPage
  if (area === "board") {
    const clusterId = params.get("cluster") ?? ""
    assetPage = SAFE_ID.test(clusterId) ? { section: "board", clusterId } : { section: "board" }
  } else if (area === "machines") {
    const machineId = params.get("machine") ?? ""
    assetPage = SAFE_ID.test(machineId) ? { section: "machines", machineId } : { section: "machines" }
  } else if (area === "clusters") {
    assetPage = { section: "clusters", query: "", expanded: [] }
  } else if (area === "scope") {
    assetPage = { section: "scope" }
  } else {
    assetPage = { section: "systems" }
  }

  return { view, tab, assetPage, deploySection, catalogKind }
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
  } else if (state.view === "catalog") {
    next.set("view", "catalog")
    next.set("kind", state.catalogKind)
  } else if (state.view === "deploy") {
    next.set("view", "deploy")
    next.set("section", state.deploySection)
  } else if (state.view === "assets") {
    next.set("view", "assets")
    next.set("area", state.assetPage.section)
    if (state.assetPage.section === "board" && state.assetPage.clusterId) {
      next.set("cluster", state.assetPage.clusterId)
    } else if (state.assetPage.section === "machines" && "machineId" in state.assetPage && state.assetPage.machineId) {
      next.set("machine", state.assetPage.machineId)
    }
  }

  const query = next.toString()
  return query ? `?${query}` : ""
}

const VALID_ADMIN_TABS = new Set<string>(ADMIN_TABS.map((item) => item.id))
export const DEFAULT_ADMIN_TAB: AdminTab = "instances"

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
