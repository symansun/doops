import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const SAFE_SLUG = /^[a-z0-9][a-z0-9_-]{0,47}$/

function resolveMCPPath(segments?: string[]): string | null {
  if (!segments || segments.length === 0) {
    return "/v1/mcp-servers"
  }
  if (segments.length === 1 && SAFE_SLUG.test(segments[0])) {
    return `/v1/mcp-servers/${segments[0]}`
  }
  return null
}

async function handleMCPServers(req: Request, ctx: { params: Promise<{ path?: string[] }> }) {
  const { path: segments } = await ctx.params
  const target = resolveMCPPath(segments)
  if (!target) {
    return Response.json({ error: "mcp_route_invalid", message: "MCP 接口路径无效" }, { status: 404 })
  }
  try {
    const query = req.method === "GET" ? new URL(req.url).search : ""
    return await proxyGatewayRequest(req, `${target}${query}`, { errorLabel: "MCP 管理服务" })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "Gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = handleMCPServers
export const POST = handleMCPServers
