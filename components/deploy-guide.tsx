"use client"

import { useState } from "react"
import Link from "next/link"
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

// 架构拓扑中的双向连接箭头
function ArrowFlow() {
  return (
    <span aria-hidden className="flex-1 text-center font-mono text-sm text-border">
      {"<——>"}
    </span>
  )
}

// 可复制命令块
function Cmd({ children }: { children: string }) {
  const [copied, setCopied] = useState(false)
  function copy() {
    navigator.clipboard?.writeText(children).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }
  return (
    <div className="group relative my-2 overflow-hidden rounded-lg border bg-muted">
      <pre className="overflow-x-auto px-3 py-2.5 pr-10 font-mono text-xs leading-relaxed text-foreground">
        {children}
      </pre>
      <button
        onClick={copy}
        aria-label="复制命令"
        className="absolute right-1.5 top-1.5 flex items-center gap-1 rounded-md border bg-background px-1.5 py-1 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
      >
        {copied ? (
          <>
            <CheckIcon width={12} height={12} className="text-primary" /> 已复制
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
      <ul className="ml-4 list-disc space-y-1 text-sm text-muted-foreground">
        <li>确认目标集群可出站访问 Gateway，并由已授权的集群管理流程实施初装</li>
        <li>准备经审查的 GitOps Chart / values、集群身份和初装清理方案</li>
        <li>
          向管理员确认 <span className="font-medium text-foreground">gateway 地址</span>、{" "}
          <span className="font-medium text-foreground">cluster</span> 与{" "}
          <span className="font-medium text-foreground">instance</span>，并核对 CI 镜像 digest
        </li>
      </ul>
    ),
  },
  {
    n: 2,
    icon: LayersIcon,
    title: "确认接入标识",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          Agent 反向注册只声明唯一的{" "}
          <span className="font-mono text-foreground">cluster/instance</span>，不读取或发送注册令牌。
          同一标识不能同时分配给两台机器。
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
          仅在尚无同身份 Agent、Helm release、Deployment 或恢复对象时初装。
          将固定版本的 Chart、完整 values、镜像 digest 和清理方案纳入目标集群的 GitOps 仓库，
          审查后通过集群已有的授权管理流程创建新资源。
        </p>
        <p className="text-xs">
          按仓库 <span className="font-mono">backend/docs/CLUSTER_INSTALL_GUIDE.md</span> 的“安装”章节执行。
          当前没有 K8S Agent 自动 installer；完成 Gateway 注册和安装验收后再使用 target。
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
          已有标准 Helm-managed Agent、匹配的 Helm release、新控制器和 upgradeAudit v1 时，
          登录获授权的 Gateway target，使用与候选 Agent 制品同 commit 的正式 CLI 升级。
          独立 Job 通过共享 kubeconfig 或专用 ServiceAccount 执行预检、备份、验收和失败自动回滚。
        </p>
        <Cmd>
          {"doops -session agent-upgrade-<RELEASE_ID> upgrade \\\n  --target <CONFIGURED_GATEWAY_TARGET> \\\n  --image <IMAGE_REPOSITORY>@sha256:<VERIFIED_DIGEST> \\\n  --source-commit <VERIFIED_40_HEX_CI_COMMIT> \\\n  --ci-url <VERIFIED_HTTPS_CI_URL>"}
        </Cmd>
        <p className="text-xs">
          发布者先核对 CI 成功证据、完整 source commit 与镜像 digest；CI URL 不含凭据、查询参数或片段。
          structuredSSH 声明不能代替 upgradeAudit v1 和新控制器。
          旧 Agent 首升需通过在线、已授权且具备集群权限的 Gateway exec 通道提交经审查的独立 Job，
          不能在 Agent 自身或 exec --bg 进程里运行替换自身的 Helm 命令。
        </p>
        <p className="text-xs">
          首升没有通用自动提交入口；Helm 配置变更也须先进入仓库 Chart / values，再经 Gateway 独立 Job 收敛。
          按 <span className="font-mono">backend/docs/BUILD_DEPLOY_SOP.md</span> 审查备份、验收和自动回滚；
          release-managed-agent.sh 仅委托受审计的 upgrade，不能绕过旧协议、权限或审计限制，也不提供初装或配置变更入口。
        </p>
      </div>
    ),
  },
  {
    n: 5,
    icon: CheckIcon,
    title: "完成安装验收",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>从 Gateway 和真实远程调用两侧确认精确 target、版本与协议能力：</p>
        <Cmd>
          {"doops -session install-verify targets --target <CONFIGURED_GATEWAY_TARGET>\ndoops -session install-verify exec --target <CONFIGURED_GATEWAY_TARGET> --cmd 'do-agent --version'\ndoops -session install-verify capabilities --target <CONFIGURED_GATEWAY_TARGET> --require structuredSSH"}
        </Cmd>
        <p className="text-xs">
          还需核验最终 Deployment spec/status、实际 Pod UID/Ready/imageID、Gateway 上的 release_commit 和 upgradeAudit v1、服务健康及关键功能路径；
          target 在线或 structuredSSH 协议声明不能代替完整验收。失败时停止宣告成功，按已确认的回滚路径处理。
        </p>
      </div>
    ),
  },
  {
    n: 6,
    icon: ServerIcon,
    title: "回到控制台确认",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          agent 连接成功后，机器会自动出现在控制台。回到{" "}
          <Link href="/console" className="text-primary underline-offset-2 hover:underline">
            控制台
          </Link>{" "}
          点击「刷新」，即可看到这台新机器，随后就能用 AI 助手运维。
        </p>
      </div>
    ),
  },
]

