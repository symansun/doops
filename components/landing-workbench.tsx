"use client"

import { useState } from "react"
import Link from "next/link"
import {
  Activity,
  Boxes,
  Check,
  Copy,
  Database,
  Lock,
  Play,
  Radar,
  ShieldCheck,
  Sparkles,
  Terminal,
} from "lucide-react"

type ModeId = "terminal" | "deploy" | "dosql" | "inspect"

const MODES: {
  id: ModeId
  label: string
  shortLabel: string
  icon: typeof Terminal
  target: string
  session: string
  auditId: string
  cliCmd: string
  docHref: string
  docLabel: string
}[] = [
  {
    id: "terminal",
    label: "AI 交互终端",
    shortLabel: "AI 终端",
    icon: Terminal,
    target: "prod-k8s / node-01",
    session: "triage-pay-09",
    auditId: "aud_9f2a8c",
    cliCmd: "doops -session triage-pay-09 exec --target prod-k8s --cmd 'kubectl get pods -n payments'",
    docHref: "/docs/webui",
    docLabel: "控制台文档",
  },
  {
    id: "deploy",
    label: "K8s & Helm 交付",
    shortLabel: "Helm 交付",
    icon: Boxes,
    target: "prod-k8s / control-01",
    session: "rel-checkout-42",
    auditId: "aud_4b7e11",
    cliCmd: "doops -session rel-42 ask --target prod-k8s --apply --credential harbor-pull --msg '升级 checkout 2.14.0'",
    docHref: "/docs/deployment",
    docLabel: "交付契约",
  },
  {
    id: "dosql",
    label: "DoSql 数据治理",
    shortLabel: "DoSql",
    icon: Database,
    target: "prod-db / pg-primary",
    session: "mig-orders-18",
    auditId: "aud_7d3c50",
    cliCmd: "doops -session mig-18 sql --target prod-db --mode migrate --database orders --dir .dosql/migrations",
    docHref: "/docs/dosql",
    docLabel: "DoSql 规范",
  },
  {
    id: "inspect",
    label: "Skill-First 巡检",
    shortLabel: "智能巡检",
    icon: Radar,
    target: "prod-cluster / 12 nodes",
    session: "inspect-daily",
    auditId: "aud_2e6f94",
    cliCmd: "doops -session inspect-daily targets --target prod-k8s --admission",
    docHref: "/docs/skill-first-inspection",
    docLabel: "巡检规范",
  },
]

