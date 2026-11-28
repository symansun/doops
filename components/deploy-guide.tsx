"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowLeft, ArrowRight } from "lucide-react"
import {
  ServerIcon,
  TerminalIcon,
  ShieldIcon,
  RocketIcon,
  CheckIcon,
  CopyIcon,
  HelpIcon,
  ChevronRightIcon,
  PlugIcon,
  RefreshIcon,
  LayersIcon,
  FileIcon,
} from "./icons"

function ArrowFlow({ label }: { label?: string }) {
  return (
    <span aria-hidden className="flex flex-1 flex-col items-center gap-1 px-2 text-center font-mono text-2xs text-muted-foreground">
      {label ? <span className="text-[10px] text-accent">{label}</span> : null}
      <span className="portal-pulse-line h-0.5 w-full rounded-full bg-border" />
    </span>
  )
}

function Cmd({ children }: { children: string }) {
  const [copied, setCopied] = useState(false)
  function copy() {
    navigator.clipboard?.writeText(children).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }
  return (
    <div className="code-surface group relative my-2.5 overflow-hidden">
      <pre className="overflow-x-auto px-3.5 py-3 pr-20 font-mono text-xs leading-relaxed text-foreground">
        {children}
      </pre>
      <button
        type="button"
        onClick={copy}
        aria-label="复制命令"
        className="absolute right-2 top-2 flex cursor-pointer items-center gap-1 rounded-md border border-border/80 bg-card/90 px-2 py-1 text-2xs text-muted-foreground transition-colors hover:border-primary/45 hover:text-foreground"
      >
        {copied ? (
          <>
            <CheckIcon width={12} height={12} className="text-success" /> 已复制
          </>
        ) : (
          <>
            <CopyIcon width={12} height={12} /> 复制
          </>
        )}
      </button>
    </div>
  )
}

type Step = {
  n: number
  icon: typeof ServerIcon
  title: string
  body: React.ReactNode
}

