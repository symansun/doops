"use client"

import { AgentWorkspace, type AgentSessionSnapshot } from "@agent-cloud/agent-ui"
import { ArrowUpCircle, CircleAlert, KeyRound, Loader2, Plus, RefreshCw, Square } from "lucide-react"
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import type { Session, Target } from "@/lib/client"
import { randomSession } from "@/lib/gateway"
import { DEPLOYMENT_EXTENSION, deploymentAvailability, deploymentExtensionCommand, type DeploymentAvailability, type DeploymentInput, type DeploymentNotice } from "@/lib/deployment"
import { createDoopsAgentUiRuntime, isDoopsAgentSessionBusy } from "@/lib/agent-ui-runtime"
import { createDoopsCodeWorkspace } from "@/lib/agent-code-workspace"
import { DeploymentForm } from "./deployment-form"

// Business workload release: the same confirmed `doops.operations/apply` command as `doops ask --apply`,
// sent through the hosted agent session so the native timeline shows the trace and the Agent's live evidence.
export function WorkloadDeployPanel({ session, target, onOpenCredentials, onOpenUpgrade }: {
  session: Session
  target: Target
  // Passed only for platform admins; it decides what is shown, the Gateway still checks every operation.
  onOpenCredentials?: () => void
  onOpenUpgrade?: () => void
}) {
  // Keep the release session across view switches so a running apply stays visible after returning.
  const storageKey = `doops.deploy-session.${target.key}`
  const [sessionId, setSessionId] = useState(() => {
    const saved = typeof window === "undefined" ? null : sessionStorage.getItem(storageKey)
    return saved && /^deploy-[a-z0-9]{1,16}$/.test(saved) ? saved : randomSession("deploy")
  })
  useEffect(() => { sessionStorage.setItem(storageKey, sessionId) }, [storageKey, sessionId])
  const codeWorkspace = useMemo(() => createDoopsCodeWorkspace({ session, target, sessionId }), [session, target.cluster, target.instance, sessionId])
  const runtime = useMemo(
    () => createDoopsAgentUiRuntime({ session, target, sessionId, codeWorkspace }),
    [session, target.cluster, target.instance, sessionId, codeWorkspace],
  )
  const subscribe = useCallback((listener: () => void) => runtime.subscribe(sessionId, listener), [runtime, sessionId])
  const getSnapshot = useCallback(() => runtime.getSnapshot(sessionId), [runtime, sessionId])
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
  const busy = isDoopsAgentSessionBusy(snapshot)
  const supportsApply = !!runtime.executeExtension && snapshot.capabilities.extensions.some(
    (capability) => capability.namespace === DEPLOYMENT_EXTENSION.namespace &&
      capability.semanticType === DEPLOYMENT_EXTENSION.semanticType && capability.schemaVersion === DEPLOYMENT_EXTENSION.schemaVersion,
  )
  const availability = deploymentAvailability({ connection: snapshot.connection, error: snapshot.error, supportsApply, running: busy })
  const reconnect = useCallback(() => {
    if (snapshot.connection !== "disconnected") runtime.close(sessionId)
    void runtime.open(sessionId).catch(() => {
      // The runtime publishes connection failures in its snapshot.
    })
  }, [runtime, sessionId, snapshot.connection])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b bg-card px-5 py-3">
        <div className="min-w-0">
          <h2 className="text-base font-semibold">发布业务工作负载</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            等同 <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">doops ask --target {target.cluster}/{target.instance} --apply</code>，由目标 Agent 执行 Helm 变更并返回实时证据。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <ConnectionBadge snapshot={snapshot} availability={availability} />
          <button
            type="button"
            disabled={busy}
            onClick={() => setSessionId(randomSession("deploy"))}
            title={`当前会话 ${sessionId}`}
            className="flex h-8 items-center gap-1.5 rounded-md border px-3 text-xs transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
          ><Plus aria-hidden="true" className="size-3.5" />新发布</button>
        </div>
      </header>

      {/* Narrow screens scroll the whole page with the form at full height; wide screens scroll each column. */}
      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-y-auto lg:grid-cols-[minmax(0,30rem)_minmax(0,1fr)] lg:grid-rows-[minmax(0,1fr)] lg:overflow-hidden">
        <div className="border-b bg-muted/20 p-4 lg:min-h-0 lg:overflow-y-auto lg:border-b-0 lg:border-r">
          {!availability.canSubmit && availability.notice && (
            <AvailabilityNotice notice={availability.notice} onReconnect={reconnect} onOpenCredentials={onOpenCredentials} onOpenUpgrade={onOpenUpgrade} />
          )}
          <DeploymentForm
            key={sessionId}
            target={target}
            canSubmit={availability.canSubmit}
            blockedReason={availability.canSubmit ? undefined : availability.blockedReason}
            onDeploy={async (input: DeploymentInput, confirmed: boolean) => {
              // The form already gates on canSubmit; this keeps every submission path behind the same check.
              if (!availability.canSubmit) throw new Error(availability.blockedReason)
              if (!runtime.executeExtension) throw new Error("当前智能体不支持部署变更")
              await runtime.executeExtension(sessionId, crypto.randomUUID(), deploymentExtensionCommand(target, input, confirmed))
            }}
          />
        </div>
        <div className="flex h-[36rem] min-w-0 flex-col lg:h-auto lg:min-h-0">
          <div className="flex shrink-0 items-center justify-between border-b px-4 py-2">
            <div className="min-w-0">
              <h3 className="text-sm font-medium">执行过程</h3>
              <p className="truncate text-[11px] text-muted-foreground">rollout 与最终 Deployment 状态以 Agent 返回为准；工具失败、连接中断或缺少终态都不算成功</p>
            </div>
            {busy && snapshot.capabilities.interrupt && (
              <button type="button" disabled={snapshot.connection !== "connected"} onClick={() => void runtime.interrupt(sessionId, crypto.randomUUID())}
                className="flex h-7 shrink-0 items-center gap-1.5 rounded-md border border-destructive/50 px-2.5 text-xs text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-40">
                <Square aria-hidden="true" className="size-3" />停止
              </button>
            )}
          </div>
          <div className="min-h-0 flex-1">
            <AgentWorkspace
              key={`deploy-${target.key}-${sessionId}`}
              className="agent-ui h-full min-h-0"
              clientLabel="doops-console"
              defaultView="conversation"
              locale="zh-CN"
              presentation="developer"
              runtime={runtime}
              sessionId={sessionId}
              codeWorkspace={codeWorkspace}
              defaultWorkbenchMode="work"
              showFullscreen={false}
              composer={<RunStatus snapshot={snapshot} />}
            />
          </div>
        </div>
      </div>
    </div>
  )
}

