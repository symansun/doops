"use client"

import Link from "next/link"
import {
  ServerIcon,
  SparkIcon,
  TerminalIcon,
  ShieldIcon,
  HistoryIcon,
  LayersIcon,
  FileIcon,
  RocketIcon,
  CheckIcon,
  ActivityIcon,
} from "./icons"
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

export function LandingPage() {
  return (
    <div className="min-h-dvh bg-background text-foreground">
      {/* 顶栏 */}
      <header className="sticky top-0 z-20 border-b border-border/60 bg-background/80 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          <div className="flex items-center gap-2">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/15 text-primary">
              <ServerIcon width={18} height={18} />
            </div>
            <span className="text-lg font-semibold tracking-tight">Doops</span>
          </div>
          <nav className="hidden items-center gap-6 text-sm text-muted-foreground md:flex">
            <a href="#features" className="transition-colors hover:text-foreground">
              功能
            </a>
            <a href="#how" className="transition-colors hover:text-foreground">
              快速上手
            </a>
            <Link href="/docs/deploy" className="transition-colors hover:text-foreground">
              部署文档
            </Link>
            <Link href="/cases" className="transition-colors hover:text-foreground">
              使用案例
            </Link>
            <Link href="/blog" className="transition-colors hover:text-foreground">
              博客
            </Link>
            <a href="#faq" className="transition-colors hover:text-foreground">
              常见问题
            </a>
          </nav>
          <div className="flex items-center gap-2">
            {DEMO_ENABLED && (
              <Link
                href="/console?demo=1"
                className="hidden rounded-lg border px-3 py-1.5 text-sm text-foreground transition-colors hover:bg-muted sm:inline-block"
              >
                在线演示
              </Link>
            )}
            <Link
              href="/console"
              className="rounded-lg bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
            >
              进入控制台
            </Link>
          </div>
        </div>
      </header>

      {/* 主视觉 */}
      <section className="relative overflow-hidden">
        <div className="mx-auto max-w-6xl px-4 py-20 text-center md:py-28">
          <div className="mx-auto mb-5 inline-flex items-center gap-2 rounded-full border border-primary/30 bg-primary/10 px-3 py-1 text-xs text-primary">
            <SparkIcon width={13} height={13} />
            智能驱动 · 全方位运维工具
          </div>
          <h1 className="mx-auto max-w-3xl text-balance text-4xl font-semibold leading-tight tracking-tight md:text-6xl">
            全方位的智能运维工具
          </h1>
          <p className="mx-auto mt-5 max-w-2xl text-pretty text-base leading-relaxed text-muted-foreground md:text-lg">
            Doops 把智能体、统一控制台、CLI 和多个领域 skill 组合成一套运维工具。
            从服务器、Kubernetes 到网站、数据库、文件和巡检，用同一套认证、授权与审计完成日常运维。
          </p>
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Link
              href="/console"
              className="flex w-full items-center justify-center gap-2 rounded-lg bg-primary px-6 py-3 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 sm:w-auto"
            >
              <RocketIcon width={16} height={16} />
              开始使用 Doops
            </Link>
            {DEMO_ENABLED && (
              <Link
                href="/console?demo=1"
                className="flex w-full items-center justify-center gap-2 rounded-lg border px-6 py-3 text-sm font-medium text-foreground transition-colors hover:bg-muted sm:w-auto"
              >
                <SparkIcon width={16} height={16} />
                先看在线演示
              </Link>
            )}
          </div>
          <p className="mt-4 text-xs text-muted-foreground">统一入口 · 多领域 skill · 可验证执行</p>
        </div>
      </section>

      {/* 功能 */}
      <section id="features" className="border-t border-border/60 py-20">
        <div className="mx-auto max-w-6xl px-4">
          <div className="mx-auto mb-12 max-w-2xl text-center">
            <h2 className="text-balance text-3xl font-semibold tracking-tight">一套工具，覆盖运维全流程</h2>
            <p className="mt-3 text-pretty text-muted-foreground">
              从发现问题、执行操作到验证结果，Doops 把不同运维场景放进同一套工作台。
            </p>
          </div>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {FEATURES.map((f) => {
              const Icon = f.icon
              return (
                <div
                  key={f.title}
                  className="rounded-xl border bg-card p-6 transition-colors hover:border-primary/40"
                >
                  <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-lg bg-primary/15 text-primary">
                    <Icon width={20} height={20} />
                  </div>
                  <h3 className="mb-1.5 text-base font-medium text-card-foreground">{f.title}</h3>
                  <p className="text-sm leading-relaxed text-muted-foreground">{f.desc}</p>
                </div>
              )
            })}
          </div>
        </div>
      </section>

      {/* 快速上手 */}
      <section id="how" className="border-t border-border/60 bg-card/30 py-20">
        <div className="mx-auto max-w-6xl px-4">
          <div className="mx-auto mb-12 max-w-2xl text-center">
            <h2 className="text-balance text-3xl font-semibold tracking-tight">三步建立统一运维入口</h2>
            <p className="mt-3 text-pretty text-muted-foreground">
              接入目标、选择入口、开始执行，复杂能力由 Gateway 和领域 skill 负责组织。
            </p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {STEPS.map((s, i) => (
              <div key={s.n} className="relative">
                <div className="rounded-xl border bg-card p-6">
                  <div className="mb-4 flex h-9 w-9 items-center justify-center rounded-full bg-primary text-sm font-semibold text-primary-foreground">
                    {s.n}
                  </div>
                  <h3 className="mb-1.5 text-base font-medium text-card-foreground">{s.title}</h3>
                  <p className="text-sm leading-relaxed text-muted-foreground">{s.desc}</p>
                </div>
                {i < STEPS.length - 1 && (
                  <div className="absolute -right-3 top-12 hidden h-px w-6 bg-border md:block" />
                )}
              </div>
            ))}
          </div>
          <div className="mt-10 text-center">
            <Link
              href="/console"
              className="inline-flex items-center justify-center gap-2 rounded-lg bg-primary px-6 py-3 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
            >
              <RocketIcon width={16} height={16} />
              立即开始
            </Link>
          </div>
        </div>
      </section>

      {/* FAQ */}
      <section id="faq" className="border-t border-border/60 py-20">
        <div className="mx-auto max-w-3xl px-4">
          <h2 className="mb-10 text-center text-balance text-3xl font-semibold tracking-tight">
            常见问题
          </h2>
          <div className="flex flex-col gap-5">
            {[
              {
                q: "Doops 适合什么规模的运维？",
                a: "从单台服务器到多节点 Kubernetes 环境都可以使用。你可以先接入一个目标，也可以逐步扩展到团队统一运维。",
              },
              {
                q: "控制台和 CLI 有什么区别？",
                a: "控制台适合可视化操作、协作和审计，CLI 适合脚本化、批量处理和自动化任务；两者共享同一套 Gateway 认证和授权。",
              },
              {
                q: "不同运维能力如何协同？",
                a: "Doops 通过领域 skill 组织不同能力。服务器、网站、数据库等 skill 独立执行，同时复用统一的目标选择、认证、授权和审计逻辑。",
              },
              {
                q: "运维操作如何被控制和追踪？",
                a: "Gateway 负责用户身份、目标和动作授权；执行过程保留审计与结果证据，变更场景还会经过确认、健康检查和回滚保护。",
              },
            ].map((item) => (
              <div key={item.q} className="rounded-xl border bg-card p-5">
                <h3 className="mb-1.5 flex items-center gap-2 text-sm font-medium text-card-foreground">
                  <CheckIcon width={15} height={15} className="text-primary" />
                  {item.q}
                </h3>
                <p className="pl-6 text-sm leading-relaxed text-muted-foreground">{item.a}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* 底部 CTA */}
      <section className="border-t border-border/60 bg-card/30 py-16">
        <div className="mx-auto max-w-3xl px-4 text-center">
          <h2 className="text-balance text-2xl font-semibold tracking-tight md:text-3xl">
            准备好建立统一的智能运维入口了吗？
          </h2>
          <p className="mt-3 text-pretty text-muted-foreground">从一个目标开始，把日常运维逐步交给 Doops 统一管理。</p>
          <Link
            href="/console"
            className="mt-6 inline-flex items-center justify-center gap-2 rounded-lg bg-primary px-6 py-3 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
          >
            <RocketIcon width={16} height={16} />
            进入控制台
          </Link>
        </div>
      </section>

      <footer className="border-t border-border/60 py-8">
        <div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-3 px-4 text-sm text-muted-foreground sm:flex-row">
          <div className="flex items-center gap-2">
            <div className="flex h-6 w-6 items-center justify-center rounded-md bg-primary/15 text-primary">
              <ServerIcon width={14} height={14} />
            </div>
            <span>Doops · 全方位智能运维工具</span>
          </div>
          <div className="flex items-center gap-4">
            <Link href="/console" className="transition-colors hover:text-foreground">
              控制台
            </Link>
            <a href="#features" className="transition-colors hover:text-foreground">
              功能
            </a>
            <Link href="/docs/deploy" className="transition-colors hover:text-foreground">
              部署文档
            </Link>
            <Link href="/cases" className="transition-colors hover:text-foreground">
              使用案例
            </Link>
            <Link href="/blog" className="transition-colors hover:text-foreground">
              博客
            </Link>
            <a href="#faq" className="transition-colors hover:text-foreground">
              常见问题
            </a>
          </div>
        </div>
      </footer>
    </div>
  )
}
