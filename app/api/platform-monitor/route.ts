import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// Proxies GET /v1/platform-monitor; Gateway enforces global vs own-grant visibility.
export async function GET(req: Request) {
  try {
    const incoming = new URL(req.url)
    const qs = new URLSearchParams()
    for (const key of ["range", "cluster"]) {
      const value = incoming.searchParams.get(key)
      if (value) qs.set(key, value)
    }
    const suffix = qs.toString() ? `?${qs.toString()}` : ""
    return await proxyGatewayRequest(req, `/v1/platform-monitor${suffix}`, { errorLabel: "平台监控" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "Gateway 策略检查失败" }, { status: 500 })
  }
}
