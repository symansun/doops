"use client"

import { useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Blocks,
  Bot,
  Check,
  Code2,
  Cpu,
  KeyRound,
  Lock,
  Plug,
  ShieldAlert,
  ShieldCheck,
  Sparkles,
  Terminal,
} from "lucide-react"
import { PortalContainer } from "./ui"

type TabId = "ide-skill" | "mcp-matrix" | "rpc-primitives"

const TABS: {
  id: TabId
  badge: string
  title: string
  subtitle: string
  icon: typeof Bot
}[] = [
  {
    id: "ide-skill",
    badge: "给外部 AI 编码助手",
    title: "IDE Agent Skill + CLI",
    subtitle: "Claude Code / Codex / Cursor 直接调用",
    icon: Code2,
  },
  {
    id: "mcp-matrix",
    badge: "给内置与托管 Agent",
    title: "读写隔离 MCP 工具矩阵",
    subtitle: "mcp_doops_* · mcp_dosql_* · MCP 注册中心",
    icon: Plug,
  },
  {
    id: "rpc-primitives",
    badge: "给自动化与自定义 Agent",
    title: "Gateway JSON-RPC 工具原语",
    subtitle: "公开执行面与控制面内部工具严格分层",
    icon: Blocks,
  },
]

const READ_MCP_TOOLS = [
  {
    name: "mcp_doops_k8s_read",
    scope: "Kubernetes 只读",
    ops: "get · describe · logs · events · top · helm list/values(仅键名)",
    guard: "禁止读取 Secret / ServiceAccount，不回退自由 kubectl",
  },
  {
    name: "mcp_dosql_read",
    scope: "DoSql 五引擎只读",
    ops: "query · discover · inspect · diff · datadiff · status · versions",
    guard: "凭据由 Gateway 自动物化，只读连接杜绝写副作用",
  },
  {
    name: "mcp_doops_web_read",
    scope: "DoWeb 站点巡检",
    ops: "SiteSpec 检查 · Nginx 状态 · TLS 与健康探针读取",
    guard: "纯只读观测，无法修改 Nginx 配置或触发重载",
  },
  {
    name: "mcp_doops_kb_read",
    scope: "运维知识库检索",
    ops: "读取项目与团队沉淀的 Runbook、排障规范与架构说明",
    guard: "受工作区相对路径约束，不越界读取宿主目录",
  },
]

const WRITE_MCP_TOOLS = [
  {
    name: "mcp_doops_helm",
    scope: "Helm 证据化交付（--apply）",
    ops: "helm upgrade --install --wait + 实测 release/revision 回读",
    guard: "需显式 --apply 与变更授权，仅 deployed 且 revision 匹配才判成功",
  },
  {
    name: "mcp_dosql_exec",
    scope: "DoSql 受控变更（--mode apply/migrate）",
    ops: "apply · migrate · backfill · cross-db copy（Agent 直读直写）",
    guard: "强制经 .dosql/policy.yaml 门禁与哈希链版本日志，不可写即拒执行",
  },
  {
    name: "mcp_doops_web",
    scope: "DoWeb 站点发布（skill=doweb）",
    ops: "SiteSpec 渲染 → nginx -t → reload → health check → 失败自动回滚",
    guard: "按 nginx:<cluster>/<instance> 资源互斥，与普通 apply 工具隔离",
  },
]

const PUBLIC_RPC_TOOLS = [
  { name: "doops_shell", desc: "确定性命令执行（经双层 argv + 正则过滤）" },
  { name: "doops_bg / task_status", desc: "长任务后台托管、日志流与优雅取消" },
  { name: "doops_terminal", desc: "真实 PTY 交互终端（OSC 133/7 + 长轮询）" },
  { name: "doops_k8s", desc: "结构化 K8s 观察与受控扩缩/重启/驱逐" },
  { name: "doops_ssh", desc: "参数化嵌套 SSH（防多层引号重解析）" },
  { name: "doops_agent_prompt", desc: "驱动目标侧 doagent + Skill 执行多步任务" },
  { name: "doops_file_read / write", desc: "会话工作区相对路径小文本安全读写" },
  { name: "doops_workspace_*", desc: "代码/产物快照上传与拉回（COS / Git）" },
  { name: "doops_database_inventory", desc: "无 Secret 的 K8s 数据库资产只读发现" },
  { name: "doops_node_info / check", desc: "节点资源、工具链与部署镜像一致性核验" },
  { name: "doops_agent_upgrade", desc: "独立 Job 控制器升级、核验与自动回滚" },
]

