// Single source of sidebar entries: tools open directly; the header selects the target.

export const CONSOLE_HOME = "home"
export type ConsoleView = typeof CONSOLE_HOME | "console" | "assets" | "deploy" | "admin" | "catalog" | "plugin"

export const CORE_TARGET_TABS = ["overview", "ask", "terminal", "kb", "credentials", "audit"] as const
export const BUILTIN_PLUGIN_TARGET_TABS = ["tasks", "database", "website", "k8s", "image", "files", "assets", "monitor"] as const

// Per-target tools. The list is the single source for their order and labels.
export const TARGET_TABS = [
  "overview",
  "ask",
  "terminal",
  "tasks",
  "database",
  "website",
  "k8s",
  "image",
  "files",
  "assets",
  "monitor",
  "credentials",
  "audit",
  "kb",
] as const
export type StaticTargetTab = (typeof TARGET_TABS)[number]
export type DeclarativePluginTab = `plugin:skill.${string}`
export type TargetTab = StaticTargetTab | DeclarativePluginTab

export const PLUGIN_TAB_PATTERN = /^plugin:skill\.[a-z0-9][a-z0-9-]{0,62}$/

export type PluginCenterSubTab = "scenarios" | "skills" | "mcp"
export type ResourceMonitorSubTab = "fleet" | "target"
export type AgentOverviewSubTab = "summary" | "manifest" | "workspace"
export type AutomationSubTab = "schedules" | "runs" | "bg"

// Areas of the platform asset & observability views:
// - "plugins" is 插件中心 (场景与插件 / Skill / MCP; "skills"/"mcp" kept as backward-compatible aliases)
// - "situation" is 态势感知 (Business -> Cluster -> Machine)
// - "monitor" is 资源监控 (unified fleet overview + per-target granular series, threshold rules & Skill trends)
// - "inspection" is a legacy alias rendered inside the unified 自动化与巡检 workbench
// - "board" is Agent 运行监控 (Gateway/Agent tunnel, concurrency, calls & LLM tokens)
// - "machines" / "clusters" / "systems" are CMDB ledger sections
export type AssetArea = "plugins" | "situation" | "monitor" | "inspection" | "skills" | "mcp" | "board" | "machines" | "clusters" | "systems"

export const ASSET_LEDGER_SECTIONS: { id: AssetArea; label: string }[] = [
  { id: "machines", label: "机器台账" },
  { id: "clusters", label: "集群台账" },
  { id: "systems", label: "业务系统" },
]
export type DeploySection = "workload" | "upgrade" | "onboard" | "cicd"

// needsTarget entries stay disabled until a target is selected.
export const DEPLOY_SECTIONS: { id: DeploySection; label: string; needsTarget: boolean }[] = [
  { id: "workload", label: "发布工作负载", needsTarget: true },
  { id: "upgrade", label: "升级 Agent", needsTarget: true },
  { id: "onboard", label: "首次接入", needsTarget: false },
  { id: "cicd", label: "CI/CD 接入", needsTarget: false },
]

// Machines are not here on purpose: the machine ledger is their only entry, so a
// machine is registered once instead of in a personal list and a platform table.
export const assetKinds = { database: "数据库", website: "网站", k8s: "Kubernetes", image: "镜像", credentials: "凭据" } as const
export type CatalogKind = keyof typeof assetKinds

export type AdminTab = "scope" | "instances" | "config" | "users" | "grants" | "tokens" | "sensitive" | "proxy" | "webhooks"

export const ADMIN_TABS: { id: AdminTab; label: string }[] = [
  { id: "instances", label: "实例" },
  { id: "config", label: "配置中心" },
  { id: "users", label: "用户" },
  { id: "grants", label: "权限" },
  { id: "tokens", label: "令牌" },
  { id: "sensitive", label: "敏感词" },
  { id: "proxy", label: "代理路由" },
  { id: "webhooks", label: "出站 Webhook" },
  { id: "scope", label: "Agent管理" },
]

export interface ConsoleNavEntry {
  id: string
  label: string
  admin?: boolean
  needsTarget?: boolean
  destination:
    | { view: typeof CONSOLE_HOME }
    | { view: "console"; tab: TargetTab }
    | { view: "assets"; area: AssetArea; sub?: PluginCenterSubTab; monitorSub?: ResourceMonitorSubTab; targetKey?: string }
    | { view: "deploy"; section: DeploySection }
    | { view: "catalog"; kind: CatalogKind }
    | { view: "plugin"; pluginId: string }
    | { view: "admin" }
}

