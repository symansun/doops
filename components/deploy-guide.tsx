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
    title: "准备工作",
    body: (
      <ul className="ml-4 list-disc space-y-1 text-sm text-muted-foreground">
        <li>一台 Linux 服务器（支持 amd64 / arm64），可联网访问 gateway 地址</li>
        <li>具备 root 或 sudo 权限</li>
        <li>
          向管理员确认 <span className="font-medium text-foreground">gateway 地址</span>、{" "}
          <span className="font-medium text-foreground">cluster</span> 与{" "}
          <span className="font-medium text-foreground">instance</span>
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
    icon: TerminalIcon,
    title: "部署 Agent",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>使用外部 CI/GitOps 发布固定 digest 的 Agent 镜像。容器模式可按下列参数启动：</p>
        <Cmd>
          {
            "docker run -d --name doops-agent \\\n  --privileged --pid=host --network=host \\\n  --restart=unless-stopped \\\n  docker.cnb.cool/l8ai/ai/doops.sh@sha256:<AGENT_DIGEST> \\\n  -gateway-url https://gateway.example.com \\\n  -cluster <CLUSTER> \\\n  -instance <INSTANCE>"
          }
        </Cmd>
      </div>
    ),
  },
  {
    n: 4,
    icon: RocketIcon,
    title: "检查运行状态",
    body: (
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>确认容器运行、健康检查成功，且日志中没有注册或心跳错误：</p>
        <Cmd>{"docker ps --filter name=doops-agent\ndocker logs --tail=100 doops-agent\ncurl -fsS http://127.0.0.1:42222/health/ready"}</Cmd>
      </div>
    ),
  },
  {
    n: 5,
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

const MANUAL = [
  { label: "拉取固定镜像", cmd: "docker pull docker.cnb.cool/l8ai/ai/doops.sh@sha256:<AGENT_DIGEST>" },
  { label: "启动并声明身份", cmd: "docker run -d --name doops-agent --privileged --pid=host --network=host --restart=unless-stopped docker.cnb.cool/l8ai/ai/doops.sh@sha256:<AGENT_DIGEST> -gateway-url https://gateway.example.com -cluster <CLUSTER> -instance <INSTANCE>" },
  { label: "验证反向注册", cmd: "docker logs --tail=100 doops-agent && curl -fsS http://127.0.0.1:42222/health/ready" },
]

const TROUBLE = [
  {
    q: "机器没有出现在控制台",
    a: "确认 agent 服务在运行（systemctl status doops-agent），并检查服务器能否访问 gateway 地址（curl -I <gateway>）。确认无误后回到控制台点击刷新。",
  },
  {
    q: "连接成功但机器仍未上线",
    a: "检查 cluster/instance 是否与管理员分配的一致，并确认该标识没有被另一台机器占用。继续查看 Agent 日志中的初始化、注册确认和心跳错误。",
  },
  {
    q: "连接被拒绝 / 网络超时",
    a: "通常是防火墙或安全组拦截。请放通服务器到 gateway 的出站连接（HTTPS 443），云服务器需在安全组中配置。",
  },
  {
    q: "查看 agent 日志",
    a: "执行 journalctl -u doops-agent -n 100 --no-pager 查看最近日志，定位连接或认证问题。",
  },
]

// 正文：公开文档页与控制台内「部署 Agent」视图共用
// onEnterConsole 提供时（控制台内嵌），底部 CTA 变为「刷新机器列表」回调；否则链接到 /console
export function DeployGuideBody({ onEnterConsole }: { onEnterConsole?: () => void }) {
  return (
    <div className="mx-auto max-w-3xl px-5 py-10">
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
        </div>

        {/* 没有实例时的两条路径 */}
        <div className="mb-10 grid gap-3 sm:grid-cols-2">
          <div className="rounded-xl border bg-card p-4">
            <div className="mb-1.5 flex items-center gap-2 text-sm font-medium">
              <RocketIcon width={16} height={16} className="text-primary" />
              自己部署
            </div>
            <p className="text-xs leading-relaxed text-muted-foreground">
              有服务器的 root/sudo 权限？按下面步骤几分钟即可接入一台机器。
            </p>
          </div>
          <div className="rounded-xl border bg-card p-4">
            <div className="mb-1.5 flex items-center gap-2 text-sm font-medium">
              <HelpIcon width={16} height={16} className="text-primary" />
              联系管理员
            </div>
            <p className="text-xs leading-relaxed text-muted-foreground">
              没有服务器权限或不确定 gateway 地址与接入标识？联系团队管理员完成接入。
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

        {/* 手动安装 */}
        <section className="mt-12">
          <h2 className="mb-1 text-lg font-semibold">手动安装（可选）</h2>
          <p className="mb-3 text-sm text-muted-foreground">
            若无法使用一键脚本（如离线环境），可手动安装：
          </p>
          <ol className="space-y-3">
            {MANUAL.map((m, i) => (
              <li key={i}>
                <div className="mb-1 text-sm font-medium text-foreground">
                  {i + 1}. {m.label}
                </div>
                <Cmd>{m.cmd}</Cmd>
              </li>
            ))}
          </ol>
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
      <header className="sticky top-0 z-10 border-b bg-background/80 backdrop-blur">
        <div className="mx-auto flex max-w-3xl items-center justify-between px-5 py-3">
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
      <DeployGuideBody />
    </main>
  )
}