export function LandingWorkbench() {
  const [active, setActive] = useState<ModeId>("terminal")
  const [ranSuggestion, setRanSuggestion] = useState(false)
  const [copied, setCopied] = useState(false)

  const current = MODES.find((m) => m.id === active) ?? MODES[0]

  function handleCopyCmd() {
    navigator.clipboard?.writeText(current.cliCmd).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1600)
    })
  }

  return (
    <div className="portal-frame mx-auto mt-7 max-w-5xl overflow-hidden text-left">
      {/* Window Titlebar */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border/75 bg-card/90 px-4 py-2">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-1.5" aria-hidden="true">
            <span className="size-2.5 rounded-full bg-destructive/55" />
            <span className="size-2.5 rounded-full bg-warning/55" />
            <span className="size-2.5 rounded-full bg-success/55" />
          </div>
          <div className="flex items-center gap-2 font-mono text-2xs text-muted-foreground">
            <span className="hidden text-foreground/80 sm:inline">doops-gateway</span>
            <span aria-hidden="true" className="hidden text-border sm:inline">/</span>
            <span className="inline-flex items-center gap-1.5 rounded-md border border-border/70 bg-background/80 px-2 py-0.5 text-foreground">
              <span className="size-1.5 rounded-full bg-success" aria-hidden="true" />
              {current.target}
            </span>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2 font-mono text-2xs text-muted-foreground">
          <span className="hidden items-center gap-1 rounded-md border border-accent/30 bg-accent/10 px-2 py-0.5 text-accent md:inline-flex">
            <Activity className="size-3" aria-hidden="true" />
            Reverse WS · 0 Inbound Ports
          </span>
          <span className="hidden items-center gap-1 rounded-md border border-border/65 bg-background/60 px-2 py-0.5 sm:inline-flex">
            <Lock className="size-3 text-success" aria-hidden="true" />
            Prompt 0 密文
          </span>
          <span className="rounded-md border border-border/70 bg-background/60 px-2 py-0.5">
            audit: <span className="text-accent">#{current.auditId}</span>
          </span>
        </div>
      </div>

      {/* Single Primary Mode Switcher (zero duplicate sidebar!) */}
      <div
        role="tablist"
        aria-label="DoOps 核心场景演示"
        className="flex items-center justify-between gap-2 overflow-x-auto border-b border-border/60 bg-background/75 px-3 py-1.5"
      >
        <div className="flex items-center gap-1">
          {MODES.map((m) => {
            const Icon = m.icon
            const selected = m.id === active
            return (
              <button
                key={m.id}
                type="button"
                role="tab"
                aria-selected={selected}
                onClick={() => {
                  setActive(m.id)
                  setCopied(false)
                }}
                className={
                  selected
                    ? "inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-lg bg-primary/16 px-3 py-1.5 text-xs font-medium text-foreground ring-1 ring-inset ring-primary/35 transition-colors"
                    : "inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
                }
              >
                <Icon className={selected ? "size-3.5 text-accent" : "size-3.5"} aria-hidden="true" />
                <span className="hidden sm:inline">{m.label}</span>
                <span className="sm:hidden">{m.shortLabel}</span>
              </button>
            )
          })}
        </div>
        <div className="hidden shrink-0 items-center gap-3 font-mono text-2xs md:flex">
          <span className="text-muted-foreground">
            session: <span className="text-foreground">{current.session}</span>
          </span>
          <Link
            href={current.docHref}
            className="text-accent transition-opacity hover:opacity-80"
          >
            {current.docLabel} →
          </Link>
        </div>
      </div>

      {/* Full-Width Interactive Stage */}
      <div className="flex flex-col justify-between bg-background/95 p-4 sm:px-5 sm:py-4">
        {active === "terminal" && (
          <div className="grid gap-3 font-mono text-xs lg:grid-cols-12">
            {/* OSC 133 Command Block */}
            <div className="rounded-lg border border-border/70 bg-card/55 p-3 lg:col-span-6">
              <div className="flex items-center justify-between gap-2 text-2xs text-muted-foreground">
                <span className="flex items-center gap-1.5">
                  <span className="rounded bg-muted px-1.5 py-0.5 text-foreground">OSC 133 #14</span>
                  <span>~/payments</span>
                </span>
                <span className="text-success">exit 0 · 84ms</span>
              </div>
              <div className="mt-2 text-foreground">
                <span className="text-accent">$</span> kubectl get pods -n payments
              </div>
              <div className="mt-2 overflow-x-auto text-2xs leading-relaxed text-muted-foreground">
                <div className="whitespace-pre">NAME                      READY STATUS           RESTARTS</div>
                <div className="whitespace-pre">
                  checkout-api-6d9f7b-k82m  1/1   <span className="text-success">Running</span>          0
                </div>
                <div className="whitespace-pre">
                  payment-worker-7c4a1-x9p  0/1   <span className="text-destructive">CrashLoopBackOff</span> 4 (2m)
                </div>
              </div>
            </div>

            {/* Inline AI Prompt (# prefix) */}
            <div className="flex flex-col justify-between rounded-lg border border-primary/40 bg-primary/[0.06] p-3 lg:col-span-6">
              <div>
                <div className="flex flex-wrap items-center justify-between gap-2 text-2xs">
                  <span className="inline-flex items-center gap-1.5 font-sans font-medium text-foreground">
                    <Sparkles className="size-3.5 text-accent" aria-hidden="true" />
                    终端单次 AI 诊断（自然语言 / <code className="font-mono text-accent">#</code> / ⌘I）
                  </span>
                  <span className="inline-flex items-center gap-1 rounded bg-background/85 px-1.5 py-0.5 text-success">
                    <Lock className="size-3" aria-hidden="true" />
                    上下文 3 层脱敏
                  </span>
                </div>
                <div className="mt-1.5 font-sans text-xs text-foreground">
                  <span className="mr-1.5 font-mono text-accent">#</span>
                  为什么 payment-worker 反复重启？给出只读排查命令
                </div>
              </div>
              <div className="mt-2.5 flex items-center justify-between gap-2 rounded-md border border-border/75 bg-background/90 px-2.5 py-1.5">
                <code className="truncate text-2xs text-foreground">
                  kubectl logs payment-worker-7c4a1-x9p2 -n payments --previous --tail=40
                </code>
                <button
                  type="button"
                  onClick={() => setRanSuggestion((v) => !v)}
                  className="inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-md bg-primary px-2.5 py-1 font-sans text-2xs font-medium text-primary-foreground transition-opacity hover:opacity-90"
                >
                  <Play className="size-3" aria-hidden="true" />
                  {ranSuggestion ? "收起回执" : "安全运行"}
                </button>
              </div>
              {ranSuggestion && (
                <div className="mt-2 rounded-md border border-success/35 bg-background/90 px-2.5 py-2 text-2xs leading-relaxed text-muted-foreground">
                  <div className="flex items-center justify-between text-success">
                    <span>✓ source=ai_suggestion · 已过敏感词与 argv 规则审计</span>
                    <span>exit 0 · 112ms</span>
                  </div>
                  <div className="mt-0.5 truncate">
                    FATAL dial tcp 10.24.8.15:5432: timeout (DSN: postgres://***REDACTED***@10.24.8.15/pay)
                  </div>
                </div>
              )}
            </div>
          </div>
        )}

        {active === "deploy" && (
          <div className="space-y-2.5 font-mono text-xs">
            <div className="grid gap-2.5 sm:grid-cols-3">
              {[
                {
                  step: "01 凭据物化",
                  detail: "harbor-pull 仅在目标侧物化为受管 Secret，Prompt 0 密文",
                  status: "VERIFIED",
                },
                {
                  step: "02 原生 Helm 执行",
                  detail: "mcp_doops_helm upgrade --install --wait checkout 2.14.0",
                  status: "DEPLOYED",
                },
                {
                  step: "03 实测回读验收",
                  detail: "revision=18 · Pods Ready 3/3 · 镜像 sha256 digest 一致",
                  status: "ATTESTED",
                },
              ].map((item) => (
                <div key={item.step} className="rounded-lg border border-border/70 bg-card/50 p-3">
                  <div className="flex items-center justify-between gap-1 text-2xs">
                    <span className="font-sans font-medium text-foreground">{item.step}</span>
                    <span className="rounded bg-success/15 px-1.5 py-0.5 text-[10px] text-success">
                      {item.status}
                    </span>
                  </div>
                  <p className="mt-1.5 font-sans text-2xs leading-relaxed text-muted-foreground">
                    {item.detail}
                  </p>
                </div>
              ))}
            </div>
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-success/35 bg-success/[0.06] px-3.5 py-2.5 text-2xs">
              <span className="inline-flex items-center gap-1.5 font-sans font-medium text-success">
                <ShieldCheck className="size-3.5" aria-hidden="true" />
                结构化终态回执（非模型口头声明）
              </span>
              <span className="text-muted-foreground">
                release=checkout · rev=18 · status=deployed · attestation=sha256:9d4a…e21c
              </span>
            </div>
          </div>
        )}

        {active === "dosql" && (
          <div className="space-y-2.5 font-mono text-xs">
            <div className="grid gap-2.5 sm:grid-cols-2">
              <div className="rounded-lg border border-border/70 bg-card/50 p-3">
                <div className="mb-1.5 flex items-center justify-between font-sans text-2xs font-medium text-foreground">
                  <span>Policy-as-Code 门禁 (.dosql/policy.yaml)</span>
                  <span className="font-mono text-accent">credential_mode=auto</span>
                </div>
                <ul className="space-y-1 text-2xs text-muted-foreground">
                  <li className="text-success">✓ disallow_drop_table: PASS</li>
                  <li className="text-success">✓ require_concurrent_index (PostgreSQL 16): PASS</li>
                  <li className="text-success">✓ max_lock_timeout (5s): PASS</li>
                </ul>
              </div>
              <div className="rounded-lg border border-border/70 bg-card/50 p-3">
                <div className="mb-1.5 font-sans text-2xs font-medium text-foreground">
                  哈希链版本日志 (Hash-Chained Journal)
                </div>
                <div className="space-y-1 text-2xs text-muted-foreground">
                  <div>rev: <span className="text-foreground">v2026.11.26-04 · 0014_add_orders_idx.sql</span></div>
                  <div>prev: <span className="text-muted-foreground/80">sha256:31fe…89a0</span></div>
                  <div>curr: <span className="text-accent">sha256:88c4…f102 (非事务 CONCURRENTLY 分段完成 · 410ms)</span></div>
                </div>
              </div>
            </div>
          </div>
        )}

        {active === "inspect" && (
          <div className="space-y-2.5">
            <div className="grid gap-2.5 sm:grid-cols-3">
              {[
                { name: "kubernetes-cluster", score: "12/12 Ready", util: 64, tone: "text-success" },
                { name: "vm-host-resources", score: "CPU 48% · Disk 79%", util: 79, tone: "text-warning" },
                { name: "database-health", score: "TPS 1.4k · Locks 0", util: 36, tone: "text-accent" },
              ].map((s) => (
                <div key={s.name} className="rounded-lg border border-border/70 bg-card/50 p-3">
                  <div className="font-mono text-2xs text-muted-foreground">{s.name}</div>
                  <div className={`mt-1 font-mono text-xs font-medium ${s.tone}`}>{s.score}</div>
                  <div className="progress-track mt-2 h-1.5 w-full">
                    <div className="progress-fill" style={{ width: `${s.util}%` }} />
                  </div>
                </div>
              ))}
            </div>
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border/70 bg-card/50 px-3.5 py-2 font-mono text-2xs text-muted-foreground">
              <span>
                <span className="text-warning">△</span> vm-host /var/lib/containerd 较上次 +6.2%（79.1%），已触发 HMAC Webhook 告警
              </span>
              <span className="text-accent">File-First 产物 · 8 内置 Skill</span>
            </div>
          </div>
        )}

        {/* Full-width Copyable CLI Strip (zero truncation!) */}
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-border/55 pt-2.5">
          <div className="docs-scroll flex min-w-0 flex-1 items-center gap-2 overflow-x-auto font-mono text-2xs text-muted-foreground">
            <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-accent">CLI</span>
            <code className="whitespace-nowrap text-foreground/90">{current.cliCmd}</code>
          </div>
          <button
            type="button"
            onClick={handleCopyCmd}
            className="btn btn-sm h-7 shrink-0 gap-1.5 px-2.5 text-2xs"
          >
            {copied ? (
              <>
                <Check className="size-3 text-success" aria-hidden="true" />
                已复制
              </>
            ) : (
              <>
                <Copy className="size-3" aria-hidden="true" />
                复制命令
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  )
}
