import rawCatalog from "./builtin-catalog.json" with { type: "json" }

export type PluginTier = "builtin" | "declarative"
export type PluginCategory = "observability" | "infrastructure" | "delivery" | "data" | "ops" | "security"
export type PluginDependencyStatus = "ready" | "disabled" | "missing"

export interface PluginScenarioDefinition {
  id: string
  name: string
  description: string
  defaults: string[]
}

export interface BuiltinPluginDefinition {
  id: string
  tier: "builtin"
  name: string
  description: string
  category: PluginCategory
  nav_ids: string[]
  target_tabs: string[]
  required_skills: string[]
  required_mcp: string[]
  requires_kubernetes?: boolean
}

export interface BuiltinPluginCatalog {
  version: number
  core_ids: string[]
  scenarios: PluginScenarioDefinition[]
  plugins: BuiltinPluginDefinition[]
}

export interface PluginDependencyState {
  slug: string
  state: PluginDependencyStatus
}

export interface PluginDependenciesView {
  skills: PluginDependencyState[]
  mcp: PluginDependencyState[]
}

export type SkillPluginInputType = "number" | "boolean" | "select" | "credential_ref"
export type SkillPluginViewType =
  | "kpi_grid"
  | "checks_table"
  | "charts"
  | "trend_series"
  | "artifact_viewer"
  | "resource_table"
  | "findings_list"
export type SkillPluginTargetMode = "kubernetes" | "docker" | "linux"
export type SkillPluginWidgetAggregate = "max" | "avg" | "latest"
export type SkillPluginCatalogInput =
  | "website_urls"
  | "website_domains"
  | "database_refs"
  | "image_refs"

export const CATALOG_INPUT_LABELS: Record<SkillPluginCatalogInput, string> = {
  website_urls: "网站 URL 清单 (DOOPS_INSPECT_URLS)",
  website_domains: "网站域名清单 (DOOPS_INSPECT_DOMAINS)",
  database_refs: "数据库元数据目录 (DOOPS_CATALOG_DATABASES)",
  image_refs: "镜像仓库目录 (DOOPS_CATALOG_IMAGES)",
}

export type SkillPluginCatalogFieldType = "domain" | "host" | "ip" | "port" | "label" | "enum"

export interface SkillPluginCatalogField {
  key: string
  label: string
  placeholder?: string
  type: SkillPluginCatalogFieldType
  default?: string
  options?: string[]
}

export interface SkillPluginCatalogSchema {
  key: string
  label: string
  description?: string
  env_var: string
  primary_field: string
  max_items?: number
  fields: SkillPluginCatalogField[]
}

export interface SkillPluginMCPServer {
  slug: string
  name?: string
  description?: string
  transport?: "stdio" | "http" | "sse"
  command?: string
  args?: string[]
  url?: string
  read_tools?: string[]
  apply_tools?: string[]
  tool_timeout_secs?: number
}
export type SkillPluginActionMode = "check" | "plan" | "apply"
export type SkillPluginTableColumnKind = "text" | "badge" | "code" | "number"

export interface SkillPluginMeta {
  id: string
  name: string
  description?: string
  category: PluginCategory
  icon: string
  scenarios: string[]
  opt_in?: boolean
}

export interface SkillPluginCapabilities {
  target_modes?: SkillPluginTargetMode[]
  requires_skills?: string[]
  requires_mcp?: string[]
  catalog_inputs?: SkillPluginCatalogInput[]
  companion_agent_skill?: string
}

export interface SkillPluginSidebarSlot {
  enabled: boolean
  label?: string
  group?: string
  order?: number
}

export interface SkillPluginTargetTabSlot {
  enabled: boolean
  label?: string
  order?: number
}

export interface SkillPluginWidgetMetric {
  key: string
  label: string
  unit?: string
  aggregate?: SkillPluginWidgetAggregate
}

export interface SkillPluginHomeWidgetSlot {
  enabled: boolean
  title?: string
  span?: 1 | 2
  metrics?: SkillPluginWidgetMetric[]
}

export interface SkillPluginAskPreset {
  title: string
  description?: string
  prompt: string
}

export interface SkillPluginSlots {
  sidebar?: SkillPluginSidebarSlot
  target_tab?: SkillPluginTargetTabSlot
  home_widget?: SkillPluginHomeWidgetSlot
  ask_presets?: SkillPluginAskPreset[]
}

export interface SkillPluginSelectOption {
  value: string
  label: string
}

