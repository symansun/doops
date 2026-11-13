import {
  allowedGatewayBase,
  clearGatewaySession,
  gatewayPolicyResponse,
  readGatewaySession,
  setGatewaySession,
} from "@/lib/gateway-policy"
import { readGatewayIdentity } from "@/lib/server/gateway-identity"
import { rejectCrossOriginMutation } from "@/lib/server/same-origin"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

export async function GET(req: Request) {
  const stored = readGatewaySession(req)
  if (!stored) return Response.json({ session: null }, { headers: { "Cache-Control": "no-store" } })

  try {
    const gateway = allowedGatewayBase(stored.gateway)
    const identity = await readGatewayIdentity(gateway, stored.token)
    if (identity.error) {
      const headers = new Headers({ "Content-Type": "application/json" })
      const authenticationFailed = identity.error.status === 401
      if (authenticationFailed) {
        clearGatewaySession(headers)
        headers.set("Cache-Control", "no-store")
        // Return 200 so the console can go to the login page without treating
        // an expired or revoked cookie as a restore failure.
        return new Response(JSON.stringify({ session: null }), { status: 200, headers })
      }
      return new Response(JSON.stringify(await identity.error.json()), {
        status: identity.error.status,
        headers,
      })
    }
    const headers = new Headers({ "Content-Type": "application/json", "Cache-Control": "no-store" })
    setGatewaySession(headers, { ...identity.session, token: stored.token })
    return new Response(JSON.stringify({
      session: identity.session,
    }), { status: 200, headers })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "无法验证 Gateway 会话" }, { status: 502 })
  }
}

export async function POST(req: Request) {
  const rejected = rejectCrossOriginMutation(req)
  if (rejected) return rejected
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
    setGatewaySession(headers, { ...identity.session, token })
    return new Response(JSON.stringify({ authenticated: true, ...identity.session }), {
      status: 200,
      headers,
    })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "无法验证 Gateway token" }, { status: 502 })
  }
}

export async function DELETE(req: Request) {
  const rejected = rejectCrossOriginMutation(req)
  if (rejected) return rejected
  const headers = new Headers({ "Content-Type": "application/json" })
  clearGatewaySession(headers)
  return new Response(JSON.stringify({ ok: true }), { status: 200, headers })
}
