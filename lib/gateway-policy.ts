export class GatewayPolicyError extends Error {}

type GatewayEnvironment = {
  NODE_ENV?: string
  DOOPS_CONSOLE_ALLOWED_GATEWAYS?: string
}

function normalizedGateway(rawGateway: string): URL {
  const raw = rawGateway.trim()
  if (!raw) throw new GatewayPolicyError("缺少 gateway 地址")

  let candidate = raw.includes("://") ? raw : `http://${raw}`
  if (candidate.startsWith("ws://")) candidate = `http://${candidate.slice(5)}`
  if (candidate.startsWith("wss://")) candidate = `https://${candidate.slice(6)}`

  let url: URL
  try {
    url = new URL(candidate)
  } catch {
    throw new GatewayPolicyError("gateway 地址格式无效")
  }
  if (!["http:", "https:"].includes(url.protocol)) {
    throw new GatewayPolicyError("gateway 协议必须是 http 或 https")
  }
  if (url.username || url.password || url.search || url.hash) {
    throw new GatewayPolicyError("gateway 地址不能包含认证信息、查询参数或片段")
  }
  if (url.pathname !== "/" && url.pathname !== "") {
    throw new GatewayPolicyError("gateway 地址不能包含路径")
  }
  return url
}

function configuredGateways(env: GatewayEnvironment): Set<string> {
  const configured = env.DOOPS_CONSOLE_ALLOWED_GATEWAYS?.trim()
  if (!configured) return new Set()
  return new Set(
    configured
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean)
      .map((item) => normalizedGateway(item).origin),
  )
}

function isLoopback(url: URL): boolean {
  return ["localhost", "127.0.0.1", "[::1]", "::1"].includes(url.hostname.toLowerCase())
}

export function allowedGatewayBase(
  rawGateway: string,
  env: GatewayEnvironment = process.env,
): string {
  const gateway = normalizedGateway(rawGateway)
  const allowlist = configuredGateways(env)
  if (allowlist.size > 0) {
    if (!allowlist.has(gateway.origin)) {
      throw new GatewayPolicyError("该 gateway 未获控制台授权")
    }
    if (env.NODE_ENV === "production" && gateway.protocol !== "https:" && !isLoopback(gateway)) {
      throw new GatewayPolicyError("生产 gateway 必须使用 https")
    }
    return gateway.origin
  }
  if (env.NODE_ENV === "production") {
    throw new GatewayPolicyError("控制台未配置 gateway allowlist")
  }
  if (!isLoopback(gateway)) {
    throw new GatewayPolicyError("开发模式只允许访问本机 gateway")
  }
  return gateway.origin
}

export function readAllowedAuth(req: Request): { gateway: string; token: string } {
  const gateway = allowedGatewayBase(req.headers.get("x-doops-gateway") || "")
  const auth = req.headers.get("authorization") || ""
  const token = auth.toLowerCase().startsWith("bearer ") ? auth.slice(7).trim() : auth.trim()
  return { gateway, token }
}

export function gatewayPolicyResponse(err: unknown): Response | null {
  if (!(err instanceof GatewayPolicyError)) return null
  return Response.json({ error: err.message }, { status: 403 })
}
