import {
  ALL_CONSOLE_NAV_ENTRIES,
  CONSOLE_HOME,
  type AssetArea,
  type CatalogKind,
  type ConsoleNavEntry,
  type DeploySection,
  type TargetTab,
} from "../console-views.ts"
import type { ConsoleRouteState } from "../console-url.ts"
import {
  BUILTIN_PLUGIN_CATALOG,
  DEFAULT_PLUGIN_SCENARIO_ID,
  type BuiltinPluginDefinition,
  type PluginOwnerRef,
  type PluginScenarioDefinition,
  type PluginStateResponse,
  type PluginView,
  type SkillPluginTargetMode,
} from "./types.ts"

const PLUGIN_BY_ID = new Map<string, BuiltinPluginDefinition>(
  BUILTIN_PLUGIN_CATALOG.plugins.map((plugin) => [plugin.id, plugin]),
)

const PLUGIN_BY_NAV_ID = new Map<string, BuiltinPluginDefinition>()
const PLUGIN_BY_TARGET_TAB = new Map<string, BuiltinPluginDefinition>()
for (const plugin of BUILTIN_PLUGIN_CATALOG.plugins) {
  for (const navId of plugin.nav_ids) PLUGIN_BY_NAV_ID.set(navId, plugin)
  for (const tab of plugin.target_tabs) PLUGIN_BY_TARGET_TAB.set(tab, plugin)
}

const SCENARIO_BY_ID = new Map<string, PluginScenarioDefinition>(
  BUILTIN_PLUGIN_CATALOG.scenarios.map((scenario) => [scenario.id, scenario]),
)

const CORE_ID_SET = new Set<string>(BUILTIN_PLUGIN_CATALOG.core_ids)

export function findBuiltinPluginMeta(pluginId: string): BuiltinPluginDefinition | null {
  return PLUGIN_BY_ID.get(pluginId) ?? null
}

export function findScenarioMeta(scenarioId: string): PluginScenarioDefinition | null {
  return SCENARIO_BY_ID.get(scenarioId) ?? null
}

export function isCoreEntryId(id: string): boolean {
  return CORE_ID_SET.has(id)
}

export function findPluginView(
  state: PluginStateResponse | null | undefined,
  pluginId: string | undefined,
): PluginView | null {
  if (!state || !pluginId) return null
  return state.plugins.find((plugin) => plugin.id === pluginId) ?? null
}

export function formatPluginName(
  pluginId: string,
  state?: PluginStateResponse | null,
): string {
  const fromState = state?.plugins.find((p) => p.id === pluginId)
  if (fromState?.name) return fromState.name
  return PLUGIN_BY_ID.get(pluginId)?.name ?? pluginId
}

export function formatScenarioName(scenarioId: string): string {
  return SCENARIO_BY_ID.get(scenarioId)?.name ?? scenarioId
}

export function normalizeTargetMode(rawMode?: string): SkillPluginTargetMode | null {
  const value = (rawMode ?? "").trim().toLowerCase()
  if (!value) return null
  if (value === "k8s" || value === "kubernetes" || value.includes("kube")) return "kubernetes"
  if (value === "docker" || value.includes("container")) return "docker"
  if (value === "linux" || value === "host" || value === "standalone" || value === "ssh" || value === "vm") return "linux"
  return null
}

export function targetMatchesPluginModes(
  targetMode: string | undefined,
  modes?: SkillPluginTargetMode[],
): boolean {
  if (!modes || modes.length === 0) return true
  const normalized = normalizeTargetMode(targetMode)
  if (!normalized) return true
  return modes.includes(normalized)
}

// §3.2 #4: Custom L2 plugins are mounted into Sidebar / Home / Target tabs ONLY
// for their owner (owner_is_me === true), never polluting admins' navigation with
// other users' custom Skill plugins.
export function isDeclarativePluginVisibleInNav(plugin: PluginView): boolean {
  if (plugin.tier !== "declarative" || !plugin.effective || !plugin.manifest) return false
  return plugin.scope === "system" || plugin.owner_is_me === true
}

export function resolveDeclarativeSidebarPlugins(
  state: PluginStateResponse | null | undefined,
): PluginView[] {
  if (!state) return []
  return state.plugins
    .filter((plugin) => isDeclarativePluginVisibleInNav(plugin) && Boolean(plugin.manifest?.ui.slots.sidebar?.enabled))
    .sort((a, b) => {
      const orderA = a.manifest?.ui.slots.sidebar?.order ?? 100
      const orderB = b.manifest?.ui.slots.sidebar?.order ?? 100
      if (orderA !== orderB) return orderA - orderB
      return a.id.localeCompare(b.id)
    })
}

