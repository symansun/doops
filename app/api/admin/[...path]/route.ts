import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// 通用代理：把 /api/admin/<path> 透传到 gateway /v1/admin/<path>
// 覆盖 users / grants / tokens / instances / operations 等 admin 接口。
async function proxy(req: Request, ctx: { params: Promise<{ path: string[] }> }) {
  try {
    const { path } = await ctx.params
    const sub = (path || []).map(encodeURIComponent).join("/")
    const incoming = new URL(req.url)
    return await proxyGatewayRequest(req, `/v1/admin/${sub}${incoming.search}`, { errorLabel: "管理请求" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = proxy
export const POST = proxy
export const DELETE = proxy
export const PUT = proxy
export const PATCH = proxy
