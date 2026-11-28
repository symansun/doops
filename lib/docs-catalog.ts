// Client-safe catalog and link-rewriting helpers for the DoOps documentation portal.
// Kept free of `node:` imports so both Server and Client components can use it.

export const GITHUB_REPO_URL = "https://github.com/symansun/doops"

export type DocCategoryId =
  | "getting-started"
  | "capabilities"
  | "architecture-security"
  | "operations"

export type DocStatus = "authoritative" | "spec" | "sop" | "design"

export interface DocCategory {
  id: DocCategoryId
  title: string
  description: string
}

export interface DocEntry {
  slug: string
  /** Relative path under `backend/docs/`, or null for custom React routes like `/docs/deploy`. */
  sourcePath: string | null
  title: string
  shortTitle: string
  description: string
  category: DocCategoryId
  status: DocStatus
  keywords: string[]
}

export const DOC_STATUS_META: Record<DocStatus, { label: string; className: string }> = {
  authoritative: {
    label: "权威指南",
    className: "border-primary/35 bg-primary/12 text-primary",
  },
  spec: {
    label: "核心规范",
    className: "border-accent/35 bg-accent/12 text-accent",
  },
  sop: {
    label: "运维手册",
    className: "border-success/35 bg-success/12 text-success",
  },
  design: {
    label: "设计方案",
    className: "border-warning/35 bg-warning/12 text-warning",
  },
}

export const DOC_CATEGORIES: DocCategory[] = [
  {
    id: "getting-started",
    title: "入门与接入",
    description: "安装 CLI 与项目 Skill、接入集群 Agent，理解统一工作台边界。",
  },
  {
    id: "capabilities",
    title: "核心能力与领域工具",
    description: "CLI 命令、Web 控制台、Helm 自然语言交付、DoSql 数据治理、DoWeb 与 Skill 扩展。",
  },
  {
    id: "architecture-security",
    title: "架构、协议与安全",
    description: "Gateway 反向隧道、受管凭据生命周期、命令过滤、全栈配置与环境变量、运行时超时表与开放 API。",
  },
  {
    id: "operations",
    title: "巡检、监控与平台运维",
    description: "Skill-First 智能巡检、出站 Webhook、资产盘点、构建发布 SOP 与 MySQL 后端。",
  },
]

