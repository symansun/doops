import {
  allowedGatewayBase,
  clearGatewaySession,
  gatewayPolicyResponse,
  readGatewaySession,
  setGatewaySession,
} from "@/lib/gateway-policy"
import { probeGatewayAdmin } from "@/lib/server/gateway-admin"
import { gatewayErrorResponse } from "@/lib/server/gateway-errors"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

export async function GET(req: Request) {
  const stored = readGatewaySession(req)
  if (!stored) return Response.json({ session: null }, { headers: { "Cache-Control": "no-store" } })

  try {
    const gateway = allowedGatewayBase(stored.gateway)
    const response = await fetch(`${gateway}/v1/targets`, {
      headers: { Authorization: `Bearer ${stored.token}` },
      cache: "no-store",
      redirect: "manual",
    })
    if (!response.ok) {
      const headers = new Headers({ "Content-Type": "application/json" })
      clearGatewaySession(headers)
      return new Response(JSON.stringify({ session: null, error: "Gateway 会话已失效" }), {
        status: response.status === 401 || response.status === 403 ? response.status : 502,
        headers,
      })
    }
    const isAdmin = await probeGatewayAdmin(gateway, stored.token)
    return Response.json({
      session: { gateway, username: stored.username || "", isAdmin },
    }, { headers: { "Cache-Control": "no-store" } })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "无法验证 Gateway 会话" }, { status: 502 })
  }
}

export async function POST(req: Request) {
  let body: { gateway?: string; token?: string; username?: string }
  try {
    body = await req.json()
  } catch {
    return Response.json({ error: "请求体不是合法 JSON" }, { status: 400 })
  }

  const token = (body.token || "").trim()
  if (!token) return Response.json({ error: "缺少 user token" }, { status: 400 })

  try {
    const gateway = allowedGatewayBase(body.gateway || "")
    const response = await fetch(`${gateway}/v1/targets`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
      redirect: "manual",
    })
    if (!response.ok) return await gatewayErrorResponse(response)
    const isAdmin = await probeGatewayAdmin(gateway, token)
    const headers = new Headers({ "Content-Type": "application/json" })
    setGatewaySession(headers, { gateway, token, username: body.username || "" })
    return new Response(JSON.stringify({ authenticated: true, gateway, username: body.username || "", isAdmin }), {
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
