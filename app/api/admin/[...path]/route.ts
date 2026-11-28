import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest, resolveSafeGatewaySubpath } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// 通用代理：把 /api/admin/<path> 透传到 gateway /v1/admin/<path>
// 覆盖 users / grants / tokens / instances 等 admin 接口。
async function proxy(req: Request, ctx: { params: Promise<{ path: string[] }> }) {
  try {
    const { path } = await ctx.params
    const suffix = resolveSafeGatewaySubpath(path)
    if (!suffix) {
      return Response.json({ error: "管理请求路径无效" }, { status: 400 })
    }
    const incoming = new URL(req.url)
    return await proxyGatewayRequest(req, `/v1/admin${suffix}${incoming.search}`, { errorLabel: "管理请求" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = proxy
export const POST = proxy
export const DELETE = proxy
export const PUT = proxy
export const PATCH = proxy