const STEPS: Step[] = [
  {
    n: 1,
    icon: ShieldIcon,
    title: "准备授权的集群管理上下文",
    body: (
      <ul className="ml-4 list-disc space-y-1.5 text-sm text-muted-foreground">
        <li>确认目标集群或宿主机可出站访问 Gateway，并由已授权的集群管理流程实施初装</li>
        <li>准备经审查的 GitOps Chart / values、集群身份和初装清理方案</li>
        <li>
          向管理员确认 <span className="font-medium text-foreground">gateway 地址</span>、{" "}
          <span className="font-medium text-foreground">cluster</span> 与{" "}
          <span className="font-medium text-foreground">instance</span>，并核对不可变镜像 digest
        </li>
      </ul>
    ),
  },
  {
    n: 2,
    icon: LayersIcon,
    title: "确认接入标识与 Enrollment Token",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          Agent 反向注册须声明唯一的{" "}
          <span className="font-mono text-foreground">cluster/instance</span>，同一标识不能同时分配给两台机器。
          生产或需要下发受管凭据的目标应通过环境变量{" "}
          <span className="font-mono text-foreground">DOOPS_AGENT_ENROLL_TOKEN</span>（Helm：
          <span className="font-mono text-foreground">gateway.enrollmentTokenSecret</span>）注入目标专属
          enrollment token；首次以此令牌连通过后，Gateway 会强制后续注册校验该令牌。
        </p>
      </div>
    ),
  },
  {
    n: 3,
    icon: RocketIcon,
    title: "新集群按已审查 GitOps 流程初装",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          仅在尚无同身份 Agent、Helm release、Deployment 或恢复对象时初装。将固定版本的 Chart、完整
          values、镜像 digest 和清理方案纳入目标集群的 GitOps 仓库，审查后通过集群已有的授权管理流程创建新资源。
        </p>
        <p className="text-xs">
          按{" "}
          <Link href="/docs/cluster-install" className="font-mono text-accent underline underline-offset-4 hover:text-foreground">
            集群接入指南 (CLUSTER_INSTALL_GUIDE.md)
          </Link>{" "}
          的“安装”章节执行。当前没有 K8S Agent 自动 installer；完成 Gateway 注册和安装验收后再使用 target。
          已有对象离线或损坏属于恢复，不能重新初装覆盖。
        </p>
      </div>
    ),
  },
  {
    n: 4,
    icon: TerminalIcon,
    title: "现有 Agent 确认 upgradeAudit v1 后升级",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          已有标准 Helm-managed Agent、匹配的 Helm release、新控制器和 <span className="font-mono text-foreground">upgradeAudit v1</span>{" "}
          时，登录获授权的 Gateway target，使用与候选 Agent 制品同 commit 的正式 CLI 升级。
          独立 Job 通过共享 kubeconfig 或专用 ServiceAccount 执行预检、备份、验收和失败自动回滚。
        </p>
        <Cmd>
          {"doops -session agent-upgrade-<RELEASE_ID> upgrade \\\n  --target <CONFIGURED_GATEWAY_TARGET> \\\n  --image <IMAGE_REPOSITORY>@sha256:<VERIFIED_DIGEST> \\\n  --source-commit <VERIFIED_40_HEX_CI_COMMIT> \\\n  --ci-url <VERIFIED_HTTPS_CI_URL>"}
        </Cmd>
        <p className="text-xs">
          发布者先核对 CI 成功证据、完整 source commit 与镜像 digest；CI URL 不含凭据、查询参数或片段。
          旧 Agent 首升需通过在线、已授权且具备集群权限的 Gateway exec 通道提交经审查的独立 Job，
          不能在 Agent 自身或 <span className="font-mono">exec --bg</span> 进程里运行替换自身的 Helm 命令。
        </p>
        <p className="text-xs">
          详见{" "}
          <Link href="/docs/build-deploy-sop" className="font-mono text-accent underline underline-offset-4 hover:text-foreground">
            构建与部署 SOP (BUILD_DEPLOY_SOP.md)
          </Link>{" "}
          审查备份、验收和自动回滚。
        </p>
      </div>
    ),
  },
  {
    n: 5,
    icon: CheckIcon,
    title: "完成安装与能力验收",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>从 Gateway 和真实远程调用两侧确认精确 target、版本与协议能力：</p>
        <Cmd>
          {"doops -session install-verify targets --target <CONFIGURED_GATEWAY_TARGET>\ndoops -session install-verify exec --target <CONFIGURED_GATEWAY_TARGET> --cmd 'do-agent --version'\ndoops -session install-verify capabilities --target <CONFIGURED_GATEWAY_TARGET> --require structuredSSH"}
        </Cmd>
        <p className="text-xs">
          还需核验最终 Deployment spec/status、实际 Pod UID/Ready/imageID、Gateway 上的{" "}
          <span className="font-mono">release_commit</span> 和 <span className="font-mono">upgradeAudit v1</span>
          、服务健康及关键功能路径；失败时停止宣告成功，按已确认的回滚路径处理。
        </p>
      </div>
    ),
  },
  {
    n: 6,
    icon: ServerIcon,
    title: "回到控制台确认并开始运维",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          Agent 连接成功后，目标会自动出现在控制台。回到{" "}
          <Link href="/console" className="text-accent underline-offset-4 hover:underline">
            控制台
          </Link>{" "}
          点击「刷新」，即可看到在线目标，随后可使用 AI 交互终端、DoSql、智能巡检与 K8s 交付能力。
        </p>
      </div>
    ),
  },
]

const TROUBLE = [
  {
    q: "首次接入被权限阻塞",
    a: "由集群已有的授权管理流程实施经审查的 GitOps 初装，准备完整 values、固定镜像 digest 和清理方案。当前没有自动 installer，页面不提供集群管理权限。",
  },
  {
    q: "已有 workload 但没有 Helm release",
    a: "已有 Deployment 属于收编或恢复，不能重新初装。按发布 SOP 审查备份、验收和自动回滚，由在线授权的 Gateway 管理通道提交独立 Job；不能用本机脚本绕过审计。",
  },
  {
    q: "现有 Agent 不能使用 doops upgrade",
    a: "核对匹配的 Helm release、新控制器、upgradeAudit v1，以及 Job 可用的共享 kubeconfig 或专用 ServiceAccount。旧协议首升须经 Gateway 提交经审查的独立 Job；没有在线授权管理通道时停止，不能改走本机 Helm。",
  },
  {
    q: "机器没有出现在控制台",
    a: "先用 doops targets 查询精确 cluster/instance 是否在线及 ENROLLED 状态，再确认 Gateway 出站网络、身份标识唯一且当前账号拥有该 target 的精确授权。",
  },
]

