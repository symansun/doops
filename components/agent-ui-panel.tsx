"use client"

import { AgentWorkspace, type AgentSessionSnapshot } from "@agent-cloud/agent-ui"
import { Plus, RefreshCw, Settings2, Square } from "lucide-react"
import { useCallback, useState, useSyncExternalStore } from "react"
import { deploymentRequest, type DeploymentInput } from "@/lib/deployment"
import { randomSession } from "@/lib/gateway"
import type { DoopsAgentUiRuntime } from "@/lib/agent-ui-runtime"
import type { Target } from "@/lib/client"
import { DeploymentForm } from "./deployment-form"

export function AgentUiPanel({
  runtime,
  target,
  sessionId,
  onConfigureModel,
  onSessionChange,
}: {
  runtime: DoopsAgentUiRuntime
  target: Target
  sessionId: string
  onConfigureModel?: () => void
  onSessionChange?: (id: string) => void
}) {
  const subscribe = useCallback(
    (listener: () => void) => runtime.subscribe(sessionId, listener),
    [runtime, sessionId],
  )
  const getSnapshot = useCallback(
    () => runtime.getSnapshot(sessionId),
    [runtime, sessionId],
  )
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
  const [operation, setOperation] = useState<"ask" | "apply">("ask")

  const startNewSession = useCallback(() => {
    if (snapshot.status === "running") return
    onSessionChange?.(randomSession("console"))
  }, [onSessionChange, snapshot.status])

  return (
    <AgentWorkspace
      className="agent-ui h-full min-h-0"
      clientLabel="doops-console"
      composer={operation === "apply" ? (
        <DoopsDeploymentComposer runtime={runtime} sessionId={sessionId} snapshot={snapshot} target={target} />
      ) : undefined}
      domainPanel={
        <DoopsAgentContextPanel
          onOperationChange={setOperation}
          onSessionChange={onSessionChange}
          operation={operation}
          sessionId={sessionId}
          snapshot={snapshot}
          target={target}
        />
      }
      headerActions={
        <div className="flex shrink-0 items-center gap-1">
          <button
            aria-label="重新检查连接"
            className="flex size-8 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"
            disabled={snapshot.status === "running" || snapshot.connection === "connecting"}
            onClick={() => void runtime.open(sessionId)}
            title="重新检查连接"
            type="button"
          ><RefreshCw aria-hidden="true" className="size-3.5" /></button>
          {onConfigureModel && (
            <button
              aria-label="配置模型"
              className="flex h-8 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              disabled={snapshot.status === "running"}
              onClick={onConfigureModel}
              title="配置该节点的大模型"
              type="button"
            >
              <Settings2 aria-hidden="true" className="size-3.5" />
              <span className="hidden sm:inline">配置模型</span>
            </button>
          )}
          <button
            aria-label="新会话"
            className="flex h-8 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40"
            disabled={snapshot.status === "running" || !onSessionChange}
            onClick={startNewSession}
            title="新会话"
            type="button"
          >
            <Plus aria-hidden="true" className="size-3.5" />
            <span className="hidden sm:inline">新会话</span>
          </button>
        </div>
      }
      defaultView="conversation"
      locale="zh-CN"
      presentation="developer"
      runtime={runtime}
      sessionId={sessionId}
      showFullscreen={false}
    />
  )
}

function DoopsAgentContextPanel({
  onOperationChange,
  onSessionChange,
  operation,
  sessionId,
  snapshot,
  target,
}: {
  onOperationChange: (operation: "ask" | "apply") => void
  onSessionChange?: (id: string) => void
  operation: "ask" | "apply"
  sessionId: string
  snapshot: AgentSessionSnapshot
  target: Target
}) {
  return (
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border bg-card/60 px-4 py-2">
        <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">目标</span>
          <code className="truncate rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]">
            {target.cluster}/{target.instance}
          </code>
          {onSessionChange && (
            <label className="flex items-center gap-1.5">
              session
              <input
                aria-label="操作 session"
                className="w-36 rounded-md border border-input bg-background px-2 py-1 font-mono text-[11px] text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/20 disabled:cursor-not-allowed disabled:opacity-50"
                disabled={snapshot.status === "running"}
                defaultValue={sessionId}
                key={sessionId}
                maxLength={128}
                onBlur={(event) => {
                  if (event.currentTarget.checkValidity()) onSessionChange(event.target.value)
                  else event.currentTarget.reportValidity()
                }}
                onKeyDown={(event) => { if (event.key === "Enter") event.currentTarget.blur() }}
                pattern={String.raw`(?!.*\.\.)[A-Za-z0-9][A-Za-z0-9._\-]{0,127}`}
                required
              />
            </label>
          )}
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">模式</span>
          <div className="flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5" role="group" aria-label="智能体操作模式">
            {(["ask", "apply"] as const).map((value) => (
              <button
                aria-pressed={operation === value}
                className={`rounded px-2 py-1 text-xs transition-colors ${operation === value ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"}`}
                disabled={snapshot.status === "running"}
                key={value}
                onClick={() => onOperationChange(value)}
                type="button"
              >
                {value === "ask" ? "只读诊断" : "部署变更"}
              </button>
            ))}
          </div>
        </div>
      </div>
  )
}

function DoopsDeploymentComposer({ runtime, sessionId, snapshot, target }: {
  runtime: DoopsAgentUiRuntime
  sessionId: string
  snapshot: AgentSessionSnapshot
  target: Target
}) {
  const running = snapshot.status === "running"
  return (
    <div className="max-h-[min(52dvh,36rem)] shrink-0 overflow-y-auto border-t border-border px-4 py-3">
      <div className="mx-auto mb-3 flex max-w-3xl items-center justify-between text-xs font-medium text-muted-foreground">
        <span>发布任务</span>
        {running && <button aria-label="停止智能体" title="停止智能体" type="button" className="flex size-8 items-center justify-center rounded-md border border-destructive/50 text-destructive" onClick={() => void runtime.interrupt(sessionId, crypto.randomUUID())}><Square aria-hidden="true" className="size-3.5" /></button>}
      </div>
      <DeploymentForm
        disabled={!snapshot.capabilities.sendMessage || running}
        onDeploy={async (input: DeploymentInput) => {
          const request = deploymentRequest(target, input)
          await runtime.sendPrompt(sessionId, crypto.randomUUID(), { ...request, operation: "apply" })
        }}
      />
    </div>
  )
}
