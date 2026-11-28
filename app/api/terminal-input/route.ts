import { TOOLS } from "@/lib/gateway"
import { gatewayPolicyResponse, readAllowedAuth } from "@/lib/gateway-policy"
import {
  defaultGatewayWsPool,
  extractTerminalToolPayload,
  GatewayWsPoolError,
} from "@/lib/server/gateway-ws-pool"
import { rejectCrossOriginMutation } from "@/lib/server/same-origin"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const SUGGESTION_ID_PATTERN = /^[A-Za-z0-9._-]{1,64}$/
const ALLOWED_OPS = new Set(["open", "input", "read", "resize", "close"])
const ALLOWED_SOURCES = new Set(["user", "ai_suggestion"])

interface TerminalInputBody {
  cluster?: string
  instance?: string
  session_id?: string
  op?: string
  term?: string
  data?: string
  cursor?: number
  cols?: number
  rows?: number
  wait_ms?: number
  source?: string
  suggestion_id?: string
}

export async function POST(req: Request) {
  const rejected = rejectCrossOriginMutation(req)
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

  let body: TerminalInputBody
  try {
    body = (await req.json()) as TerminalInputBody
  } catch {
    return Response.json({ error: "请求体不是合法 JSON" }, { status: 400 })
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    return Response.json({ error: "请求体必须为对象" }, { status: 400 })
  }

  const cluster = typeof body.cluster === "string" ? body.cluster.trim() : ""
  const instance = typeof body.instance === "string" ? body.instance.trim() : ""
  const sessionId = typeof body.session_id === "string" ? body.session_id.trim() : ""
  const op = typeof body.op === "string" ? body.op.trim().toLowerCase() : ""

  if (!ID_PATTERN.test(cluster) || cluster.includes("..") || !ID_PATTERN.test(instance) || instance.includes("..")) {
    return Response.json({ error: "缺少或无效的 cluster / instance" }, { status: 400 })
  }
  if (!ID_PATTERN.test(sessionId) || sessionId.includes("..")) {
    return Response.json({ error: "需要有效的操作 session_id" }, { status: 400 })
  }
  if (!ALLOWED_OPS.has(op)) {
    return Response.json({ error: "不支持的终端操作" }, { status: 400 })
  }

  const args: Record<string, unknown> = { session_id: sessionId, op }
  if (typeof body.term === "string" && body.term) args.term = body.term
  if (typeof body.data === "string") args.data = body.data
  if (typeof body.cursor === "number" && Number.isFinite(body.cursor)) args.cursor = Math.max(0, Math.floor(body.cursor))
  if (typeof body.cols === "number" && Number.isFinite(body.cols)) args.cols = Math.floor(body.cols)
  if (typeof body.rows === "number" && Number.isFinite(body.rows)) args.rows = Math.floor(body.rows)
  if (typeof body.wait_ms === "number" && Number.isFinite(body.wait_ms)) {
    args.wait_ms = Math.max(0, Math.min(10_000, Math.floor(body.wait_ms)))
  }
  if (typeof body.source === "string" && body.source.trim()) {
    const source = body.source.trim().toLowerCase()
    if (!ALLOWED_SOURCES.has(source)) {
      return Response.json({ error: "不支持的终端输入来源" }, { status: 400 })
    }
    args.source = source
  }
  if (typeof body.suggestion_id === "string" && body.suggestion_id.trim()) {
    const sid = body.suggestion_id.trim()
    if (!SUGGESTION_ID_PATTERN.test(sid)) {
      return Response.json({ error: "suggestion_id 格式无效" }, { status: 400 })
    }
    args.suggestion_id = sid
  }

  try {
    const raw = await defaultGatewayWsPool.callTool(
      { gateway, token, cluster, instance },
      TOOLS.terminal,
      args,
      { signal: req.signal },
    )
    const payload = extractTerminalToolPayload(raw)
    return Response.json(payload, { headers: { "Cache-Control": "no-store" } })
  } catch (err) {
    if (err instanceof GatewayWsPoolError) {
      return Response.json(
        { error: err.message, ...(err.code !== undefined ? { code: err.code } : {}) },
        { status: err.status === 499 ? 499 : err.status, headers: { "Cache-Control": "no-store" } },
      )
    }
    return Response.json(
      { error: err instanceof Error ? err.message : "终端调用失败" },
      { status: 502, headers: { "Cache-Control": "no-store" } },
    )
  }
}