export interface SkillPluginInput {
  key: string
  label: string
  description?: string
  type: SkillPluginInputType
  required?: boolean
  default?: string | number | boolean
  min?: number
  max?: number
  step?: number
  unit?: string
  options?: SkillPluginSelectOption[]
  credential_use?: string
}

export interface SkillPluginPreset {
  id: string
  title: string
  description?: string
  mode: "script" | "agent"
  inputs?: Record<string, string | number | boolean>
  prompt?: string
}

export interface SkillPluginAction {
  id: string
  label: string
  description?: string
  mode: SkillPluginActionMode
  skill?: string
  requires_mcp?: string
  credential_use?: string
  prompt_template: string
}

export interface SkillPluginTableColumn {
  key: string
  label: string
  kind?: SkillPluginTableColumnKind
}

export interface SkillPluginKPIItem {
  key: string
  label: string
  unit?: string
  warn_threshold?: number
  crit_threshold?: number
}

export interface SkillPluginView {
  type: SkillPluginViewType
  title?: string
  items?: SkillPluginKPIItem[]
  metric_keys?: string[]
  range?: "1h" | "6h" | "24h" | "7d" | "30d"
  source_key?: string
  columns?: SkillPluginTableColumn[]
  row_action_id?: string
}

export interface SkillPluginScheduleTemplate {
  name: string
  description?: string
  interval_seconds: number
  scope_mode?: "all_online" | "targets" | "tags"
  inputs?: Record<string, string | number | boolean>
}

export interface SkillPluginUI {
  slots: SkillPluginSlots
  inputs?: SkillPluginInput[]
  presets?: SkillPluginPreset[]
  actions?: SkillPluginAction[]
  views?: SkillPluginView[]
}

export interface SkillPluginManifest {
  api_version: string
  kind: string
  plugin: SkillPluginMeta
  capabilities?: SkillPluginCapabilities
  catalog_schemas?: SkillPluginCatalogSchema[]
  mcp_server?: SkillPluginMCPServer
  ui: SkillPluginUI
  schedule_template?: SkillPluginScheduleTemplate
}

export interface PluginView {
  id: string
  tier: PluginTier
  skill_slug?: string
  scope?: "system" | "custom"
  builtin?: boolean
  can_delete?: boolean
  owner_is_me?: boolean
  skill_enabled?: boolean
  name: string
  description: string
  category: PluginCategory
  nav_ids?: string[]
  target_tabs?: string[]
  requires_kubernetes?: boolean
  effective: boolean
  scenario_default: boolean
  override: boolean | null
  dependencies: PluginDependenciesView
  manifest?: SkillPluginManifest | null
  manifest_error?: string
}

export interface PluginOwnerRef {
  id: string
  name: string
  description: string
  tier: PluginTier
  scope?: "system" | "custom"
  skill_slug?: string
  skill_enabled?: boolean
}

export interface PluginStateResponse {
  supported: boolean
  configured: boolean
  scenario_id: string
  revision: number
  customized: boolean
  updated_by: string
  updated_at_ms: number
  scenarios: PluginScenarioDefinition[]
  plugins: PluginView[]
  stale_overrides: string[]
}

export interface PluginAffectedSchedule {
  id: string
  name: string
  skills: string[]
  note: string
}

export interface PluginMutationDiff {
  show: string[]
  hide: string[]
  dropped_overrides: string[]
  affected_schedules: PluginAffectedSchedule[]
}

export interface PluginMutationResponse {
  dry_run: boolean
  revision: number
  scenario_id: string
  customized: boolean
  diff: PluginMutationDiff
}

export type PluginMutationRequest =
  | { action: "apply_scenario"; scenario_id: string; expected_revision: number; dry_run?: boolean }
  | { action: "set_plugin"; plugin_id: string; enabled: boolean; expected_revision: number; dry_run?: boolean }
  | { action: "clear_override"; plugin_id: string; expected_revision: number }
  | { action: "reset"; expected_revision: number; dry_run?: boolean }

export const DEFAULT_PLUGIN_SCENARIO_ID = "all-in-one"
export const BUILTIN_PLUGIN_CATALOG = rawCatalog as BuiltinPluginCatalog
export const MAX_HOME_PLUGIN_WIDGETS = 12

export const PLUGIN_CATEGORY_LABELS: Record<PluginCategory, string> = {
  observability: "巡检与监控",
  infrastructure: "基础设施",
  delivery: "交付与镜像",
  data: "数据治理",
  ops: "服务与编排",
  security: "安全合规",
}