const NAV: ConsoleNavEntry[] = [
  { id: "home", label: "首页", destination: { view: CONSOLE_HOME } },
  { id: "overview", label: "Agent 概览", destination: { view: "console", tab: "overview" } },
  { id: "ask", label: "AI 助手", destination: { view: "console", tab: "ask" } },
  { id: "terminal", label: "终端", destination: { view: "console", tab: "terminal" } },
  { id: "tasks", label: "自动化", destination: { view: "console", tab: "tasks" } },
  { id: "files", label: "部署包", destination: { view: "console", tab: "files" } },
  { id: "audit", label: "审计记录", destination: { view: "console", tab: "audit" } },
  { id: "kb", label: "知识库", destination: { view: "console", tab: "kb" } },

  // Unified CMDB asset ledgers (资产台账).
  { id: "ledger-machines", label: "机器台账", destination: { view: "assets", area: "machines" } },
  { id: "k8s", label: "Kubernetes", destination: { view: "assets", area: "clusters" } },
  { id: "ledger-systems", label: "业务系统", destination: { view: "assets", area: "systems" } },
  ...Object.entries(assetKinds)
    .filter(([kind]) => kind !== "k8s")
    .map(([kind, label]): ConsoleNavEntry => ({
      id: kind, label, destination: { view: "catalog", kind: kind as CatalogKind },
    })),

  // Unified observability (监控与态势).
  { id: "situational-awareness", label: "态势感知", destination: { view: "assets", area: "situation" } },
  { id: "resource-monitor", label: "资源监控", destination: { view: "assets", area: "monitor" } },
  { id: "platform-monitor", label: "Agent 运行监控", destination: { view: "assets", area: "board" } },

  // Plugin center & scenario extensions (场景与扩展).
  { id: "plugin-center", label: "插件中心", destination: { view: "assets", area: "plugins" } },

  { id: "deploy-workload", label: "发布工作负载", needsTarget: true, destination: { view: "deploy", section: "workload" } },
  { id: "deploy-upgrade", label: "升级 Agent", needsTarget: true, destination: { view: "deploy", section: "upgrade" } },
  { id: "deploy-onboard", label: "首次接入", destination: { view: "deploy", section: "onboard" } },
  { id: "deploy-cicd", label: "CI/CD 接入", destination: { view: "deploy", section: "cicd" } },
  { id: "platform-admin", label: "平台管理", admin: true, destination: { view: "admin" } },
]

// Labels for legacy target tabs that were consolidated into Agent 概览 and 资源监控.
const LEGACY_TARGET_TAB_LABELS: Partial<Record<StaticTargetTab, string>> = {
  assets: "资源对账清单",
  monitor: "资源监控",
}

// Complete unfiltered list exposed to lib/plugins/resolver.ts and label lookups.
export const ALL_CONSOLE_NAV_ENTRIES: readonly ConsoleNavEntry[] = NAV

// Backward-compatible all-in-one entry filter. Plugin-aware callers should use
// resolveConsoleNavItems from lib/plugins/resolver.ts.
// The admin entries only decide what is shown; the Gateway still checks every
// admin request.
export function consoleNavEntries(isAdmin: boolean, kubernetesAvailable?: boolean): ConsoleNavEntry[] {
  return NAV.filter(entry => (!entry.admin || isAdmin) &&
    (kubernetesAvailable !== false || (entry.id !== "k8s" && entry.id !== "deploy-workload")))
}

// Always looks up labels against the complete catalog (never a plugin-filtered
// subset) and falls back cleanly for declarative plugin tabs.
export function targetTabLabel(tab: TargetTab): string {
  if (Object.prototype.hasOwnProperty.call(assetKinds, tab)) return assetKinds[tab as CatalogKind]
  const found = NAV.find(entry => entry.destination.view === "console" && entry.destination.tab === tab)
  if (found) return found.label
  const legacy = LEGACY_TARGET_TAB_LABELS[tab as StaticTargetTab]
  if (legacy) return legacy
  if (tab.startsWith("plugin:")) return tab.slice("plugin:".length)
  return tab
}

// Where "打开凭据" leads from the AI assistant and the release page. DoAgent
// models come only from modelProvider credentials, so this is the console's
// credentials tab, never the admin config center. Only admins get the link.
export function credentialsDestination(isAdmin: boolean): { view: "console"; tab: "credentials" } | null {
  return isAdmin ? { view: "console", tab: "credentials" } : null
}
