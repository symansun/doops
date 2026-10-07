"use client"

import type { Target, AuditEvent, AuditSummary } from "./gateway"

export type { Target, AuditEvent, AuditSummary }

export interface Session {
  gateway: string
  token?: string
  username?: string
  demo?: boolean
}

export type RpcEvent =
  | { type: "open" }
  | { type: "output"; data: string; session?: string; kind?: string }
  | { type: "result"; result: unknown }
  | { type: "error"; error: string; code?: number }
  | { type: "done" }

function authHeaders(s: Session): HeadersInit {
  void s
  return { "Content-Type": "application/json" }
}

export async function login(
  gateway: string,
  username: string,
  password: string,
): Promise<{ gateway: string; username: string }> {
  const res = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ gateway, username, password }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || "登录失败")
  return { gateway: data.gateway || gateway, username: data.username || username }
}

export async function restoreServerSession(): Promise<Session | null> {
  const res = await fetch("/api/session", { cache: "no-store" })
  const data = await res.json()
  if (!res.ok || !data.session?.gateway) return null
  return {
    gateway: data.session.gateway,
    username: data.session.username || undefined,
  }
}

export async function persistTokenSession(
  gateway: string,
  token: string,
  username?: string,
): Promise<Session> {
  const res = await fetch("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ gateway, token, username: username || "" }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || "保存会话失败")
  return { gateway: data.gateway || gateway, username: data.username || username }
}

export async function clearServerSession(): Promise<void> {
  const res = await fetch("/api/session", { method: "DELETE" })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.error || `断开连接失败 (${res.status})`)
  }
}

export async function fetchTargets(s: Session): Promise<Target[]> {
  if (s.demo) {
    const { DEMO_TARGETS } = await import("./demo")
    return DEMO_TARGETS
  }
  const res = await fetch("/api/targets", { headers: authHeaders(s) })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || "查询目标失败")
  return (data.targets || []) as Target[]
}

export async function fetchAudit(
  s: Session,
  params: Record<string, string>,
): Promise<{ events?: AuditEvent[]; summary?: AuditSummary; error?: string; status?: number }> {
  if (s.demo) {
    const { DEMO_AUDIT } = await import("./demo")
    const inst = params.instance
    const events = inst ? DEMO_AUDIT.filter((e) => !e.instance || e.instance === inst) : DEMO_AUDIT
    return { events, summary: summarizeAuditEvents(events) }
  }
  const qs = new URLSearchParams(params)
  const res = await fetch(`/api/audit?${qs.toString()}`, { headers: authHeaders(s) })
  const data = await res.json()
  if (!res.ok) return { error: data.error || "查询审计失败", status: res.status }
  return {
    events: (data.events || []) as AuditEvent[],
    summary: data.summary as AuditSummary | undefined,
  }
}

function summarizeAuditEvents(events: AuditEvent[]): AuditSummary {
  const summary: AuditSummary = {
    total: 0,
    running: 0,
    success: 0,
    failed: 0,
    bytes_in: 0,
    bytes_out: 0,
    status: {},
  }
  for (const event of events) {
    const status = event.status || "unknown"
    summary.total += 1
    summary.bytes_in += event.bytes_in || 0
    summary.bytes_out += event.bytes_out || 0
    summary.status![status] = (summary.status![status] || 0) + 1
    if (status === "running") summary.running += 1
    else if (status === "success") summary.success += 1
    else summary.failed += 1
  }
  return summary
}

// 调用一个 gateway 工具并流式返回事件
export async function callTool(
  s: Session,
  opts: {
    cluster: string
    instance: string
    tool: string
    arguments: Record<string, unknown>
    signal?: AbortSignal
    requiredProtocol?: "structuredSSH"
  },
  onEvent: (ev: RpcEvent) => void,
): Promise<void> {
  if (s.demo) {
    const { demoCallTool } = await import("./demo")
    await demoCallTool({ tool: opts.tool, arguments: opts.arguments, signal: opts.signal }, onEvent)
    return
  }
  const res = await fetch("/api/rpc", {
    method: "POST",
    headers: authHeaders(s),
    body: JSON.stringify({
      cluster: opts.cluster,
      instance: opts.instance,
      tool: opts.tool,
      arguments: opts.arguments,
      requiredProtocol: opts.requiredProtocol,
    }),
    signal: opts.signal,
  })

  if (!res.ok || !res.body) {
    let msg = `请求失败 (${res.status})`
    try {
      const data = await res.json()
      msg = data.error || msg
    } catch {
      /* noop */
    }
    onEvent({ type: "error", error: msg })
    onEvent({ type: "done" })
    throw new Error(msg)
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  let terminal = false
  let failure = ""
  function receive(line: string) {
    let event: RpcEvent
    try { event = JSON.parse(line) as RpcEvent } catch { throw new Error("运维服务返回了无效事件") }
    if (!event || !["open", "output", "result", "error", "done"].includes(event.type)) throw new Error("运维服务返回了未知事件")
    if (event.type === "result") {
      terminal = true
      if ((event.result as { isError?: boolean })?.isError) {
        failure = extractText(event.result) || "工具执行失败"
        onEvent({ type: "error", error: failure })
        return
      }
    }
    if (event.type === "error") {
      terminal = true
      failure = event.error || "工具执行失败"
    }
    onEvent(event)
  }

  try { while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let idx: number
    while ((idx = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, idx).trim()
      buffer = buffer.slice(idx + 1)
      if (!line) continue
      receive(line)
    }
  }
  buffer += decoder.decode()
  if (buffer.trim()) {
    receive(buffer.trim())
  }
  if (!terminal) throw new Error("连接已结束，未收到工具执行结果")
  if (failure) throw new Error(failure)
  } finally { reader.releaseLock() }
}

// 从工具结果里提取文本内容（MCP content 数组或字符串）
export function extractText(result: unknown): string {
  if (result == null) return ""
  if (typeof result === "string") return result
  const r = result as any
  if (Array.isArray(r.content)) {
    return r.content
      .map((c: any) => (typeof c === "string" ? c : c?.text ?? ""))
      .join("")
  }
  if (typeof r.text === "string") return r.text
  try {
    return JSON.stringify(r, null, 2)
  } catch {
    return String(result)
  }
}
