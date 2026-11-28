"use client"

import { useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  BookOpen,
  Boxes,
  Check,
  ChevronDown,
  Code2,
  Copy,
  Database,
  FileCode2,
  Globe,
  KeyRound,
  Layers,
  Lock,
  Radar,
  Rocket,
  Shield,
  ShieldCheck,
  Sparkles,
  Terminal,
  Webhook,
} from "lucide-react"
import { isDemoAllowed } from "@/lib/demo-policy"
import { GITHUB_REPO_URL } from "@/lib/docs-catalog"
import { SiteFooter, SiteHeader } from "./site-chrome"
import { LandingWorkbench } from "./landing-workbench"
import { LandingAiTools } from "./landing-ai-tools"
import { LandingTopology } from "./landing-topology"
import { Button, PortalContainer } from "./ui"

const DEMO_ENABLED = isDemoAllowed()

const HERO_SNIPPETS = [
  {
    id: "login",
    label: "CLI 登录",
    cmd: "doops login --gateway https://gateway.example.com && doops list --status",
  },
  {
    id: "install",
    label: "安装 Skill",
    cmd: "bash scripts/install.sh --version <tag> --project <repo>",
  },
  {
    id: "web",
    label: "本地体验",
    cmd: "pnpm install --frozen-lockfile && pnpm dev",
  },
] as const

const ENGINEERING_STATS = [
  { value: "0", unit: "入站端口", desc: "Agent 主动出站反连网关，内网与 NAT 后无需开放 SSH" },
  { value: "7", unit: "组读写隔离 MCP", desc: "mcp_doops_* 与 mcp_dosql_*，诊断与变更白名单严格分离" },
  { value: "14", unit: "个内置 Skill 包", desc: "6 大领域执行 Skill + 8 大巡检包，支持团队自定义 SKILL.md" },
  { value: "4", unit: "端同一套审计", desc: "Web · CLI · IDE AI（Claude / Codex / Cursor）· Open API" },
]

const SEVEN_GATES_TOP = [
  { n: "01", title: "身份令牌", desc: "SHA-256 哈希核验与常时比对，Web 端存 HttpOnly Cookie，撤销下一条请求即刻生效。" },
  { n: "02", title: "精确目标授权", desc: "按 cluster/instance 授权，未授权的目标在列表与调用中完全不可见。" },
  { n: "03", title: "先记账后执行", desc: "审计事件在执行前落库，终态不可改写；审计存储异常即 Fail-Closed 拒绝服务。" },
  { n: "04", title: "凭据按需物化", desc: "AEAD 静态加密、stdin 注入，仅在授权目标按需物化，绝不进入 Prompt 或命令行。" },
]

const SEVEN_GATES_BOTTOM = [
  { n: "05", title: "双层命令过滤", desc: "Agent 结构化 argv AST 规则 + 正则兜底，联动 Gateway 敏感词策略守住高危操作防线。" },
  { n: "06", title: "AI 工具白名单", desc: "诊断与变更工具严格隔离；变更成功须由实测回执与 attestation 共同证明，不轻信口头总结。" },
  { n: "07", title: "独立控制器升级", desc: "Agent 永不自我替换，升级经独立 K8s Job 执行备份、digest 核验与失败自动回滚。" },
]

const HONEST_PRINCIPLES = [
  {
    icon: Shield,
    title: "诚实边界：DoOps 不是强隔离沙箱",
    desc: "Agent 持有被管节点的真实执行权限，安全隔离由容器、虚拟机与节点本身提供。DoOps 管住的是「谁能进、能去哪、做过什么」。",
  },
  {
    icon: Radar,
    title: "结果未知纪律：绝不盲目重放变更",
    desc: "断连、超时或被信号中断而缺少终态时，统一标记 execution_outcome_unknown 并提供 execution_id 续接查询，避免双写事故。",
  },
  {
    icon: KeyRound,
    title: "凭据全链路脱敏与原地主密钥轮换",
    desc: "支持 DOOPS_GATEWAY_SECRET_KEY 双钥匙窗口原地轮换，审计 URL 自动剥离 userinfo 与 query，后台任务按字符边界脱敏。",
  },
  {
    icon: Database,
    title: "SQLite 开箱即用，MySQL 8.4 规模扩展",
    desc: "单文件 SQLite 零运维启动；生产大规模集群可切换 MySQL 8.4 后端，配套 store mysql-copy 只读快照校验割接。",
  },
]