export function resolveDeclarativeTargetTabPlugins(
  state: PluginStateResponse | null | undefined,
  targetMode?: string,
): PluginView[] {
  if (!state) return []
  return state.plugins
    .filter((plugin) => {
      if (!isDeclarativePluginVisibleInNav(plugin)) return false
      if (!plugin.manifest?.ui.slots.target_tab?.enabled) return false
      return targetMatchesPluginModes(targetMode, plugin.manifest.capabilities?.target_modes)
    })
    .sort((a, b) => {
      const orderA = a.manifest?.ui.slots.target_tab?.order ?? 100
      const orderB = b.manifest?.ui.slots.target_tab?.order ?? 100
      if (orderA !== orderB) return orderA - orderB
      return a.id.localeCompare(b.id)
    })
}

export function resolveHomeWidgetPlugins(
  state: PluginStateResponse | null | undefined,
): PluginView[] {
  if (!state) return []
  return state.plugins
    .filter((plugin) => isDeclarativePluginVisibleInNav(plugin) && Boolean(plugin.manifest?.ui.slots.home_widget?.enabled))
    .sort((a, b) => {
      const orderA = a.manifest?.ui.slots.sidebar?.order ?? 100
      const orderB = b.manifest?.ui.slots.sidebar?.order ?? 100
      if (orderA !== orderB) return orderA - orderB
      return a.id.localeCompare(b.id)
    })
}

export function buildDefaultPluginState(options: {
  supported?: boolean
  configured?: boolean
  scenarioId?: string
  overrides?: Record<string, boolean>
  revision?: number
  updatedBy?: string
  updatedAtMs?: number
  staleOverrides?: string[]
  extraPlugins?: PluginView[]
} = {}): PluginStateResponse {
  const scenarioId = SCENARIO_BY_ID.has(options.scenarioId ?? "")
    ? (options.scenarioId as string)
    : DEFAULT_PLUGIN_SCENARIO_ID
  const scenario = SCENARIO_BY_ID.get(scenarioId)!
  const defaultSet = new Set(scenario.defaults)
  const overrides = options.overrides ?? {}

  let customCount = 0
  const plugins: PluginView[] = BUILTIN_PLUGIN_CATALOG.plugins.map((plugin) => {
    const scenarioDefault = defaultSet.has(plugin.id)
    const hasOverride = Object.prototype.hasOwnProperty.call(overrides, plugin.id)
    const effective = hasOverride ? Boolean(overrides[plugin.id]) : scenarioDefault
    const override = hasOverride ? Boolean(overrides[plugin.id]) : null
    if (hasOverride && effective !== scenarioDefault) customCount++
    return {
      id: plugin.id,
      tier: plugin.tier,
      name: plugin.name,
      description: plugin.description,
      category: plugin.category,
      nav_ids: [...plugin.nav_ids],
      target_tabs: [...plugin.target_tabs],
      requires_kubernetes: plugin.requires_kubernetes,
      effective,
      scenario_default: scenarioDefault,
      override,
      dependencies: {
        skills: plugin.required_skills.map((slug) => ({ slug, state: "ready" })),
        mcp: plugin.required_mcp.map((slug) => ({ slug, state: "ready" })),
      },
    }
  })

  if (options.extraPlugins?.length) {
    for (const extra of options.extraPlugins) {
      if (extra.override !== null && extra.override !== extra.scenario_default) {
        customCount++
      }
      plugins.push(extra)
    }
  }

  return {
    supported: options.supported ?? true,
    configured: options.configured ?? false,
    scenario_id: scenarioId,
    revision: options.revision ?? 0,
    customized: customCount > 0,
    updated_by: options.updatedBy ?? "",
    updated_at_ms: options.updatedAtMs ?? 0,
    scenarios: BUILTIN_PLUGIN_CATALOG.scenarios,
    plugins,
    stale_overrides: options.staleOverrides ?? [],
  }
}

export function isPluginEnabled(state: PluginStateResponse | null | undefined, pluginId: string): boolean {
  if (!state) return true
  const found = state.plugins.find((plugin) => plugin.id === pluginId)
  if (!found) {
    // Unknown declarative L2 plugin is never enabled.
    return !pluginId.startsWith("skill.")
  }
  return found.effective
}

