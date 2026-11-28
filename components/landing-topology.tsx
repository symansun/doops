"use client"

import { useState } from "react"
import Link from "next/link"
import {
  ArrowLeft,
  ArrowRight,
  Boxes,
  Check,
  Code2,
  Database,
  Globe,
  KeyRound,
  Monitor,
  Server,
  Shield,
  Terminal,
  X,
} from "lucide-react"

type NodeKey = "entries" | "gateway" | "tunnel" | "agents"

const NODE_DETAILS: Record<
  NodeKey,
  {
    badge: string
    title: string
    summary: string
    specs: string[]
    docHref: string
    docLabel: string
  }
> = {
  entries: {
    badge: "开发者入口层 · 4 Entries, 1 Login",
    title: "人、终端、IDE AI 与流水线共用同一套身份",
    summary:
      "Web 控制台使用 HttpOnly Cookie 会话，doops CLI 覆盖 macOS / Linux / Windows，编辑器通过 SKILL.md 接入，自动化与 CI 通过 JSON-RPC / Git HTTP / REST 调用。",
    specs: [
      "一次 doops login 共享同一 Gateway 下全部授权 Target",
      "IDE Skill（Claude Code / Codex / Cursor）与人类走同一审计与权限闸门",
      "所有变更均返回结构化 JSON 终态与稳定错误码，避免解析不稳定屏幕文本",
    ],
    docHref: "/docs/usage-guide",
    docLabel: "阅读 CLI 与入口指南",
  },
  gateway: {
    badge: "统一控制面 · doops-gateway",
    title: "认证、精确授权、受管凭据与 Fail-Closed 审计",
    summary:
      "控制面可部署在企业内网闭环、公网 TLS 边缘或分域多网关。每次请求在执行前先完成 SHA-256 Token 鉴权、cluster/instance 授权校验与审计记账，账本不可写即拒绝执行。",
    specs: [
      "Token 仅签发时显示一次，服务端常时比对 SHA-256 哈希，撤销下一条请求即生效",
      "受管凭据经 DOOPS_GATEWAY_SECRET_KEY AEAD 加密落库，支持原地双密钥轮换",
      "默认 SQLite 零配置开箱即用，生产规模支持切换 MySQL 8.4 与 HMAC 出站 Webhook",
    ],
    docHref: "/docs/gateway-tunnel",
    docLabel: "阅读 Gateway 架构文档",
  },
  tunnel: {
    badge: "主动反向隧道 · Outbound Reverse WebSocket",
    title: "目标机器不开放任何入站端口，NAT / 防火墙内直连",
    summary:
      "doops-agent 从受管集群或宿主机主动向 Gateway 发起加密出站连接，注册唯一 cluster/instance 身份，支持 Target 绑定 Enrollment Token 与断线续接（execution_id）。",
    specs: [
      "目标节点无需公网 IP，无需维护分散 SSH 端口与跳板机密钥",
      "按 session 与资源键互斥排队，targets --admission 零模型成本观测实时拥堵",
      "网络中断或超时缺失终态时强制报告 execution_outcome_unknown，绝不静默重放变更",
    ],
    docHref: "/docs/runtime-limits",
    docLabel: "查看超时与连接契约",
  },
  agents: {
    badge: "目标执行端 · doops-agent + Domain Skills",
    title: "运行在目标侧的无状态执行通道与领域 Skill 引擎",
    summary:
      "在受管节点提供真实 PTY 终端、结构化 K8s/Helm、DoSql 五引擎数据治理、DoWeb 站点交付、BuildKit 镜像构建以及 8 大 Skill-First 巡检，并由双层命令过滤器守住高危防线。",
    specs: [
      "结构化 argv AST 规则 + 正则兜底两层命令过滤，拦截 rm -rf / 等变体",
      "AI 诊断（ask）与受控变更（ask --apply）严格隔离工具白名单",
      "Agent 自升级由独立 K8s Job 控制器完成备份、digest 核验与失败自动回滚",
    ],
    docHref: "/docs/deploy",
    docLabel: "查看 Agent 部署与升级",
  },
}

const CONTRAST_ROWS = [
  {
    dim: "网络暴露面",
    ssh: "每台机器开放入站 SSH 端口，需逐台维护堡垒机/白名单",
    doops: "Agent 主动出站反向连接 Gateway，目标零入站端口",
  },
  {
    dim: "身份与密钥",
    ssh: "私钥、密码散落个人终端与文档，人员离职逐台清理",
    doops: "一人一令牌 + 统一凭据金库，撤销令牌全目标即时失效",
  },
  {
    dim: "AI 协同边界",
    ssh: "AI 直接拿 shell 风险不可控，凭据容易漏进提示词",
    doops: "工具白名单 + 三层脱敏 + 凭据服务端按需物化，AI 与人同源审计",
  },
  {
    dim: "变更与追溯",
    ssh: "各机 shell history 割裂，中断后难辨变更是否落地",
    doops: "先记账后执行 + 实测回执验收 + execution_outcome_unknown 纪律",
  },
]

