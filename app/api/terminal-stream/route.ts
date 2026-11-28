import { TOOLS } from "@/lib/gateway"
import { gatewayPolicyResponse, readAllowedAuth } from "@/lib/gateway-policy"
import {
  defaultGatewayWsPool,
  extractTerminalToolPayload,
  GatewayWsPoolError,
} from "@/lib/server/gateway-ws-pool"
import { rejectCrossOriginRead } from "@/lib/server/same-origin"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const DEFAULT_WAIT_MS = 8_000
const MIN_WAIT_MS = 500
const MAX_WAIT_MS = 10_000
const LEGACY_POLL_DELAY_MS = 250

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve()
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort)
      resolve()
    }, ms)
    const onAbort = () => {
      clearTimeout(timer)
      resolve()
    }
    signal.addEventListener("abort", onAbort, { once: true })
  })
}

export async function GET(req: Request) {
  const rejected = rejectCrossOriginRead(req)
  if (rejected) return rejected

  let gateway: string
  let token: string
  try {
    ;({ gateway, token } = readAllowedAuth(req))
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
  if (!gateway || !token) {
    return Response.json({ error: "缺少 gateway 地址或 token" }, { status: 401 })
  }

  const params = new URL(req.url).searchParams
  const cluster = (params.get("cluster") || "").trim()
  const instance = (params.get("instance") || "").trim()
  const sessionId = (params.get("session_id") || "").trim()
  const term = (params.get("term") || "").trim()
  const rawCursor = Number(params.get("cursor") || "0")
  const rawWait = Number(params.get("wait_ms") || String(DEFAULT_WAIT_MS))

  if (!ID_PATTERN.test(cluster) || cluster.includes("..") || !ID_PATTERN.test(instance) || instance.includes("..")) {
    return Response.json({ error: "缺少或无效的 cluster / instance" }, { status: 400 })
  }
  if (!ID_PATTERN.test(sessionId) || sessionId.includes("..")) {
    return Response.json({ error: "需要有效的操作 session_id" }, { status: 400 })
  }
  if (!ID_PATTERN.test(term) || term.includes("..")) {
    return Response.json({ error: "缺少有效的 term 句柄" }, { status: 400 })
  }
  if (!Number.isFinite(rawCursor) || rawCursor < 0) {
    return Response.json({ error: "cursor 必须为非负整数" }, { status: 400 })
  }

  let cursor = Math.floor(rawCursor)
  const waitMs = Number.isFinite(rawWait)
    ? Math.max(MIN_WAIT_MS, Math.min(MAX_WAIT_MS, Math.floor(rawWait)))
    : DEFAULT_WAIT_MS

  const encoder = new TextEncoder()
  const abort = new AbortController()
  const onReqAbort = () => abort.abort()
  req.signal.addEventListener("abort", onReqAbort, { once: true })
  if (req.signal.aborted) abort.abort()

  const stream = new ReadableStream({
    async start(controller) {
      let closed = false
      const send = (obj: unknown) => {
        if (closed || abort.signal.aborted) return
        try {
          controller.enqueue(encoder.encode(JSON.stringify(obj) + "\n"))
        } catch {
          closed = true
        }
      }
      const finish = () => {
        if (closed) return
        closed = true
        req.signal.removeEventListener("abort", onReqAbort)
        try {
          controller.close()
        } catch {
          /* noop */
        }
      }

      let featuresSent = false
      try {
        while (!abort.signal.aborted && !closed) {
          const startedAt = Date.now()
          const raw = await defaultGatewayWsPool.callTool(
            { gateway, token, cluster, instance },
            TOOLS.terminal,
            {
              session_id: sessionId,
              op: "read",
              term,
              cursor,
              wait_ms: waitMs,
            },
            { signal: abort.signal, timeoutMs: waitMs + 10_000 },
          )
          if (abort.signal.aborted || closed) break

          const payload = extractTerminalToolPayload(raw)
          if (payload.features && !featuresSent) {
            featuresSent = true
            send({ type: "features", features: payload.features })
          }
          if (payload.truncated) {
            send({ type: "truncated" })
          }
          if (typeof payload.cursor === "number") {
            cursor = payload.cursor
          }
          if (payload.data) {
            send({ type: "data", data: payload.data, cursor })
          }
          if (payload.alive === false) {
            send({ type: "exit", exit: payload.exit ?? 0, cursor })
            break
          }
          // 旧 Agent 不支持 wait_ms 时会立刻返回空数据；这里节流 250ms 避免空转打爆。
          if (!payload.data && !payload.features?.long_poll && Date.now() - startedAt < LEGACY_POLL_DELAY_MS) {
            await sleep(LEGACY_POLL_DELAY_MS, abort.signal)
          }
        }
      } catch (err) {
        if (!abort.signal.aborted && !closed) {
          const message = err instanceof Error ? err.message : "终端流断开"
          const status = err instanceof GatewayWsPoolError ? err.status : undefined
          const code = err instanceof GatewayWsPoolError ? err.code : undefined
          send({
            type: "error",
            error: message,
            ...(status !== undefined ? { status } : {}),
            ...(code !== undefined ? { code } : {}),
          })
        }
      } finally {
        finish()
      }
    },
    cancel() {
      abort.abort()
    },
  })

  return new Response(stream, {
    headers: {
      "Content-Type": "application/x-ndjson; charset=utf-8",
      "Cache-Control": "no-store, no-cache, no-transform",
      "X-Content-Type-Options": "nosniff",
      "X-Accel-Buffering": "no",
      Connection: "keep-alive",
    },
  })
}