const INTERNAL_ONLY_TOOLS = [
  { name: "doops_credential_materialize", why: "仅 Gateway 私有控制面下发解密凭据至目标" },
  { name: "doops_credential_import", why: "仅平台管理员经私有通道安全导入指定 Secret" },
  { name: "doops_credential_cleanup", why: "凭据吊销时强制清理目标侧物化 Secret" },
  { name: "skill_sync / mcp_sync", why: "仅控制面同步受审 Skill 包与加密 MCP 配置" },
]

const AI_CONTRACTS = [
  {
    icon: ShieldCheck,
    title: "工具白名单严格隔离",
    desc: "诊断（ask）拿不到变更工具，变更（--apply）拿不到自由 Bash，DoSql 与 Helm 互不越界。",
  },
  {
    icon: KeyRound,
    title: "凭据 0 字节进入 Prompt",
    desc: "AI 仅传递资源名 --credential <name> 或走 credential_mode=auto，密文在目标侧按需物化。",
  },
  {
    icon: Cpu,
    title: "成功由实测回执裁决",
    desc: "模型口头说「已完成」不算数，必须由原生命令终态、回读版本号与 attestation 共同证明。",
  },
  {
    icon: Lock,
    title: "与人类完全同源审计",
    desc: "AI 不享有隐藏后门，复用开发者的 Token、Target 授权与 Fail-Closed 审计；撤权即刻生效。",
  },
]

