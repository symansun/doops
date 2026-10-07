import {
  allowedGatewayBase,
  gatewayPolicyResponse,
  setGatewaySession,
} from "@/lib/gateway-policy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// 透传到 gateway POST /v1/auth/login，返回 user token
export async function POST(req: Request) {
  let body: { gateway?: string; username?: string; password?: string; name?: string }
  try {
    body = await req.json()
  } catch {
    return Response.json({ error: "请求体不是合法 JSON" }, { status: 400 })
  }

  const { gateway, username, password, name } = body
  if (!gateway || !username || !password) {
    return Response.json({ error: "缺少 gateway / username / password" }, { status: 400 })
  }

  try {
    const gatewayBase = allowedGatewayBase(gateway)
    const res = await fetch(`${gatewayBase}/v1/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password, name: name || "doops-console" }),
      redirect: "manual",
    })
    const text = await res.text()
    if (!res.ok) {
      return Response.json(
        { error: text || `登录失败 (${res.status})` },
        { status: res.status },
      )
    }
    let parsed: { token?: string; username?: string }
    try {
      parsed = JSON.parse(text) as { token?: string; username?: string }
    } catch {
      return Response.json({ error: "gateway 登录响应不是合法 JSON" }, { status: 502 })
    }
    if (!parsed.token) return Response.json({ error: "gateway 登录响应缺少 token" }, { status: 502 })
    const usernameValue = parsed.username || username
    const headers = new Headers({ "Content-Type": "application/json" })
    setGatewaySession(headers, { gateway: gatewayBase, token: parsed.token, username: usernameValue })
    return new Response(JSON.stringify({ authenticated: true, gateway: gatewayBase, username: usernameValue }), {
      status: 200,
      headers,
    })
  } catch (err) {
    const policyResponse = gatewayPolicyResponse(err)
    if (policyResponse) return policyResponse
    return Response.json(
      { error: `无法连接 gateway: ${(err as Error).message}` },
      { status: 502 },
    )
  }
}
