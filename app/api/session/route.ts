import {
  allowedGatewayBase,
  clearGatewaySession,
  gatewayPolicyResponse,
  gatewaySessionGeneration,
  readGatewaySession,
  setGatewaySession,
} from "@/lib/gateway-policy"
import { readGatewayIdentity } from "@/lib/server/gateway-identity"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

export async function GET(req: Request) {
  const stored = readGatewaySession(req)
  if (!stored) return Response.json({ session: null }, { headers: { "Cache-Control": "no-store" } })

  try {
    const gateway = allowedGatewayBase(stored.gateway)
    const sessionGeneration = gatewaySessionGeneration(stored)
    const identity = await readGatewayIdentity(gateway, stored.token)
    if (identity.error) {
      const headers = new Headers({ "Content-Type": "application/json" })
      const authenticationFailed = identity.error.status === 401
      if (authenticationFailed) clearGatewaySession(headers)
      return new Response(JSON.stringify({
        ...(authenticationFailed ? { session: null } : {}),
        ...await identity.error.json(),
      }), {
        status: identity.error.status,
        headers,
      })
    }
    return Response.json({
      session: { ...identity.session, sessionGeneration },
    }, { headers: { "Cache-Control": "no-store" } })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "无法验证 Gateway 会话" }, { status: 502 })
  }
}

export async function POST(req: Request) {
  let body: { gateway?: string; token?: string }
  try {
    body = await req.json()
  } catch {
    return Response.json({ error: "请求体不是合法 JSON" }, { status: 400 })
  }

  const token = (body.token || "").trim()
  if (!token) return Response.json({ error: "缺少 user token" }, { status: 400 })

  try {
    const gateway = allowedGatewayBase(body.gateway || "")
    const identity = await readGatewayIdentity(gateway, token)
    if (identity.error) return identity.error
    const headers = new Headers({ "Content-Type": "application/json" })
    const sessionGeneration = setGatewaySession(headers, { ...identity.session, token })
    return new Response(JSON.stringify({ authenticated: true, ...identity.session, sessionGeneration }), {
      status: 200,
      headers,
    })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "无法验证 Gateway token" }, { status: 502 })
  }
}

export async function DELETE() {
  const headers = new Headers({ "Content-Type": "application/json" })
  clearGatewaySession(headers)
  return new Response(JSON.stringify({ ok: true }), { status: 200, headers })
}