// 正文：公开文档页（/docs/deploy）与控制台内「首次接入」视图（DeployView）共用
// onEnterConsole 提供时（控制台内嵌），底部 CTA 变为「刷新机器列表」回调；否则链接到 /console
export function DeployGuideBody({ onEnterConsole }: { onEnterConsole?: () => void }) {
  return (
    <div className={onEnterConsole ? "px-4 py-8" : "pb-12"}>
      {/* 标题 */}
      <div className="mb-8 border-b border-border/55 pb-6">
        <span className="mb-3 inline-flex items-center gap-1.5 rounded-full border border-success/35 bg-success/12 px-3 py-0.5 font-mono text-2xs text-success">
          <PlugIcon width={13} height={13} />
          运维手册 · 部署与升级
        </span>
        <h1 className="text-balance text-2xl font-semibold tracking-tight sm:text-3xl">
          部署 DoOps Agent
        </h1>
        <p className="mt-2.5 text-pretty text-sm leading-relaxed text-muted-foreground">
          DoOps 通过在服务器或 Kubernetes 集群内运行轻量 <code className="font-mono text-foreground">doops-agent</code> 工作。
          Agent 主动建立反向隧道连接 Gateway，受管节点无需暴露任何入站端口。接入后即可统一使用 AI 终端、命令、DoSql、巡检与受控交付。
        </p>
        <p className="mt-4 text-xs font-medium text-foreground/90">
          安装或升级前先核对完整 source commit 与不可变镜像 digest，再拉取已核验制品：
        </p>
        <Cmd>{"docker pull <IMAGE_REPOSITORY>@sha256:<VERIFIED_DIGEST>"}</Cmd>
        <p className="text-xs text-muted-foreground">
          控制台镜像由操作者单独指定仓库，同样固定为{" "}
          <span className="font-mono text-foreground/90">&lt;IMAGE_REPOSITORY&gt;@sha256:&lt;VERIFIED_DIGEST&gt;</span>。
          拉取镜像不等于已完成安装或升级，仍需按下列流程实施与验收。
        </p>
      </div>

      {/* 没有实例时的两条路径 */}
      <div className="mb-10 grid gap-3.5 sm:grid-cols-2">
        <div className="card-surface p-4">
          <div className="mb-1.5 flex items-center gap-2 text-sm font-semibold text-foreground">
            <RocketIcon width={16} height={16} className="text-accent" />
            自己部署
          </div>
          <p className="text-xs leading-relaxed text-muted-foreground">
            已获集群或宿主机管理授权？按下面的 GitOps 初装或 Gateway 独立控制器升级路径实施。
          </p>
        </div>
        <div className="card-surface p-4">
          <div className="mb-1.5 flex items-center gap-2 text-sm font-semibold text-foreground">
            <HelpIcon width={16} height={16} className="text-primary" />
            联系管理员
          </div>
          <p className="text-xs leading-relaxed text-muted-foreground">
            没有集群管理授权或不确定 gateway 地址与接入标识？联系团队平台管理员完成接入与目标授权。
          </p>
        </div>
      </div>

      {/* 标准连接架构 */}
      <section className="mb-12">
        <h2 className="mb-1 flex items-center gap-2 text-lg font-semibold">
          <LayersIcon width={18} height={18} className="text-accent" />
          Gateway-First 反向隧道架构
        </h2>
        <p className="mb-4 text-sm text-muted-foreground">
          Agent 与客户端都主动连接 Gateway，由 Gateway 统一执行认证、精确目标授权、凭据物化与审计。
        </p>
        <div className="card-surface flex flex-col p-5">
          <div className="mb-4 flex items-center gap-2.5">
            <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15 text-primary ring-1 ring-inset ring-primary/25">
              <ServerIcon width={16} height={16} />
            </span>
            <div>
              <div className="text-sm font-semibold text-foreground">经 Gateway 统一收口</div>
              <div className="font-mono text-2xs text-muted-foreground">唯一标准接入架构</div>
            </div>
          </div>
          <div className="mb-4 flex items-center justify-between gap-2 rounded-xl border border-border/70 bg-background/85 px-4 py-4 text-center text-2xs text-muted-foreground">
            <span className="flex flex-col items-center gap-1.5">
              <FileIcon width={16} height={16} className="text-foreground" />
              <span className="font-medium text-foreground">Web / CLI / Skill</span>
            </span>
            <ArrowFlow label="HTTPS / WS" />
            <span className="flex flex-col items-center gap-1.5">
              <ShieldIcon width={16} height={16} className="text-accent" />
              <span className="font-medium text-accent">doops-gateway</span>
            </span>
            <ArrowFlow label="← 主动反向连出" />
            <span className="flex flex-col items-center gap-1.5">
              <ServerIcon width={16} height={16} className="text-foreground" />
              <span className="font-medium text-foreground">doops-agent</span>
            </span>
          </div>
          <ul className="grid gap-2 text-xs text-muted-foreground sm:grid-cols-3">
            <li className="flex gap-2 rounded-lg border border-border/50 bg-background/60 p-3">
              <CheckIcon width={14} height={14} className="mt-0.5 shrink-0 text-success" />
              <span>实例可位于内网 / NAT 之后，零入站端口</span>
            </li>
            <li className="flex gap-2 rounded-lg border border-border/50 bg-background/60 p-3">
              <CheckIcon width={14} height={14} className="mt-0.5 shrink-0 text-success" />
              <span>统一身份、精确授权与 Fail-Closed 审计</span>
            </li>
            <li className="flex gap-2 rounded-lg border border-border/50 bg-background/60 p-3">
              <CheckIcon width={14} height={14} className="mt-0.5 shrink-0 text-success" />
              <span>支持内网闭环、公网边缘或分域多网关</span>
            </li>
          </ul>
        </div>
      </section>

      {/* 步骤 */}
      <section className="space-y-6">
        {STEPS.map((s) => (
          <div key={s.n} className="flex gap-4">
            <div className="flex flex-col items-center">
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary/15 text-primary ring-1 ring-inset ring-primary/25">
                <s.icon width={18} height={18} />
              </span>
              {s.n < STEPS.length && <span className="mt-1.5 w-px flex-1 bg-border/70" />}
            </div>
            <div className="min-w-0 flex-1 pb-2">
              <h2 className="mb-2 flex items-center gap-2 text-base font-semibold text-foreground">
                <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-2xs text-accent">
                  0{s.n}
                </span>
                {s.title}
              </h2>
              {s.body}
            </div>
          </div>
        ))}
      </section>

      {/* 故障排查 */}
      <section className="mt-12">
        <h2 className="mb-3 text-lg font-semibold">常见故障排查</h2>
        <div className="divide-y divide-border/55 rounded-xl border border-border/70 bg-card/60">
          {TROUBLE.map((t, i) => (
            <details key={i} className="group px-4 py-3.5 [&_summary::-webkit-details-marker]:hidden">
              <summary className="flex cursor-pointer list-none items-center gap-2 text-sm font-medium text-foreground">
                <ChevronRightIcon
                  width={15}
                  height={15}
                  className="text-muted-foreground transition-transform group-open:rotate-90"
                />
                {t.q}
              </summary>
              <p className="ml-6 mt-2 text-xs leading-relaxed text-muted-foreground sm:text-sm">
                {t.a}
              </p>
            </details>
          ))}
        </div>
      </section>

      {/* 底部 CTA */}
      <div className="card-surface mt-12 flex flex-col items-center justify-between gap-4 p-6 sm:flex-row">
        <div>
          <div className="text-sm font-semibold text-foreground">目标节点已连上 Gateway？</div>
          <p className="mt-0.5 text-xs text-muted-foreground">
            回到控制台刷新在线目标，立即使用 AI 交互终端与领域工具。
          </p>
        </div>
        {onEnterConsole ? (
          <button
            type="button"
            onClick={onEnterConsole}
            className="btn btn-primary shrink-0"
          >
            <RefreshIcon width={16} height={16} />
            刷新机器列表
          </button>
        ) : (
          <Link href="/console" className="btn btn-primary shrink-0">
            <ServerIcon width={16} height={16} />
            进入控制台
          </Link>
        )}
      </div>
    </div>
  )
}

