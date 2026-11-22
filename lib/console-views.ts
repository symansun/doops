// Single source of sidebar entries: tools open directly; the header selects the target.

export const CONSOLE_HOME = "home"
export type ConsoleView = typeof CONSOLE_HOME | "console" | "assets" | "deploy" | "admin" | "catalog"

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
]
export type TargetTab = (typeof TARGET_TABS)[number]

// Areas of the platform asset view. "board" is 平台监控; the rest are the ledger
// sections, which keep the view open on the section the user picked.
export type AssetArea = "board" | "machines" | "clusters" | "systems" | "scope"

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

export type AdminTab = "scope" | "instances" | "config" | "users" | "grants" | "tokens" | "proxy"

export const ADMIN_TABS: { id: AdminTab; label: string }[] = [
  { id: "instances", label: "实例" },
  { id: "config", label: "配置中心" },
  { id: "users", label: "用户" },
  { id: "grants", label: "权限" },
  { id: "tokens", label: "令牌" },
  { id: "proxy", label: "代理路由" },
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
    | { view: "assets"; area: AssetArea }
    | { view: "deploy"; section: DeploySection }
    | { view: "catalog"; kind: CatalogKind }
    | { view: "admin" }
}

const NAV: ConsoleNavEntry[] = [
  { id: "home", label: "首页", destination: { view: CONSOLE_HOME } },
  { id: "overview", label: "Agent 概览", destination: { view: "console", tab: "overview" } },
  { id: "ask", label: "AI 助手", destination: { view: "console", tab: "ask" } },
  { id: "terminal", label: "终端", destination: { view: "console", tab: "terminal" } },
  { id: "tasks", label: "自动化", destination: { view: "console", tab: "tasks" } },
  { id: "files", label: "文件", destination: { view: "console", tab: "files" } },
  { id: "assets", label: "Agent 资产", destination: { view: "console", tab: "assets" } },
  { id: "monitor", label: "Agent 监控", destination: { view: "console", tab: "monitor" } },

  { id: "audit", label: "审计记录", destination: { view: "console", tab: "audit" } },
  { id: "kb", label: "知识库", destination: { view: "console", tab: "kb" } },
  ...Object.entries(assetKinds).map(([kind, label]): ConsoleNavEntry => ({
    id: kind, label, destination: kind === "k8s" ? { view: "assets", area: "clusters" } : { view: "catalog", kind: kind as CatalogKind },
  })),
  // Platform functions, grouped by destination in the sidebar.
  { id: "platform-monitor", label: "平台监控", destination: { view: "assets", area: "board" } },
  { id: "ledger-machines", label: "机器台账", destination: { view: "assets", area: "machines" } },
  { id: "ledger-systems", label: "业务系统", destination: { view: "assets", area: "systems" } },

  { id: "deploy-workload", label: "发布工作负载", needsTarget: true, destination: { view: "deploy", section: "workload" } },
  { id: "deploy-upgrade", label: "升级 Agent", needsTarget: true, destination: { view: "deploy", section: "upgrade" } },
  { id: "deploy-onboard", label: "首次接入", destination: { view: "deploy", section: "onboard" } },
  { id: "deploy-cicd", label: "CI/CD 接入", destination: { view: "deploy", section: "cicd" } },
  { id: "platform-admin", label: "平台管理", admin: true, destination: { view: "admin" } },
]

// The admin entries only decide what is shown; the Gateway still checks every
// admin request.
export function consoleNavEntries(isAdmin: boolean): ConsoleNavEntry[] {
  return NAV.filter(entry => !entry.admin || isAdmin)
}

export function targetTabLabel(tab: TargetTab): string {
  if (Object.prototype.hasOwnProperty.call(assetKinds, tab)) return assetKinds[tab as CatalogKind]
  return NAV.find(entry => entry.destination.view === "console" && entry.destination.tab === tab)!.label
}

// Where "打开凭据" leads from the AI assistant and the release page. DoAgent
// models come only from modelProvider credentials, so this is the console's
// credentials tab, never the admin config center. Only admins get the link.
export function credentialsDestination(isAdmin: boolean): { view: "console"; tab: "credentials" } | null {
  return isAdmin ? { view: "console", tab: "credentials" } : null
}
