import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest, resolveSafeGatewaySubpath } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// Owner-scoped detached background tasks. The Gateway enforces ownership and
// target authorization; the proxy only forwards list/detail/cancel.
async function proxy(req: Request, ctx: { params: Promise<{ path?: string[] }> }) {
  try {
    const { path } = await ctx.params
    const suffix = resolveSafeGatewaySubpath(path)
    if (suffix === null) {
      return Response.json({ error: "后台任务路径无效" }, { status: 400 })
    }
    const incoming = new URL(req.url)
    return await proxyGatewayRequest(req, `/v1/background-tasks${suffix}${incoming.search}`, { errorLabel: "后台任务请求" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = proxy
export const POST = proxy
