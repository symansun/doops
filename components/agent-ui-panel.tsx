"use client"

import { AgentWorkspace, type AgentArtifactItem, type AgentSessionRuntime, type AgentSessionSnapshot, type CodeWorkspaceSource } from "@agent-cloud/agent-ui"
import { Plus, RefreshCw, Settings2 } from "lucide-react"
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { randomSession } from "@/lib/gateway"
import { isDoopsAgentSessionBusy } from "@/lib/agent-ui-runtime"
import { workspaceArtifactsFromChanges } from "@/lib/agent-code-workspace"

export function AgentUiPanel({
  runtime,
  sessionId,
  codeWorkspace,
  onConfigureModel,
  onSessionChange,
}: {
  runtime: AgentSessionRuntime
  sessionId: string
  codeWorkspace: CodeWorkspaceSource
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
  const workspaceSourceId = useMemo(() => `workspace:${codeWorkspace.workspaceKey}/${sessionId}`, [codeWorkspace.workspaceKey, sessionId])

  useEffect(() => {
    const controller = new AbortController()
    void codeWorkspace.changes(sessionId, controller.signal)
      .then((changes) => setWorkspaceArtifacts(workspaceArtifactsFromChanges(workspaceSourceId, changes)))
      .catch(() => {
        if (!controller.signal.aborted) setWorkspaceArtifacts([])
      })
    return () => controller.abort()
  }, [codeWorkspace, sessionId, snapshot.status, snapshot.latestUserCommandId, workspaceSourceId])

  const startNewSession = useCallback(() => {
    if (busy) return
    onSessionChange?.(randomSession("console"))
  }, [busy, onSessionChange])

  return (
    <AgentWorkspace
      className="agent-ui h-full min-h-0"
      clientLabel="doops-console"
      headerActions={
        <div className="flex shrink-0 items-center gap-1">
          <button
            aria-label="重新检查连接"
            className="flex size-8 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"
            disabled={busy || snapshot.connection === "connecting"}
            onClick={() => {
              runtime.close(sessionId)
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
  )
}