export function LandingTopology() {
  const [selected, setSelected] = useState<NodeKey>("gateway")
  const detail = NODE_DETAILS[selected]

  return (
    <div className="space-y-6">
      {/* Interactive Symmetric 3-Node + 2-Rail Architecture Diagram */}
      <div className="portal-frame overflow-hidden p-5 sm:p-6">
        <div className="mb-5 flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2 font-mono text-xs text-muted-foreground">
            <span className="size-2 rounded-full bg-accent" aria-hidden="true" />
            <span>点击架构节点或反向隧道查看协议与安全规格</span>
          </div>
          <span className="rounded-full border border-border/70 bg-background/75 px-3 py-0.5 font-mono text-2xs text-muted-foreground">
            内网闭环 · 公网 TLS 边缘 · 分域多网关
          </span>
        </div>

        <div className="grid items-center gap-3 lg:grid-cols-[minmax(0,1fr)_7.25rem_minmax(0,1.15fr)_7.25rem_minmax(0,1fr)]">
          {/* Layer 1: Developer Entries */}
          <button
            type="button"
            onClick={() => setSelected("entries")}
            className={`h-full cursor-pointer rounded-xl border p-4 text-left transition-all ${
              selected === "entries"
                ? "border-primary/70 bg-primary/10 shadow-[0_0_30px_-14px_color-mix(in_oklch,var(--primary)_70%,transparent)]"
                : "border-border/70 bg-card/55 hover:border-border hover:bg-card/85"
            }`}
          >
            <div className="mb-3 flex items-center justify-between">
              <span className="font-mono text-2xs uppercase tracking-wider text-muted-foreground">
                01 · 四端入口
              </span>
              <span className="rounded bg-background/80 px-1.5 py-0.5 font-mono text-[10px] text-accent">
                1 Login
              </span>
            </div>
            <div className="grid grid-cols-2 gap-2">
              {[
                { icon: Monitor, label: "Web 控制台" },
                { icon: Terminal, label: "doops CLI" },
                { icon: Code2, label: "IDE Skill" },
                { icon: Globe, label: "RPC / Git" },
              ].map((e) => {
                const Icon = e.icon
                return (
                  <div
                    key={e.label}
                    className="flex items-center gap-1.5 rounded-lg border border-border/60 bg-background/80 px-2.5 py-2 text-xs text-foreground"
                  >
                    <Icon className="size-3.5 shrink-0 text-primary" aria-hidden="true" />
                    <span className="truncate">{e.label}</span>
                  </div>
                )
              })}
            </div>
          </button>

          {/* Symmetric Connector 1: Client -> Gateway */}
          <div className="flex flex-col items-center justify-center gap-1.5 py-1">
            <span className="inline-flex items-center gap-1 rounded-full border border-border/75 bg-card/80 px-2.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              HTTPS/WS
              <ArrowRight className="size-2.5 text-primary" aria-hidden="true" />
            </span>
            <div className="portal-pulse-line h-0.5 w-full rounded-full bg-border" />
            <span className="font-mono text-[10px] text-muted-foreground/80">Token 鉴权</span>
          </div>

          {/* Layer 2: Gateway Hub */}
          <button
            type="button"
            onClick={() => setSelected("gateway")}
            className={`h-full cursor-pointer rounded-xl border p-4 text-left transition-all ${
              selected === "gateway"
                ? "border-accent/70 bg-primary/12 shadow-[0_0_36px_-14px_color-mix(in_oklch,var(--accent)_65%,transparent)]"
                : "border-border/70 bg-card/55 hover:border-border hover:bg-card/85"
            }`}
          >
            <div className="mb-3 flex items-center justify-between">
              <span className="inline-flex items-center gap-1.5 font-mono text-2xs uppercase tracking-wider text-accent">
                <Shield className="size-3.5" aria-hidden="true" />
                02 · doops-gateway
              </span>
              <span className="rounded bg-success/15 px-1.5 py-0.5 font-mono text-[10px] text-success">
                控制面
              </span>
            </div>
            <div className="space-y-1.5 text-2xs">
              {[
                "SHA-256 Token 认证 · 即时撤销",
                "cluster/instance 精确目标授权",
                "AEAD 受管凭据库 · 按需物化",
                "Fail-Closed 审计 · SQLite / MySQL",
              ].map((line) => (
                <div
                  key={line}
                  className="flex items-center gap-2 rounded-md border border-border/50 bg-background/75 px-2.5 py-1.5 text-foreground/90"
                >
                  <KeyRound className="size-3 shrink-0 text-accent" aria-hidden="true" />
                  <span className="truncate">{line}</span>
                </div>
              ))}
            </div>
          </button>

          {/* Symmetric Connector 2: Reverse Tunnel (interactive pill rail!) */}
          <button
            type="button"
            onClick={() => setSelected("tunnel")}
            aria-pressed={selected === "tunnel"}
            className="group flex cursor-pointer flex-col items-center justify-center gap-1.5 py-1 text-center"
          >
            <span
              className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 font-mono text-[10px] transition-colors ${
                selected === "tunnel"
                  ? "border-accent bg-accent/18 font-medium text-accent"
                  : "border-accent/40 bg-accent/10 text-accent group-hover:border-accent"
              }`}
            >
              <ArrowLeft className="size-2.5" aria-hidden="true" />
              反向隧道
            </span>
            <div className="portal-pulse-line h-0.5 w-full rounded-full bg-accent/40" />
            <span className="font-mono text-[10px] text-muted-foreground/85">主动出站连出</span>
          </button>

          {/* Layer 3: Target Agents */}
          <button
            type="button"
            onClick={() => setSelected("agents")}
            className={`h-full cursor-pointer rounded-xl border p-4 text-left transition-all ${
              selected === "agents"
                ? "border-primary/70 bg-primary/10 shadow-[0_0_30px_-14px_color-mix(in_oklch,var(--primary)_70%,transparent)]"
                : "border-border/70 bg-card/55 hover:border-border hover:bg-card/85"
            }`}
          >
            <div className="mb-3 flex items-center justify-between">
              <span className="font-mono text-2xs uppercase tracking-wider text-muted-foreground">
                03 · doops-agent
              </span>
              <span className="rounded bg-background/80 px-1.5 py-0.5 font-mono text-[10px] text-success">
                0 入站端口
              </span>
            </div>
            <div className="grid grid-cols-2 gap-2">
              {[
                { icon: Boxes, label: "K8s & Helm" },
                { icon: Server, label: "VM / 物理机" },
                { icon: Database, label: "DoSql 数据库" },
                { icon: Globe, label: "DoWeb 站点" },
              ].map((t) => {
                const Icon = t.icon
                return (
                  <div
                    key={t.label}
                    className="flex items-center gap-1.5 rounded-lg border border-border/60 bg-background/80 px-2.5 py-2 text-xs text-foreground"
                  >
                    <Icon className="size-3.5 shrink-0 text-accent" aria-hidden="true" />
                    <span className="truncate">{t.label}</span>
                  </div>
                )
              })}
            </div>
          </button>
        </div>

        {/* Selected Node Technical Spec Readout */}
        <div className="mt-5 grid gap-5 rounded-xl border border-border/70 bg-background/85 p-5 lg:grid-cols-12">
          <div className="lg:col-span-5">
            <span className="inline-flex rounded-md bg-primary/12 px-2 py-0.5 font-mono text-2xs text-primary">
              {detail.badge}
            </span>
            <h3 className="mt-2.5 text-base font-semibold text-foreground">{detail.title}</h3>
            <p className="mt-2 text-xs leading-relaxed text-muted-foreground">{detail.summary}</p>
            <Link
              href={detail.docHref}
              className="mt-3.5 inline-flex items-center gap-1.5 text-xs font-medium text-accent transition-opacity hover:opacity-85"
            >
              {detail.docLabel}
              <ArrowRight className="size-3.5" aria-hidden="true" />
            </Link>
          </div>
          <div className="flex flex-col justify-center gap-2 lg:col-span-7 lg:border-l lg:border-border/55 lg:pl-6">
            {detail.specs.map((spec) => (
              <div
                key={spec}
                className="flex items-start gap-2.5 rounded-lg border border-border/50 bg-card/45 px-3.5 py-2 text-xs text-foreground/90"
              >
                <Check className="mt-0.5 size-3.5 shrink-0 text-success" aria-hidden="true" />
                <span className="leading-relaxed">{spec}</span>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Engineering Contrast Table: SSH vs DoOps */}
      <div className="card-surface overflow-hidden">
        <div className="grid border-b border-border/65 bg-card/70 px-5 py-3 text-xs font-semibold md:grid-cols-12">
          <div className="text-muted-foreground md:col-span-2">工程维度</div>
          <div className="mt-1 flex items-center gap-1.5 text-muted-foreground md:col-span-5 md:mt-0">
            <X className="size-3.5 text-destructive" aria-hidden="true" />
            传统 SSH / 散落运维脚本
          </div>
          <div className="mt-1 flex items-center gap-1.5 text-foreground md:col-span-5 md:mt-0">
            <Check className="size-3.5 text-success" aria-hidden="true" />
            DoOps 智能运维中心
          </div>
        </div>
        <div className="divide-y divide-border/45">
          {CONTRAST_ROWS.map((row) => (
            <div key={row.dim} className="grid gap-2 px-5 py-3.5 text-xs md:grid-cols-12 md:items-center md:gap-4">
              <div className="font-medium text-foreground md:col-span-2">{row.dim}</div>
              <div className="text-muted-foreground md:col-span-5">{row.ssh}</div>
              <div className="font-medium text-foreground/95 md:col-span-5">{row.doops}</div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
