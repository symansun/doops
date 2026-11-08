import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

async function proxy(req: Request) {
  try {
    const incoming = new URL(req.url)
    return await proxyGatewayRequest(req, `/v1/credential-bundles${incoming.search}`, { errorLabel: "凭据组请求" })
  } catch (err) {
    return gatewayPolicyResponse(err) || Response.json({ error: "gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = proxy
export const POST = proxy