export const DOCS_CATALOG: DocEntry[] = [
  // Getting Started
  {
    slug: "cluster-install",
    sourcePath: "CLUSTER_INSTALL_GUIDE.md",
    title: "客户端与集群接入指南",
    shortTitle: "客户端与集群接入",
    description: "从 GitHub Releases 安装 CLI 与项目 Skill、首次登录发现目标，以及不同形态的 Agent 接入入口。",
    category: "getting-started",
    status: "authoritative",
    keywords: ["install", "cli", "skill", "onboard", "login", "agent", "k8s", "安装", "接入"],
  },
  {
    slug: "deploy",
    sourcePath: null,
    title: "部署 DoOps Agent 指引",
    shortTitle: "Agent 部署与升级",
    description: "交互式部署与升级指引：GitOps 初装、upgradeAudit v1 独立控制器升级与常见排障。",
    category: "getting-started",
    status: "sop",
    keywords: ["deploy", "agent", "helm", "upgrade", "gitops", "部署", "升级"],
  },
  {
    slug: "workbench",
    sourcePath: "UNIFIED_OPERATIONS_WORKBENCH.md",
    title: "统一运维工作台架构与边界",
    shortTitle: "统一运维工作台",
    description: "Web 控制台、CLI 与各领域 Skill 如何共用唯一 Gateway 身份、授权矩阵与执行证据。",
    category: "getting-started",
    status: "authoritative",
    keywords: ["workbench", "dosql", "doweb", "architecture", "boundary", "工作台", "授权"],
  },

  // Capabilities
  {
    slug: "usage-guide",
    sourcePath: "DOOPS_USAGE_GUIDE.md",
    title: "CLI 日常使用指南",
    shortTitle: "CLI 命令手册",
    description: "命令选择、session 工作区、代码同步（COS / Git）、后台任务、DoSql/DoWeb 与管理员命令详解。",
    category: "capabilities",
    status: "authoritative",
    keywords: ["cli", "exec", "ask", "push", "pull", "sql", "k8s", "ssh", "命令"],
  },
  {
    slug: "webui",
    sourcePath: "DOOPS_WEBUI.md",
    title: "Web 控制台使用与行为说明",
    shortTitle: "Web 控制台",
    description: "AI 交互终端、AI 助手协同、目标切换、资产台账、巡检中心与平台管理页的交互规范。",
    category: "capabilities",
    status: "authoritative",
    keywords: ["webui", "console", "terminal", "pty", "ai", "控制台", "终端"],
  },
  {
    slug: "deployment",
    sourcePath: "NATURAL_LANGUAGE_DEPLOYMENT.md",
    title: "自然语言业务发布与 Helm 契约",
    shortTitle: "自然语言与 Helm 交付",
    description: "ask --apply 结构化 Helm 发布契约、凭据隔离、实测回读验收与错误处理边界。",
    category: "capabilities",
    status: "authoritative",
    keywords: ["helm", "apply", "release", "cicd", "kubernetes", "发布", "交付"],
  },
  {
    slug: "dosql",
    sourcePath: "design/DOSQL_ENGINE_SPEC.md",
    title: "DoSql 统一 Go 数据治理引擎规范",
    shortTitle: "DoSql 数据治理",
    description: "PostgreSQL / MySQL / Oracle / MSSQL / MongoDB 统一引擎：inspect、diff、datadiff、migrate、backfill 与 Policy-as-Code。",
    category: "capabilities",
    status: "spec",
    keywords: ["dosql", "database", "sql", "migrate", "diff", "policy", "数据库", "治理"],
  },
  {
    slug: "doweb",
    sourcePath: "DOWEB_INTEGRATION.md",
    title: "DoWeb 站点交付与工作台集成",
    shortTitle: "DoWeb 站点交付",
    description: "基于 SiteSpec 的 Nginx 配置渲染、校验、平滑重载、健康检查与自动回滚流程。",
    category: "capabilities",
    status: "authoritative",
    keywords: ["doweb", "nginx", "website", "tls", "sitespec", "网站"],
  },
  {
    slug: "skill-integration",
    sourcePath: "SKILL_INTEGRATION_GUIDE.md",
    title: "业务 Skill 编写与集成指南",
    shortTitle: "Skill 编写与集成",
    description: "如何编写团队专属 SKILL.md，把发布规范、诊断 SOP 与约束固化给 AI 执行。",
    category: "capabilities",
    status: "authoritative",
    keywords: ["skill", "sop", "agent", "claude", "codex", "扩展"],
  },
  {
    slug: "models",
    sourcePath: "MODEL_STANDARDS.md",
    title: "模型配置与使用规范",
    shortTitle: "模型配置规范",
    description: "通过 modelProvider 受管凭据安全下发模型设置，以及 ask / doagent 运行时约定。",
    category: "capabilities",
    status: "authoritative",
    keywords: ["model", "llm", "doagent", "acp", "modelProvider", "模型"],
  },
  {
    slug: "host-access",
    sourcePath: "PHYSICAL_HOST_ACCESS.md",
    title: "物理机与宿主机访问边界",
    shortTitle: "物理机与宿主机访问",
    description: "通过 nsenter 与结构化入口检查容器宿主机与物理机硬件，避免绕过审计的直连操作。",
    category: "capabilities",
    status: "authoritative",
    keywords: ["host", "nsenter", "physical", "vm", "宿主机", "物理机"],
  },

  // Architecture & Security
  {
    slug: "gateway-tunnel",
    sourcePath: "DOOPS_GATEWAY_TUNNEL.md",
    title: "Gateway 反向隧道与执行生命周期",
    shortTitle: "Gateway 隧道与生命周期",
    description: "Agent 主动连接协议、enrollment token、并发排队、取消确认与断线续接（execution_id）机制。",
    category: "architecture-security",
    status: "authoritative",
    keywords: ["gateway", "tunnel", "websocket", "execution", "resume", "enrollment", "隧道", "生命周期"],
  },
  {
    slug: "credentials",
    sourcePath: "CREDENTIAL_RESOURCES.md",
    title: "受管凭据资源全生命周期",
    shortTitle: "受管凭据资源",
    description: "create → stage → grant → verify → promote、主密钥原地轮换、开发预检 manifest 与安全边界。",
    category: "architecture-security",
    status: "authoritative",
    keywords: ["credential", "secret", "vault", "aead", "rotate", "preflight", "凭据", "密钥"],
  },
  {
    slug: "command-filter",
    sourcePath: "design/COMMAND_FILTER.md",
    title: "Agent 双层命令过滤策略",
    shortTitle: "双层命令过滤",
    description: "结构化 argv AST 策略文件（DOOPS_COMMAND_POLICY_FILE）与正则兜底的拦截机制。",
    category: "architecture-security",
    status: "spec",
    keywords: ["security", "filter", "argv", "deny", "policy", "安全", "过滤"],
  },
  {
    slug: "configuration",
    sourcePath: "CONFIGURATION.md",
    title: "配置文件与环境变量权威参考",
    shortTitle: "配置与环境变量",
    description: "Gateway、Agent、Web 控制台与 CLI 全量配置文件模板、环境变量说明、默认值与安全边界（与源码自动核对）。",
    category: "architecture-security",
    status: "authoritative",
    keywords: ["config", "configuration", "env", "dotenv", "systemd", "helm", "配置", "环境变量"],
  },
  {
    slug: "runtime-limits",
    sourcePath: "RUNTIME_LIMITS.md",
    title: "运行时超时与限额权威表",
    shortTitle: "运行时超时与限额",
    description: "与源码测试逐项校验对齐的全部 DOOPS_* 超时配置、取值范围、顺序不等式与启动校验。",
    category: "architecture-security",
    status: "authoritative",
    keywords: ["timeout", "limits", "env", "lease", "budget", "超时", "限额"],
  },
  {
    slug: "api-guide",
    sourcePath: "DOOPS_API_GUIDE.md",
    title: "HTTP / WebSocket API 对接指南",
    shortTitle: "API 对接指南",
    description: "面向平台、CI 与内部工具的登录、Target 查询、JSON-RPC 调用、进度流、资产台账与审计接口。",
    category: "architecture-security",
    status: "authoritative",
    keywords: ["api", "rpc", "http", "websocket", "rest", "ci", "接口"],
  },

  // Operations
  {
    slug: "skill-first-inspection",
    sourcePath: "design/SKILL_FIRST_INSPECTION_SPEC.md",
    title: "Skill-First 智能巡检与能力框架规范",
    shortTitle: "Skill-First 智能巡检",
    description: "8 大内置巡检 Skill 包、File-First 产物落盘、可视化图表协议、指标桥接与保留清理策略。",
    category: "operations",
    status: "spec",
    keywords: ["inspection", "monitor", "skill", "charts", "artifacts", "巡检", "监控"],
  },
  {
    slug: "webhooks",
    sourcePath: "design/DOOPS_WEBHOOKS.md",
    title: "出站 Webhook 事件通知规范",
    shortTitle: "出站 Webhook 通知",
    description: "Outbox 事件投递、HMAC-SHA256 签名、SSRF 防护、退避重试与管理员配置接口。",
    category: "operations",
    status: "spec",
    keywords: ["webhook", "alert", "hmac", "event", "notification", "告警", "通知"],
  },
  {
    slug: "asset-coverage",
    sourcePath: "ASSET_COVERAGE.md",
    title: "集群盘点底册与覆盖率对账",
    shortTitle: "资产覆盖率对账",
    description: "在役资产底册导入、归属对账、缺口识别与冻结批次导出。",
    category: "operations",
    status: "sop",
    keywords: ["asset", "coverage", "inventory", "csv", "资产", "覆盖率"],
  },
  {
    slug: "asset-monitor",
    sourcePath: "ASSET_MONITOR_DEVICES.md",
    title: "宿主采样与设备监控曲线",
    shortTitle: "设备监控曲线",
    description: "从授权目标宿主采集 CPU、磁盘 I/O、网卡、挂载点与 GPU 时序指标。",
    category: "operations",
    status: "sop",
    keywords: ["monitor", "metrics", "disk", "gpu", "cpu", "监控", "指标"],
  },
  {
    slug: "machine-sampling",
    sourcePath: "MACHINE_SAMPLING.md",
    title: "宿主机采样与机器详情绑定",
    shortTitle: "宿主机采样绑定",
    description: "通过只读宿主采样脚本与 machine-id 指纹核验，安全拉取主机实时快照。",
    category: "operations",
    status: "sop",
    keywords: ["machine", "sampling", "systemd", "fingerprint", "宿主", "采样"],
  },
  {
    slug: "build-deploy-sop",
    sourcePath: "BUILD_DEPLOY_SOP.md",
    title: "DoOps 构建、镜像与部署 SOP",
    shortTitle: "构建与部署 SOP",
    description: "CLI、Gateway、Agent 与 Web 控制台的不可变镜像构建、provenance 核验、发布与自动回滚规范。",
    category: "operations",
    status: "sop",
    keywords: ["build", "docker", "release", "rollback", "sop", "构建", "镜像"],
  },
  {
    slug: "mysql-ops",
    sourcePath: "GATEWAY_MYSQL_OPERATIONS.md",
    title: "Gateway MySQL 8.4 后端运维手册",
    shortTitle: "Gateway MySQL 运维",
    description: "开启 MySQL 8.4 存储后端、字符集与排序规则启动门禁、快照割接（store mysql-copy）与备份回退。",
    category: "operations",
    status: "sop",
    keywords: ["mysql", "sqlite", "storage", "migration", "backup", "数据库", "后端"],
  },
  {
    slug: "edge-tunnel",
    sourcePath: "design/DOOPS_EDGE_TUNNEL.md",
    title: "DoOps Edge 反向代理隧道设计",
    shortTitle: "Edge 反向代理（设计）",
    description: "Agent 反向代理与 Gateway 动态域名映射方案（默认关闭，阶段 4/5 演进中）。",
    category: "operations",
    status: "design",
    keywords: ["edge", "proxy", "domain", "tls", "隧道", "反代"],
  },
]

