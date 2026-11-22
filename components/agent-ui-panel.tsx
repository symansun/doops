"use client"

import { AgentWorkspace, type AgentArtifactItem, type AgentSessionRuntime, type CodeWorkspaceSource } from "@agent-cloud/agent-ui"
import { History, KeyRound, Plus, RefreshCw } from "lucide-react"
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { auditQueryParams, randomSession } from "@/lib/gateway"
import { isDoopsAgentSessionBusy } from "@/lib/agent-ui-runtime"
import { workspaceArtifactsFromChanges } from "@/lib/agent-code-workspace"
import { createVisibilityScopedRuntime } from "@/lib/agent-error"
import { doopsStartPromptGroups } from "@/lib/doops-start-prompts"
import {
  Badge,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui"
import { fetchAudit, type AuditEvent, type Session, type Target } from "@/lib/client"
import { AgentExecBar } from "./agent-exec-bar"

// Audit pages are 200 rows; read up to five before grouping sessions.
const HISTORY_EVENT_LIMIT = 1000

interface HistorySession {
  id: string
  latest: string
  count: number
}

function relativeTime(iso: string): string {
  const time = Date.parse(iso)
  if (Number.isNaN(time)) return ""
  const minutes = Math.floor((Date.now() - time) / 60000)
  if (minutes < 1) return "刚刚"
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}

export function AgentUiPanel({
  session,
  target,
  onRunInTerminal,
  runtime: sourceRuntime,
  sessionId,
  codeWorkspace,
  active,
  onConfigureModel,
  onSessionChange,
  onExecRunningChange,
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
  onExecRunningChange?: (running: boolean) => void
}) {
  // 面板在其他标签下保持挂载（hidden），此时不渲染智能体连接错误，避免出现在凭据等页面里。
  // 初始可见性只在创建时读取，之后由 effect 同步。
  const scoped = useMemo(() => createVisibilityScopedRuntime(sourceRuntime, active), [sourceRuntime])
  useEffect(() => scoped.setVisible(active), [scoped, active])
  const runtime = scoped.runtime
  const subscribe = useCallback(
    (listener: () => void) => runtime.subscribe(sessionId, listener),
    [runtime, sessionId],
  )
  const getSnapshot = useCallback(
    () => runtime.getSnapshot(sessionId),
    [runtime, sessionId],
  )
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
  // 执行栏里的命令还在跑时也不能换会话：面板会重新挂载，正在执行的命令输出会丢失。
  const [execRunning, setExecRunning] = useState(false)
  const busy = isDoopsAgentSessionBusy(snapshot) || execRunning
  // 同时告诉外层：执行栏有命令在跑时不能切换目标。卸载时归零，避免外层一直被锁住。
  useEffect(() => {
    onExecRunningChange?.(execRunning)
    return () => onExecRunningChange?.(false)
  }, [execRunning, onExecRunningChange])
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

  // 历史会话：从本人审计记录里聚出本目标的 console- 会话，懒加载
  const [historyLoading, setHistoryLoading] = useState(false)
  const [historyError, setHistoryError] = useState("")
  const [history, setHistory] = useState<HistorySession[] | null>(null)
  const [historyTruncated, setHistoryTruncated] = useState(false)

  const loadHistory = useCallback(async () => {
    setHistoryLoading(true)
    setHistoryError("")
    try {
      const events: AuditEvent[] = []
      let beforeId: number | undefined
      let truncated = false
      while (events.length < HISTORY_EVENT_LIMIT) {
        const plan = auditQueryParams({
          scope: "self",
          target: { cluster: "", instance: "" },
          sessionId: "",
          credentialId: "",
          action: "",
          status: "",
          limit: 200,
          beforeId,
        })
        if (!plan.ok) throw new Error(plan.message)
        plan.params.cluster = target.cluster
        plan.params.instance = target.instance
        const res = await fetchAudit(session, plan.params)
        if (res.error) throw new Error(res.error)
        const page = res.events || []
        events.push(...page)
        if (page.length < 200) break
        truncated = events.length >= HISTORY_EVENT_LIMIT
        beforeId = page[page.length - 1].id
      }
      setHistoryTruncated(truncated)
      const byId = new Map<string, HistorySession>()
      for (const event of events) {
        const sid = event.session || ""
        if (!sid.startsWith("console-")) continue
        if (event.cluster !== target.cluster || event.instance !== target.instance) continue
        const ts = event.started_at || ""
        const current = byId.get(sid)
        if (!current) byId.set(sid, { id: sid, latest: ts, count: 1 })
        else {
          current.count += 1
          if (ts > current.latest) current.latest = ts
        }
      }
      setHistory(
        Array.from(byId.values())
          .sort((a, b) => b.latest.localeCompare(a.latest))
          .slice(0, 20),
      )
    } catch (err) {
      setHistoryError((err as Error).message)
    } finally {
      setHistoryLoading(false)
    }
  }, [session, target.cluster, target.instance])

  return (
    <div className="flex h-full min-h-0 flex-col">
    <AgentWorkspace
      className="agent-ui min-h-0 flex-1"
      clientLabel="doops-console"
      headerActions={
        <div className="flex shrink-0 items-center gap-1">
          {workspaceError && <span role="alert" className="max-w-40 truncate text-xs text-destructive" title={workspaceError}>成果读取失败</span>}
          <Button
            aria-label="重新检查连接"
            variant="ghost"
            size="sm"
            icon
            disabled={snapshot.connection === "connecting" || snapshot.connection === "reconnecting" || (busy && snapshot.connection !== "disconnected")}
            onClick={() => {
              if (snapshot.connection !== "disconnected") runtime.close(sessionId)
              void runtime.open(sessionId).catch(() => {
                // The shared runtime publishes connection failures in its snapshot.
              })
            }}
            title="重新检查连接"
            type="button"
          ><RefreshCw aria-hidden="true" className="size-3.5" /></Button>
          {onConfigureModel && (
            <Button
              aria-label="打开凭据"
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={onConfigureModel}
              title="DoAgent 模型来自该目标绑定的 modelProvider 凭据"
              type="button"
            >
              <KeyRound aria-hidden="true" className="size-3.5" />
              <span className="hidden sm:inline">打开凭据</span>
            </Button>
          )}
          {onSessionChange && (
            <DropdownMenu
              onOpenChange={(open) => {
                if (open) void loadHistory()
              }}
            >
              <DropdownMenuTrigger asChild>
                <Button
                  aria-label="历史会话"
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  title="历史会话"
                  type="button"
                >
                  <History aria-hidden="true" className="size-3.5" />
                  <span className="hidden sm:inline">历史会话</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent className="w-72">
                <DropdownMenuLabel className="px-2 py-1.5 text-xs text-muted-foreground">
                  本目标的 console- 会话（来自我的审计记录）
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                {historyLoading && <div className="px-2 py-2 text-xs text-muted-foreground">正在加载…</div>}
                {historyError && <div className="px-2 py-2 text-xs text-destructive">{historyError}</div>}
                {!historyLoading && !historyError && history && history.length === 0 && (
                  <div className="px-2 py-2 text-xs text-muted-foreground">暂无历史会话</div>
                )}
                {!historyLoading &&
                  !historyError &&
                  history?.map((item) => (
                    <DropdownMenuItem
                      key={item.id}
                      disabled={busy || item.id === sessionId}
                      onSelect={() => onSessionChange(item.id)}
                      className="flex items-center justify-between gap-2"
                    >
                      <span className="min-w-0 truncate font-mono text-xs">{item.id}</span>
                      <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                        {item.id === sessionId && <Badge variant="primary">当前</Badge>}
                        <span>{item.count} 条</span>
                        <span>{relativeTime(item.latest)}</span>
                      </span>
                    </DropdownMenuItem>
                  ))}
                {!historyLoading && !historyError && historyTruncated && (
                  <div className="px-2 py-2 text-xs text-muted-foreground">只统计最近 {HISTORY_EVENT_LIMIT} 条审计记录，更早的会话请到审计记录查看</div>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <Button
            aria-label="新会话"
            variant="ghost"
            size="sm"
            disabled={busy || !onSessionChange}
            onClick={startNewSession}
            title="新会话"
            type="button"
          >
            <Plus aria-hidden="true" className="size-3.5" />
            <span className="hidden sm:inline">新会话</span>
          </Button>
        </div>
      }
      defaultMode="work"
      startPromptGroups={doopsStartPromptGroups}
      locale="zh-CN"
      presentation="developer"
      runtime={runtime}
      sessionId={sessionId}
      codeWorkspace={codeWorkspace}
      workspaceArtifacts={workspaceArtifacts}
      showFullscreen={false}
    />
    <AgentExecBar
      session={session}
      target={target}
      sessionId={sessionId}
      items={snapshot.items}
      onRunInTerminal={onRunInTerminal}
      onRunningChange={setExecRunning}
    />
    </div>
  )
}
