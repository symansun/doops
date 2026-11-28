"use client"

import {
  AgentWorkspace,
  ToolRendererRegistry,
  type AgentArtifactItem,
  type AgentSessionRuntime,
  type AgentToolRendererRegistration,
  type CodeWorkspaceSource,
  type ComposerPrefill,
} from "@agent-cloud/agent-ui"
import { History, KeyRound, Plus, RefreshCw, Terminal as TerminalLucide } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react"
import { auditQueryParams, randomSession, targetClusterAvailable } from "@/lib/gateway"
import { isDoopsAgentSessionBusy } from "@/lib/agent-ui-runtime"
import { extractAgentExecutionEntries, type AgentTimelineToolItemLike } from "@/lib/agent-execution-stream"
import {
  shouldLoadWorkspaceArtifacts,
  shouldSuppressWorkspaceError,
  workspaceArtifactsFromChanges,
} from "@/lib/agent-code-workspace"
import { createVisibilityScopedRuntime } from "@/lib/agent-error"
import {
  AGENT_WORKSPACE_ALLOWED_MODES,
  classifyAgentModelError,
  sortTargetCredentialsForAssistant,
} from "@/lib/credential-workflow"
import {
  listCredentials,
  type CredentialResource,
  type CredentialType,
  type CredentialUse,
} from "@/lib/credentials"
import { startPromptGroupsForTarget } from "@/lib/doops-start-prompts"
import {
  Badge,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  StatusBanner,
} from "./ui"
import { fetchAgentSessions, fetchAudit, type AuditEvent, type Session, type Target } from "@/lib/client"
import { AgentExecBar } from "./agent-exec-bar"

// Legacy fallback only: audit pages are 200 rows; read up to five before grouping sessions.
const HISTORY_EVENT_LIMIT = 1000

const CREDENTIAL_TYPE_LABELS: Record<CredentialType, string> = {
  registry: "镜像仓库",
  tls: "TLS 证书",
  opaque: "通用密钥",
  helmRepository: "Helm 仓库",
  gitToken: "Git 令牌",
  modelProvider: "模型服务",
}

const CREDENTIAL_USE_LABELS: Record<CredentialUse, string> = {
  imagePull: "拉取镜像",
  gitCheckout: "读取代码",
  helmPull: "拉取 Helm 包",
  tls: "TLS 证书",
  opaqueSecret: "通用密钥",
  database: "数据库连接",
  modelProvider: "调用模型",
}