function toOwnerRef(plugin: BuiltinPluginDefinition | PluginView): PluginOwnerRef {
  return {
    id: plugin.id,
    name: plugin.name,
    description: plugin.description,
    tier: plugin.tier,
    scope: "scope" in plugin ? plugin.scope : undefined,
    skill_slug: "skill_slug" in plugin ? plugin.skill_slug : undefined,
    skill_enabled: "skill_enabled" in plugin ? plugin.skill_enabled : undefined,
  }
}

function resolvePluginOwnerById(
  pluginId: string,
  state?: PluginStateResponse | null,
): PluginOwnerRef | null {
  const fromState = findPluginView(state, pluginId)
  if (fromState) return toOwnerRef(fromState)
  const builtin = PLUGIN_BY_ID.get(pluginId)
  if (builtin) return toOwnerRef(builtin)
  if (pluginId.startsWith("skill.")) {
    return {
      id: pluginId,
      name: pluginId,
      description: "未找到该声明式 Skill 插件，或当前账号无权访问。",
      tier: "declarative",
      skill_slug: pluginId.slice("skill.".length),
    }
  }
  return null
}

export function pluginOwningNavEntry(navId: string): BuiltinPluginDefinition | null {
  return PLUGIN_BY_NAV_ID.get(navId) ?? null
}

export function pluginOwningTargetTab(
  tab: TargetTab,
  state?: PluginStateResponse | null,
): PluginOwnerRef | null {
  if (tab.startsWith("plugin:skill.")) {
    return resolvePluginOwnerById(tab.slice("plugin:".length), state)
  }
  const builtin = PLUGIN_BY_TARGET_TAB.get(tab)
  return builtin ? toOwnerRef(builtin) : null
}

export function pluginOwningAssetArea(area: AssetArea): PluginOwnerRef | null {
  switch (area) {
    case "situation":
    case "monitor":
    case "board": {
      const p = PLUGIN_BY_ID.get("doops.inspection")
      return p ? toOwnerRef(p) : null
    }
    case "inspection": {
      const p = PLUGIN_BY_ID.get("doops.automation")
      return p ? toOwnerRef(p) : null
    }
    case "clusters": {
      const p = PLUGIN_BY_ID.get("doops.k8s")
      return p ? toOwnerRef(p) : null
    }
    case "machines":
    case "systems": {
      const p = PLUGIN_BY_ID.get("doops.cmdb")
      return p ? toOwnerRef(p) : null
    }
    case "plugins":
    case "skills":
    case "mcp":
      return null
  }
}

export function pluginOwningCatalogKind(kind: CatalogKind): PluginOwnerRef | null {
  let id: string | null = null
  switch (kind) {
    case "database": id = "doops.database"; break
    case "website": id = "doops.website"; break
    case "k8s": id = "doops.k8s"; break
    case "image": id = "doops.image"; break
    case "credentials": return null
  }
  const p = id ? PLUGIN_BY_ID.get(id) : null
  return p ? toOwnerRef(p) : null
}

export function pluginOwningDeploySection(section: DeploySection): PluginOwnerRef | null {
  switch (section) {
    case "workload":
    case "cicd": {
      const p = PLUGIN_BY_ID.get("doops.delivery")
      return p ? toOwnerRef(p) : null
    }
    case "onboard":
    case "upgrade":
      return null
  }
}

export function pluginOwningDestination(
  dest: ConsoleNavEntry["destination"],
  state?: PluginStateResponse | null,
): PluginOwnerRef | null {
  switch (dest.view) {
    case CONSOLE_HOME:
    case "admin":
      return null
    case "console":
      return pluginOwningTargetTab(dest.tab, state)
    case "assets":
      return pluginOwningAssetArea(dest.area)
    case "catalog":
      return pluginOwningCatalogKind(dest.kind)
    case "deploy":
      return pluginOwningDeploySection(dest.section)
    case "plugin":
      return resolvePluginOwnerById(dest.pluginId, state)
  }
}

export function pluginOwningRoute(
  route: ConsoleRouteState,
  state?: PluginStateResponse | null,
): PluginOwnerRef | null {
  switch (route.view) {
    case CONSOLE_HOME:
    case "admin":
      return null
    case "console":
      return pluginOwningTargetTab(route.tab, state)
    case "assets":
      return pluginOwningAssetArea(route.assetPage.section)
    case "catalog":
      return pluginOwningCatalogKind(route.catalogKind)
    case "deploy":
      return pluginOwningDeploySection(route.deploySection)
    case "plugin":
      return route.pluginId ? resolvePluginOwnerById(route.pluginId, state) : null
  }
}

