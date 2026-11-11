"use client"

import Link from "next/link"
import {
  SparkIcon,
  RocketIcon,
  ActivityIcon,
  ShieldIcon,
  LayersIcon,
  TerminalIcon,
  CheckIcon,
  ChevronRightIcon,
} from "./icons"
import { BrandLockup, BrandMark } from "./brand"
import { isDemoAllowed } from "@/lib/demo-policy"

const DEMO_ENABLED = isDemoAllowed()

const METRICS = [
  { value: "92%", label: "重复运维操作被 AI 接管" },
  { value: "8 倍", label: "故障平均响应速度提升" },
  { value: "0", label: "因误操作导致的线上事故" },
]

type Case = {
  tag: string
  icon: typeof SparkIcon
  company: string
  scene: string
  challenge: string
  solution: string
  results: string[]
}

const CASES: Case[] = [
  {
    tag: "初创团队",
    icon: SparkIcon,
    company: "一家 6 人的 SaaS 初创公司",
    scene: "全栈团队没有专职运维，一台云服务器跑着全部业务",
    challenge:
      "团队成员都不熟悉 Linux 运维，每次部署、查日志、重启服务都要翻文档、贴命令，出问题时手忙脚乱，半夜告警没人敢动手。",
    solution:
      "接入 DoOps 后，开发同学直接用中文对 AI 助手下达「帮我把最新代码部署上去并确认健康」，AI 自动拉取仓库、构建镜像、滚动更新并回写部署报告，全程可追溯。",
    results: [
      "部署耗时从 40 分钟降到 5 分钟",
      "新人当天即可独立上手发布",
      "再没有因为敲错命令搞挂服务",
    ],
  },
  {
    tag: "电商 · 大促",
    icon: ActivityIcon,
    company: "一家区域型电商平台",
    scene: "大促期间数十台节点，流量波动剧烈",
    challenge:
      "大促时机器负载、磁盘、服务存活状态需要时刻盯着，靠人工巡检根本看不过来，异常发现往往滞后，影响下单转化。",
    solution:
      "值班同学在控制台用自然语言让 AI 对节点做只读巡检，查看负载、磁盘和服务状态并返回证据；追问「db-03 现在负载为什么高」即可定位。",
    results: [
      "定位异常不再需要逐台登录",
      "大促全程零线上事故",
      "每次排查都有审计记录，复盘有据可查",
    ],
  },
  {
    tag: "多区域集群",
    icon: LayersIcon,
    company: "一家出海工具类公司",
    scene: "国内 + 海外多区域部署，配置极易漂移",
    challenge:
      "不同区域节点的配置文件、模型参数经常各自为政，一处改动要手动同步到几十台机器，漏改、改错时有发生。",
    solution:
      "把大模型与配置文件沉淀为本地配置模板，改完通过 doops 下发到指定节点；发布前需「应用」确认，避免误推。",
    results: [
      "配置同步从半天缩短到 1 分钟",
      "彻底消除区域间配置漂移",
      "每次下发都有版本与审计记录",
    ],
  },
]

const TESTIMONIALS = [
  {
    quote:
      "以前最怕半夜告警，现在直接对着手机用中文问 AI 怎么回事、让它处理，运维这件事第一次变得没有压力。",
    name: "陈航",
    role: "SaaS 初创 · 技术合伙人",
  },
  {
    quote:
      "我们没有招专职 SRE，DoOps 的巡检和审计基本顶上了一个值班岗，性价比非常高。",
    name: "林薇",
    role: "电商平台 · 研发负责人",
  },
]

