import WebSocket from "ws"
import { toWsBase } from "@/lib/gateway"
import {
  gatewayPolicyResponse,
  readAllowedAuth,
} from "@/lib/gateway-policy"
import { readGatewayUpgradeRejection } from "@/lib/server/gateway-upgrade"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

interface RpcBody {
  cluster: string
  instance: string
  tool?: string
  arguments: Record<string, unknown>
  requiredProtocol?: "structuredSSH"
  requestId?: string
  cancelRequestId?: string
}

const AGENT_PROMPT_SILENCE_MS = 45_000
const RPC_REQUEST_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/

interface ActiveRpcCall {
  gateway: string
  token: string
  cluster: string
  instance: string
  sessionId: string
  cancel: () => void
}

const activeRpcCalls = new Map<string, ActiveRpcCall>()

// 通过服务端 WS 桥接 gateway /v1/rpc。
// 浏览器 POST 工具调用，服务端注入 Bearer token，
// 并把 notifications/message 与最终结果以 NDJSON 行流式回传。
export async function POST(req: Request) {
  let gateway: string
  let token: string
  try {
    ;({ gateway, token } = readAllowedAuth(req, { requireGeneration: true }))
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
  if (!gateway || !token) {
    return Response.json({ error: "缺少 gateway 地址或 token" }, { status: 400 })
  }

  let body: RpcBody
  try {
    body = (await req.json()) as RpcBody
  } catch {
    return Response.json({ error: "请求体不是合法 JSON" }, { status: 400 })
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    return Response.json({ error: "请求体必须为对象" }, { status: 400 })
  }

  const { cluster, instance, tool, arguments: args } = body
  if (!cluster || !instance) {
    return Response.json({ error: "缺少 cluster / instance" }, { status: 400 })
  }
  if (!args || Array.isArray(args) || typeof args !== "object" ||
      typeof args.session_id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(args.session_id) || args.session_id.includes("..")) {
    return Response.json({ error: "需要有效的操作 session_id" }, { status: 400 })
  }
  if (body.requestId !== undefined &&
      (typeof body.requestId !== "string" || !RPC_REQUEST_ID_PATTERN.test(body.requestId) || body.cancelRequestId !== undefined)) {
    return Response.json({ error: "requestId 无效" }, { status: 400 })
  }
  if (body.cancelRequestId !== undefined &&
      (typeof body.cancelRequestId !== "string" || !RPC_REQUEST_ID_PATTERN.test(body.cancelRequestId))) {
    return Response.json({ error: "cancelRequestId 无效" }, { status: 400 })
  }
  if (body.cancelRequestId) {
    const active = activeRpcCalls.get(body.cancelRequestId)
    if (!active ||
        active.gateway !== gateway ||
        active.token !== token ||
        active.cluster !== cluster ||
        active.instance !== instance ||
        active.sessionId !== args.session_id) {
      return Response.json({ error: "RPC 不存在或已结束" }, { status: 409 })
    }
    active.cancel()
    return Response.json({
      request_id: body.cancelRequestId,
      cancel_requested: true,
    }, { headers: { "Cache-Control": "no-store" } })
  }
  if (!tool) {
    return Response.json({ error: "缺少 tool" }, { status: 400 })
  }
  if (!body.requestId) {
    return Response.json({ error: "缺少 requestId" }, { status: 400 })
  }

  const requestId = body.requestId
  if (activeRpcCalls.has(requestId)) {
    return Response.json({ error: "requestId 已被占用" }, { status: 409 })
  }

  const wsUrl = `${toWsBase(gateway)}/v1/rpc?cluster=${encodeURIComponent(
    cluster,
  )}&instance=${encodeURIComponent(instance)}`

  const encoder = new TextEncoder()
  let requestCancellation = () => {}
  let cancellationRequested = false
  const activeCall: ActiveRpcCall = {
    gateway,
    token,
    cluster,
    instance,
    sessionId: args.session_id,
    cancel: () => {
      cancellationRequested = true
      requestCancellation()
    },
  }
  activeRpcCalls.set(requestId, activeCall)

  const stream = new ReadableStream({
    start(controller) {
      let closed = false
      const send = (obj: unknown) => {
        if (closed) return
        try {
          controller.enqueue(encoder.encode(JSON.stringify(obj) + "\n"))
        } catch {
          // The browser may have canceled the response while the upstream RPC
          // is still being drained for terminal evidence.
        }
      }
      const finish = () => {
        if (closed) return
        send({ type: "done" })
        closed = true
        clearCancelTimer()
        clearSilenceTimer()
        activeRpcCalls.delete(requestId)
        req.signal.removeEventListener("abort", requestCancellation)
        try {
          controller.close()
        } catch {
          /* noop */
        }
      }

      let ws: WebSocket | undefined
      let dispatched = false
      let cancelSent = false
      let terminal = false
      let sawToolEvent = false
      let silenceTimer: ReturnType<typeof setTimeout> | null = null
      let cancelTimer: ReturnType<typeof setTimeout> | null = null
      const clearSilenceTimer = () => {
        if (silenceTimer) {
          clearTimeout(silenceTimer)
          silenceTimer = null
        }
      }
      const clearCancelTimer = () => {
        if (cancelTimer) {
          clearTimeout(cancelTimer)
          cancelTimer = null
        }
      }
      const armSilenceTimer = () => {
        clearSilenceTimer()
        if (tool !== "doops_agent_prompt") return
        silenceTimer = setTimeout(() => {
          if (closed || sawToolEvent) return
          requestCancellation()
        }, AGENT_PROMPT_SILENCE_MS)
      }
      const finishUncertain = () => {
        if (terminal || closed) return
        terminal = true
        send({ type: "error", error: "execution_outcome_unknown：远端终态未确认，请核对实际状态，不要直接重放原任务", code: -32008 })
        try { ws?.close() } catch { /* noop */ }
        finish()
      }
      const sendCancel = () => {
        if (!cancellationRequested || cancelSent || terminal || !dispatched || ws?.readyState !== WebSocket.OPEN) return
        cancelSent = true
        try {
          ws.send(JSON.stringify({ jsonrpc: "2.0", id: 3, method: "tools/cancel", params: { id: callId } }))
          cancelTimer = setTimeout(finishUncertain, 15_000)
        } catch {
          finishUncertain()
        }
      }
      requestCancellation = () => {
        if (terminal) return
        cancellationRequested = true
        clearSilenceTimer()
        if (!dispatched) {
          terminal = true
          send({ type: "error", error: "请求派发前已停止", code: -32007 })
          ws?.close()
          finish()
          return
        }
        sendCancel()
      }
      try {
        ws = new WebSocket(wsUrl, {
          headers: { Authorization: `Bearer ${token}` },
          followRedirects: false,
        })
      } catch (err) {
        send({ type: "error", error: `无法建立连接: ${(err as Error).message}` })
        finish()
        return
      }

      const callId = 2

      ws.on("open", () => {
        if (terminal) {
          ws?.close()
          return
        }
        send({ type: "open", request_id: requestId })
        ws.send(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }))
      })

      ws.on("message", (raw: WebSocket.RawData) => {
        let msg: any
        try {
          msg = JSON.parse(raw.toString())
        } catch {
          return
        }
        if (terminal) return

        // initialize 应答后发起工具调用
        if (msg.id === 1 && (msg.result !== undefined || msg.error !== undefined)) {
          if (msg.error) {
            terminal = true
            send({ type: "error", error: msg.error?.message || "initialize 失败" })
            ws.close()
            return
          }
          if (body.requiredProtocol) {
            const protocol = msg.result?.capabilities?.protocols?.[body.requiredProtocol]
            if (body.requiredProtocol !== "structuredSSH" || protocol?.schemaVersion !== "doops.sh/structured-ssh/v1" || protocol?.tool !== "doops_ssh" || protocol?.structuredArgv !== true) {
              terminal = true
              send({ type: "error", error: "目标 Agent 缺少 structuredSSH 协议能力，升级验收未通过" })
              ws.close()
              return
            }
          }
          ws.send(
            JSON.stringify({
              jsonrpc: "2.0",
              id: callId,
              method: "tools/call",
              params: { name: tool, arguments: args || {} },
            }),
          )
          dispatched = true
          armSilenceTimer()
          sendCancel()
          return
        }

        // 流式输出通知
        if (msg.method === "notifications/message") {
          if (msg.params?.sessionID !== args.session_id || msg.params?.request_id !== callId) return
          sawToolEvent = true
          clearSilenceTimer()
          const data = msg.params?.data ?? ""
          send({ type: "output", data, kind: msg.params?.kind, session: msg.params?.sessionID })
          return
        }

        // 最终结果
        if (msg.id === callId) {
          terminal = true
          clearCancelTimer()
          sawToolEvent = true
          clearSilenceTimer()
          if (msg.error) {
            send({ type: "error", error: msg.error?.message || "执行失败", code: msg.error?.code })
          } else if (msg.result?.isError) {
            send({ type: "error", error: msg.result.content?.map((item: { text?: string }) => item.text || "").join("") || "工具执行失败" })
          } else if (msg.result !== undefined) {
            send({ type: "result", result: msg.result })
          } else {
            send({ type: "error", error: "工具返回了无效结果" })
          }
          ws.close()
        }
      })

      // Gateway refuses login or target access before the upgrade with a plain HTTP
      // status. Nothing was dispatched, so this is a terminal error, never an unknown outcome.
      ws.on("unexpected-response", async (_request, response) => {
        clearSilenceTimer()
        if (!terminal) {
          terminal = true
          const rejection = await readGatewayUpgradeRejection(response)
          send({ type: "error", error: rejection.message, code: rejection.code })
        }
        // With this listener ws no longer aborts the handshake on its own.
        ws?.terminate()
        finish()
      })

      ws.on("error", (err: Error) => {
        clearSilenceTimer()
        if (!terminal && dispatched) finishUncertain()
        else if (!terminal) send({ type: "error", error: `连接错误: ${err.message}` })
        finish()
      })

      ws.on("close", () => {
        clearSilenceTimer()
        clearCancelTimer()
        if (!terminal && dispatched) finishUncertain()
        else if (!terminal) send({ type: "error", error: "Gateway 连接已结束，未收到执行结果" })
        finish()
      })

      // Cancellation is deliberately separate from closing the original RPC stream.
      req.signal.addEventListener("abort", requestCancellation, { once: true })
      if (req.signal.aborted) requestCancellation()
    },
    cancel() {
      activeCall.cancel()
    },
  })

  return new Response(stream, {
    headers: {
      "Content-Type": "application/x-ndjson; charset=utf-8",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
    },
  })
}
