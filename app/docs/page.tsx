import Link from "next/link"
import {
  ArrowRight,
  BookOpen,
  Bot,
  Code2,
  Database,
  FileCode2,
  Layers,
  Plug,
  Shield,
  Terminal,
} from "lucide-react"
import {
  DOC_CATEGORIES,
  DOC_STATUS_META,
  DOCS_CATALOG,
  docHref,
  docsByCategory,
} from "@/lib/docs-catalog"

const READING_PATHS = [
  {
    icon: Terminal,
    role: "开发者 · 5 分钟接入",
    title: "装好 CLI & IDE Skill，立刻开始排障与交付",
    steps: [
      { label: "客户端与集群接入", href: "/docs/cluster-install" },
      { label: "CLI 命令手册", href: "/docs/usage-guide" },
      { label: "自然语言与 Helm 交付", href: "/docs/deployment" },
    ],
  },
  {
    icon: Layers,
    role: "平台管理员 · 自托管与纳管",
    title: "部署 Gateway 控制面、纳管目标 Agent 与升级回滚",
    steps: [
      { label: "Agent 部署与升级指引", href: "/docs/deploy" },
      { label: "构建与部署 SOP", href: "/docs/build-deploy-sop" },
      { label: "Gateway MySQL 运维", href: "/docs/mysql-ops" },
    ],
  },
  {
    icon: Shield,
    role: "安全与架构审查",
    title: "核查反向隧道协议、受管凭据边界与命令过滤机制",
    steps: [
      { label: "Gateway 隧道与生命周期", href: "/docs/gateway-tunnel" },
      { label: "受管凭据资源", href: "/docs/credentials" },
      { label: "双层命令过滤策略", href: "/docs/command-filter" },
    ],
  },
  {
    icon: Database,
    role: "数据治理与智能巡检",
    title: "使用 DoSql 统一治理多引擎数据库，扩展巡检 Skill",
    steps: [
      { label: "DoSql 数据治理引擎", href: "/docs/dosql" },
      { label: "Skill-First 智能巡检", href: "/docs/skill-first-inspection" },
      { label: "业务 Skill 编写与集成", href: "/docs/skill-integration" },
    ],
  },
]

const CHEAT_SHEET = [
  { label: "登录并自动发现目标", cmd: "doops login --gateway https://gateway.example.com --username <user>" },
  { label: "查看本地路由与实时在线态", cmd: "doops list --status" },
  { label: "执行确定性远程命令", cmd: "doops -session diag-01 exec --target <t> --cmd 'hostname && df -h'" },
  { label: "AI 只读诊断排障", cmd: "doops -session diag-01 ask --target <t> --msg '只读检查异常 Pod 并给出证据'" },
  { label: "自然语言受控 Helm 交付", cmd: "doops -session rel-01 ask --target <t> --apply --credential <name> --msg '<facts>'" },
  { label: "DoSql 多引擎数据治理", cmd: "doops -session db-01 sql --target <t> --mode diff --database <db>" },
]

