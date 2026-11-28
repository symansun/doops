import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const SAFE_RUN_ID = /^[a-zA-Z0-9_-]{1,64}$/

function resolveMonitorPath(segments: string[]): string | null {
  if (segments.length === 1 && (segments[0] === "overview" || segments[0] === "schedules" || segments[0] === "runs" || segments[0] === "metrics")) {
    return `/v1/monitor/${segments[0]}`
  }
  if (segments.length === 2 && segments[0] === "resources" && segments[1] === "history") {
    return "/v1/monitor/resources/history"
  }
  if (segments.length === 2 && segments[0] === "metrics" && segments[1] === "latest") {
    return "/v1/monitor/metrics/latest"
  }
  if (segments.length === 2 && segments[0] === "runs" && SAFE_RUN_ID.test(segments[1])) {
    return `/v1/monitor/runs/${segments[1]}`
  }
  return null
}

async function handleMonitor(req: Request, ctx: { params: Promise<{ path: string[] }> }) {
  const { path: segments } = await ctx.params
  const target = resolveMonitorPath(segments || [])
  if (!target) {
    return Response.json({ error: "monitor_route_invalid", message: "监控接口路径无效" }, { status: 404 })
  }
  try {
    const query = req.method === "GET" ? new URL(req.url).search : ""
    return await proxyGatewayRequest(req, `${target}${query}`, { errorLabel: "监控巡检引擎" })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "Gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = handleMonitor
export const POST = handleMonitor
