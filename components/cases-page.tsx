"use client"

import { useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  BookOpen,
  Boxes,
  Check,
  Code2,
  Copy,
  Database,
  Radar,
  Rocket,
  Shield,
  Sparkles,
  Terminal,
} from "lucide-react"
import { isDemoAllowed } from "@/lib/demo-policy"
import { SiteFooter, SiteHeader } from "./site-chrome"
import { Button, PortalContainer, SegmentedControl } from "./ui"

const DEMO_ENABLED = isDemoAllowed()

type ScenarioId = "all" | "delivery" | "dosql" | "terminal" | "inspect"

type Scenario = {
  id: Exclude<ScenarioId, "all">
  tag: string
  icon: typeof Sparkles
  team: string
  scene: string
  challenge: string
  workflow: string
  command: string
  outcomes: string[]
  docHref: string
  docLabel: string
}

const SCENARIOS: Scenario[] = [
  {
    id: "delivery",
    tag: "AI 编码闭环交付",
    icon: Code2,
    team: "全栈研发团队 · 无专职 SRE",
    scene: "在 Claude Code / Codex / Cursor 里直接完成镜像构建、Helm 发布与上线核验",
    challenge:
      "开发同学熟悉业务代码，但每次发布要在 kubeconfig、Harbor 密码、SSH 跳板机和文档间反复切换，极易把密钥误贴进聊天框或改错命名空间。",
    workflow:
      "在业务仓库通过 scripts/install.sh 装入 DoOps CLI 与项目 SKILL.md，配置 .agent/doops-credentials.yaml 元数据预检清单。AI 编码助手通过受控的 ask --apply 调用原生 mcp_doops_helm，并自动回读真实 revision 与 Pod 就绪态。",
    command:
      "doops -session rel-api ask --target prod-k8s --apply --credential harbor-pull --msg '升级 apps/api 到 1.8.2 并核验 Pod 就绪'",
    outcomes: [
      "Prompt、Git 与日志中零凭据明文泄露",
      "以原生 Helm deployed + revision 实测回执为准",
      "每次发布自动落库 Gateway 审计账本，按操作号追溯",
    ],
    docHref: "/docs/deployment",
    docLabel: "自然语言与 Helm 交付契约",
  },
  {
    id: "dosql",
    tag: "多引擎数据治理",
    icon: Database,
    team: "后端与 DBA 协同团队",
    scene: "跨开发 / 预发 / 生产多库执行 Schema 对比、迁移与数据核验",
    challenge:
      "传统改表靠人工传 SQL 文件、各自拿数据库密码直连客户端，缺少统一变更门禁与历史版本审计，误删表或长锁表风险极高。",
    workflow:
      "启用 DoSql 统一 Go 数据治理引擎：数据库凭据托管在 Gateway 并由 credential_mode=auto 内部物化，团队在仓库维护 .dosql/policy.yaml 门禁规则，用 diff / migrate / datadiff 完成全流程治理并写入哈希链版本日志。",
    command:
      "doops -session db-mig sql --target prod-db --mode migrate --database orders --dir .dosql/migrations",
    outcomes: [
      "统一覆盖 PG / MySQL / Oracle / SQL Server / MongoDB",
      "Policy-as-Code 拦截高危 DDL，PG CONCURRENTLY 自动分段",
      "哈希链版本日志记录每次变更指纹与回读验证结果",
    ],
    docHref: "/docs/dosql",
    docLabel: "DoSql 数据治理引擎规范",
  },
  {
    id: "terminal",
    tag: "AI 交互终端排障",
    icon: Terminal,
    team: "高并发线上业务值班组",
    scene: "凌晨突发告警时快速定位 Pod 崩溃、磁盘飙升或慢查询根因",
    challenge:
      "传统方式要逐台 SSH 登录查日志、翻命令参数，排查过程零散留在个人终端里；交接班时说不清刚才查了什么、改了什么。",
    workflow:
      "打开 DoOps Web 控制台交互终端（真实 PTY + OSC 133 分块），发现异常 Pod 后直接输入中文自然语言（或 # / ⌘I）发起单次 AI 提问——问题与上下文自动脱敏、不打断当前终端也不弹右侧栏，直接返回只读命令卡片，经敏感词与 argv 规则审计后一键运行，复杂问题再按需转入同屏 AI 协同分栏深挖。",
    command:
      "doops -session oncall-03 ask --target prod-k8s --msg '只读检查 payments 命名空间异常 Pod 与近期事件，列出证据'",
    outcomes: [
      "零入站 SSH 端口直达内网集群，支持断线续接",
      "三层输出脱敏保护日志中 Token / DSN 不进入模型",
      "排障命令与回执全量落库，交接班直接按审计复盘",
    ],
    docHref: "/docs/webui",
    docLabel: "Web 控制台与 AI 终端指南",
  },
  {
    id: "inspect",
    tag: "混合基础设施巡检",
    icon: Radar,
    team: "多集群 / 混合云基础设施团队",
    scene: "纳管分布在不同内网、云厂商与边缘机房的 VM、容器、K8s 与数据库",
    challenge:
      "节点分散在 NAT 和防火墙后，健康巡检脚本版本五花八门，历史趋势与上次巡检差异难以直观比对，告警通道各自为政。",
    workflow:
      "各区域节点部署 doops-agent 主动反向连入统一或分域 Gateway，启用 8 大内置 Skill-First 巡检包按计划调度执行；产物以 File-First 落盘并生成水位图表、上次对比与历史趋势，异常事件通过 HMAC-SHA256 出站 Webhook 推送到团队告警平台。",
    command: "doops -session fleet targets --target edge-cluster --admission",
    outcomes: [
      "业务系统 → 集群 → 机器台账关联，支持盘点覆盖率对账",
      "巡检产物自动渲染水位条、仪表盘、趋势曲线与 Diff",
      "Agent 升级由独立 Job 备份、核验与失败自动回滚",
    ],
    docHref: "/docs/skill-first-inspection",
    docLabel: "Skill-First 智能巡检规范",
  },
]

