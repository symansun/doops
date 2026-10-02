"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { fetchAudit, type AuditEvent, type Session, type Target } from "@/lib/client"
import {
  AUDIT_ACTIONS,
  AUDIT_STATUSES,
  type AuditScope,
} from "@/lib/gateway"
import { HistoryIcon, RefreshIcon } from "./icons"

function fmtTime(s?: string) {
  if (!s) return "—"
  try {
    return new Date(s).toLocaleString("zh-CN", { hour12: false })
  } catch {
    return s
  }
}

export function AuditPanel({
  session,
  target,
  sessionId,
}: {
  session: Session
  target: Target
  sessionId: string
}) {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [scope, setScope] = useState<AuditScope>("target")
  const [action, setAction] = useState("")
  const [status, setStatus] = useState("")
  const requestId = useRef(0)

  const load = useCallback(async () => {
    const id = ++requestId.current
    setLoading(true)
    setError("")
    setEvents([])
    const params: Record<string, string> = { limit: "50" }
    if (scope === "target" || scope === "session") {
      params.cluster = target.cluster
      params.instance = target.instance
    }
    if (scope === "session") params.session = sessionId
    if (scope === "self") params.user_id = "self"
    if (scope === "global") params.view = "global"
    if (action) params.action = action
    if (status) params.status = status

    try {
      const r = await fetchAudit(session, params)
      if (id !== requestId.current) return
      if (r.error) {
        setError(r.status === 403 ? "当前账号无权查看此审计范围" : r.error)
        setEvents([])
      } else {
        setEvents(r.events || [])
      }
    } catch (err) {
      if (id !== requestId.current) return
      setEvents([])
      setError(err instanceof Error ? err.message : "查询审计失败")
    } finally {
      if (id === requestId.current) setLoading(false)
    }
  }, [session, target, sessionId, scope, action, status])

  useEffect(() => {
    load()
    return () => { requestId.current += 1 }
  }, [load])

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-card px-4 py-2.5">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <select
            value={scope}
            onChange={(e) => setScope(e.target.value as AuditScope)}
            className="rounded-md border bg-background px-2 py-1.5 text-xs text-foreground outline-none focus:border-ring"
            aria-label="审计范围"
          >
            <option value="target">该目标</option>
            <option value="session">本会话</option>
            <option value="self">本人跨目标</option>
            <option value="global">全局（管理员）</option>
          </select>
          <select
            value={action}
            onChange={(e) => setAction(e.target.value)}
            className="rounded-md border bg-background px-2 py-1.5 text-xs text-foreground outline-none focus:border-ring"
            aria-label="审计动作"
          >
            <option value="">全部动作</option>
            {AUDIT_ACTIONS.map((item) => (
              <option key={item} value={item}>
                {item}
              </option>
            ))}
          </select>
          <select
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            className="rounded-md border bg-background px-2 py-1.5 text-xs text-foreground outline-none focus:border-ring"
            aria-label="审计状态"
          >
            <option value="">全部状态</option>
            {AUDIT_STATUSES.map((item) => (
              <option key={item} value={item}>
                {item}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-2">
          {scope === "session" && (
            <span className="break-all font-mono text-xs text-muted-foreground">{sessionId}</span>
          )}
          <button
            onClick={load}
            disabled={loading}
            className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
          >
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        <p className="mb-3 text-xs text-muted-foreground">
          普通用户仅查看本人记录；管理员可查看全局，凭据审计另按授权范围可见。最近 50 条。
        </p>
        {error && (
          <p className="mb-3 rounded-lg bg-destructive/15 px-3 py-2 text-sm text-destructive">
            {error}
          </p>
        )}
        {loading && <p className="mb-3 text-xs text-muted-foreground">正在查询审计记录…</p>}
        {!error && events.length === 0 && !loading && (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
            <HistoryIcon width={28} height={28} />
            <p className="text-sm">暂无审计记录</p>
          </div>
        )}
        <ul className="flex flex-col gap-2">
          {events.map((e) => (
            <li key={e.id} className="min-w-0 rounded-lg border bg-card p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="rounded-md bg-muted px-1.5 py-0.5 text-xs font-medium text-foreground">
                  {e.action || "?"}
                </span>
                <span
                  className={`rounded-md px-1.5 py-0.5 text-xs font-medium ${
                    e.status === "success"
                      ? "bg-success/15 text-success"
                      : e.status === "running"
                        ? "bg-warning/15 text-warning"
                        : "bg-destructive/15 text-destructive"
                  }`}
                >
                  status={e.status || "unknown"}
                </span>
                <span className="ml-auto text-xs text-muted-foreground">
                  {fmtTime(e.started_at)}
                </span>
              </div>
              <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 break-all text-xs text-muted-foreground">
                <span>user={e.user_id || "—"}</span>
                <span>token={e.token_id || "—"}</span>
                <span>
                  target={e.cluster || "—"}/{e.instance || "—"}
                </span>
                {e.session && <span className="font-mono">session={e.session}</span>}
              </div>
              {e.command_summary && (
                <p className="mt-1.5 break-words font-mono text-xs text-foreground/90">
                  {e.command_summary}
                </p>
              )}
              {e.error && (
                <p className="mt-1.5 break-words text-xs text-destructive">error={e.error}</p>
              )}
              {e.tail && (
                <pre className="mt-1.5 max-h-24 overflow-y-auto whitespace-pre-wrap break-words rounded-md bg-background/60 p-2 font-mono text-xs text-muted-foreground">
                  {e.tail}
                </pre>
              )}
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
