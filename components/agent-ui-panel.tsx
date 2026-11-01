"use client"

import { AgentWorkspace, type AgentArtifactItem, type AgentSessionRuntime, type CodeWorkspaceSource } from "@agent-cloud/agent-ui"
import { Plus, RefreshCw, Settings2 } from "lucide-react"
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { randomSession } from "@/lib/gateway"
import { isDoopsAgentSessionBusy } from "@/lib/agent-ui-runtime"
import { workspaceArtifactsFromChanges } from "@/lib/agent-code-workspace"
import type { Session, Target } from "@/lib/client"
import { AgentExecBar } from "./agent-exec-bar"

export function AgentUiPanel({
  session,
  target,
  onRunInTerminal,
  runtime,
  sessionId,
  codeWorkspace,
  active,
  onConfigureModel,
  onSessionChange,
}: {
  session: Session
  target: Target
  onRunInTerminal?: (command: string) => void
  runtime: AgentSessionRuntime
  sessionId: string
  codeWorkspace: CodeWorkspaceSource
  active: boolean
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
  const busy = isDoopsAgentSessionBusy(snapshot)
  const [workspaceArtifacts, setWorkspaceArtifacts] = useState<AgentArtifactItem[]>([])
  const [workspaceError, setWorkspaceError] = useState("")
  const workspaceSourceId = useMemo(() => `workspace:${codeWorkspace.workspaceKey}/${sessionId}`, [codeWorkspace.workspaceKey, sessionId])

  useEffect(() => {
    if (!active) return
    const controller = new AbortController()
    setWorkspaceError("")
    void codeWorkspace.changes(sessionId, controller.signal)
      .then((changes) => {
        if (!controller.signal.aborted) setWorkspaceArtifacts(workspaceArtifactsFromChanges(workspaceSourceId, changes))
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setWorkspaceArtifacts([])
          setWorkspaceError(error instanceof Error ? error.message : String(error))
        }
      })
    return () => controller.abort()
  }, [active, codeWorkspace, sessionId, snapshot.status, snapshot.latestUserCommandId, workspaceSourceId])

  const startNewSession = useCallback(() => {
    if (busy) return
    onSessionChange?.(randomSession("console"))
  }, [busy, onSessionChange])

  return (
    <div className="flex h-full min-h-0 flex-col">
    <AgentWorkspace
      className="agent-ui min-h-0 flex-1"
      clientLabel="doops-console"
      headerActions={
        <div className="flex shrink-0 items-center gap-1">
          {workspaceError && <span role="alert" className="max-w-40 truncate text-xs text-destructive" title={workspaceError}>成果读取失败</span>}
          <button
            aria-label="重新检查连接"
            className="flex size-8 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"
            disabled={snapshot.connection === "connecting" || snapshot.connection === "reconnecting" || (busy && snapshot.connection !== "disconnected")}
            onClick={() => {
              if (snapshot.connection !== "disconnected") runtime.close(sessionId)
              void runtime.open(sessionId).catch(() => {
                // The shared runtime publishes connection failures in its snapshot.
              })
            }}
            title="重新检查连接"
            type="button"
          ><RefreshCw aria-hidden="true" className="size-3.5" /></button>
          {onConfigureModel && (
            <button
              aria-label="配置模型"
              className="flex h-8 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              disabled={busy}
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
            disabled={busy || !onSessionChange}
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
      codeWorkspace={codeWorkspace}
      defaultWorkbenchMode="work"
      workspaceArtifacts={workspaceArtifacts}
      showFullscreen={false}
    />
    <AgentExecBar
      session={session}
      target={target}
      sessionId={sessionId}
      items={snapshot.items}
      onRunInTerminal={onRunInTerminal}
    />
    </div>
  )
}
