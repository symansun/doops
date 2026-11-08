"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { fetchAudit, type AuditEvent, type Session, type Target } from "@/lib/client"
import {
  AUDIT_ACTIONS,
  AUDIT_STATUSES,
  auditStatusKind,
  auditStatusLabel,
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
      <div className="flex flex-wrap items-center justify-between gap-3 border-b bg-card px-4 py-3">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <select
            value={scope}
            onChange={(e) => setScope(e.target.value as AuditScope)}
            className="input input-sm"
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
            className="input input-sm"
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
            className="input input-sm"
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
            <span className="break-all font-mono text-sm text-foreground">{sessionId}</span>
          )}
          <button
            onClick={load}
            disabled={loading}
            className="btn btn-sm"
          >
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        <p className="mb-4 max-w-4xl text-sm leading-6 text-muted-foreground">
          普通用户看到本人记录，以及引用自己凭据的事件。全局只有平台管理员能看。结果未知表示终态没有写上，不能当成失败或成功。最近 50 条。
        </p>
        {error && (
          <p role="alert" className="status-error mb-3">
            {error}
          </p>
        )}
        {loading && <p className="mb-3 text-sm text-muted-foreground">正在查询审计记录…</p>}
        {!error && events.length === 0 && !loading && (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
            <HistoryIcon width={28} height={28} />
            <p className="text-sm">暂无审计记录</p>
          </div>
        )}
        <ul className="flex flex-col gap-2">
          {events.map((e) => (
            <li key={e.id} className="min-w-0 rounded-lg border bg-card p-4 shadow-sm">
              <div className="flex flex-wrap items-center gap-2.5">
                <span className="rounded-md bg-muted px-2 py-1 text-sm font-medium text-foreground">
                  {e.action || "?"}
                </span>
                <span
                  className={`rounded-md px-2 py-1 text-sm font-medium ${
                    auditStatusKind(e.status) === "success"
                      ? "bg-success/15 text-success"
                      : auditStatusKind(e.status) === "failed"
                        ? "bg-destructive/15 text-destructive"
                        : auditStatusKind(e.status) === "unknown" || auditStatusKind(e.status) === "running"
                          ? "bg-warning/15 text-warning"
                          : "bg-muted text-muted-foreground"
                  }`}
                >
                  {auditStatusLabel(e.status)}
                </span>
                <span className="ml-auto text-sm text-muted-foreground">
                  {fmtTime(e.started_at)}
                </span>
              </div>
              <div className="mt-3 grid min-w-0 gap-x-5 gap-y-1.5 text-sm leading-6 text-muted-foreground sm:grid-cols-2">
                <span className="min-w-0 break-all"><strong className="font-medium text-foreground/80">用户</strong> {e.user_id || "—"}</span>
                <span className="min-w-0 break-all"><strong className="font-medium text-foreground/80">Token</strong> {e.token_id || "—"}</span>
                <span className="min-w-0 break-all"><strong className="font-medium text-foreground/80">目标</strong> {e.cluster || "—"}/{e.instance || "—"}</span>
                {e.credential_id && <span className="min-w-0 break-all font-mono"><strong className="font-sans font-medium text-foreground/80">凭据</strong> {e.credential_id}</span>}
                {e.session && <span className="min-w-0 break-all font-mono"><strong className="font-sans font-medium text-foreground/80">Session</strong> {e.session}</span>}
                {e.ended_at && <span className="min-w-0 break-all"><strong className="font-medium text-foreground/80">结束</strong> {fmtTime(e.ended_at)}</span>}
              </div>
              {e.command_summary && (
                <p className="mt-3 break-words rounded-md border border-border/80 bg-background/70 px-3 py-2.5 font-mono text-[13px] leading-6 text-foreground">
                  {e.command_summary}
                </p>
              )}
              {e.error && (
                <p className="mt-3 break-words rounded-md bg-destructive/10 px-3 py-2.5 text-sm leading-6 text-destructive">错误：{e.error}</p>
              )}
              {e.tail && (
                <pre className="mt-3 max-h-48 overflow-y-auto whitespace-pre-wrap break-words rounded-md border border-border/70 bg-background/80 p-3 font-mono text-[13px] leading-6 text-foreground/85">
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