export interface ResolvedNavItem {
  entry: ConsoleNavEntry
  pluginId: string | null
  icon?: string
  disabled: boolean
  disabledReason?: string
}

export interface ResolveNavOptions {
  isAdmin: boolean
  hasTarget: boolean
  kubernetesAvailable?: boolean
  pluginState?: PluginStateResponse | null
  // When false (cold boot with no cache yet), only Core entries are returned so
  // the sidebar can show clean placeholders without flashing full-to-narrow.
  pluginsReady?: boolean
}

export function resolveConsoleNavItems({
  isAdmin,
  hasTarget,
  kubernetesAvailable,
  pluginState,
  pluginsReady = true,
}: ResolveNavOptions): ResolvedNavItem[] {
  const items: ResolvedNavItem[] = []
  for (const entry of ALL_CONSOLE_NAV_ENTRIES) {
    if (entry.admin && !isAdmin) continue
    const owner = pluginOwningNavEntry(entry.id)
    if (owner) {
      if (!pluginsReady) continue
      if (pluginState && !isPluginEnabled(pluginState, owner.id)) continue
    }
    let disabled = false
    let disabledReason: string | undefined
    if (entry.needsTarget && !hasTarget) {
      disabled = true
      disabledReason = "请先选择在线 Agent"
    } else if (
      kubernetesAvailable === false &&
      (entry.id === "k8s" || entry.id === "deploy-workload")
    ) {
      disabled = true
      disabledReason = "当前目标不支持 Kubernetes"
    }
    items.push({
      entry,
      pluginId: owner?.id ?? null,
      disabled,
      disabledReason,
    })
  }

  // Phase 2: mount enabled declarative L2 plugins into 「场景扩展」 (extensions).
  if (pluginsReady && pluginState) {
    for (const plugin of resolveDeclarativeSidebarPlugins(pluginState)) {
      const label = plugin.manifest?.ui.slots.sidebar?.label || plugin.name
      items.push({
        entry: {
          id: plugin.id,
          label,
          destination: { view: "plugin", pluginId: plugin.id },
        },
        pluginId: plugin.id,
        icon: plugin.manifest?.plugin.icon || "layers",
        disabled: false,
      })
    }
  }

  return items
}

export type NavigationCheckResult =
  | { ok: true }
  | { ok: false; reason: string; pluginId?: string }

// Unified cross-page navigation guard used by AssetInventory, ResourceCatalog,
// AutomationPanel, and Dashboard widgets so jumps into disabled plugins fail
// gracefully with a clear disabled reason instead of landing on dead views.
export function canNavigateDestination(
  dest: ConsoleNavEntry["destination"],
  pluginState: PluginStateResponse | null | undefined,
  options: { kubernetesAvailable?: boolean; isAdmin?: boolean; targetMode?: string } = {},
): NavigationCheckResult {
  if (dest.view === "admin" && options.isAdmin === false) {
    return { ok: false, reason: "仅平台管理员可访问平台管理" }
  }
  const owner = pluginOwningDestination(dest, pluginState)
  if (owner && pluginState && !isPluginEnabled(pluginState, owner.id)) {
    return {
      ok: false,
      reason: `「${owner.name}」插件当前已停用`,
      pluginId: owner.id,
    }
  }
  if (owner?.tier === "declarative" && pluginState && dest.view === "console") {
    const view = findPluginView(pluginState, owner.id)
    if (view?.manifest && !targetMatchesPluginModes(options.targetMode, view.manifest.capabilities?.target_modes)) {
      return {
        ok: false,
        reason: `当前目标运行模式不支持「${owner.name}」插件`,
        pluginId: owner.id,
      }
    }
  }
  if (options.kubernetesAvailable === false) {
    if (
      (dest.view === "console" && dest.tab === "k8s") ||
      (dest.view === "deploy" && dest.section === "workload")
    ) {
      return { ok: false, reason: "当前目标不支持 Kubernetes", pluginId: owner?.id }
    }
  }
  return { ok: true }
}

export function activeOverrideCount(state: PluginStateResponse | null | undefined): number {
  if (!state) return 0
  return state.plugins.filter((plugin) => plugin.override !== null && plugin.override !== plugin.scenario_default).length
}
