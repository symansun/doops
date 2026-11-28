import { gatewayPolicyResponse } from "@/lib/gateway-policy"
import { proxyGatewayRequest } from "@/lib/server/gateway-proxy"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const SAFE_SLUG = /^[a-z0-9][a-z0-9-]{0,62}$/

function resolveSkillPath(segments?: string[]): string | null {
  if (!segments || segments.length === 0) {
    return "/v1/skills"
  }
  if (segments.length === 1 && SAFE_SLUG.test(segments[0])) {
    return `/v1/skills/${segments[0]}`
  }
  return null
}

async function handleSkills(req: Request, ctx: { params: Promise<{ path?: string[] }> }) {
  const { path: segments } = await ctx.params
  const target = resolveSkillPath(segments)
  if (!target) {
    return Response.json({ error: "skill_route_invalid", message: "Skill 接口路径无效" }, { status: 404 })
  }
  try {
    const query = req.method === "GET" ? new URL(req.url).search : ""
    return await proxyGatewayRequest(req, `${target}${query}`, { errorLabel: "Skill 管理服务" })
  } catch (error) {
    return gatewayPolicyResponse(error) || Response.json({ error: "Gateway 策略检查失败" }, { status: 500 })
  }
}

export const GET = handleSkills
export const POST = handleSkills
