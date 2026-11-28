import { consoleFetch } from "./network-error.ts"

export interface TerminalFeatures {
  long_poll?: boolean
  shell_integration?: string
  input_source?: boolean
}

export interface TerminalPayload {
  ok?: boolean
  term?: string
  cursor?: number
  data?: string
  alive?: boolean
  exit?: number
  truncated?: boolean
  cols?: number
  rows?: number
  cwd?: string
  features?: TerminalFeatures
}

export type TerminalStreamEvent =
  | { type: "data"; data: string; cursor: number }
  | { type: "truncated" }
  | { type: "exit"; exit: number; cursor: number }
  | { type: "features"; features: TerminalFeatures }
  | { type: "error"; error: string; status?: number; code?: number }

export class TerminalTransportError extends Error {
  status?: number
  code?: number

  constructor(message: string, status?: number, code?: number) {
    super(message)
    this.name = "TerminalTransportError"
    this.status = status
    this.code = code
  }
}

export const TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS = 8
export const TERMINAL_RECONNECT_DELAYS_MS = [1000, 2000, 4000, 8000, 15_000] as const

export interface TerminalReconnectDecision {
  retry: boolean
  action: "resume" | "reopen" | "stop"
  reason: "auth" | "forbidden" | "session_gone" | "transient"
}

export function nextTerminalReconnectDelayMs(attempt: number): number {
  if (!Number.isFinite(attempt) || attempt <= 0) return TERMINAL_RECONNECT_DELAYS_MS[0]
  const idx = Math.min(Math.floor(attempt), TERMINAL_RECONNECT_DELAYS_MS.length - 1)
  return TERMINAL_RECONNECT_DELAYS_MS[idx]
}

// 判定终端断连是否可自动恢复：
// - 401/403/400 或鉴权/权限拒绝一律 fail-closed 停止重连；
// - 远端 PTY 句柄已失效/不存在（如 Agent 重启或 30 分钟空闲回收）改为重开新会话；
// - 网络抖动、502/503/504、Gateway 重启等瞬断按原 term+cursor 自动续连。
export function classifyTerminalDisconnect(err: unknown): TerminalReconnectDecision {
  const rec = (err && typeof err === "object" ? err : {}) as {
    status?: number
    code?: number
    message?: string
  }
  const status = typeof rec.status === "number" ? rec.status : undefined
  const code = typeof rec.code === "number" ? rec.code : undefined
  const message =
    typeof rec.message === "string"
      ? rec.message
      : err instanceof Error
        ? err.message
        : typeof err === "string"
          ? err
          : ""

  if (
    status === 401 ||
    code === -32001 ||
    /gateway_auth_|未登录|登录会话|令牌.*(失效|过期|吊销)|unauthorized/i.test(message)
  ) {
    return { retry: false, action: "stop", reason: "auth" }
  }
  if (
    status === 403 ||
    code === -32003 ||
    status === 400 ||
    status === 422 ||
    /forbidden|无权|未授权|permission denied|cross-origin|敏感词|blocked_command|unknown doops action|unknown tool over ws/i.test(
      message,
    )
  ) {
    return { retry: false, action: "stop", reason: "forbidden" }
  }
  if (code === -32004 || /target offline|agent offline|目标.*离线/i.test(message)) {
    return { retry: true, action: "resume", reason: "transient" }
  }
  if (
    /terminal not found|already closed|terminal is closed|terminal process has exited|未知终端|终端.*不存在|not owned/i.test(
      message,
    )
  ) {
    return { retry: true, action: "reopen", reason: "session_gone" }
  }
  return { retry: true, action: "resume", reason: "transient" }
}

export interface TerminalTargetRef {
  cluster: string
  instance: string
}

export type TerminalFetch = typeof fetch

export function supportsTerminalLongPoll(payload: TerminalPayload | null | undefined): boolean {
  return payload?.features?.long_poll === true
}

export type TerminalAiAction = "ask" | "explain" | "fix" | "followup"

export interface TerminalAiCommand {
  command: string
  description?: string
}

export interface TerminalAiUsage {
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
}

export interface TerminalAiResponse {
  ok: boolean
  model: string
  provider: string
  explanation: string
  commands: TerminalAiCommand[]
  usage?: TerminalAiUsage
}

export class TerminalAiError extends Error {
  status?: number
  code?: string
  nextAction?: string

  constructor(message: string, status?: number, code?: string, nextAction?: string) {
    super(message)
    this.name = "TerminalAiError"
    this.status = status
    this.code = code
    this.nextAction = nextAction
  }
}

