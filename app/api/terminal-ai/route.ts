import { gatewayPolicyResponse, readAllowedAuth } from "@/lib/gateway-policy"
import { rejectCrossOriginMutation } from "@/lib/server/same-origin"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/
const ALLOWED_ACTIONS = new Set(["ask", "explain", "fix", "followup"])

const RESPONSE_HEADERS = {
  "Cache-Control": "no-store",
  "X-Content-Type-Options": "nosniff",
  "Cross-Origin-Resource-Policy": "same-origin",
}

interface TerminalAiBody {
  cluster?: string
  instance?: string
  session_id?: string
  question?: string
  action?: string
  cwd?: string
  context_prompt?: string
}

export async function POST(req: Request) {
  const rejected = rejectCrossOriginMutation(req)
  if (rejected) return rejected

  let gateway: string
  let token: string
  try {
    ;({ gateway, token } = readAllowedAuth(req))
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500, headers: RESPONSE_HEADERS })
  }
  if (!gateway || !token) {
    return Response.json({ error: "未建立 Gateway 会话，请重新登录" }, { status: 401, headers: RESPONSE_HEADERS })
  }

  let body: TerminalAiBody
  try {
    body = (await req.json()) as TerminalAiBody
  } catch {
    return Response.json({ error: "请求体不是合法 JSON" }, { status: 400, headers: RESPONSE_HEADERS })
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    return Response.json({ error: "请求体必须为对象" }, { status: 400, headers: RESPONSE_HEADERS })
  }

  const cluster = typeof body.cluster === "string" ? body.cluster.trim() : ""
  const instance = typeof body.instance === "string" ? body.instance.trim() : ""
  const sessionId = typeof body.session_id === "string" ? body.session_id.trim() : ""
  const question = typeof body.question === "string" ? body.question.trim() : ""
  const action = typeof body.action === "string" && body.action.trim() ? body.action.trim().toLowerCase() : "ask"
  const cwd = typeof body.cwd === "string" ? body.cwd.trim().slice(0, 512) : ""
  const contextPrompt = typeof body.context_prompt === "string" ? body.context_prompt.trim().slice(0, 32_000) : ""

  if (!ID_PATTERN.test(cluster) || cluster.includes("..") || !ID_PATTERN.test(instance) || instance.includes("..")) {
    return Response.json({ error: "缺少或无效的 cluster / instance" }, { status: 400, headers: RESPONSE_HEADERS })
  }
  if (!ID_PATTERN.test(sessionId) || sessionId.includes("..")) {
    return Response.json({ error: "需要有效的 session_id" }, { status: 400, headers: RESPONSE_HEADERS })
  }
  if (!question || question.length > 4000) {
    return Response.json({ error: "提问内容不能为空且不能超过 4000 字符" }, { status: 400, headers: RESPONSE_HEADERS })
  }
  if (!ALLOWED_ACTIONS.has(action)) {
    return Response.json({ error: "不支持的操作类型" }, { status: 400, headers: RESPONSE_HEADERS })
  }

  try {
    const upstream = await fetch(`${gateway}/v1/terminal/ai`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify({
        cluster,
        instance,
        session_id: sessionId,
        question,
        action,
        ...(cwd ? { cwd } : {}),
        ...(contextPrompt ? { context_prompt: contextPrompt } : {}),
      }),
      cache: "no-store",
      redirect: "manual",
      signal: req.signal,
    })

    let parsed: Record<string, unknown> | null = null
    try {
      parsed = (await upstream.json()) as Record<string, unknown>
    } catch {
      parsed = null
    }

    if (!upstream.ok) {
      if (upstream.status === 404) {
        return Response.json(
          {
            error: "terminal_ai_gateway_unsupported",
            code: "terminal_ai_gateway_unsupported",
            message: "当前 Gateway 尚未升级单次终端 AI 接口，请升级 Gateway 或改用右侧「AI 协同」",
          },
          { status: 501, headers: RESPONSE_HEADERS },
        )
      }
      const message =
        typeof parsed?.message === "string" && parsed.message.trim()
          ? parsed.message.trim()
          : typeof parsed?.error === "string" && parsed.error.trim()
            ? parsed.error.trim()
            : `单次模型调用失败（HTTP ${upstream.status}）`
      const code = typeof parsed?.code === "string" ? parsed.code : typeof parsed?.error === "string" ? parsed.error : undefined
      const nextAction = typeof parsed?.next_action === "string" ? parsed.next_action : undefined
      return Response.json(
        {
          error: message,
          ...(code ? { code } : {}),
          ...(nextAction ? { next_action: nextAction } : {}),
        },
        { status: upstream.status, headers: RESPONSE_HEADERS },
      )
    }

    if (!parsed || typeof parsed !== "object") {
      return Response.json({ error: "Gateway 返回了无法解析的模型结果" }, { status: 502, headers: RESPONSE_HEADERS })
    }
    return Response.json(parsed, { status: 200, headers: RESPONSE_HEADERS })
  } catch (err) {
    if (req.signal.aborted) {
      return Response.json({ error: "已取消模型调用", code: "terminal_ai_canceled" }, { status: 499, headers: RESPONSE_HEADERS })
    }
    return Response.json(
      { error: err instanceof Error ? `无法连接 Gateway：${err.message}` : "无法连接 Gateway" },
      { status: 502, headers: RESPONSE_HEADERS },
    )
  }
}