export function docHref(slug: string): string {
  return `/docs/${slug}`
}

export function findDocBySlug(slug: string): DocEntry | undefined {
  return DOCS_CATALOG.find((item) => item.slug === slug)
}

export function markdownDocSlugs(): string[] {
  return DOCS_CATALOG.filter((item) => item.sourcePath !== null).map((item) => item.slug)
}

export function docsByCategory(category: DocCategoryId): DocEntry[] {
  return DOCS_CATALOG.filter((item) => item.category === category)
}

export function adjacentDocs(slug: string): { prev: DocEntry | null; next: DocEntry | null } {
  const index = DOCS_CATALOG.findIndex((item) => item.slug === slug)
  if (index === -1) return { prev: null, next: null }
  return {
    prev: index > 0 ? DOCS_CATALOG[index - 1] : null,
    next: index < DOCS_CATALOG.length - 1 ? DOCS_CATALOG[index + 1] : null,
  }
}

const SOURCE_TO_SLUG = new Map<string, string>(
  DOCS_CATALOG.filter((item): item is DocEntry & { sourcePath: string } => item.sourcePath !== null).map(
    (item) => [item.sourcePath.toLowerCase(), item.slug],
  ),
)

function normalizeRelativePosix(fromDir: string, target: string): string {
  const baseParts = fromDir ? fromDir.split("/").filter(Boolean) : []
  const targetParts = target.split("/")
  const stack = [...baseParts]
  for (const part of targetParts) {
    if (!part || part === ".") continue
    if (part === "..") stack.pop()
    else stack.push(part)
  }
  return stack.join("/")
}

