import { createHash } from "node:crypto"
import WebSocket from "ws"
import { toWsBase } from "../gateway.ts"
import { gatewayAuthMessage } from "../gateway-auth.ts"
import { readGatewayUpgradeRejection } from "./gateway-upgrade.ts"

export class GatewayWsPoolError extends Error {
  status: number
  code?: number

  constructor(message: string, status = 502, code?: number) {
    super(message)
    this.name = "GatewayWsPoolError"
    this.status = status
    this.code = code
  }
}

export interface GatewayWsPoolKey {
  gateway: string
  token: string
  cluster: string
  instance: string
}

export interface GatewayWsPoolOptions {
  maxConnections?: number
  idleTimeoutMs?: number
  maxLifetimeMs?: number
  callTimeoutMs?: number
  initTimeoutMs?: number
  createWebSocket?: (url: string, headers: Record<string, string>) => WebSocket
}

interface PendingCall {
  resolve: (result: unknown) => void
  reject: (err: Error) => void
  timer: ReturnType<typeof setTimeout>
}

interface PooledConnection {
  key: string
  sessionHash: string
  ws: WebSocket
  ready: Promise<void>
  nextId: number
  pending: Map<number, PendingCall>
  idleTimer: ReturnType<typeof setTimeout> | null
  closed: boolean
  draining: boolean
  createdAt: number
  touchedAt: number
}

const DEFAULT_MAX_CONNECTIONS = 200
const DEFAULT_IDLE_TIMEOUT_MS = 60_000
const DEFAULT_MAX_LIFETIME_MS = 5 * 60_000
const DEFAULT_CALL_TIMEOUT_MS = 20_000
const DEFAULT_INIT_TIMEOUT_MS = 15_000

export function poolKeyHash(scope: GatewayWsPoolKey): string {
  return createHash("sha256")
    .update(JSON.stringify([scope.gateway, scope.token, scope.cluster, scope.instance]))
    .digest("hex")
}

export function sessionScopeHash(gateway: string, token: string): string {
  return createHash("sha256")
    .update(JSON.stringify([gateway, token]))
    .digest("hex")
}

export class GatewayWsPool {
  private readonly connections = new Map<string, PooledConnection>()
  private readonly drainingConnections = new Set<PooledConnection>()
  private readonly maxConnections: number
  private readonly idleTimeoutMs: number
  private readonly maxLifetimeMs: number
  private readonly callTimeoutMs: number
  private readonly initTimeoutMs: number
  private readonly createWebSocket: (url: string, headers: Record<string, string>) => WebSocket

  constructor(options: GatewayWsPoolOptions = {}) {
    this.maxConnections = options.maxConnections ?? DEFAULT_MAX_CONNECTIONS
    this.idleTimeoutMs = options.idleTimeoutMs ?? DEFAULT_IDLE_TIMEOUT_MS
    this.maxLifetimeMs = options.maxLifetimeMs ?? DEFAULT_MAX_LIFETIME_MS
    this.callTimeoutMs = options.callTimeoutMs ?? DEFAULT_CALL_TIMEOUT_MS
    this.initTimeoutMs = options.initTimeoutMs ?? DEFAULT_INIT_TIMEOUT_MS
    this.createWebSocket =
      options.createWebSocket ??
      ((url, headers) => new WebSocket(url, { headers, followRedirects: false }))
  }

  get size(): number {
    return this.connections.size
  }

  get drainingSize(): number {
    return this.drainingConnections.size
  }

  // 登出或收到 401 时立即销毁该用户 token 对应的所有池化连接（含正在优雅排空的旧连接）。
  evictBySession(gateway: string, token: string): number {
    if (!gateway || !token) return 0
    const targetHash = sessionScopeHash(gateway, token)
    let evicted = 0
    for (const conn of [
      ...Array.from(this.connections.values()),
      ...Array.from(this.drainingConnections.values()),
    ]) {
      if (conn.sessionHash === targetHash) {
        this.teardown(conn, new GatewayWsPoolError("登录会话已断开", 401, -32001))
        evicted += 1
      }
    }
    return evicted
  }

