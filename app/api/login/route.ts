import {
  allowedGatewayBase,
  gatewayPolicyResponse,
  setGatewaySession,
} from "@/lib/gateway-policy"
import { readGatewayIdentity } from "@/lib/server/gateway-identity"
import { gatewayErrorResponse } from "@/lib/server/gateway-errors"
import { rejectCrossOriginMutation } from "@/lib/server/same-origin"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// 透传到 gateway POST /v1/auth/login，返回 user token
export async function POST(req: Request) {
  const rejected = rejectCrossOriginMutation(req)
  if (rejected) return rejected
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
    if (!res.ok) return await gatewayErrorResponse(res)
    const parsed = await res.json().catch(() => null)
    if (typeof parsed?.token !== "string" || !parsed.token) return Response.json({ error: "gateway 登录响应缺少 token" }, { status: 502 })
    const identity = await readGatewayIdentity(gatewayBase, parsed.token)
    if (identity.error) return identity.error
    const headers = new Headers({ "Content-Type": "application/json" })
    setGatewaySession(headers, { ...identity.session, token: parsed.token })
    return new Response(JSON.stringify({ authenticated: true, ...identity.session }), {
      status: 200,
      headers,
    })
  } catch (err) {
    const policyResponse = gatewayPolicyResponse(err)
    if (policyResponse) return policyResponse
    // 网络层错误信息是英文且可能带内部地址，不回给浏览器。
    return Response.json({ error: "无法连接 Gateway，请检查地址或网络" }, { status: 502 })
  }
}
