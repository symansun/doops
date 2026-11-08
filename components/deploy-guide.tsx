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
        <li>准备可联网访问 gateway 的 Linux 服务器或持有 kubeconfig 的 CI / 管理主机</li>
        <li>首次接入新集群必须具备外部控制器 kubeconfig 及对应集群管理权限</li>
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
    title: "新集群使用 Helm bootstrap",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          新集群尚无 Agent 时，使用外部控制器执行本仓库 Helm bootstrap。
          这是首次接入路径，需要显式提供授权 kubeconfig；当前页面不会替你申请或绕过该权限。
        </p>
        <Cmd>
          {
            "bash backend/scripts/release-managed-agent.sh \\\n  --release <HELM_RELEASE> \\\n  --namespace <NAMESPACE> \\\n  --values <REVIEWED_COMPLETE_VALUES.yaml> \\\n  --kubeconfig <EXTERNAL_CONTROLLER_KUBECONFIG> \\\n  --image <IMAGE_REPOSITORY>@sha256:<VERIFIED_CI_DIGEST>"
          }
        </Cmd>
        <p className="text-xs">
          从仓库根目录执行。--values 路径只做 bootstrap、Helm wait 和最终 Deployment 检查，不自动完成 Gateway 接管或失败回滚。
          旧 Agent 若无自升级控制器，需管理员使用该脚本的现有 release 模式，提供 --target、--session、--cluster、--instance，
          不传 --values，不重新 bootstrap 或手工替换容器。
        </p>
      </div>
    ),
  },
  {
    n: 4,
    icon: TerminalIcon,
    title: "现有 controller-ready Agent 用 doops upgrade",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>
          已有标准 Helm-managed Agent、匹配的 Helm release 和自升级控制器时，只更新 Agent 镜像：
          该路径由集群内 Job 复用 Agent 挂载的 kubeconfig，不需要操作机 kubeconfig。
        </p>
        <Cmd>
          {"doops -session agent-upgrade-<RELEASE_ID> upgrade \\\n  --target <CONFIGURED_GATEWAY_TARGET> \\\n  --image docker.cnb.cool/l8ai/ai/doops.sh:latest"}
        </Cmd>
        <p className="text-xs">
          <span className="font-mono">:latest</span> 跟随 main 最新构建。固定版本写成{" "}
          <span className="font-mono">docker.cnb.cool/l8ai/ai/doops.sh@sha256:&lt;digest&gt;</span>。
          doops upgrade 只适用于 controller-ready 的现有 Helm release；structuredSSH 声明不代表具备自升级控制器。
          显式 Helm 配置变更使用外部脚本的现有 release 模式，不重新 bootstrap。
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
          还需核验最终 Deployment spec/status、实际 Pod UID/Ready/imageID、服务健康和关键功能路径；
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
    a: "首次接入新集群需要外部控制器的集群 kubeconfig、完整 reviewed values 和核验过的 CI 镜像 digest。当前页面不提供集群管理权限。",
  },
  {
    q: "已有 workload 但没有 Helm release",
    a: "release-managed-agent.sh 会在 Helm release 缺失时停止，不执行 adoption。先完成独立、明确的 Helm adoption，再按现有 release 路径验收。",
  },
  {
    q: "现有 Agent 不能使用 doops upgrade",
    a: "确认它是标准 Helm-managed Deployment，位于自身 namespace，已有匹配 release、控制器和可由 Job 共享的 /root/.kube 或 /root/.kube/config 挂载。缺少任一条件时回到外部控制器路径。",
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
          <p className="mt-3 text-sm text-muted-foreground">拉取当前 main 构建的最新镜像。不写标签就是 <span className="font-mono text-foreground">latest</span>：</p>
          <Cmd>{"docker pull docker.cnb.cool/l8ai/ai/doops.sh"}</Cmd>
          <p className="text-xs text-muted-foreground">
            控制台镜像是 <span className="font-mono">docker.cnb.cool/l8ai/ai/doops.sh/web:latest</span>。
            需要固定版本时使用完整 commit 标签或 <span className="font-mono">@sha256:&lt;digest&gt;</span>。
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
              有集群 kubeconfig？按下面路径完成首次接入或受控升级。
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