  async callTool(
    scope: GatewayWsPoolKey,
    tool: string,
    args: Record<string, unknown>,
    options: { signal?: AbortSignal; timeoutMs?: number } = {},
  ): Promise<unknown> {
    if (options.signal?.aborted) {
      throw new GatewayWsPoolError("请求已取消", 499)
    }
    const conn = await this.acquire(scope)
    if (options.signal?.aborted) {
      this.scheduleIdleIfEmpty(conn)
      throw new GatewayWsPoolError("请求已取消", 499)
    }
    return this.sendCall(conn, tool, args, options)
  }

  closeAll(): void {
    for (const conn of [
      ...Array.from(this.connections.values()),
      ...Array.from(this.drainingConnections.values()),
    ]) {
      this.teardown(conn, new GatewayWsPoolError("连接池已关闭", 503))
    }
  }

  // 超过 maxLifetimeMs 的连接优雅退位：从活跃池摘除、新请求建新 WS，在途长轮询等自然结束后静默关闭，避免周期性 503 闪断。
  private retireConnection(conn: PooledConnection): void {
    if (conn.closed || conn.draining) return
    conn.draining = true
    if (this.connections.get(conn.key) === conn) {
      this.connections.delete(conn.key)
    }
    if (conn.pending.size === 0) {
      this.teardown(conn, new GatewayWsPoolError("连接达到最大复用周期，重新握手", 503))
      return
    }
    this.clearIdleTimer(conn)
    this.drainingConnections.add(conn)
  }

  private async acquire(scope: GatewayWsPoolKey): Promise<PooledConnection> {
    const key = poolKeyHash(scope)
    const existing = this.connections.get(key)
    const now = Date.now()
    if (
      existing &&
      !existing.closed &&
      !existing.draining &&
      existing.ws.readyState !== WebSocket.CLOSING &&
      existing.ws.readyState !== WebSocket.CLOSED
    ) {
      const expired = this.maxLifetimeMs > 0 && now - existing.createdAt >= this.maxLifetimeMs
      if (expired) {
        this.retireConnection(existing)
      } else {
        this.clearIdleTimer(existing)
        existing.touchedAt = now
        await existing.ready
        if (!existing.closed && !existing.draining && existing.ws.readyState === WebSocket.OPEN) {
          return existing
        }
      }
    }

    if (this.connections.size >= this.maxConnections) {
      this.evictOneIdle()
    }
    if (this.connections.size >= this.maxConnections) {
      throw new GatewayWsPoolError("终端连接池容量已满", 429)
    }

    const conn = this.openConnection(key, scope)
    this.connections.set(key, conn)
    try {
      await conn.ready
      return conn
    } catch (err) {
      if (this.connections.get(key) === conn) {
        this.connections.delete(key)
      }
      throw err
    }
  }

  private evictOneIdle(): void {
    let oldest: PooledConnection | null = null
    for (const conn of this.connections.values()) {
      if (conn.pending.size > 0) continue
      if (!oldest || conn.touchedAt < oldest.touchedAt) {
        oldest = conn
      }
    }
    if (oldest) {
      this.teardown(oldest, new GatewayWsPoolError("连接池回收空闲连接", 503))
    }
  }

