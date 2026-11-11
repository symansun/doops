"use client"

import Link from "next/link"
import {
  ServerIcon,
  SparkIcon,
  TerminalIcon,
  ShieldIcon,
  LayersIcon,
  FileIcon,
  RocketIcon,
  CheckIcon,
  ChevronDownIcon,
} from "./icons"
import { BrandLockup, BrandMark } from "./brand"
import { isDemoAllowed } from "@/lib/demo-policy"

const DEMO_ENABLED = isDemoAllowed()

const FEATURES = [
  {
    icon: SparkIcon,
    title: "智能驱动运维",
    desc: "用自然语言描述目标，智能体负责理解、规划、执行和反馈，减少重复操作与命令记忆。",
  },
  {
    icon: TerminalIcon,
    title: "控制台与 CLI 双入口",
    desc: "浏览器工作台适合协作和审查，doops CLI 适合脚本、批量任务和日常快速操作。",
  },
  {
    icon: ServerIcon,
    title: "服务器与 Kubernetes",
    desc: "统一管理节点、工作负载、部署、终端、文件和运行状态，让基础设施操作集中在一个入口。",
  },
  {
    icon: LayersIcon,
    title: "网站与数据库技能",
    desc: "按领域加载独立 skill，覆盖网站发布、Nginx、健康检查、数据库巡检和受控变更流程。",
  },
  {
    icon: FileIcon,
    title: "配置、文件与自动化",
    desc: "从配置模板、文件管理到知识库，用受管凭据和审计记录支撑高频运维动作。",
  },
  {
    icon: ShieldIcon,
    title: "统一认证与可验证审计",
    desc: "一套 Gateway 登录贯通控制台、CLI 和各个 skill，精确授权、操作留痕、结果可核验。",
  },
]

const STEPS = [
  {
    n: "1",
    title: "接入运维目标",
    desc: "在服务器或 Kubernetes 节点安装 Agent，让目标通过 Gateway 安全接入统一工作台。",
  },
  {
    n: "2",
    title: "选择最合适的入口",
    desc: "登录一次即可使用 Web 控制台或 doops CLI，并按任务选择对应的领域 skill。",
  },
  {
    n: "3",
    title: "执行、验证并留痕",
    desc: "智能体和确定性工具协同执行，权限、审计、健康检查和回滚证据贯穿全过程。",
  },
]

const TRUST = ["统一入口", "多领域 skill", "可验证执行"]

const FAQS = [
  {
    q: "DoOps 适合什么规模的运维？",
    a: "从单台服务器到多节点 Kubernetes 环境都可以使用。你可以先接入一个目标，也可以逐步扩展到团队统一运维。",
  },
  {
    q: "控制台和 CLI 有什么区别？",
    a: "控制台适合可视化操作、协作和审计，CLI 适合脚本化、批量处理和自动化任务；两者共享同一套 Gateway 认证和授权。",
  },
  {
    q: "不同运维能力如何协同？",
    a: "DoOps 通过领域 skill 组织不同能力。服务器、网站、数据库等 skill 独立执行，同时复用统一的目标选择、认证、授权和审计逻辑。",
  },
  {
    q: "运维操作如何被控制和追踪？",
    a: "Gateway 负责用户身份、目标和动作授权；执行过程保留审计与结果证据，变更场景还会经过确认、健康检查和回滚保护。",
  },
]

const NAV = [
  { label: "功能", href: "/#features" },
  { label: "快速上手", href: "/#how" },
  { label: "部署文档", href: "/docs/deploy" },
  { label: "使用案例", href: "/cases" },
  { label: "博客", href: "/blog" },
  { label: "常见问题", href: "/#faq" },
]