const TROUBLE = [
  {
    q: "首次接入被权限阻塞",
    a: "由集群已有的授权管理流程实施经审查的 GitOps 初装，准备完整 values、固定 CI 镜像 digest 和清理方案。当前没有自动 installer，页面不提供集群管理权限。",
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
    a: "先用 targets 查询精确 cluster/instance 是否在线，再确认 Gateway 出站网络、身份标识唯一且没有被另一台机器占用，最后回到控制台刷新。",
  },
]

// 正文：公开文档页与控制台内「部署 Agent」视图共用
// onEnterConsole 提供时（控制台内嵌），底部 CTA 变为「刷新机器列表」回调；否则链接到 /console
export function DeployGuideBody({ onEnterConsole }: { onEnterConsole?: () => void }) {
  return (
    <div className="px-4 py-10">
        {/* 标题 */}
        <div className="mb-8">
          <span className="mb-3 inline-flex items-center gap-1.5 rounded-full border bg-card px-3 py-1 text-xs text-muted-foreground">
            <PlugIcon width={13} height={13} className="text-primary" />
            部署指南
          </span>
          <h1 className="text-2xl font-semibold text-balance">部署 Doops Agent</h1>
          <p className="mt-2 text-pretty text-sm leading-relaxed text-muted-foreground">
            Doops 通过在你的服务器上安装一个轻量 agent 来工作。Agent 主动连接 Gateway，机器无需暴露公网端口。接入后即可远程执行命令、管理文件、用 AI 完成部署与巡检。
          </p>
          <p className="mt-3 text-sm text-muted-foreground">安装或升级前核对成功 CI、完整 source commit 和不可变镜像 digest，再拉取已核验制品：</p>
          <Cmd>{"docker pull <IMAGE_REPOSITORY>@sha256:<VERIFIED_DIGEST>"}</Cmd>
          <p className="text-xs text-muted-foreground">
            控制台镜像由操作者单独指定仓库，同样固定为 <span className="font-mono">&lt;IMAGE_REPOSITORY&gt;@sha256:&lt;VERIFIED_DIGEST&gt;</span>。
            拉取镜像不等于已完成安装或升级，仍需按下列流程实施和验收。
          </p>
        </div>

        {/* 没有实例时的两条路径 */}
        <div className="mb-10 grid gap-3 sm:grid-cols-2">
          <div className="rounded-xl border bg-card p-4">
            <div className="mb-1.5 flex items-center gap-2 text-sm font-medium">
              <RocketIcon width={16} height={16} className="text-primary" />
              自己部署
            </div>
            <p className="text-xs leading-relaxed text-muted-foreground">
              已获集群管理授权？按下面的 GitOps 初装或 Gateway 升级路径实施。
            </p>
          </div>
          <div className="rounded-xl border bg-card p-4">
            <div className="mb-1.5 flex items-center gap-2 text-sm font-medium">
              <HelpIcon width={16} height={16} className="text-primary" />
              联系管理员
            </div>
            <p className="text-xs leading-relaxed text-muted-foreground">
              没有集群管理授权或不确定 gateway 地址与接入标识？联系团队管理员完成接入。
            </p>
          </div>
        </div>

        {/* 标准连接架构 */}
        <section className="mb-12">
          <h2 className="mb-1 flex items-center gap-2 text-lg font-semibold">
            <LayersIcon width={18} height={18} className="text-primary" />
            Gateway 标准架构
          </h2>
          <p className="mb-4 text-sm text-muted-foreground">
            Agent 与客户端都主动连接 Gateway，由 Gateway 统一执行用户鉴权、授权、审计与转发。
          </p>
          <div>
            <div className="flex flex-col rounded-xl border bg-card p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/15 text-primary">
                  <ServerIcon width={16} height={16} />
                </span>
                <div>
                  <div className="text-sm font-semibold text-foreground">经 Gateway 中转</div>
                  <div className="text-xs text-muted-foreground">唯一标准接入方式</div>
                </div>
              </div>
              {/* 拓扑示意 */}
              <div className="mb-3 flex items-center justify-between gap-1 rounded-lg border bg-background px-3 py-3 text-center text-[11px] text-muted-foreground">
                <span className="flex flex-col items-center gap-1">
                  <FileIcon width={15} height={15} className="text-foreground" />
                  Web / 客户端
                </span>
                <ArrowFlow />
                <span className="flex flex-col items-center gap-1">
                  <ShieldIcon width={15} height={15} className="text-primary" />
                  Gateway
                </span>
                <ArrowFlow />
                <span className="flex flex-col items-center gap-1">
                  <ServerIcon width={15} height={15} className="text-foreground" />
                  实例 Agent
                </span>
              </div>
              <p className="mb-3 text-xs leading-relaxed text-muted-foreground text-pretty">
                Agent 与 Web/客户端都<strong className="text-foreground">主动连接同一个 gateway</strong>，由 gateway 居中转发。实例无需暴露公网端口，只要能出站访问 gateway 即可。
              </p>
              <ul className="mt-auto space-y-1.5 text-xs text-muted-foreground">
                <li className="flex gap-2">
                  <CheckIcon width={14} height={14} className="mt-0.5 shrink-0 text-primary" />
                  实例可位于内网 / NAT 之后，无需公网 IP
                </li>
                <li className="flex gap-2">
                  <CheckIcon width={14} height={14} className="mt-0.5 shrink-0 text-primary" />
                  统一鉴权与审计，便于管理多台机器
                </li>
                <li className="flex gap-2">
                  <CheckIcon width={14} height={14} className="mt-0.5 shrink-0 text-primary" />
                  需要先部署 / 拥有一个可用的 gateway
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* 步骤 */}
        <section className="space-y-6">
          {STEPS.map((s) => (
            <div key={s.n} className="flex gap-4">
              <div className="flex flex-col items-center">
                <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary/15 text-primary">
                  <s.icon width={18} height={18} />
                </span>
                {s.n < STEPS.length && <span className="mt-1 w-px flex-1 bg-border" />}
              </div>
              <div className="min-w-0 flex-1 pb-2">
                <h2 className="mb-2 flex items-center gap-2 text-base font-medium">
                  <span className="text-xs text-muted-foreground">步骤 {s.n}</span>
                  {s.title}
                </h2>
                {s.body}
              </div>
            </div>
          ))}
        </section>

        {/* 故障排查 */}
        <section className="mt-12">
          <h2 className="mb-3 text-lg font-semibold">故障排查</h2>
          <div className="divide-y rounded-xl border bg-card">
            {TROUBLE.map((t, i) => (
              <details key={i} className="group px-4 py-3">
                <summary className="flex cursor-pointer list-none items-center gap-2 text-sm font-medium text-foreground">
                  <ChevronRightIcon
                    width={15}
                    height={15}
                    className="text-muted-foreground transition-transform group-open:rotate-90"
                  />
                  {t.q}
                </summary>
                <p className="ml-6 mt-2 text-sm leading-relaxed text-muted-foreground">{t.a}</p>
              </details>
            ))}
          </div>
        </section>

        {/* 底部 CTA */}
        <div className="mt-12 flex flex-col items-center gap-3 rounded-xl border bg-card p-6 text-center">
          <p className="text-sm text-muted-foreground">机器已接入？开始运维</p>
          {onEnterConsole ? (
            <button
              onClick={onEnterConsole}
              className="flex items-center gap-1.5 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
            >
              <RefreshIcon width={16} height={16} />
              刷新机器列表
            </button>
          ) : (
            <Link
              href="/console"
              className="flex items-center gap-1.5 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
            >
              <ServerIcon width={16} height={16} />
              进入控制台
            </Link>
          )}
        </div>
    </div>
  )
}

// 公开文档页：带营销顶栏，复用上方正文
export function DeployGuide() {
  return (
    <main className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-20 border-b border-border/60 bg-background/80 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          <Link href="/" className="flex items-center gap-2 text-sm font-semibold">
            <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-primary text-primary-foreground">
              <ServerIcon width={16} height={16} />
            </span>
            Doops
          </Link>
          <Link
            href="/console"
            className="flex items-center gap-1 rounded-lg border px-3 py-1.5 text-sm text-foreground transition-colors hover:bg-muted"
          >
            进入控制台
            <ChevronRightIcon width={15} height={15} />
          </Link>
        </div>
      </header>
      <div className="mx-auto max-w-6xl">
        <DeployGuideBody />
      </div>
    </main>
  )
}