export function CasesPage() {
  return (
    <div className="min-h-dvh bg-background text-foreground">
      {/* 顶栏 */}
      <header className="site-header">
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-4 px-4">
          <Link href="/" aria-label="DoOps 首页" className="flex items-center transition-opacity hover:opacity-90">
            <BrandLockup height={28} />
          </Link>
          <nav className="hidden items-center gap-1 rounded-full border border-border/60 bg-card/40 p-1 text-sm text-muted-foreground backdrop-blur lg:flex">
            <Link href="/#features" className="rounded-full px-3 py-1.5 transition-colors hover:bg-foreground/5 hover:text-foreground">
              功能
            </Link>
            <Link href="/#how" className="rounded-full px-3 py-1.5 transition-colors hover:bg-foreground/5 hover:text-foreground">
              快速上手
            </Link>
            <Link href="/docs/deploy" className="rounded-full px-3 py-1.5 transition-colors hover:bg-foreground/5 hover:text-foreground">
              部署文档
            </Link>
            <Link href="/cases" className="rounded-full bg-foreground/10 px-3 py-1.5 text-foreground">
              使用案例
            </Link>
            <Link href="/#faq" className="rounded-full px-3 py-1.5 transition-colors hover:bg-foreground/5 hover:text-foreground">
              常见问题
            </Link>
          </nav>
          <div className="flex items-center gap-2">
            {DEMO_ENABLED && (
              <Link href="/console?demo=1" className="btn btn-ghost btn-sm hidden sm:inline-flex">
                在线演示
              </Link>
            )}
            <Link href="/console" className="btn btn-primary btn-sm">
              进入控制台
            </Link>
          </div>
        </div>
      </header>

      {/* 主视觉 */}
      <section className="relative overflow-hidden border-b border-border/40">
        <div aria-hidden className="hero-ambient pointer-events-none absolute inset-0" />
        <div className="relative mx-auto max-w-6xl px-4 pb-20 pt-24 text-center md:pb-24 md:pt-28">
          <div className="brand-gradient mx-auto mb-6 inline-flex rounded-full p-px">
            <span className="inline-flex items-center gap-2 rounded-full bg-background/95 px-3.5 py-1 text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground backdrop-blur">
              <RocketIcon width={13} height={13} className="text-primary" />
              使用案例
            </span>
          </div>
          <h1 className="mx-auto max-w-4xl text-balance text-5xl font-semibold leading-[1.06] tracking-tight md:text-6xl">
            他们这样用 DoOps 把运维交给 AI
          </h1>
          <p className="mx-auto mt-6 max-w-2xl text-pretty text-base leading-relaxed text-muted-foreground md:text-lg">
            从单机初创到多区域集群，不同规模的团队都用 DoOps
            重新定义了日常运维。看看他们遇到的问题、用了哪些能力，以及最终拿到的结果。
          </p>
          <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row">
            {DEMO_ENABLED && (
              <Link href="/console?demo=1" className="btn btn-primary btn-lg w-full sm:w-auto">
                <SparkIcon width={16} height={16} />
                体验同款在线演示
              </Link>
            )}
            <Link href="/#features" className="btn btn-ghost btn-lg w-full sm:w-auto">
              查看全部功能
            </Link>
          </div>
        </div>
      </section>

      {/* 指标 */}
      <section className="border-b border-border/40 bg-card/20">
        <div className="mx-auto grid max-w-6xl grid-cols-1 gap-8 px-4 py-12 sm:grid-cols-3">
          {METRICS.map((m) => (
            <div key={m.label} className="text-center">
              <div className="text-gradient text-4xl font-semibold tracking-tight md:text-5xl">{m.value}</div>
              <p className="mx-auto mt-2 max-w-[15rem] text-sm leading-relaxed text-muted-foreground text-pretty">
                {m.label}
              </p>
            </div>
          ))}
        </div>
      </section>

      {/* 案例详情 */}
      <section className="py-24">
        <div className="mx-auto max-w-6xl px-4">
          <div className="mx-auto mb-12 max-w-2xl text-center">
            <span className="eyebrow justify-center">
              <span className="h-px w-6 bg-border" />
              场景
              <span className="h-px w-6 bg-border" />
            </span>
            <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight md:text-4xl">三个真实场景</h2>
            <p className="mt-3 text-pretty text-muted-foreground">
              每个案例都包含他们的难题、采用的方案，以及落地后的变化。
            </p>
          </div>
          <div className="flex flex-col gap-6">
            {CASES.map((c) => {
              const Icon = c.icon
              return (
                <article key={c.company} className="card-surface card-interactive grid gap-6 p-6 md:grid-cols-3 md:p-8">
                  {/* 左侧：场景概述 */}
                  <div className="md:border-r md:border-border/60 md:pr-6">
                    <span className="inline-flex items-center gap-1.5 rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary ring-1 ring-inset ring-primary/20">
                      <Icon width={13} height={13} />
                      {c.tag}
                    </span>
                    <h3 className="mt-4 text-lg font-medium text-card-foreground text-balance">
                      {c.company}
                    </h3>
                    <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{c.scene}</p>
                  </div>

                  {/* 中间：难题 + 方案 */}
                  <div className="flex flex-col gap-4 md:col-span-2">
                    <div>
                      <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                        遇到的难题
                      </p>
                      <p className="text-sm leading-relaxed text-card-foreground">{c.challenge}</p>
                    </div>
                    <div>
                      <p className="mb-1 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-primary">
                        <SparkIcon width={13} height={13} />
                        DoOps 方案
                      </p>
                      <p className="text-sm leading-relaxed text-card-foreground">{c.solution}</p>
                    </div>
                    <div className="grid gap-2 sm:grid-cols-3">
                      {c.results.map((r) => (
                        <div
                          key={r}
                          className="flex items-start gap-2 rounded-xl border border-border/50 bg-background/40 p-3"
                        >
                          <CheckIcon width={15} height={15} className="mt-0.5 shrink-0 text-primary" />
                          <span className="text-xs leading-relaxed text-card-foreground">{r}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                </article>
              )
            })}
          </div>
        </div>
      </section>

      {/* 客户评价 */}
      <section className="border-t border-border/40 py-24">
        <div className="mx-auto max-w-6xl px-4">
          <div className="mx-auto mb-12 max-w-2xl text-center">
            <span className="eyebrow justify-center">
              <span className="h-px w-6 bg-border" />
              评价
              <span className="h-px w-6 bg-border" />
            </span>
            <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight md:text-4xl">来自一线的声音</h2>
          </div>
          <div className="grid gap-6 md:grid-cols-2">
            {TESTIMONIALS.map((t) => (
              <figure key={t.name} className="card-surface relative p-6 md:p-8">
                <span
                  aria-hidden
                  className="pointer-events-none absolute right-6 top-2 font-serif text-6xl leading-none text-foreground/[0.06]"
                >
                  “
                </span>
                <blockquote className="text-base leading-relaxed text-card-foreground text-pretty">
                  {`“${t.quote}”`}
                </blockquote>
                <figcaption className="mt-5 flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-full bg-primary/15 text-sm font-semibold text-primary">
                    {t.name.slice(0, 1)}
                  </div>
                  <div>
                    <div className="text-sm font-medium text-card-foreground">{t.name}</div>
                    <div className="text-xs text-muted-foreground">{t.role}</div>
                  </div>
                </figcaption>
              </figure>
            ))}
          </div>
        </div>
      </section>

      {/* 能力标签 */}
      <section className="border-t border-border/40 py-20">
        <div className="mx-auto max-w-4xl px-4 text-center">
          <span className="eyebrow justify-center">
            <span className="h-px w-6 bg-border" />
            组合能力
            <span className="h-px w-6 bg-border" />
          </span>
          <p className="mt-3 text-sm text-muted-foreground">这些案例都用到了 DoOps 的核心能力</p>
          <div className="mt-5 flex flex-wrap items-center justify-center gap-2.5">
            {[
              { icon: SparkIcon, label: "自然语言运维" },
              { icon: ActivityIcon, label: "网站 DoWeb · 数据库 DoSql" },
              { icon: LayersIcon, label: "配置模板 · 一键下发" },
              { icon: TerminalIcon, label: "远程终端" },
              { icon: ShieldIcon, label: "权限与审计" },
            ].map((cap) => {
              const Icon = cap.icon
              return (
                <span
                  key={cap.label}
                  className="inline-flex items-center gap-1.5 rounded-full border border-border/60 bg-card/60 px-3 py-1.5 text-xs text-card-foreground backdrop-blur"
                >
                  <Icon width={13} height={13} className="text-primary" />
                  {cap.label}
                </span>
              )
            })}
          </div>
        </div>
      </section>

      {/* 底部 CTA */}
      <section className="relative overflow-hidden border-t border-border/40 py-24">
        <div aria-hidden className="hero-ambient pointer-events-none absolute inset-0 opacity-70" />
        <div className="relative mx-auto max-w-3xl px-4 text-center">
          <h2 className="text-balance text-3xl font-semibold tracking-tight md:text-4xl">下一个案例，可以是你</h2>
          <p className="mx-auto mt-3 max-w-xl text-pretty text-muted-foreground">
            无需信用卡，先用在线演示体验完整能力，再接入你的第一台机器。
          </p>
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Link href="/console" className="btn btn-primary btn-lg w-full sm:w-auto">
              <RocketIcon width={16} height={16} />
              进入控制台
            </Link>
            {DEMO_ENABLED && (
              <Link href="/console?demo=1" className="btn btn-ghost btn-lg w-full sm:w-auto">
                先看在线演示
                <ChevronRightIcon width={15} height={15} />
              </Link>
            )}
          </div>
        </div>
      </section>

      <footer className="border-t border-border/40">
        <div className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-10 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <BrandMark height={24} />
            <div className="text-xs leading-relaxed text-muted-foreground">
              <div className="text-sm font-medium text-foreground">DoOps</div>
              智能运维控制台
            </div>
          </div>
          <nav className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-muted-foreground">
            <Link href="/" className="transition-colors hover:text-foreground">
              首页
            </Link>
            <Link href="/cases" className="transition-colors hover:text-foreground">
              使用案例
            </Link>
            <Link href="/docs/deploy" className="transition-colors hover:text-foreground">
              部署文档
            </Link>
            <Link href="/blog" className="transition-colors hover:text-foreground">
              博客
            </Link>
            <Link href="/console" className="transition-colors hover:text-foreground">
              控制台
            </Link>
          </nav>
        </div>
        <div className="border-t border-border/40">
          <div className="mx-auto flex max-w-6xl flex-col gap-2 px-4 py-5 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
            <span>© 2026 DoOps</span>
            <span>统一入口 · 多领域 skill · 可验证执行</span>
          </div>
        </div>
      </footer>
    </div>
  )
}