interface HistorySession {
  id: string
  title: string
  preview?: string
  latest: string
  count: number
  turnCount?: number
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
  composerPrefill,
  onComposerPrefillApplied,
  splitMode = false,
  terminalOpen = false,
  onToggleTerminal,
  onFocusToolInTerminal,
  onConfigureModel,
  onOpenCredentials,
  reconnectNonce = 0,
  onSessionChange,
  onExecRunningChange,
}: {
  session: Session
  target: Target
  onRunInTerminal?: (command: string, options?: { run?: boolean }) => void
  runtime: AgentSessionRuntime
  sessionId: string
  codeWorkspace: CodeWorkspaceSource
  active: boolean
  composerPrefill?: ComposerPrefill
  onComposerPrefillApplied?: (id: string) => void
  splitMode?: boolean
  terminalOpen?: boolean
  onToggleTerminal?: () => void
  onFocusToolInTerminal?: (toolId: string) => void
  onConfigureModel?: (options?: { focusType?: CredentialType }) => void
  onOpenCredentials?: () => void
  reconnectNonce?: number
  onSessionChange?: (id: string) => void
  onExecRunningChange?: (running: boolean) => void
}) {
  const composerOptions = useMemo(
    () => (composerPrefill ? { prefill: composerPrefill, onPrefillApplied: onComposerPrefillApplied } : undefined),
    [composerPrefill, onComposerPrefillApplied],
  )
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
  const [workspaceReloadTick, setWorkspaceReloadTick] = useState(0)
  const workspaceSourceId = useMemo(() => `workspace:${codeWorkspace.workspaceKey}/${sessionId}`, [codeWorkspace.workspaceKey, sessionId])
  const hasConversation = snapshot.items.length > 0
  const executionEntries = useMemo(
    () => extractAgentExecutionEntries(snapshot.items as readonly AgentTimelineToolItemLike[]),
    [snapshot.items],
  )
  const hasRunningTool = useMemo(
    () => executionEntries.some((entry) => entry.status === "running" || entry.status === "pending"),
    [executionEntries],
  )
  const toolRenderers = useMemo(() => {
    const registry = new ToolRendererRegistry<AgentToolRendererRegistration>()
    registry.register(
      { namespace: "doagent", semanticKey: "shell.execute", schemaVersion: "1" },
      {
        presentation: {
          label: "运行命令",
          icon: TerminalLucide,
          inputLabel: "命令",
          outputLabel: "输出",
        },
        ownsEvidence: () => true,
        detail: ({ item }) => {
          const entry = extractAgentExecutionEntries([item as unknown as AgentTimelineToolItemLike])[0]
          const cmd = entry?.command || ""
          const output = entry?.output || item.output || ""
          return (
            <div className="space-y-2 text-xs">
              {cmd && (
                <div className="flex items-start justify-between gap-2 rounded bg-background/70 px-2.5 py-1.5 font-mono">
                  <span className="min-w-0 flex-1 whitespace-pre-wrap break-all text-foreground">
                    <span className="select-none text-emerald-400">$ </span>
                    {cmd}
                  </span>
                  <div className="flex shrink-0 items-center gap-1 font-sans">
                    {onFocusToolInTerminal && (
                      <button
                        type="button"
                        onClick={() => onFocusToolInTerminal(item.id)}
                        className="whitespace-nowrap rounded px-1.5 py-0.5 text-[11px] text-primary hover:bg-primary/10"
                      >
                        在终端查看
                      </button>
                    )}
                    {onRunInTerminal && (
                      <button
                        type="button"
                        onClick={() => onRunInTerminal(cmd, { run: false })}
                        className="whitespace-nowrap rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
                      >
                        → 终端
                      </button>
                    )}
                  </div>
                </div>
              )}
              {output && (
                <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-all font-mono text-[11.5px] leading-5 text-muted-foreground">
                  {output}
                </pre>
              )}
            </div>
          )
        },
      },
      "doops.shell.execute",
    )
    return registry
  }, [onFocusToolInTerminal, onRunInTerminal])

  useEffect(() => {
    if (!active) return
    if (!hasConversation) {
      setWorkspaceArtifacts([])
      setWorkspaceError("")
      return
    }
    if (!shouldLoadWorkspaceArtifacts({ active, hasConversation, connection: snapshot.connection })) {
      return
    }
    const controller = new AbortController()
    setWorkspaceError("")
    void codeWorkspace.changes(sessionId, controller.signal)
      .then((changes) => {
        if (!controller.signal.aborted) {
          setWorkspaceArtifacts(workspaceArtifactsFromChanges(workspaceSourceId, changes))
          setWorkspaceError("")
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return
        if (shouldSuppressWorkspaceError({ status: snapshot.status, error })) return
        setWorkspaceError(error instanceof Error ? error.message : String(error))
      })
    return () => controller.abort()
  }, [active, codeWorkspace, hasConversation, sessionId, snapshot.connection, snapshot.status, snapshot.latestUserCommandId, workspaceReloadTick, workspaceSourceId])

  const startNewSession = useCallback(() => {
    if (busy) return
    onSessionChange?.(randomSession("console"))
  }, [busy, onSessionChange])

  const reconnectSession = useCallback(() => {
    if (snapshot.connection !== "disconnected") runtime.close(sessionId)
    void runtime.open(sessionId).catch(() => {
      // The shared runtime publishes connection failures in its snapshot.
    })
    setWorkspaceReloadTick((tick) => tick + 1)
  }, [runtime, sessionId, snapshot.connection])

  // 从凭据页激活模型凭据返回 AI 助手时自动触发一次重新检查连接。
  const appliedReconnectNonce = useRef(reconnectNonce)
  useEffect(() => {
    if (!active || reconnectNonce === appliedReconnectNonce.current) return
    appliedReconnectNonce.current = reconnectNonce
    if (!busy) reconnectSession()
  }, [active, busy, reconnectNonce, reconnectSession])

  // 当前目标可用凭据摘要（仅元数据，懒加载）：让用户在 AI 助手内直观查看已就绪的模型与业务凭据。
  const [credLoading, setCredLoading] = useState(false)
  const [credError, setCredError] = useState("")
  const [targetCredentials, setTargetCredentials] = useState<CredentialResource[] | null>(null)

  const loadTargetCredentials = useCallback(async () => {
    setCredLoading(true)
    setCredError("")
    try {
      const list = await listCredentials(session, target, "target")
      setTargetCredentials(sortTargetCredentialsForAssistant(list))
    } catch (err) {
      setCredError((err as Error).message)
    } finally {
      setCredLoading(false)
    }
  }, [session, target])

  const modelError = useMemo(() => classifyAgentModelError(snapshot.error), [snapshot.error])
  const boundModelCredential = useMemo(
    () => targetCredentials?.find((item) => item.type === "modelProvider") ?? null,
    [targetCredentials],
  )

  // 历史会话：优先从目标 Agent 读取持久化的会话列表与摘要；旧版 Agent 自动回退至审计记录
  const [historyLoading, setHistoryLoading] = useState(false)
  const [historyError, setHistoryError] = useState("")
  const [history, setHistory] = useState<HistorySession[] | null>(null)
  const [historyMode, setHistoryMode] = useState<"agent" | "audit_fallback">("agent")
  const [historyTruncated, setHistoryTruncated] = useState(false)

  const loadHistory = useCallback(async () => {
    setHistoryLoading(true)
    setHistoryError("")
    try {
      const agentList = await fetchAgentSessions(session, target)
      if (agentList.supported) {
        setHistoryMode("agent")
        setHistoryTruncated(false)
        setHistory(
          agentList.sessions.map((item) => ({
            id: item.session_id,
            title: item.title || item.session_id,
            preview: item.last_message_preview,
            latest: item.updated_at || item.created_at || "",
            count: item.message_count,
            turnCount: item.turn_count,
          })),
        )
        return
      }

      setHistoryMode("audit_fallback")
      const events: AuditEvent[] = []
      let beforeId: number | undefined
      let truncated = false
      while (events.length < HISTORY_EVENT_LIMIT) {
        const plan = auditQueryParams({
          scope: "self",
          target: { cluster: "", instance: "" },
          sessionId: "",
          credentialId: "",
          action: "ask",
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
        if (event.command_summary && /mode=metadata\b/.test(event.command_summary)) continue
        const ts = event.started_at || ""
        const current = byId.get(sid)
        if (!current) byId.set(sid, { id: sid, title: sid, latest: ts, count: 1 })
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
  }, [session, target])

  return (
    <div className="flex h-full min-h-0 flex-col">
    {modelError.isModelConfigError && (
      <StatusBanner tone="warning" compact className="flex shrink-0 flex-wrap items-center justify-between gap-2 rounded-none border-x-0 border-t-0 px-3 py-2">
        <span className="min-w-0 flex-1 text-xs">
          {modelError.missingModel
            ? "当前目标尚未绑定 AI 模型服务（modelProvider 凭据），绑定并激活后即可开始对话。"
            : "当前目标的 AI 模型连接不可用，请核对该目标绑定的 modelProvider 凭据。"}
        </span>
        <div className="flex shrink-0 items-center gap-1.5">
          {onConfigureModel && (
            <Button
              size="sm"
              variant="primary"
              disabled={busy}
              onClick={() => onConfigureModel({ focusType: "modelProvider" })}
              type="button"
            >
              配置模型凭据
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            disabled={snapshot.connection === "connecting" || snapshot.connection === "reconnecting"}
            onClick={reconnectSession}
            type="button"
          >
            重新检查连接
          </Button>
        </div>
      </StatusBanner>
    )}
    <AgentWorkspace
      className="agent-ui min-h-0 flex-1"
      clientLabel="doops-console"
      headerActions={
        <div className="flex shrink-0 items-center gap-1">
          {workspaceError && (
            <button
              type="button"
              role="alert"
              className="max-w-40 truncate text-xs text-destructive underline-offset-2 hover:underline"
              title={`${workspaceError}（点击重试）`}
              onClick={() => setWorkspaceReloadTick((tick) => tick + 1)}
            >
              成果读取失败
            </button>
          )}
          <Button
            aria-label="重新检查连接"
            variant="ghost"
            size="sm"
            icon
            disabled={snapshot.connection === "connecting" || snapshot.connection === "reconnecting" || (busy && snapshot.connection !== "disconnected")}
            onClick={reconnectSession}
            title="重新检查连接"
            type="button"
          ><RefreshCw aria-hidden="true" className="size-3.5" /></Button>
          <DropdownMenu
            onOpenChange={(open) => {
              if (open) void loadTargetCredentials()
            }}
          >
            <DropdownMenuTrigger asChild>
              <Button
                aria-label="凭据"
                variant="ghost"
                size="sm"
                icon={splitMode}
                disabled={busy}
                title="查看当前目标可用凭据与模型服务配置"
                type="button"
              >
                <KeyRound aria-hidden="true" className="size-3.5" />
                <span className={splitMode ? "sr-only" : "hidden sm:inline"}>凭据</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent className="max-h-96 w-80 overflow-y-auto sm:w-96">
              <DropdownMenuLabel className="px-2 py-1.5 text-xs text-muted-foreground">
                {target.cluster}/{target.instance} 可用凭据（仅元数据）
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              {credLoading && <div className="px-2 py-2 text-xs text-muted-foreground">正在读取目标凭据…</div>}
              {credError && <div className="px-2 py-2 text-xs text-destructive">{credError}</div>}
              {!credLoading && !credError && targetCredentials && (
                <>
                  <div className="flex items-center justify-between gap-2 px-2 py-1.5 text-xs">
                    <span className="text-muted-foreground">AI 模型服务</span>
                    {boundModelCredential ? (
                      <span className="flex min-w-0 items-center gap-1.5">
                        <Badge variant="success">已绑定</Badge>
                        <span className="truncate font-mono text-foreground">{boundModelCredential.name}</span>
                      </span>
                    ) : (
                      <Badge variant="warning">未配置</Badge>
                    )}
                  </div>
                  {targetCredentials.length === 0 ? (
                    <div className="px-2 py-2 text-xs text-muted-foreground">
                      当前目标尚无已激活凭据；任务需要模型、镜像或数据库凭据时请先绑定并激活。
                    </div>
                  ) : (
                    <div className="space-y-1 px-2 py-1">
                      {targetCredentials.map((item) => {
                        const binding = item.bindings?.[0]
                        return (
                          <div key={item.id} className="flex items-center justify-between gap-2 rounded-md bg-muted/40 px-2 py-1.5 text-xs">
                            <div className="min-w-0">
                              <div className="truncate font-medium text-foreground">{item.name}</div>
                              <div className="truncate text-[11px] text-muted-foreground">
                                {CREDENTIAL_TYPE_LABELS[item.type]}
                                {binding?.scope.use ? ` · ${CREDENTIAL_USE_LABELS[binding.scope.use]}` : ""}
                                {binding?.scope.namespace && binding.scope.use !== "modelProvider" ? ` · ns:${binding.scope.namespace}` : ""}
                              </div>
                            </div>
                            <Badge variant={item.type === "modelProvider" ? "primary" : "default"}>
                              {item.scope === "platform" ? "平台" : "个人"}
                            </Badge>
                          </div>
                        )
                      })}
                    </div>
                  )}
                </>
              )}
              {(onConfigureModel || onOpenCredentials) && (
                <>
                  <DropdownMenuSeparator />
                  {onConfigureModel && (
                    <DropdownMenuItem
                      disabled={busy}
                      onSelect={() => onConfigureModel({ focusType: "modelProvider" })}
                    >
                      <KeyRound aria-hidden="true" className="size-3.5" />
                      <span>{boundModelCredential ? "更换或管理模型凭据" : "配置模型凭据（modelProvider）"}</span>
                    </DropdownMenuItem>
                  )}
                  <DropdownMenuItem
                    disabled={busy}
                    onSelect={() => (onConfigureModel ? onConfigureModel({}) : onOpenCredentials?.())}
                  >
                    <span>打开凭据管理</span>
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
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
                  icon={splitMode}
                  disabled={busy}
                  title="历史会话"
                  type="button"
                >
                  <History aria-hidden="true" className="size-3.5" />
                  <span className={splitMode ? "sr-only" : "hidden sm:inline"}>历史会话</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent className="max-h-96 w-80 overflow-y-auto sm:w-96">
                <DropdownMenuLabel className="px-2 py-1.5 text-xs text-muted-foreground">
                  {historyMode === "agent"
                    ? "本 Agent 历史会话（按最近对话排序）"
                    : "本目标的历史会话（旧版 Agent 审计回退）"}
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                {historyLoading && <div className="px-2 py-2 text-xs text-muted-foreground">正在加载…</div>}
                {historyError && <div className="px-2 py-2 text-xs text-destructive">{historyError}</div>}
                {!historyLoading && !historyError && historyMode === "audit_fallback" && (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    当前 Agent 版本较旧，升级后可查看完整历史对话与摘要
                  </div>
                )}
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
                      className="flex flex-col items-stretch gap-0.5 py-2"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="min-w-0 truncate text-xs font-medium text-foreground" title={item.title}>
                          {item.title}
                        </span>
                        <span className="flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                          {item.id === sessionId && <Badge variant="primary">当前</Badge>}
                          <span>{relativeTime(item.latest)}</span>
                        </span>
                      </div>
                      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                        <span className="min-w-0 truncate font-mono">{item.id}</span>
                        <span className="shrink-0">
                          {item.turnCount && item.turnCount > 0 ? `${item.turnCount} 轮` : `${item.count} 条`}
                        </span>
                      </div>
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
            icon={splitMode}
            disabled={busy || !onSessionChange}
            onClick={startNewSession}
            title="新会话"
            type="button"
          >
            <Plus aria-hidden="true" className="size-3.5" />
            <span className={splitMode ? "sr-only" : "hidden sm:inline"}>新会话</span>
          </Button>
          {!splitMode && onToggleTerminal && (
            <Button
              aria-label="打开执行终端"
              aria-pressed={terminalOpen}
              data-testid="ask-terminal-toggle"
              variant={terminalOpen ? "primary" : "ghost"}
              size="sm"
              onClick={onToggleTerminal}
              title={terminalOpen ? "收起右侧执行过程与交互终端" : "打开右侧执行过程与交互终端（人机协作）"}
              type="button"
            >
              <TerminalLucide aria-hidden="true" className="size-3.5" />
              <span>终端{executionEntries.length > 0 ? ` · ${executionEntries.length}` : ""}</span>
              {hasRunningTool && <span aria-label="命令执行中" className="size-1.5 rounded-full bg-accent animate-pulse" />}
            </Button>
          )}
        </div>
      }
      allowedModes={[...AGENT_WORKSPACE_ALLOWED_MODES]}
      defaultMode="work"
      startPromptGroups={startPromptGroupsForTarget(targetClusterAvailable(target))}
      locale="zh-CN"
      runtime={runtime}
      sessionId={sessionId}
      workspaceArtifacts={terminalOpen ? undefined : workspaceArtifacts}
      toolRenderers={toolRenderers}
      composer={composerOptions}
      showFullscreen={false}
    />
    <AgentExecBar
      session={session}
      target={target}
      sessionId={sessionId}
      items={snapshot.items}
      splitMode={splitMode || terminalOpen}
      onRunInTerminal={onRunInTerminal}
      onRunningChange={setExecRunning}
    />
    </div>
  )
}