export function LandingPage() {
  return (
    <div className="min-h-dvh bg-background text-foreground">
      {/* 顶栏 */}
      <header className="site-header">
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-4 px-4">
          <Link href="/" aria-label="DoOps 首页" className="flex items-center transition-opacity hover:opacity-90">
            <BrandLockup height={28} />
          </Link>
          <nav className="hidden items-center gap-1 rounded-full border border-border/60 bg-card/40 p-1 text-sm text-muted-foreground backdrop-blur lg:flex">
            {NAV.map((item) => (
              <Link
                key={item.label}
                href={item.href}
                className="rounded-full px-3 py-1.5 transition-colors hover:bg-foreground/5 hover:text-foreground"
              >
                {item.label}
              </Link>
            ))}
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
      <section className="relative overflow-hidden">
        <div aria-hidden className="hero-ambient pointer-events-none absolute inset-0" />
        <div className="relative mx-auto max-w-6xl px-4 pb-24 pt-24 text-center md:pb-32 md:pt-32">
          <div className="brand-gradient mx-auto mb-6 inline-flex rounded-full p-px shadow-[0_0_48px_-16px_color-mix(in_oklch,var(--primary)_60%,transparent)]">
            <span className="inline-flex items-center gap-2 rounded-full bg-background/95 px-3.5 py-1 text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground backdrop-blur">
              <span className="h-1.5 w-1.5 rounded-full bg-primary" />
              智能驱动 · 全方位运维工具
            </span>
          </div>
          <h1 className="mx-auto max-w-4xl text-balance text-5xl font-semibold leading-[1.06] tracking-tight md:text-7xl">
            全方位的<span className="text-gradient">智能运维</span>工具
          </h1>
          <p className="mx-auto mt-6 max-w-2xl text-pretty text-base leading-relaxed text-muted-foreground md:text-lg">
            DoOps 把智能体、统一控制台、CLI 和多个领域 skill 组合成一套运维工具。
            从服务器、Kubernetes 到网站、数据库、文件和巡检，用同一套认证、授权与审计完成日常运维。
          </p>
          <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Link href="/console" className="btn btn-primary btn-lg w-full sm:w-auto">
              <RocketIcon width={16} height={16} />
              开始使用 DoOps
            </Link>
            {DEMO_ENABLED && (
              <Link href="/console?demo=1" className="btn btn-ghost btn-lg w-full sm:w-auto">
                <SparkIcon width={16} height={16} />
                先看在线演示
              </Link>
            )}
          </div>
          <ul className="mx-auto mt-10 flex flex-wrap items-center justify-center gap-x-6 gap-y-3 text-xs text-muted-foreground">
            {TRUST.map((item) => (
              <li key={item} className="flex items-center gap-1.5">
                <CheckIcon width={13} height={13} className="text-primary" />
                {item}
              </li>
            ))}
          </ul>
        </div>
      </section>

      {/* 功能 */}
      <section id="features" className="border-t border-border/40 py-24">
        <div className="mx-auto max-w-6xl px-4">
          <div className="mb-14 flex flex-col items-start justify-between gap-4 md:flex-row md:items-end">
            <div>
              <span className="eyebrow">
                <span className="h-px w-6 bg-border" />
                能力矩阵
              </span>
              <h2 className="mt-3 max-w-2xl text-balance text-3xl font-semibold tracking-tight md:text-4xl">
                一套工具，覆盖运维全流程
              </h2>
            </div>
            <p className="max-w-md text-pretty text-sm leading-relaxed text-muted-foreground md:text-right">
              从发现问题、执行操作到验证结果，DoOps 把不同运维场景放进同一套工作台。
            </p>
          </div>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {FEATURES.map((f) => {
              const Icon = f.icon
              return (
                <div key={f.title} className="card-surface card-interactive group p-6">
                  <div className="mb-5 flex h-11 w-11 items-center justify-center rounded-xl bg-primary/15 text-primary shadow-[0_0_24px_-12px_color-mix(in_oklch,var(--primary)_80%,transparent)] ring-1 ring-inset ring-primary/25 transition-colors group-hover:bg-primary/20">
                    <Icon width={20} height={20} />
                  </div>
                  <h3 className="text-[15px] font-semibold text-card-foreground">{f.title}</h3>
                  <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{f.desc}</p>
                </div>
              )
            })}
          </div>
        </div>
      </section>

      {/* 快速上手 */}
      <section id="how" className="border-t border-border/40 py-24">
        <div className="mx-auto max-w-6xl px-4">
          <div className="mb-14 text-center">
            <span className="eyebrow justify-center">
              <span className="h-px w-6 bg-border" />
              快速上手
              <span className="h-px w-6 bg-border" />
            </span>
            <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight md:text-4xl">
              三步建立统一运维入口
            </h2>
            <p className="mx-auto mt-3 max-w-xl text-pretty text-muted-foreground">
              接入目标、选择入口、开始执行，复杂能力由 Gateway 和领域 skill 负责组织。
            </p>
          </div>
          <ol className="grid gap-6 md:grid-cols-3">
            {STEPS.map((s) => (
              <li key={s.n} className="card-surface relative p-6">
                <span
                  aria-hidden
                  className="absolute right-6 top-5 text-4xl font-semibold tracking-tight text-foreground/[0.06]"
                >
                  {s.n.padStart(2, "0")}
                </span>
                <div className="mb-5 flex h-10 w-10 items-center justify-center rounded-full bg-primary/10 text-sm font-semibold text-primary ring-1 ring-inset ring-primary/25">
                  {s.n}
                </div>
                <h3 className="text-[15px] font-semibold text-card-foreground">{s.title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{s.desc}</p>
              </li>
            ))}
          </ol>
          <div className="mt-12 text-center">
            <Link href="/console" className="btn btn-primary btn-lg">
              <RocketIcon width={16} height={16} />
              立即开始
            </Link>
          </div>
        </div>
      </section>

      {/* FAQ */}
      <section id="faq" className="border-t border-border/40 py-24">
        <div className="mx-auto max-w-3xl px-4">
          <div className="mb-12 text-center">
            <span className="eyebrow justify-center">
              <span className="h-px w-6 bg-border" />
              FAQ
              <span className="h-px w-6 bg-border" />
            </span>
            <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight md:text-4xl">常见问题</h2>
          </div>
          <div className="flex flex-col gap-3">
            {FAQS.map((item) => (
              <details key={item.q} className="card-surface group overflow-hidden [&_summary::-webkit-details-marker]:hidden">
                <summary className="flex cursor-pointer list-none items-center justify-between gap-4 px-5 py-4 text-sm font-medium text-card-foreground transition-colors hover:bg-foreground/[0.03]">
                  <span className="flex items-center gap-2.5">
                    <CheckIcon width={15} height={15} className="shrink-0 text-primary" />
                    {item.q}
                  </span>
                  <ChevronDownIcon
                    width={16}
                    height={16}
                    className="shrink-0 text-muted-foreground transition-transform group-open:rotate-180"
                  />
                </summary>
                <p className="px-5 pb-5 pl-[2.4rem] text-sm leading-relaxed text-muted-foreground">{item.a}</p>
              </details>
            ))}
          </div>
        </div>
      </section>

      {/* 底部 CTA */}
      <section className="relative overflow-hidden border-t border-border/40 py-24">
        <div aria-hidden className="hero-ambient pointer-events-none absolute inset-0 opacity-70" />
        <div className="relative mx-auto max-w-3xl px-4 text-center">
          <h2 className="text-balance text-3xl font-semibold tracking-tight md:text-4xl">
            准备好建立统一的智能运维入口了吗？
          </h2>
          <p className="mx-auto mt-3 max-w-xl text-pretty text-muted-foreground">
            从一个目标开始，把日常运维逐步交给 DoOps 统一管理。
          </p>
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Link href="/console" className="btn btn-primary btn-lg w-full sm:w-auto">
              <RocketIcon width={16} height={16} />
              进入控制台
            </Link>
            {DEMO_ENABLED && (
              <Link href="/console?demo=1" className="btn btn-ghost btn-lg w-full sm:w-auto">
                先看在线演示
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
              全方位智能运维工具
            </div>
          </div>
          <nav className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-muted-foreground">
            {NAV.map((item) => (
              <Link key={item.label} href={item.href} className="transition-colors hover:text-foreground">
                {item.label}
              </Link>
            ))}
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