// 终端单次 AI 调用：直接经 BFF -> Gateway /v1/terminal/ai 调用目标绑定的 modelProvider，
// 不走多轮 AI 助手（do-agent），不污染会话历史。
export async function requestTerminalAi(
  opts: {
    target: TerminalTargetRef
    sessionId: string
    question: string
    action?: TerminalAiAction
    cwd?: string
    contextPrompt?: string
    signal?: AbortSignal
    fetchImpl?: TerminalFetch
  },
): Promise<TerminalAiResponse> {
  const fetcher = opts.fetchImpl ?? consoleFetch
  const res = await fetcher("/api/terminal-ai", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      cluster: opts.target.cluster,
      instance: opts.target.instance,
      session_id: opts.sessionId,
      question: opts.question,
      action: opts.action ?? "ask",
      ...(opts.cwd ? { cwd: opts.cwd } : {}),
      ...(opts.contextPrompt ? { context_prompt: opts.contextPrompt } : {}),
    }),
    cache: "no-store",
    signal: opts.signal,
  })
  let payload: (Partial<TerminalAiResponse> & { error?: string; message?: string; code?: string; next_action?: string }) | null = null
  try {
    payload = (await res.json()) as Partial<TerminalAiResponse> & {
      error?: string
      message?: string
      code?: string
      next_action?: string
    }
  } catch {
    payload = null
  }
  if (!res.ok) {
    const msg = payload?.message || payload?.error || `单次 AI 调用失败 (${res.status})`
    throw new TerminalAiError(msg, res.status, payload?.code, payload?.next_action)
  }
  if (!payload || typeof payload !== "object") {
    throw new TerminalAiError("AI 返回了无法解析的响应", 502)
  }
  return {
    ok: payload.ok !== false,
    model: typeof payload.model === "string" ? payload.model : "",
    provider: typeof payload.provider === "string" ? payload.provider : "",
    explanation: typeof payload.explanation === "string" ? payload.explanation : "",
    commands: Array.isArray(payload.commands)
      ? payload.commands.filter(
          (item): item is TerminalAiCommand =>
            Boolean(item) && typeof item === "object" && typeof item.command === "string" && item.command.trim().length > 0,
        )
      : [],
    usage: payload.usage,
  }
}

export async function terminalRequest(
  target: TerminalTargetRef,
  sessionId: string,
  args: Record<string, unknown>,
  options: { signal?: AbortSignal; fetchImpl?: TerminalFetch } = {},
): Promise<TerminalPayload> {
  const fetcher = options.fetchImpl ?? consoleFetch
  const res = await fetcher("/api/terminal-input", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      cluster: target.cluster,
      instance: target.instance,
      session_id: sessionId,
      ...args,
    }),
    signal: options.signal,
  })
  let payload: (TerminalPayload & { error?: string; code?: number }) | null = null
  try {
    payload = (await res.json()) as TerminalPayload & { error?: string; code?: number }
  } catch {
    payload = null
  }
  if (!res.ok) {
    throw new TerminalTransportError(payload?.error || `终端调用失败 (${res.status})`, res.status, payload?.code)
  }
  if (!payload || typeof payload !== "object") {
    throw new TerminalTransportError("终端返回了无法解析的结果", 502)
  }
  return payload
}

export async function streamTerminalOutput(
  opts: {
    target: TerminalTargetRef
    sessionId: string
    term: string
    cursor: number
    waitMs?: number
    signal?: AbortSignal
    fetchImpl?: TerminalFetch
  },
  onEvent: (ev: TerminalStreamEvent) => void,
): Promise<void> {
  const fetcher = opts.fetchImpl ?? consoleFetch
  const qs = new URLSearchParams({
    cluster: opts.target.cluster,
    instance: opts.target.instance,
    session_id: opts.sessionId,
    term: opts.term,
    cursor: String(Math.max(0, Math.floor(opts.cursor))),
  })
  if (opts.waitMs !== undefined) {
    qs.set("wait_ms", String(Math.max(500, Math.min(10_000, Math.floor(opts.waitMs)))))
  }
  const res = await fetcher(`/api/terminal-stream?${qs.toString()}`, {
    method: "GET",
    cache: "no-store",
    signal: opts.signal,
  })
  if (!res.ok) {
    let message = `终端流连接失败 (${res.status})`
    let code: number | undefined
    try {
      const data = (await res.json()) as { error?: string; code?: number }
      if (data?.error) message = data.error
      if (typeof data?.code === "number") code = data.code
    } catch {
      /* noop */
    }
    throw new TerminalTransportError(message, res.status, code)
  }
  if (!res.body) {
    throw new TerminalTransportError("终端流响应缺少数据体", 502)
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""

  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split("\n")
      buffer = lines.pop() ?? ""
      for (const line of lines) {
        const trimmed = line.trim()
        if (!trimmed) continue
        let event: TerminalStreamEvent
        try {
          event = JSON.parse(trimmed) as TerminalStreamEvent
        } catch {
          continue
        }
        onEvent(event)
      }
    }
    const tail = (buffer + decoder.decode()).trim()
    if (tail) {
      try {
        onEvent(JSON.parse(tail) as TerminalStreamEvent)
      } catch {
        /* ignore trailing partial line */
      }
    }
  } finally {
    try {
      reader.releaseLock()
    } catch {
      /* noop */
    }
  }
}