const FILTERS: { id: ScenarioId; label: string }[] = [
  { id: "all", label: "全部场景" },
  { id: "delivery", label: "AI 编码交付" },
  { id: "dosql", label: "DoSql 数据治理" },
  { id: "terminal", label: "AI 交互终端" },
  { id: "inspect", label: "智能巡检" },
]

function CommandLine({ cmd }: { cmd: string }) {
  const [copied, setCopied] = useState(false)
  function copy() {
    navigator.clipboard?.writeText(cmd).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }
  return (
    <div className="code-surface flex items-start justify-between gap-3 px-4 py-3">
      <div className="flex min-w-0 flex-1 items-start gap-2 font-mono text-2xs leading-relaxed text-foreground">
        <span className="shrink-0 select-none text-accent">$</span>
        <code className="break-all sm:break-words">{cmd}</code>
      </div>
      <button
        type="button"
        onClick={copy}
        aria-label="复制命令"
        className="inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-md border border-border/75 bg-card/85 px-2 py-1 text-2xs text-muted-foreground transition-colors hover:border-primary/45 hover:text-foreground"
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

export function CasesPage() {
  const [filter, setFilter] = useState<ScenarioId>("all")
  const visible = filter === "all" ? SCENARIOS : SCENARIOS.filter((s) => s.id === filter)

  return (
    <div className="min-h-dvh bg-background text-foreground">
      <SiteHeader />

      {/* Hero */}
      <section className="relative overflow-hidden border-b border-border/45">
        <div aria-hidden="true" className="portal-grid-bg pointer-events-none absolute inset-0 opacity-65" />
        <div aria-hidden="true" className="hero-ambient pointer-events-none absolute inset-0" />
        <PortalContainer className="relative pb-16 pt-16 text-center md:pb-20 md:pt-20">
          <div className="brand-gradient mx-auto mb-5 inline-flex rounded-full p-px">
            <span className="inline-flex items-center gap-2 rounded-full bg-background/95 px-3.5 py-1 font-mono text-2xs font-medium uppercase tracking-widest text-muted-foreground backdrop-blur">
              <Sparkles className="size-3.5 text-accent" aria-hidden="true" />
              Developer Scenarios
            </span>
          </div>
          <h1 className="mx-auto max-w-4xl text-balance text-4xl font-semibold leading-tight tracking-tight sm:text-5xl">
            开发者如何用 DoOps 重构
            <span className="text-gradient">日常运维与交付</span>
          </h1>
          <p className="mx-auto mt-4 max-w-2xl text-pretty text-sm leading-relaxed text-muted-foreground sm:text-base">
            以下场景均基于 DoOps 现行已实现的协议、CLI 命令与控制面契约，按研发交付与排障主线拆解落地路径。
          </p>
          <div className="mt-7 flex flex-col items-center justify-center gap-2.5 sm:flex-row">
            {DEMO_ENABLED && (
              <Button asChild variant="primary" size="md" className="w-full sm:w-auto">
                <Link href="/console?demo=1">
                  <Sparkles className="size-4" aria-hidden="true" />
                  体验在线交互演示
                </Link>
              </Button>
            )}
            <Button asChild size="md" className="w-full bg-card/85 sm:w-auto">
              <Link href="/docs">
                <BookOpen className="size-4 text-accent" aria-hidden="true" />
                查阅配套文档
              </Link>
            </Button>
          </div>
        </PortalContainer>
      </section>

      {/* Scenarios with Clean Domain Filter */}
      <section className="py-16 sm:py-20">
        <PortalContainer>
          <div className="mb-10 flex flex-col items-start justify-between gap-4 sm:flex-row sm:items-center">
            <div>
              <h2 className="text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
                四大典型工程落地场景
              </h2>
              <p className="mt-1 text-xs text-muted-foreground">
                按领域筛选查看痛点拆解、真实工作流与对应 CLI 入口。
              </p>
            </div>
            <SegmentedControl
              ariaLabel="按领域筛选使用案例"
              value={filter}
              onChange={setFilter}
              options={FILTERS.map((f) => ({ value: f.id, label: f.label }))}
              className="flex-wrap"
            />
          </div>

          <div className="flex flex-col gap-6">
            {visible.map((s) => {
              const Icon = s.icon
              return (
                <article key={s.id} className="portal-frame grid gap-6 p-6 lg:grid-cols-12 lg:p-8">
                  {/* Balanced Left Column: Context + Key Verified Outcomes + Doc Link */}
                  <div className="flex flex-col justify-between gap-5 lg:col-span-5 lg:border-r lg:border-border/55 lg:pr-7">
                    <div>
                      <span className="inline-flex items-center gap-1.5 rounded-full bg-primary/12 px-3 py-1 font-mono text-2xs font-medium text-primary ring-1 ring-inset ring-primary/25">
                        <Icon className="size-3.5 text-accent" aria-hidden="true" />
                        {s.tag}
                      </span>
                      <h3 className="mt-4 text-lg font-semibold text-foreground">{s.team}</h3>
                      <p className="mt-1.5 text-sm leading-relaxed text-muted-foreground">{s.scene}</p>

                      <ul className="mt-5 space-y-2 border-t border-border/50 pt-4">
                        {s.outcomes.map((o) => (
                          <li key={o} className="flex items-start gap-2 text-xs leading-relaxed text-foreground/90">
                            <Check className="mt-0.5 size-3.5 shrink-0 text-success" aria-hidden="true" />
                            <span>{o}</span>
                          </li>
                        ))}
                      </ul>
                    </div>

                    <Link
                      href={s.docHref}
                      className="inline-flex items-center gap-1.5 text-xs font-medium text-accent transition-opacity hover:opacity-85"
                    >
                      <BookOpen className="size-3.5" aria-hidden="true" />
                      阅读权威文档：{s.docLabel}
                      <ArrowRight className="size-3.5" aria-hidden="true" />
                    </Link>
                  </div>

                  {/* Clean Flat Right Column: Editorial Flow + Single Command Surface */}
                  <div className="flex flex-col justify-between gap-5 lg:col-span-7">
                    <div className="space-y-5">
                      <div>
                        <div className="font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
                          面临挑战
                        </div>
                        <p className="mt-1.5 text-sm leading-relaxed text-foreground/85">{s.challenge}</p>
                      </div>
                      <div className="border-t border-border/50 pt-4">
                        <div className="inline-flex items-center gap-1.5 font-mono text-2xs font-semibold uppercase tracking-wider text-accent">
                          <Sparkles className="size-3" aria-hidden="true" />
                          DoOps 工程落地方案
                        </div>
                        <p className="mt-1.5 text-sm leading-relaxed text-foreground/90">{s.workflow}</p>
                      </div>
                    </div>

                    <div className="space-y-1.5">
                      <div className="font-mono text-2xs text-muted-foreground">典型 CLI 入口</div>
                      <CommandLine cmd={s.command} />
                    </div>
                  </div>
                </article>
              )
            })}
          </div>
        </PortalContainer>
      </section>

      {/* Capability Tags */}
      <section className="border-t border-border/45 py-16">
        <PortalContainer className="text-center">
          <span className="eyebrow justify-center">
            <span className="h-px w-6 bg-border" />
            Shared Foundation
            <span className="h-px w-6 bg-border" />
          </span>
          <p className="mt-3 text-sm text-muted-foreground">所有场景均复用同一套网关底座与工程纪律</p>
          <div className="mt-5 flex flex-wrap items-center justify-center gap-2.5">
            {[
              { icon: Terminal, label: "AI 交互终端 (PTY + OSC 133)" },
              { icon: Boxes, label: "K8s & Helm 证据化交付" },
              { icon: Database, label: "DoSql 五引擎数据治理" },
              { icon: Radar, label: "Skill-First 智能巡检" },
              { icon: Shield, label: "受管凭据 & Fail-Closed 审计" },
            ].map((cap) => {
              const Icon = cap.icon
              return (
                <span
                  key={cap.label}
                  className="inline-flex items-center gap-1.5 rounded-full border border-border/70 bg-card/65 px-3.5 py-1.5 text-xs text-foreground"
                >
                  <Icon className="size-3.5 text-accent" aria-hidden="true" />
                  {cap.label}
                </span>
              )
            })}
          </div>
        </PortalContainer>
      </section>

      {/* Bottom CTA */}
      <section className="border-t border-border/45 py-20">
        <PortalContainer>
          <div className="portal-frame relative overflow-hidden px-6 py-14 text-center sm:px-12">
            <div aria-hidden="true" className="hero-ambient pointer-events-none absolute inset-0 opacity-75" />
            <div className="relative mx-auto max-w-3xl">
              <h2 className="text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
                把第一个场景接入 DoOps
              </h2>
              <p className="mx-auto mt-3 max-w-xl text-pretty text-sm text-muted-foreground sm:text-base">
                先打开在线演示体验控制台全貌，或按文档中心 5 分钟接入你的第一台测试节点。
              </p>
              <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
                <Button asChild variant="primary" size="lg" className="w-full sm:w-auto">
                  <Link href="/console">
                    <Rocket className="size-4" aria-hidden="true" />
                    进入控制台
                  </Link>
                </Button>
                <Button asChild size="lg" className="w-full bg-card/80 sm:w-auto">
                  <Link href="/docs/cluster-install">
                    <BookOpen className="size-4 text-accent" aria-hidden="true" />
                    客户端与集群接入指南
                  </Link>
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