  private openConnection(key: string, scope: GatewayWsPoolKey): PooledConnection {
    const wsUrl = `${toWsBase(scope.gateway)}/v1/rpc?cluster=${encodeURIComponent(
      scope.cluster,
    )}&instance=${encodeURIComponent(scope.instance)}`

    const ws = this.createWebSocket(wsUrl, { Authorization: `Bearer ${scope.token}` })
    let resolveReady!: () => void
    let rejectReady!: (err: Error) => void
    let readySettled = false
    const ready = new Promise<void>((resolve, reject) => {
      resolveReady = () => {
        if (readySettled) return
        readySettled = true
        resolve()
      }
      rejectReady = (err: Error) => {
        if (readySettled) return
        readySettled = true
        reject(err)
      }
    })
    // Avoid unhandled rejection if the connection fails before anyone awaits ready.
    ready.catch(() => {})

    const now = Date.now()
    const conn: PooledConnection = {
      key,
      sessionHash: sessionScopeHash(scope.gateway, scope.token),
      ws,
      ready,
      nextId: 2,
      pending: new Map(),
      idleTimer: null,
      closed: false,
      draining: false,
      createdAt: now,
      touchedAt: now,
    }

    const initTimer = setTimeout(() => {
      const err = new GatewayWsPoolError("Gateway 终端连接初始化超时", 504)
      rejectReady(err)
      this.teardown(conn, err)
    }, this.initTimeoutMs)

    const clearInitTimer = () => clearTimeout(initTimer)

    ws.on("open", () => {
      if (conn.closed) {
        clearInitTimer()
        try { ws.close() } catch { /* noop */ }
        return
      }
      try {
        ws.send(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }))
      } catch (err) {
        clearInitTimer()
        const wrapped = new GatewayWsPoolError(`初始化发送失败: ${(err as Error).message}`, 502)
        rejectReady(wrapped)
        this.teardown(conn, wrapped)
      }
    })

    ws.on("message", (raw: WebSocket.RawData) => {
      let msg: { id?: unknown; result?: { isError?: boolean; content?: Array<{ text?: string }> }; error?: { message?: string; code?: number } }
      try {
        msg = JSON.parse(raw.toString())
      } catch {
        return
      }
      if (msg.id === 1 && !readySettled) {
        clearInitTimer()
        if (msg.error) {
          const err = new GatewayWsPoolError(msg.error.message || "initialize 失败", 502, msg.error.code)
          rejectReady(err)
          this.teardown(conn, err)
          return
        }
        conn.touchedAt = Date.now()
        resolveReady()
        return
      }
      if (typeof msg.id === "number") {
        const entry = conn.pending.get(msg.id)
        if (!entry) return
        conn.pending.delete(msg.id)
        clearTimeout(entry.timer)
        conn.touchedAt = Date.now()
        if (msg.error) {
          const code = msg.error.code
          const rawMsg = msg.error.message || "工具调用失败"
          if (code === -32001) {
            const authMsg = gatewayAuthMessage(rawMsg) || "登录已失效，请重新登录"
            const authErr = new GatewayWsPoolError(authMsg, 401, code)
            entry.reject(authErr)
            // token 被吊销、过期或用户被禁用：立即销毁连接，绝不继续复用。
            this.teardown(conn, authErr)
            return
          }
          if (code === -32003) {
            const err = new GatewayWsPoolError(rawMsg, 403, code)
            entry.reject(err)
            // 目标授权被撤回（forbidden: ...）时销毁连接；敏感词/命令规则拦截则保留连接。
            if (/^forbidden\b/i.test(rawMsg)) {
              this.teardown(conn, err)
              return
            }
          } else if (code === -32004) {
            // Gateway -32004 表示目标 Agent 暂时离线（可随重连恢复），映射 503 而非 404。
            entry.reject(new GatewayWsPoolError(rawMsg, 503, code))
          } else {
            entry.reject(new GatewayWsPoolError(rawMsg, 502, code))
          }
        } else if (msg.result?.isError) {
          const text = msg.result.content?.map((item) => item.text || "").join("") || "工具执行失败"
          entry.reject(new GatewayWsPoolError(text, 502))
        } else {
          entry.resolve(msg.result)
        }
        this.scheduleIdleIfEmpty(conn)
      }
    })

    ws.on("unexpected-response", async (_req, response) => {
      clearInitTimer()
      const rejection = await readGatewayUpgradeRejection(response)
      const err = new GatewayWsPoolError(rejection.message, rejection.status, rejection.code)
      rejectReady(err)
      this.teardown(conn, err)
    })

    ws.on("error", (err: Error) => {
      clearInitTimer()
      const wrapped = new GatewayWsPoolError(`Gateway 连接错误: ${err.message}`, 502)
      rejectReady(wrapped)
      this.teardown(conn, wrapped)
    })

    ws.on("close", () => {
      clearInitTimer()
      const wrapped = new GatewayWsPoolError("Gateway 终端连接已断开", 502)
      rejectReady(wrapped)
      this.teardown(conn, wrapped)
    })

    return conn
  }

  private sendCall(
    conn: PooledConnection,
    tool: string,
    args: Record<string, unknown>,
    options: { signal?: AbortSignal; timeoutMs?: number },
  ): Promise<unknown> {
    if (conn.closed || conn.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new GatewayWsPoolError("Gateway 终端连接不可用", 502))
    }
    this.clearIdleTimer(conn)
    const id = conn.nextId++
    const timeoutMs = options.timeoutMs ?? this.callTimeoutMs

    return new Promise<unknown>((resolve, reject) => {
      const cleanupAbort = () => {
        options.signal?.removeEventListener("abort", onAbort)
      }
      const onAbort = () => {
        const entry = conn.pending.get(id)
        if (!entry) return
        conn.pending.delete(id)
        clearTimeout(entry.timer)
        cleanupAbort()
        this.scheduleIdleIfEmpty(conn)
        reject(new GatewayWsPoolError("请求已取消", 499))
      }

      const timer = setTimeout(() => {
        const entry = conn.pending.get(id)
        if (!entry) return
        conn.pending.delete(id)
        cleanupAbort()
        this.scheduleIdleIfEmpty(conn)
        reject(new GatewayWsPoolError("终端调用超时", 504))
      }, timeoutMs)

      conn.pending.set(id, {
        resolve: (value) => {
          cleanupAbort()
          resolve(value)
        },
        reject: (err) => {
          cleanupAbort()
          reject(err)
        },
        timer,
      })

      options.signal?.addEventListener("abort", onAbort, { once: true })

      try {
        conn.ws.send(
          JSON.stringify({
            jsonrpc: "2.0",
            id,
            method: "tools/call",
            params: { name: tool, arguments: args },
          }),
        )
      } catch (err) {
        conn.pending.delete(id)
        clearTimeout(timer)
        cleanupAbort()
        const wrapped = new GatewayWsPoolError(`发送终端请求失败: ${(err as Error).message}`, 502)
        this.teardown(conn, wrapped)
        reject(wrapped)
      }
    })
  }

  private clearIdleTimer(conn: PooledConnection): void {
    if (conn.idleTimer) {
      clearTimeout(conn.idleTimer)
      conn.idleTimer = null
    }
  }

  private scheduleIdleIfEmpty(conn: PooledConnection): void {
    if (conn.closed || conn.pending.size > 0) return
    if (conn.draining) {
      this.teardown(conn, new GatewayWsPoolError("连接轮换排空完成", 503))
      return
    }
    this.clearIdleTimer(conn)
    conn.idleTimer = setTimeout(() => {
      if (conn.pending.size === 0) {
        this.teardown(conn, new GatewayWsPoolError("空闲连接关闭", 503))
      }
    }, this.idleTimeoutMs)
  }

  private teardown(conn: PooledConnection, reason: Error): void {
    if (conn.closed) return
    conn.closed = true
    this.clearIdleTimer(conn)
    this.drainingConnections.delete(conn)
    if (this.connections.get(conn.key) === conn) {
      this.connections.delete(conn.key)
    }
    for (const [id, entry] of conn.pending) {
      conn.pending.delete(id)
      clearTimeout(entry.timer)
      entry.reject(reason)
    }
    try {
      if (conn.ws.readyState === WebSocket.OPEN || conn.ws.readyState === WebSocket.CONNECTING) {
        conn.ws.terminate()
      }
    } catch {
      /* noop */
    }
  }
}

export interface TerminalToolPayload {
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
  features?: {
    long_poll?: boolean
    shell_integration?: string
    input_source?: boolean
  }
}

export function extractTerminalToolPayload(result: unknown): TerminalToolPayload {
  const content = (result as { content?: Array<{ text?: string }> } | null)?.content
  const text = Array.isArray(content)
    ? content.map((item) => (typeof item?.text === "string" ? item.text : "")).join("")
    : ""
  if (!text) {
    throw new GatewayWsPoolError("终端返回了空响应", 502)
  }
  try {
    return JSON.parse(text) as TerminalToolPayload
  } catch {
    throw new GatewayWsPoolError(text || "终端返回了无法解析的结果", 502)
  }
}

export const defaultGatewayWsPool = new GatewayWsPool()