export default function DocsIndexPage() {
  return (
    <div className="space-y-12 pb-12">
      {/* Header Banner */}
      <section className="portal-frame relative overflow-hidden p-6 sm:p-8">
        <div aria-hidden="true" className="hero-ambient pointer-events-none absolute inset-0 opacity-70" />
        <div className="relative">
          <div className="inline-flex flex-wrap items-center gap-2 rounded-full border border-border/75 bg-background/85 px-3 py-1 font-mono text-2xs text-muted-foreground">
            <BookOpen className="size-3.5 text-accent" aria-hidden="true" />
            <span>DoOps 配套文档中心</span>
            <span aria-hidden="true" className="text-border">·</span>
            <span className="text-foreground">共 {DOCS_CATALOG.length} 篇权威主题</span>
            <span aria-hidden="true" className="text-border">·</span>
            <span>单一源 backend/docs/</span>
          </div>
          <h1 className="mt-4 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
            面向开发者的完整工程文档
          </h1>
          <p className="mt-3 max-w-2xl text-pretty text-sm leading-relaxed text-muted-foreground sm:text-base">
            本中心直接渲染仓库 <code className="font-mono text-foreground">backend/docs/</code> 下的现行权威文档与架构规范，
            确保网页阅读与 CLI 随包安装的文档始终一致、零漂移。
          </p>
          <div className="mt-6 flex flex-wrap gap-2.5">
            <Link href="/docs/cluster-install" className="btn btn-primary btn-sm">
              从接入指南开始
              <ArrowRight className="size-3.5" aria-hidden="true" />
            </Link>
            <Link href="/docs/usage-guide" className="btn btn-sm bg-background/80">
              CLI 命令手册
            </Link>
            <Link href="/docs/deploy" className="btn btn-sm bg-background/80">
              Agent 部署指引
            </Link>
            <Link href="/blog" className="btn btn-ghost btn-sm">
              <Code2 className="size-3.5 text-accent" aria-hidden="true" />
              深度架构长文
            </Link>
          </div>
        </div>
      </section>

      {/* Role-Based Reading Tracks */}
      <section>
        <div className="mb-5">
          <h2 className="text-lg font-semibold tracking-tight text-foreground">按角色推荐阅读路径</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            不确定从哪一篇开始？按你的目标选择最直接的 3 篇组合。
          </p>
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          {READING_PATHS.map((path) => {
            const Icon = path.icon
            return (
              <div key={path.role} className="card-surface flex flex-col justify-between p-5">
                <div>
                  <div className="flex items-center gap-2 text-xs font-medium text-accent">
                    <Icon className="size-4" aria-hidden="true" />
                    <span>{path.role}</span>
                  </div>
                  <h3 className="mt-2 text-sm font-semibold text-foreground">{path.title}</h3>
                </div>
                <ol className="mt-4 space-y-1.5 border-t border-border/50 pt-3.5">
                  {path.steps.map((st, idx) => (
                    <li key={st.href}>
                      <Link
                        href={st.href}
                        className="group flex items-center justify-between rounded-md px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
                      >
                        <span className="flex items-center gap-2">
                          <span className="font-mono text-2xs text-primary">0{idx + 1}</span>
                          {st.label}
                        </span>
                        <ArrowRight
                          className="size-3.5 opacity-50 transition-transform group-hover:translate-x-0.5 group-hover:opacity-100"
                          aria-hidden="true"
                        />
                      </Link>
                    </li>
                  ))}
                </ol>
              </div>
            )
          })}
        </div>
      </section>

      {/* Tools Provided to AI Agents */}
      <section className="portal-frame overflow-hidden p-5 sm:p-6">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2 border-b border-border/55 pb-3.5">
          <div className="flex items-center gap-2">
            <Bot className="size-4 text-accent" aria-hidden="true" />
            <h2 className="text-base font-semibold text-foreground">提供给 AI Agent 的运维工具面</h2>
          </div>
          <Link href="/docs/skill-integration" className="font-mono text-2xs text-accent hover:underline">
            Skill 与 MCP 集成指南 →
          </Link>
        </div>
        <p className="mb-4 text-xs leading-relaxed text-muted-foreground">
          DoOps 通过三层受控工具面赋能外部 AI 编码助手（Claude Code / Codex / Cursor）与内置{" "}
          <code className="font-mono text-foreground">doagent</code>：工具读写隔离、凭据 0 密文进入 Prompt、所有调用强制审计并返回结构化回执。
        </p>
        <div className="grid gap-3 md:grid-cols-3">
          <div className="rounded-xl border border-border/65 bg-background/80 p-4">
            <div className="flex items-center gap-1.5 font-mono text-2xs font-medium text-accent">
              <Code2 className="size-3.5" aria-hidden="true" />
              IDE Agent Skill + CLI
            </div>
            <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
              <code className="font-mono text-foreground">scripts/install.sh</code> 自动同步{" "}
              <code className="font-mono text-foreground">SKILL.md</code> 至项目{" "}
              <code className="font-mono text-foreground">.agent/</code> 与{" "}
              <code className="font-mono text-foreground">.codex/</code>，AI 直接调用结构化{" "}
              <code className="font-mono text-foreground">doops</code> 命令与无密文预检清单。
            </p>
            <Link href="/docs/cluster-install" className="mt-3 inline-flex items-center gap-1 text-2xs font-medium text-primary hover:text-accent">
              安装与接入 →
            </Link>
          </div>
          <div className="rounded-xl border border-border/65 bg-background/80 p-4">
            <div className="flex items-center gap-1.5 font-mono text-2xs font-medium text-primary">
              <Plug className="size-3.5" aria-hidden="true" />
              读写隔离 MCP 矩阵
            </div>
            <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
              只读诊断开放 <code className="font-mono text-foreground">mcp_doops_k8s_read</code> /{" "}
              <code className="font-mono text-foreground">mcp_dosql_read</code> /{" "}
              <code className="font-mono text-foreground">mcp_doops_web_read</code>；受控变更仅显式{" "}
              <code className="font-mono text-foreground">--apply</code> 开放{" "}
              <code className="font-mono text-foreground">mcp_doops_helm</code> /{" "}
              <code className="font-mono text-foreground">mcp_dosql_exec</code> /{" "}
              <code className="font-mono text-foreground">mcp_doops_web</code>。
            </p>
            <Link href="/docs/deployment" className="mt-3 inline-flex items-center gap-1 text-2xs font-medium text-primary hover:text-accent">
              变更与回执契约 →
            </Link>
          </div>
          <div className="rounded-xl border border-border/65 bg-background/80 p-4">
            <div className="flex items-center gap-1.5 font-mono text-2xs font-medium text-success">
              <Terminal className="size-3.5" aria-hidden="true" />
              Gateway JSON-RPC 原语
            </div>
            <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
              通过 <code className="font-mono text-foreground">/v1/rpc</code> 开放{" "}
              <code className="font-mono text-foreground">doops_shell</code>、
              <code className="font-mono text-foreground">doops_k8s</code>、
              <code className="font-mono text-foreground">doops_terminal</code> 等原子工具；凭据物化与同步严格隔离在{" "}
              <code className="font-mono text-foreground">gatewayInternal</code>。
            </p>
            <Link href="/docs/api-guide" className="mt-3 inline-flex items-center gap-1 text-2xs font-medium text-primary hover:text-accent">
              开放 API 契约 →
            </Link>
          </div>
        </div>
      </section>

      {/* Quick CLI Reference */}
      <section className="card-surface overflow-hidden p-5 sm:p-6">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <FileCode2 className="size-4 text-accent" aria-hidden="true" />
            <h2 className="text-base font-semibold text-foreground">高频 CLI 命令速查</h2>
          </div>
          <Link href="/docs/usage-guide" className="font-mono text-2xs text-accent hover:underline">
            完整命令参考 →
          </Link>
        </div>
        <div className="grid gap-2.5 md:grid-cols-2">
          {CHEAT_SHEET.map((item) => (
            <div key={item.label} className="rounded-lg border border-border/65 bg-background/80 p-3">
              <div className="text-2xs font-medium text-muted-foreground">{item.label}</div>
              <pre className="mt-1.5 overflow-x-auto font-mono text-2xs text-foreground">
                <code>{item.cmd}</code>
              </pre>
            </div>
          ))}
        </div>
      </section>

      {/* Categorized Full Catalog */}
      <div className="space-y-10">
        {DOC_CATEGORIES.map((category) => {
          const items = docsByCategory(category.id)
          return (
            <section key={category.id}>
              <div className="mb-4 border-b border-border/50 pb-3">
                <h2 className="text-lg font-semibold tracking-tight text-foreground">{category.title}</h2>
                <p className="mt-1 text-xs text-muted-foreground">{category.description}</p>
              </div>
              <div className="grid gap-3.5 sm:grid-cols-2">
                {items.map((doc) => {
                  const statusMeta = DOC_STATUS_META[doc.status]
                  return (
                    <Link
                      key={doc.slug}
                      href={docHref(doc.slug)}
                      className="card-surface card-interactive group flex flex-col justify-between p-4"
                    >
                      <div>
                        <div className="flex items-start justify-between gap-2">
                          <h3 className="text-sm font-semibold text-foreground transition-colors group-hover:text-accent">
                            {doc.title}
                          </h3>
                          <span
                            className={`shrink-0 rounded-full border px-2 py-0.5 font-mono text-[10px] ${statusMeta.className}`}
                          >
                            {statusMeta.label}
                          </span>
                        </div>
                        <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                          {doc.description}
                        </p>
                      </div>
                      <div className="mt-3.5 flex items-center justify-between pt-2 font-mono text-[11px] text-muted-foreground/80">
                        <span className="truncate">
                          {doc.sourcePath ? `backend/docs/${doc.sourcePath}` : "/docs/deploy"}
                        </span>
                        <ArrowRight
                          className="size-3.5 shrink-0 text-primary transition-transform group-hover:translate-x-0.5"
                          aria-hidden="true"
                        />
                      </div>
                    </Link>
                  )
                })}
              </div>
            </section>
          )
        })}
      </div>
    </div>
  )
}