export function LandingAiTools() {
  const [tab, setTab] = useState<TabId>("ide-skill")

  return (
    <section id="ai-tools" className="scroll-mt-20 py-20 sm:py-24">
      <PortalContainer>
        {/* Section Header */}
        <div className="mb-10 flex flex-col items-start justify-between gap-4 lg:flex-row lg:items-end">
          <div className="max-w-3xl">
            <span className="eyebrow">
              <span className="h-px w-6 bg-accent" />
              Tools Built for AI Agents
            </span>
            <h2 className="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">
              不止给人用：为 <span className="text-gradient">AI 智能体</span> 打造的受控运维工具箱
            </h2>
            <p className="mt-3 max-w-2xl text-sm leading-relaxed text-muted-foreground">
              DoOps 不是给旧工具贴一个聊天框，而是把 AI 当成与人类并列的正式执行者——提供
              <span className="text-foreground">项目级 SKILL.md、读写隔离 MCP 工具矩阵、开放 JSON-RPC 原语</span>
              与结构化回执，让 Claude Code、Codex、Cursor 与内置 doagent 既能直接干活，又绝不越权失控。
            </p>
          </div>
          <Link
            href="/docs/skill-integration"
            className="inline-flex shrink-0 items-center gap-1.5 text-sm font-medium text-accent transition-opacity hover:opacity-85"
          >
            查看 Skill 与 MCP 集成文档
            <ArrowRight className="size-4" aria-hidden="true" />
          </Link>
        </div>

        {/* 3-Layer Interactive Selector */}
        <div role="tablist" aria-label="提供给 AI 的三层工具面" className="mb-5 grid gap-3 md:grid-cols-3">
          {TABS.map((item) => {
            const Icon = item.icon
            const active = tab === item.id
            return (
              <button
                key={item.id}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => setTab(item.id)}
                className={`cursor-pointer rounded-xl border p-4 text-left transition-all ${
                  active
                    ? "border-accent/65 bg-primary/12 shadow-lg ring-1 ring-accent/35"
                    : "border-border/70 bg-card/50 hover:border-border hover:bg-card/85"
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="inline-flex items-center gap-1.5 font-mono text-2xs text-accent">
                    <Icon className="size-3.5" aria-hidden="true" />
                    {item.badge}
                  </span>
                  {active ? <span className="size-2 rounded-full bg-accent" aria-hidden="true" /> : null}
                </div>
                <div className="mt-2 text-base font-semibold text-foreground">{item.title}</div>
                <div className="mt-1 font-mono text-2xs text-muted-foreground">{item.subtitle}</div>
              </button>
            )
          })}
        </div>

        {/* Interactive Stage */}
        <div className="portal-frame overflow-hidden p-5 sm:p-6">
          {tab === "ide-skill" && (
            <div className="grid gap-6 lg:grid-cols-12">
              {/* Left: Installed Project Skill Tree */}
              <div className="flex flex-col justify-between gap-4 lg:col-span-5">
                <div>
                  <span className="inline-flex items-center gap-1.5 rounded-md bg-primary/12 px-2.5 py-0.5 font-mono text-2xs text-primary">
                    <Terminal className="size-3" aria-hidden="true" />
                    一键同步到开发者的 AI 编辑器
                  </span>
                  <h3 className="mt-3 text-lg font-semibold text-foreground">
                    装入项目 Skill，让编码助手直接拥有生产级运维双手
                  </h3>
                  <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                    执行 <code className="font-mono text-foreground">scripts/install.sh</code> 后，DoOps 自动把校验过的{" "}
                    <code className="font-mono text-foreground">doops</code> CLI、权威{" "}
                    <code className="font-mono text-foreground">SKILL.md</code> 与完整配套文档写入项目和本机客户端目录（同步覆盖{" "}
                    <code className="font-mono text-foreground">.agent/</code>、<code className="font-mono text-foreground">.codex/</code>、<code className="font-mono text-foreground">.agents/</code>）。
                  </p>
                </div>

                <div className="code-surface p-4 font-mono text-2xs leading-relaxed text-muted-foreground">
                  <div className="mb-2 flex items-center justify-between border-b border-border/55 pb-2 text-foreground">
                    <span>your-project/</span>
                    <span className="text-accent">AI 自动发现上下文</span>
                  </div>
                  <div>├── .agent/skills/doops/</div>
                  <div className="text-foreground">│   ├── SKILL.md <span className="text-accent"># 红线 + 命令选择 + 故障入口</span></div>
                  <div>│   └── docs/*.md <span className="text-muted-foreground/80"># 权威主题手册</span></div>
                  <div className="text-foreground">├── .agent/doops-credentials.yaml <span className="text-success"># 无密文预检清单</span></div>
                  <div className="text-foreground">└── .dosql/policy.yaml <span className="text-primary"># SQL 策略即代码门禁</span></div>
                </div>
              </div>

              {/* Right: Live AI Agent Tool Call Flow */}
              <div className="flex flex-col justify-between gap-3 rounded-xl border border-border/65 bg-background/85 p-4 lg:col-span-7">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border/55 pb-3">
                  <span className="inline-flex items-center gap-2 text-xs font-semibold text-foreground">
                    <Bot className="size-4 text-accent" aria-hidden="true" />
                    AI 编码助手真实调用链路（Claude Code / Codex / Cursor）
                  </span>
                  <span className="rounded bg-success/15 px-2 py-0.5 font-mono text-2xs text-success">
                    0 SSH Key · 0 Password
                  </span>
                </div>

                <div className="space-y-2.5 font-mono text-2xs">
                  <div className="rounded-lg border border-border/60 bg-card/55 p-3">
                    <div className="flex items-center justify-between text-muted-foreground">
                      <span>Step 1 · 凭据元数据预检（Fail-Closed）</span>
                      <span className="text-success">PREFLIGHT OK</span>
                    </div>
                    <div className="mt-1 text-foreground">
                      $ doops -session rel-9 credential preflight --manifest .agent/doops-credentials.yaml
                    </div>
                  </div>

                  <div className="rounded-lg border border-border/60 bg-card/55 p-3">
                    <div className="flex items-center justify-between text-muted-foreground">
                      <span>Step 2 · 执行结构化数据治理或 K8s 交付</span>
                      <span className="text-accent">STRUCTURED JSON</span>
                    </div>
                    <div className="docs-scroll mt-1 overflow-x-auto whitespace-nowrap text-foreground">
                      $ doops -session rel-9 sql --target prod-db --mode diff --database orders
                    </div>
                    <div className="docs-scroll mt-1 overflow-x-auto whitespace-nowrap text-foreground">
                      $ doops -session rel-9 ask --target prod-k8s --apply --credential harbor-pull --msg &apos;升级 api&apos;
                    </div>
                  </div>

                  <div className="rounded-lg border border-primary/35 bg-primary/[0.06] p-3">
                    <div className="flex items-center justify-between text-primary">
                      <span>Step 3 · 机器可读回执 + 失败自修复引导</span>
                      <span className="text-success">EXIT 0 · AUDITED</span>
                    </div>
                    <div className="mt-1 font-sans text-xs text-muted-foreground">
                      返回实测 Pod/Revision 回执与稳定错误码（含 <code className="font-mono text-foreground">next:</code> 建议命令），AI 无需猜测屏幕输出即可继续验证或精准交接。
                    </div>
                  </div>
                </div>
              </div>
            </div>
          )}

          {tab === "mcp-matrix" && (
            <div className="space-y-5">
              <div className="grid gap-4 lg:grid-cols-2">
                {/* Read-Only MCP Tools */}
                <div className="rounded-xl border border-border/65 bg-background/80 p-4">
                  <div className="mb-3 flex items-center justify-between gap-2 border-b border-border/55 pb-2.5">
                    <span className="inline-flex items-center gap-2 text-sm font-semibold text-foreground">
                      <Sparkles className="size-4 text-accent" aria-hidden="true" />
                      只读观察 MCP 白名单（ask 模式）
                    </span>
                    <span className="rounded-full bg-accent/12 px-2.5 py-0.5 font-mono text-2xs text-accent">
                      READ-ONLY
                    </span>
                  </div>
                  <div className="space-y-2.5">
                    {READ_MCP_TOOLS.map((t) => (
                      <div key={t.name} className="rounded-lg border border-border/55 bg-card/50 p-3">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <code className="font-mono text-xs font-semibold text-accent">{t.name}</code>
                          <span className="text-2xs text-muted-foreground">{t.scope}</span>
                        </div>
                        <div className="mt-1 font-mono text-2xs text-foreground/85">{t.ops}</div>
                        <div className="mt-1.5 flex items-center gap-1.5 text-2xs text-muted-foreground">
                          <Check className="size-3 shrink-0 text-success" aria-hidden="true" />
                          <span>{t.guard}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Controlled Mutation MCP Tools */}
                <div className="flex flex-col justify-between gap-4 rounded-xl border border-primary/35 bg-primary/[0.04] p-4">
                  <div>
                    <div className="mb-3 flex items-center justify-between gap-2 border-b border-border/55 pb-2.5">
                      <span className="inline-flex items-center gap-2 text-sm font-semibold text-foreground">
                        <ShieldCheck className="size-4 text-primary" aria-hidden="true" />
                        受控变更 MCP 白名单（显式 --apply）
                      </span>
                      <span className="rounded-full bg-primary/15 px-2.5 py-0.5 font-mono text-2xs text-primary">
                        ATTESTED MUTATION
                      </span>
                    </div>
                    <div className="space-y-2.5">
                      {WRITE_MCP_TOOLS.map((t) => (
                        <div key={t.name} className="rounded-lg border border-border/60 bg-background/80 p-3">
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <code className="font-mono text-xs font-semibold text-primary">{t.name}</code>
                            <span className="text-2xs text-muted-foreground">{t.scope}</span>
                          </div>
                          <div className="mt-1 font-mono text-2xs text-foreground/85">{t.ops}</div>
                          <div className="mt-1.5 flex items-center gap-1.5 text-2xs text-muted-foreground">
                            <Lock className="size-3 shrink-0 text-accent" aria-hidden="true" />
                            <span>{t.guard}</span>
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>

                  <div className="rounded-lg border border-border/65 bg-background/85 p-3 text-xs text-muted-foreground">
                    <span className="font-medium text-foreground">平台自定义 MCP Registry：</span>
                    平台管理员可集中登记团队内部 MCP Server 并一键同步至目标 Agent；敏感{" "}
                    <code className="font-mono text-foreground">env</code> 与{" "}
                    <code className="font-mono text-foreground">headers</code> 经{" "}
                    <code className="font-mono text-foreground">DOOPS_GATEWAY_SECRET_KEY</code> AEAD 加密落库，API 仅返回键名。
                  </div>
                </div>
              </div>
            </div>
          )}

          {tab === "rpc-primitives" && (
            <div className="grid gap-5 lg:grid-cols-12">
              <div className="lg:col-span-8">
                <div className="mb-3 flex items-center justify-between">
                  <span className="text-sm font-semibold text-foreground">
                    公开客户端 / Agent 可调用原语（`/v1/rpc` · JSON-RPC over WS/HTTP）
                  </span>
                  <span className="rounded bg-success/15 px-2 py-0.5 font-mono text-2xs text-success">
                    clientCallable
                  </span>
                </div>
                <div className="grid gap-2 sm:grid-cols-2">
                  {PUBLIC_RPC_TOOLS.map((tool) => (
                    <div
                      key={tool.name}
                      className="rounded-lg border border-border/60 bg-background/80 px-3 py-2.5"
                    >
                      <code className="font-mono text-xs font-medium text-accent">{tool.name}</code>
                      <p className="mt-0.5 text-2xs text-muted-foreground">{tool.desc}</p>
                    </div>
                  ))}
                </div>
              </div>

              <div className="flex flex-col justify-between gap-4 rounded-xl border border-destructive/30 bg-destructive/[0.04] p-4 lg:col-span-4">
                <div>
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <span className="inline-flex items-center gap-1.5 text-sm font-semibold text-foreground">
                      <ShieldAlert className="size-4 text-destructive" aria-hidden="true" />
                      控制面严格隔离工具
                    </span>
                    <span className="rounded bg-destructive/15 px-2 py-0.5 font-mono text-2xs text-destructive">
                      gatewayInternal
                    </span>
                  </div>
                  <p className="mb-3 text-xs leading-relaxed text-muted-foreground">
                    以下高敏工具仅允许 Gateway 内部控制链路触发，任何公开 RPC 或 AI 调用尝试都会被网关直接拒绝：
                  </p>
                  <div className="space-y-2">
                    {INTERNAL_ONLY_TOOLS.map((tool) => (
                      <div key={tool.name} className="rounded-lg border border-border/60 bg-background/85 p-2.5">
                        <code className="font-mono text-2xs font-medium text-foreground">{tool.name}</code>
                        <p className="mt-0.5 text-2xs text-muted-foreground">{tool.why}</p>
                      </div>
                    ))}
                  </div>
                </div>
                <Link
                  href="/docs/api-guide"
                  className="inline-flex items-center gap-1.5 text-xs font-medium text-accent transition-opacity hover:opacity-85"
                >
                  查看完整 API 与 RPC 契约
                  <ArrowRight className="size-3.5" aria-hidden="true" />
                </Link>
              </div>
            </div>
          )}
        </div>

        {/* Bottom 4 AI-Native Engineering Contracts */}
        <div className="mt-5 grid gap-3.5 sm:grid-cols-2 lg:grid-cols-4">
          {AI_CONTRACTS.map((c) => {
            const Icon = c.icon
            return (
              <div
                key={c.title}
                className="flex flex-col gap-2 rounded-xl border border-border/65 bg-card/45 p-4"
              >
                <div className="flex items-center gap-2 text-xs font-semibold text-foreground">
                  <Icon className="size-4 shrink-0 text-accent" aria-hidden="true" />
                  <span>{c.title}</span>
                </div>
                <p className="text-xs leading-relaxed text-muted-foreground">{c.desc}</p>
              </div>
            )
          })}
        </div>
      </PortalContainer>
    </section>
  )
}
