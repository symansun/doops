import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// 透传到 gateway GET /v1/targets
export async function GET(req: Request) {
  try {
    return await proxyGatewayRequest(req, "/v1/targets", { errorLabel: "查询目标" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
}
