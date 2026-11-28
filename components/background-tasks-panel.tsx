"use client"

import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react"
import {
  Check,
  ChevronDown,
  ChevronRight,
  Clock,
  Code2,
  Copy,
  Download,
  FolderOpen,
  Play,
  Plus,
  RotateCcw,
  Search,
  Sparkles,
  Square,
  WrapText,
  X,
} from "lucide-react"
import type { Session, Target } from "@/lib/client"
import {
  describeBackgroundTaskStatus,
  filterBackgroundTasks,
  formatTaskDuration,
  isBackgroundTaskTerminal,
  summarizeBackgroundTasks,
  type TaskStatusFilter,
  type TaskStatusSummary,
} from "@/lib/automation"
import {
  buildExplainTaskPrompt,
  composeBackgroundTaskCommand,
  parseAutomationCommand,
  pickHighlightEnvVars,
  summarizeTaskOutput,
} from "@/lib/automation-command"
import {
  BackgroundTaskError,
  getBackgroundTask,
  listBackgroundTasks,
  stopBackgroundTask,
  submitBackgroundTask,
  type BackgroundTaskView,
} from "@/lib/background-tasks"
import { copyTerminalText } from "./terminal-context-menu"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Inline,
  Input,
  Stack,
  StatusBanner,
  Textarea,
} from "./ui"
import { HistoryIcon, RefreshIcon } from "./icons"

const FILTER_OPTIONS: { id: TaskStatusFilter; label: string }[] = [
  { id: "all", label: "全部" },
  { id: "running", label: "运行中" },
  { id: "success", label: "成功" },
  { id: "failed", label: "失败" },
  { id: "canceled", label: "已停止" },
  { id: "unknown", label: "结果未知" },
]

function filterCount(summary: TaskStatusSummary, id: TaskStatusFilter): number {
  switch (id) {
    case "all":
      return summary.total
    case "running":
      return summary.running
    case "success":
      return summary.success
    case "failed":
      return summary.failed
    case "canceled":
      return summary.canceled
    case "unknown":
      return summary.unknown
  }
}