function AvailabilityNotice({ notice, onReconnect, onOpenCredentials, onOpenUpgrade }: {
  notice: DeploymentNotice
  onReconnect: () => void
  onOpenCredentials?: () => void
  onOpenUpgrade?: () => void
}) {
  const connecting = notice.kind === "connecting"
  const buttons = notice.actions.flatMap((action): { action: string; label: string; Icon: typeof RefreshCw; onClick: () => void }[] => {
    if (action === "reconnect") return [{ action, label: "重新连接", Icon: RefreshCw, onClick: onReconnect }]
    if (action === "open_credentials" && onOpenCredentials) return [{ action, label: "去凭据页配置模型", Icon: KeyRound, onClick: onOpenCredentials }]
    if (action === "upgrade_agent" && onOpenUpgrade) return [{ action, label: "去升级 Agent", Icon: ArrowUpCircle, onClick: onOpenUpgrade }]
    return []
  })
  return (
    <div role={connecting ? "status" : "alert"} data-notice={notice.kind}
      className={`mb-4 rounded-lg border p-3 ${connecting ? "bg-card" : "border-amber-500/40 bg-amber-500/10"}`}>
      <div className="flex items-start gap-2">
        {connecting
          ? <Loader2 aria-hidden="true" className="mt-0.5 size-4 shrink-0 animate-spin text-muted-foreground" />
          : <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />}
        <div className="min-w-0">
          <p className="text-sm font-medium">{notice.title}</p>
          <p className="mt-1 break-words text-xs text-muted-foreground">{notice.detail}</p>
          {buttons.length > 0 && (
            <div className="mt-2.5 flex flex-wrap gap-2">
              {buttons.map(({ action, label, Icon, onClick }) => (
                <button key={action} type="button" onClick={onClick}
                  className="flex h-7 items-center gap-1.5 rounded-md border bg-background px-2.5 text-xs transition-colors hover:bg-muted">
                  <Icon aria-hidden="true" className="size-3.5" />{label}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function ConnectionBadge({ snapshot, availability }: { snapshot: AgentSessionSnapshot; availability: DeploymentAvailability }) {
  const connecting = snapshot.connection === "connecting" || snapshot.connection === "reconnecting"
  const [tone, label] = availability.canSubmit
    ? ["bg-emerald-500", "Agent 已就绪"]
    : snapshot.connection === "connected" ? ["bg-amber-500", isDoopsAgentSessionBusy(snapshot) ? "发布执行中" : "不能发布"]
      : connecting ? ["bg-amber-500 animate-pulse", "正在连接 Agent"] : ["bg-destructive", "Agent 不可用"]
  return (
    <span className="flex h-8 max-w-64 items-center gap-1.5 rounded-md border px-2.5 text-xs text-muted-foreground" title={availability.canSubmit ? undefined : availability.blockedReason}>
      <span aria-hidden="true" className={`size-1.5 shrink-0 rounded-full ${tone}`} />
      <span className="truncate">{label}</span>
    </span>
  )
}

// The workspace composer slot only reports run state; releases are submitted from the form.
function RunStatus({ snapshot }: { snapshot: AgentSessionSnapshot }) {
  const text = isDoopsAgentSessionBusy(snapshot) ? "发布执行中…" : snapshot.status === "failed" ? "上次发布失败，详情见上方执行过程" : snapshot.status === "completed" ? "上次发布已结束，请按验收条件核对 Agent 返回的证据" : "填写并确认发布信息后，执行过程显示在这里"
  return <div role="status" className="shrink-0 border-t px-4 py-2.5 text-xs text-muted-foreground">{text}</div>
}