const GITHUB_DOCS_BLOB_BASE = "https://github.com/symansun/doops/blob/main/backend/docs/"
const GITHUB_BACKEND_BLOB_BASE = "https://github.com/symansun/doops/blob/main/backend/"

/**
 * Rewrite relative Markdown links inside `backend/docs/` files so internal doc
 * cross-references resolve to `/docs/<slug>` instead of 404ing, and repo assets
 * outside the catalog resolve cleanly to GitHub.
 */
export function rewriteDocLinks(markdown: string, currentSourcePath: string): string {
  const currentDir = currentSourcePath.includes("/")
    ? currentSourcePath.slice(0, currentSourcePath.lastIndexOf("/"))
    : ""

  // Avoid rewriting inside fenced code blocks.
  const lines = markdown.replace(/\r\n/g, "\n").split("\n")
  let fence: string | null = null
  const out: string[] = []

  for (const line of lines) {
    const trimmed = line.trimStart()
    const fenceMatch = /^(`{3,}|~{3,})/.exec(trimmed)
    if (fenceMatch) {
      const marker = fenceMatch[1]
      if (!fence) fence = marker
      else if (trimmed.startsWith(fence[0].repeat(fence.length)) && /^[`~]+\s*$/.test(trimmed)) {
        fence = null
      }
      out.push(line)
      continue
    }
    if (fence) {
      out.push(line)
      continue
    }

    const rewritten = line.replace(
      /(?<!!)\[([^\]]+)\]\(([^)\s]+)\)/g,
      (full, label: string, rawHref: string) => {
        if (
          rawHref.startsWith("#") ||
          rawHref.startsWith("/") ||
          /^[a-z][a-z0-9+.-]*:/i.test(rawHref)
        ) {
          return full
        }

        const hashIdx = rawHref.indexOf("#")
        const filePart = hashIdx >= 0 ? rawHref.slice(0, hashIdx) : rawHref
        const hashPart = hashIdx >= 0 ? rawHref.slice(hashIdx) : ""
        if (!filePart) return full

        // Links reaching out of `backend/docs/` (e.g. `../scripts/...` or `../deploy/...`)
        const upLevels = (filePart.match(/^\.\.\//g) ? filePart.split("../").length - 1 : 0)
        const dirDepth = currentDir ? currentDir.split("/").length : 0
        if (upLevels > dirDepth) {
          const stripped = filePart.replace(/^(\.\.\/)+/, "")
          return `[${label}](${GITHUB_BACKEND_BLOB_BASE}${stripped}${hashPart})`
        }

        const normalized = normalizeRelativePosix(currentDir, filePart)
        const targetSlug = SOURCE_TO_SLUG.get(normalized.toLowerCase())
        if (targetSlug) {
          return `[${label}](/docs/${targetSlug}${hashPart})`
        }
        return `[${label}](${GITHUB_DOCS_BLOB_BASE}${normalized}${hashPart})`
      },
    )
    out.push(rewritten)
  }

  return out.join("\n")
}
