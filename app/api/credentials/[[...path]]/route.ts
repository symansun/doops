import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest, resolveSafeGatewaySubpath } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

async function proxy(req: Request, ctx: { params: Promise<{ path?: string[] }> }) {
  try {
    const { path } = await ctx.params
    const suffix = resolveSafeGatewaySubpath(path)
    if (suffix === null) {
      return Response.json({ error: "凭据请求路径无效" }, { status: 400 })
    }
    const incoming = new URL(req.url)
    return await proxyGatewayRequest(req, `/v1/credentials${suffix}${incoming.search}`, { errorLabel: "凭据请求" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = proxy
export const POST = proxy
export const PUT = proxy
export const DELETE = proxy
