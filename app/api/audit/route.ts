import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// Gateway authorizes own-user records and privileged audit scopes.
export async function GET(req: Request) {
  try {
    const incoming = new URL(req.url)
    const qs = new URLSearchParams()
    for (const k of ["cluster", "instance", "session", "action", "status", "user_id", "view", "limit", "correlation_id", "before_id", "since", "until", "hide_upgrade_observe"]) {
      const v = incoming.searchParams.get(k)
      if (v) qs.set(k, v)
    }
    const suffix = qs.toString() ? `?${qs.toString()}` : ""
    return await proxyGatewayRequest(req, `/v1/audit${suffix}`, { errorLabel: "查询审计" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
}