// 公开文档页：位于 app/docs/layout.tsx 之内，复用统一侧栏与页头页脚
export function DeployGuide() {
  return (
    <div className="min-w-0 max-w-4xl">
      <nav
        aria-label="面包屑导航"
        className="mb-5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground"
      >
        <Link href="/docs" className="transition-colors hover:text-foreground">
          文档中心
        </Link>
        <span aria-hidden="true">/</span>
        <span>入门与接入</span>
        <span aria-hidden="true">/</span>
        <span className="text-foreground">Agent 部署与升级</span>
      </nav>

      <DeployGuideBody />

      <nav
        aria-label="上一篇与下一篇文档"
        className="grid gap-3 border-t border-border/55 pt-8 sm:grid-cols-2"
      >
        <Link
          href="/docs/cluster-install"
          className="card-surface card-interactive group flex flex-col gap-1 p-4"
        >
          <span className="inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
            <ArrowLeft className="size-3 transition-transform group-hover:-translate-x-0.5" aria-hidden="true" />
            上一篇
          </span>
          <span className="text-sm font-semibold text-foreground group-hover:text-accent">
            客户端与集群接入
          </span>
        </Link>
        <Link
          href="/docs/workbench"
          className="card-surface card-interactive group flex flex-col items-end gap-1 p-4 text-right"
        >
          <span className="inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
            下一篇
            <ArrowRight className="size-3 transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
          </span>
          <span className="text-sm font-semibold text-foreground group-hover:text-accent">
            统一运维工作台
          </span>
        </Link>
      </nav>
    </div>
  )
}
