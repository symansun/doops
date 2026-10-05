import WebSocket from "ws"
import { toWsBase } from "../gateway"

export interface AgentGatewayContext {
  gateway: string
  token: string
  cluster: string
  instance: string
  sessionId: string
}

export interface AgentGatewayUpdate {
  kind: string
  data: string
}

export class AgentGatewayError extends Error {
  constructor(message: string, readonly status = 502, readonly outcome: "terminal" | "unknown" | "not_dispatched" = "unknown", readonly canceled = false) {
    super(message)
  }
}

export function agentGatewayCall(
  context: AgentGatewayContext,
  args: Record<string, unknown>,
  signal: AbortSignal,
  onUpdate: (update: AgentGatewayUpdate) => void = () => {},
): Promise<string> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(new AgentGatewayError("请求派发前已停止", 499, "not_dispatched", true)); return }
    const url = new URL(`${toWsBase(context.gateway)}/v1/rpc`)
    url.searchParams.set("cluster", context.cluster)
    url.searchParams.set("instance", context.instance)
    const ws = new WebSocket(url, { headers: { Authorization: `Bearer ${context.token}` }, followRedirects: false })
    let timer = setTimeout(() => fail(new AgentGatewayError("Gateway 未返回连接结果")), 45_000)
    let dispatched = false
    let terminal = false
    const cleanup = () => {
      clearTimeout(timer)
      signal.removeEventListener("abort", abort)
      ws.close()
    }
    const fail = (error: Error) => {
      if (terminal) return
      terminal = true
      cleanup()
      reject(error instanceof AgentGatewayError && !dispatched
        ? new AgentGatewayError(error.message, error.status, "not_dispatched", error.canceled) : error)
    }
    ws.on("error", (error) => fail(new AgentGatewayError(error.message)))
    const abort = () => {
      if (!dispatched) { fail(new AgentGatewayError("请求派发前已停止", 499, "not_dispatched", true)); return }
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ jsonrpc: "2.0", id: 3, method: "tools/cancel", params: { id: 2 } }))
      }
      clearTimeout(timer)
      timer = setTimeout(() => fail(new AgentGatewayError("已请求停止，远端终态未确认", 409)), 15_000)
    }
    signal.addEventListener("abort", abort, { once: true })
    ws.on("open", () => ws.send(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} })))
    ws.on("message", (raw) => {
      if (terminal) return
      try {
        const message = JSON.parse(raw.toString())
        if (message.id === 1) {
          if (message.error) throw new AgentGatewayError(message.error.message || "Gateway initialize 失败")
          ws.send(JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/call", params: {
            name: "doops_agent_prompt", arguments: { ...args, session_id: context.sessionId },
          } }))
          dispatched = true
          return
        }
        if (message.method === "notifications/message") {
          if (message.params?.sessionID !== context.sessionId || message.params?.request_id !== 2) {
            throw new AgentGatewayError("Agent 事件缺少请求或会话归属")
          }
          if (!signal.aborted) clearTimeout(timer)
          onUpdate({ kind: message.params.kind, data: message.params.data })
          return
        }
        if (message.id !== 2) return
        const error = message.error?.message || (message.result?.isError
          ? message.result.content?.map((item: { text?: string }) => item.text || "").join("") : "")
        if (error) throw new AgentGatewayError(error, [-32001, -32003].includes(message.error?.code) ? 403 : 502,
          error === "execution_outcome_unknown" || error.startsWith("execution_outcome_unknown:") ? "unknown" : "terminal",
          message.error?.code === -32007)
        if (!message.result || !Array.isArray(message.result.content)) throw new AgentGatewayError("Agent 执行结果无效")
        terminal = true
        const text = message.result.content.map((item: { text?: string }) => item.text || "").join("")
        cleanup()
        resolve(text)
      } catch (error) {
        fail(error instanceof Error ? error : new AgentGatewayError("Agent 事件无效"))
      }
    })
    ws.on("unexpected-response", (_request, response) => {
      response.resume()
      fail(new AgentGatewayError("Gateway 拒绝连接", response.statusCode === 401 || response.statusCode === 403 ? response.statusCode : 502))
    })
    ws.on("close", () => fail(new AgentGatewayError("Gateway 已断开，远端终态未确认")))
  })
}
