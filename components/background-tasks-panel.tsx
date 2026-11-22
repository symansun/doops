"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import type { Session, Target } from "@/lib/client"
import {
  BackgroundTaskError,
  getBackgroundTask,
  listBackgroundTasks,
  stopBackgroundTask,
  type BackgroundTaskView,
} from "@/lib/background-tasks"
import { Badge, Button, StatusBanner } from "./ui"
import { HistoryIcon, RefreshIcon } from "./icons"

const STATUS_LABELS: Record<string, string> = {
  running: "运行中",
  success: "成功",
  failed: "失败",
  canceled: "已停止",
  timeout: "超时",
  outcome_unknown: "结果未知",
}

function statusVariant(status: string): "success" | "warning" | "danger" | "default" {
  switch (status) {
    case "success": return "success"
    case "running": return "warning"
    case "failed":
    case "timeout": return "danger"
    case "canceled":
    case "outcome_unknown":
    default: return "default"
  }
}

function isTerminal(status: string): boolean {
  return status !== "running"
}

export function BackgroundTasksPanel({
  session,
  target,
}: {
  session: Session
  target: Target
}) {
  const [tasks, setTasks] = useState<BackgroundTaskView[] | null>(null)
  const [error, setError] = useState("")
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [detail, setDetail] = useState<BackgroundTaskView | null>(null)
  const [detailError, setDetailError] = useState("")
  const [stopping, setStopping] = useState(false)
  const [stopNote, setStopNote] = useState("")
  const selectedRef = useRef<number | null>(null)
  selectedRef.current = selectedId

  const loadList = useCallback(async () => {
    try {
      const list = await listBackgroundTasks(session, target)
      setTasks(list)
      setError("")
    } catch (err) {
      setError((err as Error).message)
    }
  }, [session, target])

  const loadDetail = useCallback(async (id: number) => {
    try {
      const view = await getBackgroundTask(session, id)
      if (selectedRef.current === id) {
        setDetail(view)
        setDetailError("")
      }
    } catch (err) {
      if (selectedRef.current === id) setDetailError((err as Error).message)
    }
  }, [session])

  useEffect(() => {
    setTasks(null)
    setSelectedId(null)
    setDetail(null)
    void loadList()
  }, [loadList])

  // 只要有任务在跑就每 5 秒刷新列表；详情里的运行中任务每 3 秒刷新输出。
  useEffect(() => {
    if (!tasks?.some((t) => !isTerminal(t.status))) return
    const timer = setInterval(() => void loadList(), 5000)
    return () => clearInterval(timer)
  }, [tasks, loadList])

  useEffect(() => {
    if (selectedId === null) return
    setDetail(null)
    setDetailError("")
    setStopNote("")
    void loadDetail(selectedId)
  }, [selectedId, loadDetail])

  useEffect(() => {
    if (!detail || isTerminal(detail.status)) return
    const timer = setInterval(() => void loadDetail(detail.audit_id), 3000)
    return () => clearInterval(timer)
  }, [detail, loadDetail])

  async function stop() {
    if (selectedId === null || stopping) return
    setStopping(true)
    setStopNote("")
    try {
      await stopBackgroundTask(session, selectedId)
      setStopNote("已请求停止，等待终态")
      void loadList()
      void loadDetail(selectedId)
    } catch (err) {
      if (err instanceof BackgroundTaskError && err.status === 409) {
        setStopNote("任务已结束")
      } else {
        setStopNote(`停止请求失败：${(err as Error).message}`)
      }
    } finally {
      setStopping(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-semibold">后台任务</h2>
          <p className="text-xs text-muted-foreground">
            Gateway 托管的 exec --bg / 后台运行任务；离开页面不会中断，仅本人可见、可停止
          </p>
        </div>
        <Button size="sm" variant="default" onClick={() => void loadList()}>
          <RefreshIcon width={14} height={14} /> 刷新
        </Button>
      </div>
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {tasks === null && !error && <p className="text-sm text-muted-foreground">加载中…</p>}
      {tasks !== null && tasks.length === 0 && (
        <div className="card-surface flex flex-col items-center gap-2 p-8 text-muted-foreground">
          <HistoryIcon width={24} height={24} />
          <p className="text-sm">暂无后台任务</p>
        </div>
      )}
      {tasks !== null && tasks.length > 0 && (
        <div className="card-surface divide-y">
          {tasks.map((task) => (
            <button
              key={task.audit_id}
              type="button"
              onClick={() => setSelectedId(task.audit_id)}
              className={`flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-muted/40 ${
                selectedId === task.audit_id ? "bg-muted/50" : ""
              }`}
            >
              <span className="font-mono text-xs text-muted-foreground">#{task.audit_id}</span>
              <Badge variant={statusVariant(task.status)}>{STATUS_LABELS[task.status] ?? task.status}</Badge>
              <span className="min-w-0 flex-1 truncate font-mono text-xs">{task.command_summary}</span>
              <span className="text-xs text-muted-foreground">
                {new Date(task.started_at).toLocaleString()}
              </span>
            </button>
          ))}
        </div>
      )}

      {selectedId !== null && (
        <div className="card-surface space-y-3 p-4">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm">#{selectedId}</span>
            {detail && <Badge variant={statusVariant(detail.status)}>{STATUS_LABELS[detail.status] ?? detail.status}</Badge>}
            {detail?.task_id && <span className="font-mono text-xs text-muted-foreground">task_id={detail.task_id}</span>}
            {detail?.cancel_requested && <span className="text-xs text-warning">已请求停止</span>}
            <span className="flex-1" />
            {detail && !isTerminal(detail.status) && (
              <Button size="sm" variant="danger" disabled={stopping} onClick={() => void stop()}>
                {stopping ? "停止中…" : "停止"}
              </Button>
            )}
          </div>
          {detail && (
            <p className="font-mono text-xs text-muted-foreground">
              {detail.command_summary} · {detail.session} · 开始 {new Date(detail.started_at).toLocaleString()}
              {detail.finished_at ? ` · 结束 ${new Date(detail.finished_at).toLocaleString()}` : ""}
            </p>
          )}
          {stopNote && <StatusBanner tone="warning">{stopNote}</StatusBanner>}
          {detailError && <StatusBanner tone="error">{detailError}</StatusBanner>}
          {detail === null && !detailError && <p className="text-sm text-muted-foreground">加载中…</p>}
          {detail !== null && (
            <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded bg-background/60 p-3 font-mono text-xs">
              {detail.output || "（暂无输出）"}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}
