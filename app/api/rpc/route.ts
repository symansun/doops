import WebSocket from "ws"
import { toWsBase } from "@/lib/gateway"
import {
  gatewayPolicyResponse,
  readAllowedAuth,
} from "@/lib/gateway-policy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

interface RpcBody {
  cluster: string
  instance: string
  tool: string
  arguments: Record<string, unknown>
  requiredProtocol?: "structuredSSH"
}

const AGENT_PROMPT_SILENCE_MS = 45_000

// 通过服务端 WS 桥接 gateway /v1/rpc。
// 浏览器 POST 工具调用，服务端注入 Bearer token，
// 并把 notifications/message 与最终结果以 NDJSON 行流式回传。
export async function POST(req: Request) {
  let gateway: string
  let token: string
  try {
    ;({ gateway, token } = readAllowedAuth(req))
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

  const { cluster, instance, tool, arguments: args } = body
  if (!cluster || !instance || !tool) {
    return Response.json({ error: "缺少 cluster / instance / tool" }, { status: 400 })
  }
  if (!args || Array.isArray(args) || typeof args !== "object" ||
      typeof args.session_id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(args.session_id) || args.session_id.includes("..")) {
    return Response.json({ error: "需要有效的操作 session_id" }, { status: 400 })
  }

  const wsUrl = `${toWsBase(gateway)}/v1/rpc?cluster=${encodeURIComponent(
    cluster,
  )}&instance=${encodeURIComponent(instance)}`

  const encoder = new TextEncoder()
  let cancelUpstream: (() => void) | undefined

  const stream = new ReadableStream({
    start(controller) {
      let closed = false
      const send = (obj: unknown) => {
        if (closed) return
        controller.enqueue(encoder.encode(JSON.stringify(obj) + "\n"))
      }
      const finish = () => {
        if (closed) return
        closed = true
        try {
          controller.close()
        } catch {
          /* noop */
        }
      }

      let ws: WebSocket
      let terminal = false
      let sawToolEvent = false
      let silenceTimer: ReturnType<typeof setTimeout> | null = null
      const clearSilenceTimer = () => {
        if (silenceTimer) {
          clearTimeout(silenceTimer)
          silenceTimer = null
        }
      }
      const armSilenceTimer = () => {
        clearSilenceTimer()
        if (tool !== "doops_agent_prompt") return
        silenceTimer = setTimeout(() => {
          if (closed || sawToolEvent) return
          send({
            type: "error",
            error:
              "智能体长时间没有返回任何事件。远端 doagent 可能模型凭据过期、模型服务不可用，或 ACP 事件流没有结束。请检查目标节点 /var/log/do-agent-acp.log。",
          })
          try {
            ws.close()
          } catch {
            /* noop */
          }
          finish()
        }, AGENT_PROMPT_SILENCE_MS)
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
        send({ type: "open" })
        ws.send(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }))
      })

      ws.on("message", (raw: WebSocket.RawData) => {
        let msg: any
        try {
          msg = JSON.parse(raw.toString())
        } catch {
          return
        }

        // initialize 应答后发起工具调用
        if (msg.id === 1 && (msg.result !== undefined || msg.error !== undefined)) {
          if (msg.error) {
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
          armSilenceTimer()
          return
        }

        // 流式输出通知
        if (msg.method === "notifications/message") {
          sawToolEvent = true
          clearSilenceTimer()
          const data = msg.params?.data ?? ""
          send({ type: "output", data, kind: msg.params?.kind, session: msg.params?.sessionID })
          return
        }

        // 最终结果
        if (msg.id === callId) {
          terminal = true
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

      ws.on("error", (err: Error) => {
        clearSilenceTimer()
        send({ type: "error", error: `连接错误: ${err.message}` })
        finish()
      })

      ws.on("close", () => {
        clearSilenceTimer()
        if (!terminal) send({ type: "error", error: "Gateway 连接已结束，未收到执行结果" })
        send({ type: "done" })
        finish()
      })

      // 客户端取消时关闭上游 WS
      cancelUpstream = () => {
        clearSilenceTimer()
        try {
          if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ jsonrpc: "2.0", id: 3, method: "tools/cancel", params: { id: callId } }))
          ws.close()
        } catch {
          /* noop */
        }
        finish()
      }
      req.signal.addEventListener("abort", cancelUpstream, { once: true })
    },
    cancel() { cancelUpstream?.() },
  })

  return new Response(stream, {
    headers: {
      "Content-Type": "application/x-ndjson; charset=utf-8",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
    },
  })
}