const QUICKSTART_TRACKS = [
  {
    id: "developer",
    badge: "路径 01",
    title: "开发者接入 CLI 与 IDE Skill",
    summary: "装一次 CLI 与项目 Skill，在本地终端和 AI 编码助手里直接排查、同步代码和发布。",
    docHref: "/docs/cluster-install",
    docLabel: "查看完整接入指南",
    steps: [
      {
        label: "1. 从 GitHub Release 安装 CLI 与项目 Skill（含 sha256 与 commit 校验）",
        code: `git clone --branch <release-tag> --depth 1 https://github.com/symansun/doops.git
cd doops
bash scripts/install.sh --version <release-tag> --project /abs/path/to/your-app \\
  --target-gateway https://gateway.example.com`,
      },
      {
        label: "2. 交互登录并自动发现已授权目标",
        code: `doops login --gateway https://gateway.example.com --username alice
doops list --status
doops -session diag-01 ask --target prod-k8s --msg '只读检查 payments 命名空间异常 Pod，返回证据'`,
      },
    ],
  },
  {
    id: "console",
    badge: "路径 02",
    title: "本地启动 Web 控制台与在线演示",
    summary: "无需部署后端，2 分钟在浏览器体验 AI 终端、DoSql、智能巡检与资产态势工作台。",
    docHref: "/docs/webui",
    docLabel: "查看 Web 控制台指南",
    steps: [
      {
        label: "1. 克隆仓库并安装前端依赖（Node 24 + pnpm 10.18）",
        code: `git clone https://github.com/symansun/doops.git
cd doops
corepack enable && pnpm install --frozen-lockfile
pnpm dev`,
      },
      {
        label: "2. 访问本地工作台、文档中心或演示模式",
        code: `# 门户首页 & 文档中心
http://localhost:3000/
http://localhost:3000/docs

# 控制台 & 离线模拟演示（不连接真实节点）
http://localhost:3000/console
http://localhost:3000/console?demo=1`,
      },
    ],
  },
  {
    id: "selfhost",
    badge: "路径 03",
    title: "自托管 Gateway 与目标 Agent",
    summary: "一条 Go 二进制跑起控制面，目标节点主动反向连接，无需公网 IP 或入站 SSH。",
    docHref: "/docs/deploy",
    docLabel: "查看部署与升级 SOP",
    steps: [
      {
        label: "1. 初始化管理员与 Token 并启动 Gateway（口令不经命令行参数）",
        code: `DB="$HOME/.local/share/doops-gateway/gateway.db"
doops-gateway user create -db "$DB" -name bootstrap-admin -generate-password
doops-gateway grant -db "$DB" -user bootstrap-admin -admin
doops-gateway serve -db "$DB" -port 42222`,
      },
      {
        label: "2. 在受管节点启动 Agent 主动反向连接 Gateway",
        code: `DOOPS_ALLOW_INSECURE_GATEWAY=1 doops-agent \\
  -listen 127.0.0.1 -port 42223 \\
  -gateway-url http://127.0.0.1:42222 \\
  -cluster demo -instance node-1`,
      },
    ],
  },
] as const

const FAQS = [
  {
    q: "DoOps 和传统堡垒机或「frp + SSH」有什么本质区别？",
    a: "堡垒机管的是人的终端会话，frp 解决的是单条隧道；DoOps 是面向开发者和 AI 的统一操作面。Web 控制台、CLI、IDE 里的 AI 编码助手（Skill）与 CI/CD 共享同一套结构化工具、受管凭据、目标授权与全链路审计，支持 K8s Helm 证据化交付、DoSql 多引擎治理与 Skill-First 巡检。",
  },
  {
    q: "Gateway 一定要部署在公网吗？企业内网可以用吗？",
    a: "不需要。Gateway 可以部署在纯内网闭环（依赖内网/VPN 边界）、公网 TLS 边缘（经反向代理终止 TLS），或按生产/测试分域部署多套 Gateway。无论哪种形态，Agent 永远主动出站连接，认证、授权与审计规则完全一致。",
  },
  {
    q: "怎么防止 AI 在生产环境乱执行命令或泄露数据库密码？",
    a: "四重机制协同守门：① 诊断（ask）和变更（ask --apply）工具白名单严格隔离；② 数据库与镜像凭据由 Gateway 加密托管并按需在目标物化，绝不进入 Prompt、参数或前端状态，终端 AI 上下文经过三层脱敏；③ Gateway 敏感词过滤 + Agent 双层 argv/正则命令过滤；④ 所有变更先记账后执行，结果必须以实测回执为准。",
  },
  {
    q: "如何把 DoOps 接入 Claude Code / Codex / Cursor 等开发工具？",
    a: "通过 scripts/install.sh --version <tag> --project <项目路径> 安装后，DoOps 会把校验过的 CLI 装入 ~/.local/bin/doops，并把权威 SKILL.md 与配套文档同步到项目的 .agent/skills/doops/（及已有 .codex/.agents 目录）。开发者在编辑器里即可让 AI 沿用本机登录态执行排障、SQL 治理与受控发布。",
  },
  {
    q: "控制面数据怎么存储？团队规模扩大后如何扩展？",
    a: "默认使用单文件 SQLite，零外部依赖即可跑起完整控制面、资产台账与巡检产物；生产环境需要更高并发与高可用备份时，可设 DOOPS_GATEWAY_STORE=mysql 切换至 MySQL 8.4 后端，并使用内置的 doops-gateway store mysql-copy 完成带校验的快照割接。",
  },
]

function CopyBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false)
  function copy() {
    navigator.clipboard?.writeText(code).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1600)
    })
  }
  return (
    <div className="code-surface group relative overflow-hidden">
      <pre className="docs-scroll overflow-x-auto px-4 py-3.5 pr-20 font-mono text-xs leading-relaxed text-foreground/95">
        {code}
      </pre>
      <button
        type="button"
        onClick={copy}
        aria-label="复制命令"
        className="absolute right-2.5 top-2.5 inline-flex cursor-pointer items-center gap-1 rounded-md border border-border/80 bg-card/90 px-2 py-1 text-2xs text-muted-foreground transition-colors hover:border-primary/45 hover:text-foreground"
      >
        {copied ? (
          <>
            <Check className="size-3 text-success" aria-hidden="true" />
            已复制
          </>
        ) : (
          <>
            <Copy className="size-3" aria-hidden="true" />
            复制
          </>
        )}
      </button>
    </div>
  )
}

