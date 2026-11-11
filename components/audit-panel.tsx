"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { fetchAudit, sessionIsAdmin, type AuditEvent, type Session, type Target } from "@/lib/client"
import {
  AUDIT_ACTIONS,
  AUDIT_STATUSES,
  auditQueryParams,
  type AuditScope,
} from "@/lib/gateway"
import { HistoryIcon, RefreshIcon } from "./icons"
import { Button, Input, Select } from "./ui"

const PAGE_SIZE = 50

function fmtTime(s?: string) {
  if (!s) return "—"
  try {
    return new Date(s).toLocaleString("zh-CN", { hour12: false })
  } catch {
    return s
  }
}

function duration(event: AuditEvent) {
  if (!event.started_at || !event.ended_at) return "—"
  const milliseconds = Date.parse(event.ended_at) - Date.parse(event.started_at)
  return Number.isFinite(milliseconds) && milliseconds >= 0 ? `${(milliseconds / 1000).toFixed(2)} s` : "—"
}

export function AuditPanel({
  session,
  target,
  sessionId,
  operationId = "",
}: {
  session: Session
  target: Target
  sessionId: string
  operationId?: string
}) {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [scope, setScope] = useState<AuditScope>(operationId ? "operation" : "target")
  const [action, setAction] = useState("")
  const [status, setStatus] = useState("")
  // 输入框的草稿；按回车或点「查询」后才写入 credentialId 并发起查询。
  const [credentialInput, setCredentialInput] = useState("")
  const [credentialId, setCredentialId] = useState("")
  const [operationInput, setOperationInput] = useState(operationId)
  const [correlationId, setCorrelationId] = useState(operationId)
  const [since, setSince] = useState("")
  const [until, setUntil] = useState("")
  const [hideObserve, setHideObserve] = useState(true)
  const [hasMore, setHasMore] = useState(false)
  const [hint, setHint] = useState("")
  const requestId = useRef(0)

  const load = useCallback(async (beforeId?: number) => {
    const id = ++requestId.current
    setError("")
    setHint("")
    if (beforeId === undefined) {
      setEvents([])
      setHasMore(false)
    }
    const plan = auditQueryParams({ scope, target, sessionId, credentialId, action, status, limit: PAGE_SIZE, correlationId, beforeId, since, until, hideUpgradeObserve: hideObserve })
    if (!plan.ok) {
      setLoading(false)
      if (plan.reason === "missing") setHint(plan.message)
      else setError(plan.message)
      return
    }
    setLoading(true)

    try {
      const r = await fetchAudit(session, plan.params)
      if (id !== requestId.current) return
      if (r.error) {
        setError(r.status === 403 ? "当前账号无权查看此审计范围" : r.error)
      } else {
        const page = r.events || []
        setEvents((previous) => beforeId === undefined ? page : [...previous, ...page.filter((event) => !previous.some((item) => item.id === event.id))])
        setHasMore(page.length === PAGE_SIZE)
      }
    } catch (err) {
      if (id !== requestId.current) return
      setError(err instanceof Error ? err.message : "查询审计失败")
    } finally {
      if (id === requestId.current) setLoading(false)
    }
    // The target poll replaces the object on every heartbeat; reload only when
    // the target itself changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, target.cluster, target.instance, sessionId, scope, action, status, credentialId, correlationId, since, until, hideObserve])

  function submitCredential() {
    const next = credentialInput.trim()
    setCredentialInput(next)
    // 值没变时 load 不会重建，手动再查一次。
    if (next === credentialId) load()
    else setCredentialId(next)
  }

  useEffect(() => {
    load()
    return () => { requestId.current += 1 }
  }, [load])

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b bg-card px-4 py-3">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <Select
            value={scope}
            onChange={(e) => setScope(e.target.value as AuditScope)}
            scale="sm"
            aria-label="审计范围"
          >
            <option value="target">该目标</option>
            <option value="session">本会话</option>
            <option value="self">本人跨目标</option>
            <option value="global" disabled={!sessionIsAdmin(session)}>全局（管理员）</option>
            <option value="credential">按凭据</option>
            <option value="operation">按操作</option>
          </Select>
          {scope === "credential" && (
            <form
              className="flex w-full flex-wrap items-center gap-2 sm:w-auto"
              onSubmit={(e) => {
                e.preventDefault()
                submitCredential()
              }}
            >
              <Input
                value={credentialInput}
                onChange={(e) => setCredentialInput(e.target.value)}
                placeholder="cred_…"
                spellCheck={false}
                className="w-full font-mono sm:w-72"
                aria-label="凭据 ID"
              />
              <Button
                type="submit"
                disabled={loading}
              >
                查询
              </Button>
            </form>
          )}
          {scope === "operation" && (
            <form className="flex w-full flex-wrap items-center gap-2 sm:w-auto" onSubmit={(e) => {
              e.preventDefault()
              const next = operationInput.trim()
              setOperationInput(next)
              if (next === correlationId) void load()
              else setCorrelationId(next)
            }}>
              <Input aria-label="操作 ID" value={operationInput} onChange={(e) => setOperationInput(e.target.value)} spellCheck={false}
                className="w-full font-mono sm:w-80" placeholder="upgrade_operation_id" />
              <Button type="submit" disabled={loading}>查询</Button>
            </form>
          )}
          <Select
            value={action}
            onChange={(e) => setAction(e.target.value)}
            scale="sm"
            aria-label="审计动作"
          >
            <option value="">全部动作</option>
            {AUDIT_ACTIONS.map((item) => (
              <option key={item} value={item}>
                {item}
              </option>
            ))}
          </Select>
          <Select
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            scale="sm"
            aria-label="审计状态"
          >
            <option value="">全部状态</option>
            {AUDIT_STATUSES.map((item) => (
              <option key={item} value={item}>
                {item}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex items-center gap-2">
          {scope === "session" && (
            <span className="break-all font-mono text-sm text-foreground">{sessionId}</span>
          )}
          <Button
            onClick={() => load()}
            disabled={loading}
            size="sm"
          >
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
          </Button>
        </div>
        <div className="flex w-full flex-wrap items-center gap-3 text-sm text-muted-foreground">
          <label className="flex min-w-0 flex-wrap items-center gap-2">开始时间<Input type="datetime-local" aria-label="开始时间" value={since} onChange={(e) => setSince(e.target.value)} /></label>
          <label className="flex min-w-0 flex-wrap items-center gap-2">结束时间<Input type="datetime-local" aria-label="结束时间" value={until} onChange={(e) => setUntil(e.target.value)} /></label>
          <label className="flex items-center gap-2"><input type="checkbox" checked={!hideObserve} onChange={(e) => setHideObserve(!e.target.checked)} />显示升级进行中的轮询</label>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        <p className="mb-4 max-w-4xl text-sm leading-6 text-muted-foreground">
          普通用户仅查看本人记录；管理员可查看全局，凭据审计另按授权范围可见。每页 50 条，已加载 {events.length} 条。
          {scope === "credential" && " 按凭据查询覆盖创建、写入版本、激活、授权、验证和吊销；部署时使用凭据、从 Secret 导入记在部署会话下，请按「该目标」或「本会话」查询。"}
        </p>
        {hint && <p className="mb-3 text-sm text-muted-foreground">{hint}</p>}
        {error && (
          <p role="alert" className="status-error mb-3">
            {error}
          </p>
        )}
        {loading && <p role="status" className="mb-3 text-sm text-muted-foreground">正在查询审计记录…</p>}
        {!error && !hint && events.length === 0 && !loading && (
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
                    e.status === "success"
                      ? "bg-success/15 text-success"
                      : ["running", "executing", "accepted", "observing"].includes(e.status || "")
                        ? "bg-warning/15 text-warning"
                        : "bg-destructive/15 text-destructive"
                  }`}
                >
                  status={e.status || "unknown"}
                </span>
                <span className="ml-auto text-sm text-muted-foreground">
                  {fmtTime(e.started_at)}
                </span>
              </div>
              <div className="mt-3 grid min-w-0 gap-x-5 gap-y-1.5 text-sm leading-6 text-muted-foreground sm:grid-cols-2">
                <span className="break-all"><strong className="font-medium text-foreground/80">审计 ID</strong> {e.id}</span>
                <span><strong className="font-medium text-foreground/80">耗时</strong> {duration(e)}</span>
                <span className="break-all"><strong className="font-medium text-foreground/80">输入 / 输出</strong> {e.bytes_in ?? 0} / {e.bytes_out ?? 0} B</span>
                {e.correlation_id && (
                  <button className="min-w-0 break-all text-left font-mono text-primary underline underline-offset-2" onClick={() => {
                    setOperationInput(e.correlation_id!)
                    setCorrelationId(e.correlation_id!)
                    setScope("operation")
                  }} title="查询本操作的审计记录">操作 {e.correlation_id}</button>
                )}
                <span className="min-w-0 break-all"><strong className="font-medium text-foreground/80">用户</strong> {e.user_id || "—"}</span>
                <span className="min-w-0 break-all"><strong className="font-medium text-foreground/80">Token</strong> {e.token_id || "—"}</span>
                <span className="min-w-0 break-all"><strong className="font-medium text-foreground/80">目标</strong> {e.cluster || "—"}/{e.instance || "—"}</span>
                {e.session && <span className="min-w-0 break-all font-mono"><strong className="font-sans font-medium text-foreground/80">Session</strong> {e.session}</span>}
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
        {events.length > 0 && (
          <div className="mt-4 flex justify-center">
            {hasMore ? <Button disabled={loading} onClick={() => load(events[events.length - 1].id)}>加载更早记录</Button>
              : <p className="text-sm text-muted-foreground">已加载全部匹配记录</p>}
          </div>
        )}
      </div>
    </div>
  )
}