function downloadTextFile(filename: string, content: string) {
  const blob = new Blob([content], { type: "text/plain;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

export function BackgroundTasksPanel({
  session,
  target,
  sessionId = "console-automation",
  embedded = false,
  onSummaryChange,
  onExplainTask,
  onOpenSchedules,
}: {
  session: Session
  target: Target
  sessionId?: string
  embedded?: boolean
  onSummaryChange?: (summary: TaskStatusSummary) => void
  onExplainTask?: (prompt: string) => void
  onOpenSchedules?: () => void
}) {
  const [tasks, setTasks] = useState<BackgroundTaskView[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [bgSupported, setBgSupported] = useState(true)

  // Structured task composer (collapsible to keep the task list high-signal)
  const [composerOpen, setComposerOpen] = useState(false)
  const [taskTitle, setTaskTitle] = useState("")
  const [taskWorkdir, setTaskWorkdir] = useState("")
  const [command, setCommand] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [submitNotice, setSubmitNotice] = useState("")
  const [submitError, setSubmitError] = useState("")
  const composerRef = useRef<HTMLTextAreaElement>(null)

  // Filters, search & display mode
  const [statusFilter, setStatusFilter] = useState<TaskStatusFilter>("all")
  const [query, setQuery] = useState("")
  const [showRawInList, setShowRawInList] = useState(false)

  // Detail & live log viewer
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [detail, setDetail] = useState<BackgroundTaskView | null>(null)
  const [detailError, setDetailError] = useState("")
  const [showRawInDetail, setShowRawInDetail] = useState(false)
  const [stoppingId, setStoppingId] = useState<number | null>(null)
  const [stopNote, setStopNote] = useState("")
  const [copiedKey, setCopiedKey] = useState<"" | "command" | "output">("")
  const [followOutput, setFollowOutput] = useState(true)
  const [wrapOutput, setWrapOutput] = useState(true)
  const [nowMs, setNowMs] = useState(() => Date.now())

  const selectedRef = useRef<number | null>(null)
  selectedRef.current = selectedId
  const outputRef = useRef<HTMLPreElement>(null)
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const summary = useMemo(() => summarizeBackgroundTasks(tasks ?? []), [tasks])

  useEffect(() => {
    onSummaryChange?.(summary)
  }, [summary, onSummaryChange])

  const filteredTasks = useMemo(
    () => filterBackgroundTasks(tasks ?? [], { statusFilter, query }),
    [tasks, statusFilter, query],
  )

  const parsedEntries = useMemo(
    () =>
      filteredTasks.map((task) => ({
        task,
        parsed: parseAutomationCommand(task.command_summary),
      })),
    [filteredTasks],
  )

  const allParsedList = useMemo(
    () => parsedEntries.map((entry) => entry.parsed),
    [parsedEntries],
  )

  const composerPreview = useMemo(() => {
    const composed = composeBackgroundTaskCommand({
      title: taskTitle,
      workdir: taskWorkdir,
      command,
    })
    return composed ? parseAutomationCommand(composed) : null
  }, [taskTitle, taskWorkdir, command])

  const detailParsed = useMemo(
    () => (detail ? parseAutomationCommand(detail.command_summary) : null),
    [detail],
  )

  const detailOutputSummary = useMemo(
    () => (detail ? summarizeTaskOutput(detail.output, detail.status) : {}),
    [detail],
  )

  const loadList = useCallback(async () => {
    setLoading(true)
    try {
      const list = await listBackgroundTasks(session, target)
      setTasks(list)
      setBgSupported(true)
      setError("")
    } catch (err) {
      const bgErr = err instanceof BackgroundTaskError ? err : null
      if (bgErr && (bgErr.status === 404 || bgErr.status === 501)) {
        setBgSupported(false)
      }
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session, target])

  const loadDetail = useCallback(
    async (id: number) => {
      try {
        const view = await getBackgroundTask(session, id)
        if (selectedRef.current === id) {
          setDetail(view)
          setDetailError("")
        }
      } catch (err) {
        if (selectedRef.current === id) setDetailError((err as Error).message)
      }
    },
    [session],
  )

  useEffect(() => {
    setTasks(null)
    setSelectedId(null)
    setDetail(null)
    setDetailError("")
    setStopNote("")
    setSubmitNotice("")
    setSubmitError("")
    setStatusFilter("all")
    setQuery("")
    void loadList()
  }, [loadList])

  // 只要有任务在跑就每 5 秒刷新列表，并每秒推进页面内的运行耗时显示。
  useEffect(() => {
    if (summary.running === 0) return
    const pollTimer = setInterval(() => void loadList(), 5000)
    const tickTimer = setInterval(() => setNowMs(Date.now()), 1000)
    return () => {
      clearInterval(pollTimer)
      clearInterval(tickTimer)
    }
  }, [summary.running, loadList])

  useEffect(() => {
    if (selectedId === null) {
      setDetail(null)
      setDetailError("")
      setStopNote("")
      setShowRawInDetail(false)
      return
    }
    setDetail(null)
    setDetailError("")
    setStopNote("")
    setShowRawInDetail(false)
    void loadDetail(selectedId)
  }, [selectedId, loadDetail])

  useEffect(() => {
    if (!detail || isBackgroundTaskTerminal(detail.status)) return
    const timer = setInterval(() => void loadDetail(detail.audit_id), 3000)
    return () => clearInterval(timer)
  }, [detail, loadDetail])

  useEffect(() => {
    if (!followOutput || !outputRef.current) return
    outputRef.current.scrollTop = outputRef.current.scrollHeight
  }, [detail?.output, followOutput])

  useEffect(() => {
    return () => {
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
    }
  }, [])

  const targetOffline = target.status === "offline"
  const composedCommand = composeBackgroundTaskCommand({
    title: taskTitle,
    workdir: taskWorkdir,
    command,
  })
  const canSubmit = bgSupported && !targetOffline && Boolean(composedCommand) && !submitting

  async function handleSubmit(e?: FormEvent) {
    e?.preventDefault()
    if (!composedCommand || submitting || targetOffline || !bgSupported) return
    setSubmitting(true)
    setSubmitError("")
    setSubmitNotice("")
    try {
      const receipt = await submitBackgroundTask(session, target, sessionId, composedCommand)
      setTaskTitle("")
      setTaskWorkdir("")
      setCommand("")
      setComposerOpen(false)
      setSubmitNotice(`已提交后台任务 #${receipt.audit_id}，Gateway 托管执行中`)
      setStatusFilter("all")
      await loadList()
      setSelectedId(receipt.audit_id)
    } catch (err) {
      setSubmitError((err as Error).message || "提交后台任务失败")
    } finally {
      setSubmitting(false)
    }
  }

  function handleComposerKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault()
      void handleSubmit()
    }
  }

  async function stopTask(auditId: number) {
    if (stoppingId !== null) return
    setStoppingId(auditId)
    if (selectedId === auditId) setStopNote("")
    try {
      const outcome = await stopBackgroundTask(session, auditId)
      const note = outcome === "already_finished" ? "任务已结束" : "已请求停止，等待远端终态"
      if (selectedId === auditId) setStopNote(note)
      else setSubmitNotice(`#${auditId}：${note}`)
      void loadList()
      if (selectedId === auditId) void loadDetail(auditId)
    } catch (err) {
      const msg =
        err instanceof BackgroundTaskError && (err.status === 409 || err.status === 404)
          ? "任务已结束"
          : `停止请求失败：${(err as Error).message}`
      if (selectedId === auditId) setStopNote(msg)
      else setSubmitError(`#${auditId}：${msg}`)
    } finally {
      setStoppingId(null)
    }
  }

  async function handleCopy(kind: "command" | "output", text: string) {
    const ok = await copyTerminalText(text)
    if (!ok) return
    setCopiedKey(kind)
    if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
    copyTimerRef.current = setTimeout(() => setCopiedKey(""), 1800)
  }

  function refillFromDetail(cmd: string) {
    const parsed = parseAutomationCommand(cmd)
    setTaskTitle(parsed.customTitle || "")
    setTaskWorkdir(parsed.workdir || "")
    setCommand(parsed.bodyCommand || parsed.raw)
    setComposerOpen(true)
    setSubmitError("")
    setSubmitNotice(
      parsed.maybeTruncated
        ? "已回填到新建表单（注意：原命令摘要可能被截断，提交前请核对完整命令）"
        : "已拆解并回填到新建表单，可调整名称、目录或参数后点击「后台运行」",
    )
    setTimeout(() => {
      composerRef.current?.focus()
      composerRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" })
    }, 0)
  }

  function handleExplainDetail() {
    if (!detail || !detailParsed || !onExplainTask) return
    const prompt = buildExplainTaskPrompt(detail, detailParsed, detailOutputSummary)
    onExplainTask(prompt)
  }

  const outputLines = detail?.output ? detail.output.split("\n").length : 0
  const detailMeta = detail ? describeBackgroundTaskStatus(detail.status) : null
  const isFailureStatus =
    detail?.status === "error" || detail?.status === "failed" || detail?.status === "timeout"

  return (
    <Stack gap={3} className={embedded ? "" : "min-h-0 flex-1 overflow-auto p-4"}>
      {!embedded ? (
        <div className="flex items-center justify-between gap-2">
          <div>
            <h2 className="text-base font-semibold text-foreground">
              一次性后台执行（<span className="font-mono">{target.cluster}/{target.instance}</span>）
            </h2>
            <p className="text-xs text-muted-foreground">
              Gateway 托管的 exec --bg 长耗时命令；离开页面不中断，仅本人可见、可停止
            </p>
          </div>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">
          用于运行或查看当前目标上通过 CLI <code className="font-mono">doops exec --bg</code> 或本页发起的一次性长耗时命令（如压测、备份、迁移），关闭页面不中断，终态记录保留 24 小时。
        </p>
      )}

      {/* 顶部筛选、搜索与操作条 */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="按状态筛选后台任务">
          {FILTER_OPTIONS.filter((opt) => opt.id !== "unknown" || summary.unknown > 0 || statusFilter === "unknown").map(
            (opt) => {
              const count = filterCount(summary, opt.id)
              const active = statusFilter === opt.id
              return (
                <button
                  key={opt.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setStatusFilter(opt.id)}
                  className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs transition-colors ${
                    active
                      ? "border-primary/60 bg-primary/15 font-medium text-primary"
                      : "border-border bg-card text-muted-foreground hover:bg-muted/50 hover:text-foreground"
                  }`}
                >
                  <span>{opt.label}</span>
                  <span className="font-mono text-2xs opacity-80">{count}</span>
                </button>
              )
            },
          )}
        </div>

        <Inline gap={2} className="flex-1 justify-end sm:flex-initial">
          <div className="relative min-w-44 flex-1 sm:w-56 sm:flex-initial">
            <Search
              width={13}
              height={13}
              aria-hidden="true"
              className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              scale="sm"
              aria-label="搜索后台任务"
              className="w-full pl-8 pr-7 text-xs"
              placeholder="搜索任务、目录、参数或 #ID…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            {query && (
              <button
                type="button"
                aria-label="清除搜索"
                onClick={() => setQuery("")}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              >
                <X width={13} height={13} />
              </button>
            )}
          </div>
          <Button
            size="sm"
            variant={showRawInList ? "primary" : "ghost"}
            aria-pressed={showRawInList}
            onClick={() => setShowRawInList((v) => !v)}
            title="在列表中切换显示原始 Shell 命令行"
          >
            <Code2 width={13} height={13} aria-hidden="true" />
            原始命令
          </Button>
          <Button
            size="sm"
            variant={composerOpen ? "default" : "primary"}
            disabled={targetOffline || !bgSupported}
            onClick={() => setComposerOpen((v) => !v)}
            title={
              !bgSupported
                ? "Gateway 未启用后台任务托管"
                : targetOffline
                  ? "目标当前离线，恢复在线后可新建任务"
                  : "下发新的后台执行任务"
            }
          >
            <Plus width={14} height={14} aria-hidden="true" />
            {composerOpen ? "收起表单" : "新建任务"}
          </Button>
          <Button size="sm" variant="default" disabled={loading} onClick={() => void loadList()}>
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
          </Button>
        </Inline>
      </div>

      {/* 离线 / 能力提示与运行中自动刷新提示 */}
      {(targetOffline || !bgSupported || summary.running > 0) && (
        <div className="flex flex-wrap items-center gap-2 text-2xs text-muted-foreground">
          {targetOffline && <Badge variant="warning">目标离线（仅可查看历史记录）</Badge>}
          {!bgSupported && <Badge variant="warning">Gateway 未启用后台任务托管</Badge>}
          {summary.running > 0 && (
            <span className="inline-flex items-center gap-1 text-primary">
              <Clock width={12} height={12} aria-hidden="true" />
              运行中任务每 5s 自动刷新，详情输出每 3s 刷新
            </span>
          )}
        </div>
      )}

      {/* 可折叠的结构化新建后台任务表单 */}
      {composerOpen && (
        <Card>
          <CardContent className="p-4">
            <form onSubmit={(e) => void handleSubmit(e)} className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-semibold text-foreground">新建后台任务</span>
                  <span className="font-mono text-2xs text-muted-foreground">会话：{sessionId}</span>
                </div>
                <span className="text-2xs text-muted-foreground">
                  Gateway 托管 <code className="font-mono">exec --bg</code> · 离开页面不中断 · 仅本人可见
                </span>
              </div>

              <div className="grid gap-3 md:grid-cols-2">
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">任务名称（可选，展示在列表主标题）</span>
                  <Input
                    scale="sm"
                    placeholder="例如：全校考试 1000 并发压测 / Nginx 日志归档"
                    value={taskTitle}
                    disabled={targetOffline || !bgSupported || submitting}
                    onChange={(e) => setTaskTitle(e.target.value)}
                  />
                </label>
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">工作目录（可选，自动切换后再执行）</span>
                  <Input
                    scale="sm"
                    className="font-mono text-xs"
                    placeholder="例如：/root/ws/exam-lt-20261123"
                    value={taskWorkdir}
                    disabled={targetOffline || !bgSupported || submitting}
                    onChange={(e) => setTaskWorkdir(e.target.value)}
                  />
                </label>
              </div>

              <label className="block space-y-1 text-xs">
                <span className="text-muted-foreground">执行命令（支持多行或带环境变量，⌘/Ctrl + Enter 提交）</span>
                <Textarea
                  ref={composerRef}
                  rows={3}
                  aria-label="后台任务 Shell 命令"
                  className="w-full font-mono text-xs"
                  placeholder="例如：VUS=100 HOLD_SECONDS=90 k6 run exam.js"
                  value={command}
                  disabled={targetOffline || !bgSupported || submitting}
                  onChange={(e) => setCommand(e.target.value)}
                  onKeyDown={handleComposerKeyDown}
                />
              </label>

              {composerPreview && (
                <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 px-3 py-1.5 text-xs">
                  <span className="text-muted-foreground">识别摘要：</span>
                  <span className="font-medium text-foreground">{composerPreview.title}</span>
                  <Badge variant="outline">{composerPreview.category}</Badge>
                  {composerPreview.workdirName && (
                    <span className="font-mono text-2xs text-muted-foreground">
                      目录：{composerPreview.workdirName}
                    </span>
                  )}
                  {composerPreview.envVars.length > 0 && (
                    <span className="font-mono text-2xs text-muted-foreground">
                      · {composerPreview.envVars.length} 项参数
                    </span>
                  )}
                </div>
              )}

              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="text-2xs text-muted-foreground">
                  切勿在命令行内写入明文密码、令牌或私钥；受管凭据请通过凭据体系注入。
                </span>
                <Inline gap={2}>
                  {(taskTitle || taskWorkdir || command) && (
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={submitting}
                      onClick={() => {
                        setTaskTitle("")
                        setTaskWorkdir("")
                        setCommand("")
                      }}
                    >
                      清空
                    </Button>
                  )}
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={submitting}
                    onClick={() => setComposerOpen(false)}
                  >
                    取消
                  </Button>
                  <Button type="submit" size="sm" variant="primary" disabled={!canSubmit}>
                    <Play width={13} height={13} aria-hidden="true" />
                    {submitting ? "提交中…" : "后台运行"}
                  </Button>
                </Inline>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      {submitError && <StatusBanner tone="error">{submitError}</StatusBanner>}
      {submitNotice && <StatusBanner tone="success">{submitNotice}</StatusBanner>}
      {error && <StatusBanner tone="error">{error}</StatusBanner>}

      {/* 列表 + 详情分栏 */}
      {tasks === null && !error ? (
        <div className="card-surface p-6 text-center text-sm text-muted-foreground">正在加载任务列表…</div>
      ) : tasks !== null && tasks.length === 0 ? (
        <div className="card-surface flex flex-col items-center gap-2.5 p-8 text-center text-muted-foreground">
          <HistoryIcon width={24} height={24} />
          <p className="text-sm text-foreground">当前目标暂无一次性后台执行记录</p>
          <p className="text-xs text-muted-foreground">
            此处仅承接单次提交的 <code className="font-mono">doops exec --bg</code> 托管长耗时命令（终态保留 24 小时）；周期任务与巡检执行历史请在「定时任务 / 执行记录」查看。
          </p>
          <Inline gap={2} wrap>
            {!targetOffline && bgSupported && (
              <Button size="sm" variant="primary" onClick={() => setComposerOpen(true)}>
                <Plus width={14} height={14} aria-hidden="true" />
                新建后台执行
              </Button>
            )}
            {onOpenSchedules && (
              <Button size="sm" variant="ghost" onClick={onOpenSchedules}>
                查看定时任务
              </Button>
            )}
          </Inline>
        </div>
      ) : parsedEntries.length === 0 ? (
        <div className="card-surface flex flex-col items-center gap-2 p-6 text-muted-foreground">
          <p className="text-sm">没有匹配当前筛选条件的任务</p>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setStatusFilter("all")
              setQuery("")
            }}
          >
            重置筛选
          </Button>
        </div>
      ) : (
        <div className={selectedId !== null ? "grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.25fr)] lg:items-start" : ""}>
          <div className="card-surface divide-y overflow-hidden">
            {parsedEntries.map(({ task, parsed }) => {
              const meta = describeBackgroundTaskStatus(task.status)
              const active = selectedId === task.audit_id
              const duration = formatTaskDuration(task.started_at, task.finished_at, nowMs)
              const canStop = !meta.terminal
              const { highlights, remainingCount } = pickHighlightEnvVars(allParsedList, parsed, 3)

              return (
                <div
                  key={task.audit_id}
                  className={`flex items-start gap-2.5 px-3.5 py-3 transition-colors hover:bg-muted/40 ${
                    active ? "bg-primary/10" : ""
                  }`}
                >
                  <button
                    type="button"
                    onClick={() => setSelectedId(task.audit_id)}
                    aria-current={active ? "true" : undefined}
                    className="flex min-w-0 flex-1 flex-col gap-1.5 text-left"
                  >
                    {/* 主行：状态 + 可读标题 + 分类 + ID / 耗时 */}
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant={meta.variant}>{meta.label}</Badge>
                      <span className="min-w-0 truncate text-sm font-medium text-foreground">
                        {parsed.title}
                      </span>
                      <Badge variant="outline">{parsed.category}</Badge>
                      {task.cancel_requested && !meta.terminal && (
                        <Badge variant="warning">停止中</Badge>
                      )}
                      <span className="ml-auto flex shrink-0 items-center gap-2 font-mono text-2xs text-muted-foreground">
                        <span>#{task.audit_id}</span>
                        <span>{meta.terminal ? `耗时 ${duration}` : `已运行 ${duration}`}</span>
                      </span>
                    </div>

                    {/* 结构化上下文行：工作目录 + 差异参数芯片 + 脚本名 */}
                    {(parsed.workdirName || highlights.length > 0 || parsed.executable) && (
                      <div className="flex flex-wrap items-center gap-1.5">
                        {parsed.workdirName && (
                          <span
                            title={`工作目录：${parsed.workdir}`}
                            className="inline-flex items-center gap-1 rounded bg-muted/60 px-1.5 py-0.5 font-mono text-2xs text-muted-foreground"
                          >
                            <FolderOpen width={11} height={11} aria-hidden="true" />
                            {parsed.workdirName}
                          </span>
                        )}
                        {parsed.executable && !parsed.title.includes(parsed.executable) && (
                          <span className="rounded bg-muted/50 px-1.5 py-0.5 font-mono text-2xs text-foreground/85">
                            {parsed.executable}
                          </span>
                        )}
                        {highlights.map((ev) => (
                          <span
                            key={ev.key}
                            title={ev.hint ? `${ev.key}=${ev.displayValue}（${ev.hint}）` : `${ev.key}=${ev.displayValue}`}
                            className="max-w-56 truncate rounded border border-border/80 bg-background/60 px-1.5 py-0.5 font-mono text-2xs text-foreground"
                          >
                            <span className="text-muted-foreground">{ev.key}=</span>
                            {ev.displayValue}
                          </span>
                        ))}
                        {remainingCount > 0 && (
                          <span className="text-2xs text-muted-foreground">+{remainingCount} 项参数</span>
                        )}
                      </div>
                    )}

                    {/* 可选原始命令预览 */}
                    {showRawInList && (
                      <p
                        title={task.command_summary}
                        className="truncate rounded bg-background/50 px-2 py-1 font-mono text-2xs text-muted-foreground"
                      >
                        $ {task.command_summary}
                      </p>
                    )}

                    {/* 元信息行 */}
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-2xs text-muted-foreground">
                      <span>开始：{new Date(task.started_at).toLocaleString()}</span>
                      <span className="font-mono">会话：{task.session}</span>
                    </div>
                  </button>

                  {canStop && (
                    <Button
                      size="sm"
                      variant="danger-outline"
                      disabled={stoppingId === task.audit_id}
                      onClick={() => void stopTask(task.audit_id)}
                      title="停止此后台任务"
                    >
                      <Square width={11} height={11} aria-hidden="true" />
                      {stoppingId === task.audit_id ? "停止中…" : "停止"}
                    </Button>
                  )}
                </div>
              )
            })}
          </div>

          {selectedId !== null && (
            <div className="card-surface space-y-3 p-4 lg:sticky lg:top-0">
              {/* 详情头部 */}
              <div className="flex flex-wrap items-start gap-2">
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    {detailMeta && <Badge variant={detailMeta.variant}>{detailMeta.label}</Badge>}
                    <h3 className="text-sm font-semibold text-foreground">
                      {detailParsed ? detailParsed.title : `任务 #${selectedId}`}
                    </h3>
                    {detailParsed && <Badge variant="outline">{detailParsed.category}</Badge>}
                    {detailOutputSummary.exitCode !== undefined && (
                      <Badge variant={detailOutputSummary.exitCode === 0 ? "success" : "danger"}>
                        退出码 {detailOutputSummary.exitCode}
                      </Badge>
                    )}
                    {detail?.cancel_requested && <Badge variant="warning">已请求停止</Badge>}
                  </div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-2xs text-muted-foreground">
                    <span>#{selectedId}</span>
                    {detail?.task_id && <span>task_id={detail.task_id}</span>}
                    {detailOutputSummary.pid && <span>PID={detailOutputSummary.pid}</span>}
                    {detail && <span>会话：{detail.session}</span>}
                  </div>
                </div>
                <Inline gap={1}>
                  {detail && detailMeta && !detailMeta.terminal && (
                    <Button
                      size="sm"
                      variant="danger"
                      disabled={stoppingId === detail.audit_id}
                      onClick={() => void stopTask(detail.audit_id)}
                    >
                      <Square width={11} height={11} aria-hidden="true" />
                      {stoppingId === detail.audit_id ? "停止中…" : "停止任务"}
                    </Button>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label="关闭任务详情"
                    onClick={() => setSelectedId(null)}
                  >
                    <X width={14} height={14} />
                  </Button>
                </Inline>
              </div>

              {/* 关键失败摘要 / 结果未知告警 */}
              {detail && isFailureStatus && detailOutputSummary.headline && (
                <StatusBanner tone="error" compact>
                  <span className="font-medium">失败摘录：</span>
                  <span className="font-mono">{detailOutputSummary.headline}</span>
                </StatusBanner>
              )}
              {detail?.status === "outcome_unknown" && (
                <StatusBanner tone="warning" compact>
                  Gateway 重启或链路中断导致终态未确认；请先核对目标侧进程实际状态，再决定是否通过「重新填入」再次执行。
                </StatusBanner>
              )}
              {stopNote && <StatusBanner tone="warning" compact>{stopNote}</StatusBanner>}
              {detailError && <StatusBanner tone="error" compact>{detailError}</StatusBanner>}

              {detail && detailParsed && (
                <>
                  {/* 结构化执行内容解析 */}
                  <div className="space-y-2.5 rounded-md border bg-muted/25 p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2 text-2xs text-muted-foreground">
                      <span className="font-medium text-foreground">执行内容解析</span>
                      <span className="font-mono">
                        开始 {new Date(detail.started_at).toLocaleString()}
                        {detail.finished_at ? ` · 结束 ${new Date(detail.finished_at).toLocaleString()}` : ""}
                        {" · "}
                        {detailMeta?.terminal ? "总耗时 " : "已运行 "}
                        {formatTaskDuration(detail.started_at, detail.finished_at, nowMs)}
                      </span>
                    </div>

                    {detailParsed.workdir && (
                      <div className="flex flex-wrap items-center gap-2 text-xs">
                        <span className="text-muted-foreground">工作目录：</span>
                        <code className="rounded bg-background/70 px-1.5 py-0.5 font-mono text-xs text-foreground">
                          {detailParsed.workdir}
                        </code>
                      </div>
                    )}

                    {detailParsed.steps.length > 0 && (
                      <div className="space-y-1">
                        <span className="block text-2xs text-muted-foreground">
                          执行步骤（{detailParsed.steps.length} 步）
                        </span>
                        <ol className="space-y-1">
                          {detailParsed.steps.map((step) => (
                            <li
                              key={step.index}
                              className="flex items-start gap-2 rounded bg-background/50 px-2.5 py-1.5 text-xs"
                            >
                              <span className="mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-primary/15 font-mono text-2xs font-medium text-primary">
                                {step.index}
                              </span>
                              <div className="min-w-0 flex-1 space-y-0.5">
                                <div className="font-medium text-foreground">{step.description}</div>
                                {step.binary && step.binary !== "cd" && (
                                  <div className="truncate font-mono text-2xs text-muted-foreground" title={step.raw}>
                                    {step.binary}
                                    {step.args.length > 0 ? ` ${step.args.join(" ")}` : ""}
                                  </div>
                                )}
                              </div>
                            </li>
                          ))}
                        </ol>
                      </div>
                    )}

                    {detailParsed.envVars.length > 0 && (
                      <div className="space-y-1">
                        <span className="block text-2xs text-muted-foreground">
                          运行参数 / 环境变量（{detailParsed.envVars.length} 项）
                        </span>
                        <div className="grid gap-1 sm:grid-cols-2">
                          {detailParsed.envVars.map((ev, i) => (
                            <div
                              key={`${ev.key}-${i}`}
                              className="flex min-w-0 items-baseline justify-between gap-2 rounded border border-border/60 bg-background/60 px-2 py-1 text-2xs"
                            >
                              <span className="shrink-0 font-mono text-muted-foreground">{ev.key}</span>
                              <span className="min-w-0 truncate text-right font-mono text-foreground" title={ev.displayValue}>
                                {ev.displayValue}
                                {ev.hint && (
                                  <span className="ml-1 font-sans text-muted-foreground">({ev.hint})</span>
                                )}
                              </span>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}

                    {/* 折叠原始命令 */}
                    <div className="border-t border-border/60 pt-2">
                      <button
                        type="button"
                        onClick={() => setShowRawInDetail((v) => !v)}
                        className="inline-flex items-center gap-1 text-2xs text-muted-foreground hover:text-foreground"
                      >
                        {showRawInDetail ? (
                          <ChevronDown width={13} height={13} aria-hidden="true" />
                        ) : (
                          <ChevronRight width={13} height={13} aria-hidden="true" />
                        )}
                        {showRawInDetail ? "收起原始 Shell 命令" : "查看原始 Shell 命令"}
                      </button>
                      {showRawInDetail && (
                        <div className="mt-1.5 space-y-1.5">
                          {detailParsed.maybeTruncated && (
                            <p className="text-2xs text-warning">
                              注：Gateway 审计摘要最多保留 512 字节，该命令可能已截断，重新填入前请核对完整命令。
                            </p>
                          )}
                          <pre className="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded border bg-background/80 p-2 font-mono text-2xs text-foreground">
                            {detail.command_summary}
                          </pre>
                        </div>
                      )}
                    </div>
                  </div>

                  {/* 操作工具栏 */}
                  <div className="flex flex-wrap items-center gap-1.5 border-y py-2">
                    {onExplainTask && (
                      <Button
                        size="sm"
                        variant="primary"
                        disabled={targetOffline}
                        onClick={handleExplainDetail}
                        title={targetOffline ? "目标离线，暂无法调用 AI 助手" : "携带脱敏后的命令与输出到 AI 助手解读"}
                      >
                        <Sparkles width={12} height={12} aria-hidden="true" />
                        AI 解释任务
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="default"
                      disabled={targetOffline || !bgSupported}
                      onClick={() => refillFromDetail(detail.command_summary)}
                    >
                      <RotateCcw width={12} height={12} aria-hidden="true" />
                      重新填入
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => void handleCopy("command", detail.command_summary)}
                    >
                      {copiedKey === "command" ? (
                        <Check width={12} height={12} aria-hidden="true" />
                      ) : (
                        <Copy width={12} height={12} aria-hidden="true" />
                      )}
                      {copiedKey === "command" ? "已复制命令" : "复制命令"}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={!detail.output}
                      onClick={() => void handleCopy("output", detail.output || "")}
                    >
                      {copiedKey === "output" ? (
                        <Check width={12} height={12} aria-hidden="true" />
                      ) : (
                        <Copy width={12} height={12} aria-hidden="true" />
                      )}
                      {copiedKey === "output" ? "已复制输出" : "复制输出"}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={!detail.output}
                      onClick={() => downloadTextFile(`doops-task-${detail.audit_id}.log`, detail.output || "")}
                    >
                      <Download width={12} height={12} aria-hidden="true" />
                      导出日志
                    </Button>
                    <span className="flex-1" />
                    <Button
                      size="sm"
                      variant={wrapOutput ? "primary" : "ghost"}
                      aria-pressed={wrapOutput}
                      onClick={() => setWrapOutput((v) => !v)}
                      title="切换输出自动换行"
                    >
                      <WrapText width={12} height={12} aria-hidden="true" />
                      换行
                    </Button>
                    <Button
                      size="sm"
                      variant={followOutput ? "primary" : "ghost"}
                      aria-pressed={followOutput}
                      onClick={() => setFollowOutput((v) => !v)}
                      title="运行中自动滚动到底部"
                    >
                      跟随底部
                    </Button>
                  </div>
                </>
              )}

              {detail === null && !detailError && (
                <p className="text-sm text-muted-foreground">正在读取任务详情与输出…</p>
              )}
              {detail !== null && (
                <div className="space-y-1">
                  <div className="flex items-center justify-between text-2xs text-muted-foreground">
                    <span>执行输出（已脱敏，上限 64 KiB）</span>
                    <span className="font-mono">
                      {detail.output ? `共 ${outputLines} 行` : "暂无输出"}
                      {detailMeta && !detailMeta.terminal ? " · 每 3s 刷新" : ""}
                    </span>
                  </div>
                  <pre
                    ref={outputRef}
                    className={`max-h-96 overflow-auto rounded-md border bg-background/80 p-3 font-mono text-xs leading-relaxed text-foreground ${
                      wrapOutput ? "whitespace-pre-wrap break-all" : "whitespace-pre"
                    }`}
                  >
                    {detail.output || "（暂无输出）"}
                  </pre>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </Stack>
  )
}