export function LandingPage() {
  const [heroTab, setHeroTab] = useState<(typeof HERO_SNIPPETS)[number]["id"]>("login")
  const [heroCopied, setHeroCopied] = useState(false)
  const [trackId, setTrackId] = useState<(typeof QUICKSTART_TRACKS)[number]["id"]>("developer")

  const activeSnippet = HERO_SNIPPETS.find((s) => s.id === heroTab) ?? HERO_SNIPPETS[0]
  const activeTrack = QUICKSTART_TRACKS.find((t) => t.id === trackId) ?? QUICKSTART_TRACKS[0]

  function copyHeroSnippet() {
    navigator.clipboard?.writeText(activeSnippet.cmd).then(() => {
      setHeroCopied(true)
      setTimeout(() => setHeroCopied(false), 1600)
    })
  }

  return (
    <div className="min-h-dvh bg-background text-foreground">
      <SiteHeader />

      {/* 1. HERO SECTION — tuned so Hero + Interactive Workbench sit comfortably in a 1440×900 fold */}
      <section className="relative overflow-hidden pb-16 pt-8 md:pb-24 md:pt-11">
        <div aria-hidden="true" className="portal-grid-bg pointer-events-none absolute inset-0 opacity-75" />
        <div aria-hidden="true" className="hero-ambient pointer-events-none absolute inset-0" />

        <PortalContainer className="relative text-center">
          {/* Announcement / Positioning Pill */}
          <Link
            href="/docs"
            className="brand-gradient group mx-auto mb-5 inline-flex max-w-full rounded-full p-px shadow-lg ring-1 ring-primary/35 transition-transform hover:-translate-y-0.5"
          >
            <span className="inline-flex flex-wrap items-center justify-center gap-x-2 gap-y-0.5 rounded-full bg-background/95 px-3.5 py-1 text-xs text-muted-foreground backdrop-blur sm:px-4">
              <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
                <span className="size-1.5 rounded-full bg-accent" aria-hidden="true" />
                <span className="font-mono text-2xs font-medium text-foreground">MIT 开源</span>
              </span>
              <span aria-hidden="true" className="text-border">·</span>
              <span className="whitespace-nowrap">Gateway-First · AI-Native</span>
              <span className="inline-flex items-center gap-0.5 whitespace-nowrap font-medium text-accent transition-transform group-hover:translate-x-0.5">
                文档中心
                <ArrowRight className="size-3" aria-hidden="true" />
              </span>
            </span>
          </Link>

          <h1 className="mx-auto max-w-4xl text-balance text-4xl font-semibold leading-tight tracking-tight sm:text-5xl lg:text-6xl">
            面向开发者的
            <span className="text-gradient">智能运维中心</span>
          </h1>

          <p className="mx-auto mt-4 max-w-3xl text-pretty text-sm leading-relaxed text-muted-foreground sm:text-base">
            为开发者与 <span className="text-foreground">AI 智能体（Claude Code / Codex / Cursor / doagent）</span>
            打造的受控运维工具箱，一扇反向隧道网关统一收口 Skill、读写隔离 MCP、CLI 与 Web 控制台。
          </p>

          {/* Balanced Primary CTAs */}
          <div className="mt-6 flex flex-col items-center justify-center gap-2.5 sm:flex-row">
            <Button asChild variant="primary" size="md" className="w-full sm:w-auto">
              <Link href="/console">
                <Rocket className="size-4" aria-hidden="true" />
                进入控制台
              </Link>
            </Button>
            {DEMO_ENABLED && (
              <Button asChild size="md" className="w-full bg-card/85 sm:w-auto">
                <Link href="/console?demo=1">
                  <Sparkles className="size-4 text-accent" aria-hidden="true" />
                  在线交互演示
                </Link>
              </Button>
            )}
            <Button asChild size="md" className="w-full border-border/75 bg-background/70 sm:w-auto">
              <Link href="/docs">
                <BookOpen className="size-4 text-muted-foreground" aria-hidden="true" />
                浏览文档中心
              </Link>
            </Button>
          </div>

          {/* Sleek Single-Line Quick-Copy Command Pill (steady width across tab switches) */}
          <div className="mx-auto mt-5 flex w-full max-w-4xl justify-center">
            <div className="flex w-full flex-wrap items-center justify-between gap-2 rounded-xl border border-border/75 bg-card/70 px-2.5 py-1.5 shadow-lg backdrop-blur sm:flex-nowrap">
              <div className="flex shrink-0 items-center gap-0.5 rounded-lg bg-background/80 p-0.5">
                {HERO_SNIPPETS.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    onClick={() => {
                      setHeroTab(s.id)
                      setHeroCopied(false)
                    }}
                    className={
                      heroTab === s.id
                        ? "cursor-pointer whitespace-nowrap rounded-md bg-primary/18 px-2 py-1 text-2xs font-medium text-foreground ring-1 ring-inset ring-primary/30"
                        : "cursor-pointer whitespace-nowrap rounded-md px-2 py-1 text-2xs text-muted-foreground transition-colors hover:text-foreground"
                    }
                  >
                    {s.label}
                  </button>
                ))}
              </div>
              <div className="docs-scroll flex min-w-0 flex-1 items-center gap-2 overflow-x-auto px-1.5 text-left font-mono text-2xs text-foreground">
                <span className="shrink-0 select-none text-accent">$</span>
                <code className="whitespace-nowrap">{activeSnippet.cmd}</code>
              </div>
              <button
                type="button"
                onClick={copyHeroSnippet}
                aria-label="复制命令"
                className="inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-lg border border-border/70 bg-background/85 px-2 py-1 text-2xs text-muted-foreground transition-colors hover:border-primary/45 hover:text-foreground"
              >
                {heroCopied ? (
                  <>
                    <Check className="size-3 text-success" aria-hidden="true" />
                    已复制
                  </>
                ) : (
                  <>
                    <Copy className="size-3" aria-hidden="true" />
                    复制
                  </>
                )}
              </button>
            </div>
          </div>

          {/* Interactive Workbench Centerpiece */}
          <LandingWorkbench />
        </PortalContainer>
      </section>

      {/* 2. ENGINEERING CAPABILITY STRIP */}
      <section className="border-y border-border/50 bg-card/25">
        <PortalContainer className="grid grid-cols-2 gap-6 py-9 lg:grid-cols-4 lg:gap-0 lg:divide-x lg:divide-border/45">
          {ENGINEERING_STATS.map((item) => (
            <div key={item.unit} className="space-y-1.5 lg:px-6 first:lg:pl-0 last:lg:pr-0">
              <div className="flex items-baseline gap-1.5">
                <span className="text-gradient font-mono text-3xl font-semibold tracking-tight sm:text-4xl">
                  {item.value}
                </span>
                <span className="text-sm font-medium text-foreground">{item.unit}</span>
              </div>
              <p className="text-xs leading-relaxed text-muted-foreground">{item.desc}</p>
            </div>
          ))}
        </PortalContainer>
      </section>

      {/* 2.5 AI AGENT TOOLBELT SPOTLIGHT */}
      <LandingAiTools />

      {/* 3. ARCHITECTURE & REVERSE TUNNEL */}
      <section id="architecture" className="scroll-mt-20 border-t border-border/45 py-20 sm:py-24">
        <PortalContainer>
          <div className="mb-10 max-w-3xl">
            <span className="eyebrow">
              <span className="h-px w-6 bg-primary" />
              Gateway-First Architecture
            </span>
            <h2 className="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">
              告别散落的 SSH 密钥，收口为一扇可审计的门
            </h2>
            <p className="mt-3 max-w-2xl text-sm leading-relaxed text-muted-foreground">
              目标 Agent 主动出站建立反向隧道，无需给服务器开放入站端口；开发者、CLI 脚本与 AI 智能体全部经统一控制面鉴权、授权与记账。
            </p>
          </div>

          <LandingTopology />
        </PortalContainer>
      </section>

      {/* 4. ASYMMETRIC BENTO CAPABILITIES MATRIX */}
      <section id="capabilities" className="scroll-mt-20 border-t border-border/45 py-20 sm:py-24">
        <PortalContainer>
          <div className="mb-12 flex flex-col items-start justify-between gap-4 md:flex-row md:items-end">
            <div>
              <span className="eyebrow">
                <span className="h-px w-6 bg-accent" />
                Developer Capabilities
              </span>
              <h2 className="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">
                围绕研发交付全链路设计的六大核心能力域
              </h2>
            </div>
            <Link
              href="/docs"
              className="inline-flex items-center gap-1.5 text-sm font-medium text-accent transition-opacity hover:opacity-85"
            >
              查阅全部文档
              <ArrowRight className="size-4" aria-hidden="true" />
            </Link>
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            {/* Bento 1: Wide (span 2) - Four Entries & AI Terminal */}
            <article className="card-surface card-interactive flex flex-col justify-between p-6 lg:col-span-2">
              <div>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-inset ring-primary/25">
                    <Terminal className="size-5" aria-hidden="true" />
                  </span>
                  <span className="rounded-full border border-border/70 bg-background/70 px-2.5 py-0.5 font-mono text-2xs text-accent">
                    Web · CLI · IDE Skill · API
                  </span>
                </div>
                <h3 className="mt-4 text-lg font-semibold text-card-foreground">
                  类 Warp AI 交互终端与四端协同工作流
                </h3>
                <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground">
                  真实 PTY 交互终端原生支持 OSC 133/7 命令分块、自然语言直输与 <code className="font-mono text-foreground">#</code> /{" "}
                  <code className="font-mono text-foreground">⌘I</code> 终端单次 AI 提问、三层上下文脱敏与按需展开的同屏 AI
                  协同分栏；同时通过 <code className="font-mono text-foreground">doops</code> CLI 与项目{" "}
                  <code className="font-mono text-foreground">SKILL.md</code> 把同一能力无缝带入本地终端和
                  Claude Code / Codex / Cursor。
                </p>
              </div>
              <div className="mt-5 grid gap-2.5 sm:grid-cols-3">
                {[
                  { k: "交互终端", v: "BFF WS 连接池 + 长轮询低延迟" },
                  { k: "IDE 协同", v: "项目级 SKILL.md 固化团队 SOP" },
                  { k: "工作区同步", v: "COS 快照 / Git HTTP 双向 push & pull" },
                ].map((chip) => (
                  <div key={chip.k} className="rounded-lg border border-border/60 bg-background/70 p-3">
                    <div className="font-mono text-2xs font-medium text-foreground">{chip.k}</div>
                    <div className="mt-1 text-2xs text-muted-foreground">{chip.v}</div>
                  </div>
                ))}
              </div>
            </article>

            {/* Bento 2: DoSql */}
            <article className="card-surface card-interactive flex flex-col justify-between p-6">
              <div>
                <div className="flex items-center justify-between">
                  <span className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-accent/15 text-accent ring-1 ring-inset ring-accent/25">
                    <Database className="size-5" aria-hidden="true" />
                  </span>
                  <Link href="/docs/dosql" className="font-mono text-2xs text-muted-foreground hover:text-accent">
                    规范 →
                  </Link>
                </div>
                <h3 className="mt-4 text-lg font-semibold text-card-foreground">
                  DoSql 统一数据治理引擎
                </h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                  原生 Go 引擎覆盖 PG / MySQL / Oracle / SQL Server / MongoDB，提供{" "}
                  <code className="font-mono text-foreground">inspect · diff · datadiff · migrate · backfill</code>
                  、策略门禁与哈希链版本日志。
                </p>
              </div>
              <div className="mt-5 rounded-lg border border-border/65 bg-background/80 p-3 font-mono text-2xs text-muted-foreground">
                <div className="text-accent">$ doops sql --mode migrate</div>
                <div className="mt-1 text-success">✓ .dosql/policy.yaml passed</div>
                <div className="text-foreground/85">✓ credential_mode=auto (0 secrets)</div>
              </div>
            </article>

            {/* Bento 3: Skill-First Inspection (enriched with live telemetry mini-bars!) */}
            <article className="card-surface card-interactive flex flex-col justify-between p-6">
              <div>
                <div className="flex items-center justify-between">
                  <span className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-inset ring-primary/25">
                    <Radar className="size-5" aria-hidden="true" />
                  </span>
                  <Link
                    href="/docs/skill-first-inspection"
                    className="font-mono text-2xs text-muted-foreground hover:text-accent"
                  >
                    规范 →
                  </Link>
                </div>
                <h3 className="mt-4 text-lg font-semibold text-card-foreground">
                  Skill-First 智能巡检与资产态势
                </h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                  内置 8 大巡检 Skill 包与自定义 MCP / Skill 仓库，File-First 落盘产物自带图表、历史趋势与上次对比。
                </p>
              </div>
              <div className="mt-5 space-y-3">
                <div className="space-y-2 rounded-lg border border-border/65 bg-background/80 p-3 font-mono text-2xs">
                  {[
                    { k: "k8s-cluster", val: "12/12", pct: 92, cls: "text-success" },
                    { k: "vm-disk-io", val: "79%", pct: 79, cls: "text-warning" },
                    { k: "db-health", val: "0 locks", pct: 42, cls: "text-accent" },
                  ].map((bar) => (
                    <div key={bar.k} className="space-y-1">
                      <div className="flex items-center justify-between">
                        <span className="text-muted-foreground">{bar.k}</span>
                        <span className={bar.cls}>{bar.val}</span>
                      </div>
                      <div className="progress-track h-1 w-full">
                        <div className="progress-fill" style={{ width: `${bar.pct}%` }} />
                      </div>
                    </div>
                  ))}
                </div>
                <div className="flex flex-wrap gap-1.5 font-mono text-2xs text-muted-foreground">
                  {["vm", "k8s", "container", "database", "site", "cve", "proxy", "ssh"].map((s) => (
                    <span key={s} className="rounded border border-border/70 bg-background/75 px-2 py-0.5">
                      {s}
                    </span>
                  ))}
                </div>
              </div>
            </article>

            {/* Bento 4: Wide (span 2) - Kubernetes, Helm, OCI Images */}
            <article className="card-surface card-interactive flex flex-col justify-between p-6 lg:col-span-2">
              <div>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-accent/15 text-accent ring-1 ring-inset ring-accent/25">
                    <Boxes className="size-5" aria-hidden="true" />
                  </span>
                  <Link href="/docs/deployment" className="font-mono text-2xs text-muted-foreground hover:text-accent">
                    发布契约 →
                  </Link>
                </div>
                <h3 className="mt-4 text-lg font-semibold text-card-foreground">
                  Kubernetes 证据化交付、OCI 镜像与独立控制器升级
                </h3>
                <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground">
                  通过 <code className="font-mono text-foreground">doops k8s</code> 执行只读观察与受控扩缩/重启；通过{" "}
                  <code className="font-mono text-foreground">ask --apply</code> 调用结构化{" "}
                  <code className="font-mono text-foreground">mcp_doops_helm</code> 完成原生 Helm
                  发布并回读真实 revision 与 Pod 就绪态。内置 BuildKit 多平台镜像构建与 Harbor digest 同步。
                </p>
              </div>
              <div className="mt-5 space-y-3">
                <div className="rounded-lg border border-border/65 bg-background/80 px-3.5 py-2.5 font-mono text-2xs text-muted-foreground">
                  <span className="text-success">✓ ATTESTED</span> release=checkout rev=18 · Pods 3/3 Ready · image=@sha256:9d4a…e21c
                </div>
                <div className="grid gap-2.5 sm:grid-cols-3">
                  {[
                    { k: "原生 Helm 终态", v: "不引入黑盒模板，回读 deployed + revision" },
                    { k: "BuildKit & Harbor", v: "按不可变 sha256 digest 构建与同步" },
                    { k: "Agent 自升级", v: "独立 Job 备份 → 核验 commit → 失败回滚" },
                  ].map((chip) => (
                    <div key={chip.k} className="rounded-lg border border-border/60 bg-background/70 p-3">
                      <div className="font-mono text-2xs font-medium text-foreground">{chip.k}</div>
                      <div className="mt-1 text-2xs text-muted-foreground">{chip.v}</div>
                    </div>
                  ))}
                </div>
              </div>
            </article>

            {/* Bento 5: DoWeb & Edge (enriched with crisp pipeline & SiteSpec visual!) */}
            <article className="card-surface card-interactive flex flex-col justify-between p-6">
              <div>
                <div className="flex items-center justify-between">
                  <span className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-inset ring-primary/25">
                    <Globe className="size-5" aria-hidden="true" />
                  </span>
                  <Link href="/docs/doweb" className="font-mono text-2xs text-muted-foreground hover:text-accent">
                    指南 →
                  </Link>
                </div>
                <h3 className="mt-4 text-lg font-semibold text-card-foreground">
                  DoWeb 站点交付与代理路由
                </h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                  通过 SiteSpec 渲染 Nginx 配置，自动串联 <code className="font-mono text-foreground">nginx -t</code>
                  、平滑 reload、健康检查与失败回滚；支持动态反向代理路由治理。
                </p>
              </div>
              <div className="mt-5 space-y-2 rounded-lg border border-border/65 bg-background/80 p-3 font-mono text-2xs">
                <div className="flex items-center justify-between text-muted-foreground">
                  <span>site: app.example.com</span>
                  <span className="text-success">HTTP 200</span>
                </div>
                <div className="flex flex-wrap items-center gap-1.5 pt-1 text-2xs">
                  {["SiteSpec", "nginx -t", "reload", "health", "rollback"].map((step, i) => (
                    <span key={step} className="inline-flex items-center gap-1">
                      <span className="rounded bg-card px-1.5 py-0.5 text-foreground/90 ring-1 ring-inset ring-border/70">
                        {step}
                      </span>
                      {i < 4 ? <span className="text-accent">→</span> : null}
                    </span>
                  ))}
                </div>
              </div>
            </article>

            {/* Bento 6: Managed Credentials, Webhooks & Automation (enriched with 5-stage lifecycle stepper!) */}
            <article className="card-surface card-interactive flex flex-col justify-between p-6 lg:col-span-2">
              <div>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-accent/15 text-accent ring-1 ring-inset ring-accent/25">
                    <KeyRound className="size-5" aria-hidden="true" />
                  </span>
                  <div className="flex items-center gap-3 font-mono text-2xs">
                    <Link href="/docs/credentials" className="text-muted-foreground hover:text-accent">
                      凭据资源 →
                    </Link>
                    <Link href="/docs/webhooks" className="text-muted-foreground hover:text-accent">
                      Webhook →
                    </Link>
                  </div>
                </div>
                <h3 className="mt-4 text-lg font-semibold text-card-foreground">
                  受管凭据全生命周期、出站 Webhook 与自动化集成
                </h3>
                <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground">
                  凭据按严谨状态机推进，支持项目级 <code className="font-mono text-foreground">.agent/doops-credentials.yaml</code>{" "}
                  无密文开发预检；内置 SSRF 防护与 HMAC-SHA256 签名的出站 Webhook，实时联动告警与 SIEM。
                </p>
              </div>
              <div className="mt-5 space-y-3">
                <div className="grid grid-cols-5 gap-1.5 rounded-lg border border-border/65 bg-background/80 p-2.5 font-mono text-2xs">
                  {[
                    { n: "01", s: "create" },
                    { n: "02", s: "stage" },
                    { n: "03", s: "grant" },
                    { n: "04", s: "verify" },
                    { n: "05", s: "promote" },
                  ].map((st, idx) => (
                    <div
                      key={st.s}
                      className={`rounded-md border px-2 py-1.5 text-center ${
                        idx === 4
                          ? "border-success/40 bg-success/10 text-success"
                          : "border-border/60 bg-card/65 text-foreground/90"
                      }`}
                    >
                      <div className="text-2xs text-muted-foreground">{st.n}</div>
                      <div className="font-medium">{st.s}</div>
                    </div>
                  ))}
                </div>
                <div className="flex flex-wrap items-center gap-2 font-mono text-2xs text-muted-foreground">
                  {[
                    { icon: Lock, text: "AEAD + AAD 静态加密" },
                    { icon: ShieldCheck, text: "凭据 Bundle & 离线终结" },
                    { icon: Webhook, text: "HMAC-SHA256 出站 Webhook" },
                    { icon: FileCode2, text: "Metadata-only 预检" },
                  ].map((tag) => {
                    const Icon = tag.icon
                    return (
                      <span
                        key={tag.text}
                        className="inline-flex items-center gap-1.5 rounded-full border border-border/70 bg-background/75 px-3 py-1 text-foreground/90"
                      >
                        <Icon className="size-3 text-accent" aria-hidden="true" />
                        {tag.text}
                      </span>
                    )
                  })}
                </div>
              </div>
            </article>
          </div>
        </PortalContainer>
      </section>

      {/* 5. SEVEN SECURITY GATES (Balanced 4 + 3 layout!) & RADICAL HONESTY */}
      <section id="security" className="scroll-mt-20 border-t border-border/45 bg-card/15 py-20 sm:py-24">
        <PortalContainer>
          <div className="mb-12 text-center">
            <span className="eyebrow justify-center">
              <span className="h-px w-6 bg-border" />
              Defense in Depth
              <span className="h-px w-6 bg-border" />
            </span>
            <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
              一次操作穿过七道门，风险可见、可控、可撤销
            </h2>
            <p className="mx-auto mt-3 max-w-2xl text-pretty text-sm leading-relaxed text-muted-foreground">
              开发者信任的不是“绝对安全”的口号，而是清晰可见的边界。DoOps 把安全落成可核验的工程约束。
            </p>
          </div>

          <div className="space-y-3.5">
            <ol className="grid gap-3.5 sm:grid-cols-2 lg:grid-cols-4">
              {SEVEN_GATES_TOP.map((g) => (
                <li
                  key={g.n}
                  className="card-surface flex flex-col justify-between p-5 transition-colors hover:border-primary/45"
                >
                  <div>
                    <div className="flex items-center justify-between">
                      <span className="rounded bg-primary/12 px-2 py-0.5 font-mono text-2xs font-semibold text-accent">
                        GATE {g.n}
                      </span>
                    </div>
                    <h3 className="mt-3 text-sm font-semibold text-foreground">{g.title}</h3>
                    <p className="mt-2 text-xs leading-relaxed text-muted-foreground">{g.desc}</p>
                  </div>
                </li>
              ))}
            </ol>

            <ol start={5} className="grid gap-3.5 sm:grid-cols-3">
              {SEVEN_GATES_BOTTOM.map((g) => (
                <li
                  key={g.n}
                  className="card-surface flex flex-col justify-between p-5 transition-colors hover:border-primary/45"
                >
                  <div>
                    <div className="flex items-center justify-between">
                      <span className="rounded bg-primary/12 px-2 py-0.5 font-mono text-2xs font-semibold text-accent">
                        GATE {g.n}
                      </span>
                    </div>
                    <h3 className="mt-3 text-sm font-semibold text-foreground">{g.title}</h3>
                    <p className="mt-2 text-xs leading-relaxed text-muted-foreground">{g.desc}</p>
                  </div>
                </li>
              ))}
            </ol>
          </div>

          <div className="mt-8 grid gap-4 md:grid-cols-2">
            {HONEST_PRINCIPLES.map((item) => {
              const Icon = item.icon
              return (
                <div
                  key={item.title}
                  className="flex items-start gap-4 rounded-xl border border-border/65 bg-background/70 p-5"
                >
                  <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/12 text-primary ring-1 ring-inset ring-primary/25">
                    <Icon className="size-5" aria-hidden="true" />
                  </span>
                  <div>
                    <h3 className="text-sm font-semibold text-foreground">{item.title}</h3>
                    <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{item.desc}</p>
                  </div>
                </div>
              )
            })}
          </div>
        </PortalContainer>
      </section>

      {/* 6. INTERACTIVE 3-PATH QUICKSTART — aligned flush to PortalContainer via 12-col split */}
      <section id="quickstart" className="scroll-mt-20 border-t border-border/45 py-20 sm:py-24">
        <PortalContainer>
          <div className="mb-10 flex flex-col items-start justify-between gap-4 md:flex-row md:items-end">
            <div className="max-w-2xl">
              <span className="eyebrow">
                <span className="h-px w-6 bg-accent" />
                Quickstart
              </span>
              <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
                按你的角色，三分钟上手 DoOps
              </h2>
              <p className="mt-3 text-pretty text-sm leading-relaxed text-muted-foreground">
                所有命令均与当前源码与契约测试保持一致，可直接复制执行。
              </p>
            </div>
            <Button asChild size="sm" className="shrink-0">
              <Link href={activeTrack.docHref}>
                <BookOpen className="size-3.5 text-primary" aria-hidden="true" />
                {activeTrack.docLabel}
              </Link>
            </Button>
          </div>

          <div className="grid items-start gap-5 lg:grid-cols-12">
            <div className="grid gap-3 sm:grid-cols-3 lg:col-span-4 lg:grid-cols-1">
              {QUICKSTART_TRACKS.map((t) => {
                const selected = t.id === trackId
                return (
                  <button
                    key={t.id}
                    type="button"
                    onClick={() => setTrackId(t.id)}
                    className={`cursor-pointer rounded-xl border p-4 text-left transition-all ${
                      selected
                        ? "border-primary/70 bg-primary/12 shadow-lg ring-1 ring-primary/35"
                        : "border-border/70 bg-card/45 hover:border-border hover:bg-card/80"
                    }`}
                  >
                    <span className="font-mono text-2xs text-accent">{t.badge}</span>
                    <div className="mt-1 text-sm font-semibold text-foreground">{t.title}</div>
                    <p className="mt-1.5 text-2xs leading-relaxed text-muted-foreground">{t.summary}</p>
                  </button>
                )
              })}
            </div>

            <div className="portal-frame min-w-0 space-y-5 p-5 sm:p-6 lg:col-span-8">
              <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border/55 pb-4">
                <div>
                  <h3 className="text-base font-semibold text-foreground">{activeTrack.title}</h3>
                  <p className="mt-0.5 text-xs text-muted-foreground">{activeTrack.summary}</p>
                </div>
                <span className="rounded-full border border-border/70 bg-background/75 px-2.5 py-0.5 font-mono text-2xs text-accent">
                  {activeTrack.badge}
                </span>
              </div>
              <div className="space-y-4">
                {activeTrack.steps.map((st) => (
                  <div key={st.label} className="space-y-2">
                    <div className="text-xs font-medium text-foreground/90">{st.label}</div>
                    <CopyBlock code={st.code} />
                  </div>
                ))}
              </div>
            </div>
          </div>
        </PortalContainer>
      </section>

      {/* 7. DOCS & ENGINEERING READING HUB */}
      <section className="border-t border-border/45 py-20">
        <PortalContainer>
          <div className="mb-10 flex flex-col items-start justify-between gap-4 sm:flex-row sm:items-end">
            <div>
              <span className="eyebrow">
                <span className="h-px w-6 bg-border" />
                Documentation &amp; Deep Dives
              </span>
              <h2 className="mt-3 text-balance text-2xl font-semibold tracking-tight sm:text-3xl">
                配套权威文档与架构深潜
              </h2>
            </div>
            <p className="max-w-md text-xs text-muted-foreground sm:text-right">
              在线文档中心直接同步仓库 <code className="font-mono text-foreground">backend/docs/</code> 权威源，零文档漂移。
            </p>
          </div>

          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {[
              {
                icon: BookOpen,
                tag: "24 篇权威主题",
                title: "DoOps 文档中心",
                desc: "按接入、能力、安全、运维分栏检索，支持 Mermaid 架构图与目录锚点。",
                href: "/docs",
              },
              {
                icon: Layers,
                tag: "部署 & 升级",
                title: "Agent 集群部署指南",
                desc: "GitOps 初装、upgradeAudit v1 独立控制器升级与完整回滚验收。",
                href: "/docs/deploy",
              },
              {
                icon: Code2,
                tag: "系列长文",
                title: "再见，SSH。",
                desc: "八张架构图讲透 DoOps 如何用一扇门替代散落 SSH 密钥，以及安全边界。",
                href: "/blog/zh/after-goodbye-ssh-what-is-doops-and-security",
              },
              {
                icon: Sparkles,
                tag: "一线实践",
                title: "典型研发运维场景",
                desc: "看全栈团队、多集群 K8s 与数据库治理如何在真实工作流中协同落地。",
                href: "/cases",
              },
            ].map((card) => {
              const Icon = card.icon
              return (
                <Link
                  key={card.title}
                  href={card.href}
                  className="card-surface card-interactive group flex flex-col justify-between p-5"
                >
                  <div>
                    <div className="flex items-center justify-between">
                      <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/12 text-primary ring-1 ring-inset ring-primary/25">
                        <Icon className="size-4" aria-hidden="true" />
                      </span>
                      <span className="font-mono text-2xs text-muted-foreground">{card.tag}</span>
                    </div>
                    <h3 className="mt-4 text-sm font-semibold text-foreground transition-colors group-hover:text-accent">
                      {card.title}
                    </h3>
                    <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{card.desc}</p>
                  </div>
                  <div className="mt-5 inline-flex items-center gap-1 text-xs font-medium text-primary">
                    立即阅读
                    <ArrowRight
                      className="size-3.5 transition-transform group-hover:translate-x-0.5"
                      aria-hidden="true"
                    />
                  </div>
                </Link>
              )
            })}
          </div>
        </PortalContainer>
      </section>

      {/* 8. FAQ — 12-col editorial split aligned flush with PortalContainer */}
      <section id="faq" className="scroll-mt-20 border-t border-border/45 py-20 sm:py-24">
        <PortalContainer className="grid items-start gap-8 lg:grid-cols-12">
          <div className="lg:sticky lg:top-24 lg:col-span-4">
            <span className="eyebrow">
              <span className="h-px w-6 bg-border" />
              FAQ
            </span>
            <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
              开发者常见疑问
            </h2>
            <p className="mt-3 text-sm leading-relaxed text-muted-foreground">
              围绕反向隧道、安全边界、受管凭据与 AI 协同机制的常见问题解答。
            </p>
            <Link
              href="/docs"
              className="mt-5 inline-flex items-center gap-1.5 text-xs font-medium text-accent transition-opacity hover:opacity-85"
            >
              查阅完整工程文档
              <ArrowRight className="size-3.5" aria-hidden="true" />
            </Link>
          </div>
          <div className="flex flex-col gap-3 lg:col-span-8">
            {FAQS.map((item, idx) => (
              <details
                key={item.q}
                className="card-surface group overflow-hidden [&_summary::-webkit-details-marker]:hidden"
              >
                <summary className="flex cursor-pointer list-none items-center justify-between gap-4 px-5 py-4 text-sm font-medium text-card-foreground transition-colors hover:bg-foreground/[0.03]">
                  <span className="flex items-center gap-3">
                    <span className="font-mono text-2xs text-accent">
                      {String(idx + 1).padStart(2, "0")}
                    </span>
                    {item.q}
                  </span>
                  <ChevronDown
                    className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180"
                    aria-hidden="true"
                  />
                </summary>
                <p className="px-5 pb-5 pl-12 text-xs leading-relaxed text-muted-foreground sm:text-sm">
                  {item.a}
                </p>
              </details>
            ))}
          </div>
        </PortalContainer>
      </section>

      {/* 9. FINAL CTA — framed full-width inside PortalContainer */}
      <section className="border-t border-border/45 py-20 sm:py-24">
        <PortalContainer>
          <div className="portal-frame relative overflow-hidden px-6 py-14 text-center sm:px-12 sm:py-16">
            <div aria-hidden="true" className="portal-grid-bg pointer-events-none absolute inset-0 opacity-45" />
            <div aria-hidden="true" className="hero-ambient pointer-events-none absolute inset-0 opacity-85" />
            <div className="relative mx-auto max-w-3xl">
              <span className="eyebrow justify-center">
                <span className="h-px w-6 bg-primary" />
                Open Source · MIT
                <span className="h-px w-6 bg-accent" />
              </span>
              <h2 className="mt-4 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
                用一扇可审计的门，重塑你的开发者运维体验
              </h2>
              <p className="mx-auto mt-3 max-w-xl text-pretty text-sm leading-relaxed text-muted-foreground sm:text-base">
                从一台测试机或一个在线演示开始，把终端排障、数据库治理、K8s 发布与智能巡检统一收拢到 DoOps。
              </p>
              <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
                <Button asChild variant="primary" size="lg" className="w-full sm:w-auto">
                  <Link href="/console">
                    <Rocket className="size-4" aria-hidden="true" />
                    进入控制台
                  </Link>
                </Button>
                <Button asChild size="lg" className="w-full bg-card/85 sm:w-auto">
                  <Link href="/docs">
                    <BookOpen className="size-4 text-accent" aria-hidden="true" />
                    阅读文档中心
                  </Link>
                </Button>
                <Button asChild variant="ghost" size="lg" className="w-full sm:w-auto">
                  <a href={GITHUB_REPO_URL} target="_blank" rel="noreferrer">
                    查看 GitHub 源码
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </a>
                </Button>
              </div>
            </div>
          </div>
        </PortalContainer>
      </section>

      <SiteFooter />
    </div>
  )
}
