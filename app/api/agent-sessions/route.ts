import { fromJson, toJson, type JsonValue } from "@bufbuild/protobuf"
import { AgentSessionReductionError } from "@agent-cloud/agent-ui/server"
import { CommandEnvelopeSchema, CommandReceiptSchema, SessionCursorSchema, SessionDeltaBatchSchema, SessionSnapshotSchema } from "@agent-cloud/agent-ui/protocol"
import { gatewayPolicyResponse, readAllowedAuth } from "@/lib/gateway-policy"
import { AgentGatewayError, type AgentGatewayContext } from "@/lib/server/agent-gateway-rpc"
import { authorizedAgentSession, executeAgentCommand } from "@/lib/server/agent-session"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

export async function GET(req: Request) {
  try {
    const query = new URL(req.url).searchParams
    const scope = context(req, Object.fromEntries(query))
    if (!["snapshot", "events"].includes(query.get("view") || "")) throw new AgentGatewayError("需要有效会话视图", 400)
    const session = await authorizedAgentSession(scope, req.signal)
    if (query.get("view") === "snapshot") return Response.json(toJson(SessionSnapshotSchema, session.source.getSnapshot()), { headers: { "Cache-Control": "no-store" } })
    let cursor
    try { cursor = fromJson(SessionCursorSchema, JSON.parse(query.get("cursor") || "null")) }
    catch { throw new AgentGatewayError("事件 cursor 不是有效协议数据", 400) }
    session.source.replay(cursor)
    const abort = new AbortController()
    const cancel = () => abort.abort()
    req.signal.addEventListener("abort", cancel, { once: true })
    if (req.signal.aborted) cancel()
    const encoder = new TextEncoder()
    return new Response(new ReadableStream({
      async start(controller) {
        session.subscribers++
        try {
          for await (const batch of session.source.subscribe(cursor, abort.signal)) {
            if (abort.signal.aborted) break
            controller.enqueue(encoder.encode(JSON.stringify(toJson(SessionDeltaBatchSchema, batch)) + "\n"))
          }
          if (!abort.signal.aborted) controller.close()
        } catch (error) { if (!abort.signal.aborted) controller.error(error) }
        finally { session.subscribers--; session.touchedAt = Date.now(); req.signal.removeEventListener("abort", cancel) }
      },
      cancel,
    }), { headers: { "Content-Type": "application/x-ndjson; charset=utf-8", "Cache-Control": "no-store, no-transform" } })
  } catch (error) { return failure(error) }
}

export async function POST(req: Request) {
  try {
    const body = await req.json()
    const scope = context(req, body)
    let command
    try { command = fromJson(CommandEnvelopeSchema, body.command as JsonValue) }
    catch { throw new AgentGatewayError("command 不是有效协议数据", 400) }
    const session = await authorizedAgentSession(scope, req.signal)
    const receipt = await executeAgentCommand(session, scope, command)
    return Response.json(toJson(CommandReceiptSchema, receipt), { headers: { "Cache-Control": "no-store" } })
  } catch (error) { return failure(error) }
}

function context(req: Request, body: unknown): AgentGatewayContext {
  const auth = readAllowedAuth(req)
  if (!auth.token) throw new AgentGatewayError("缺少登录 token", 401)
  if (!body || typeof body !== "object" || Array.isArray(body)) throw new AgentGatewayError("无效请求范围", 400)
  const scope = body as Record<string, unknown>
  for (const key of ["cluster", "instance", "session_id"]) {
    const value = scope[key]
    if (typeof value !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value) || value.includes("..")) {
      throw new AgentGatewayError(`需要有效 ${key}`, 400)
    }
  }
  return { ...auth, cluster: scope.cluster as string, instance: scope.instance as string, sessionId: scope.session_id as string }
}

function failure(error: unknown): Response {
  const policy = gatewayPolicyResponse(error)
  if (policy) return policy
  if (error instanceof AgentGatewayError) return Response.json({ error: error.message }, { status: error.status })
  if (error instanceof AgentSessionReductionError) {
    const status = error.code.startsWith("cursor_") || error.code === "command_position_mismatch" ? 412 : 400
    return Response.json({ error: error.code }, { status })
  }
  if (error instanceof SyntaxError) return Response.json({ error: "请求 JSON 无效" }, { status: 400 })
  return Response.json({ error: "会话协议处理失败" }, { status: 500 })
}
