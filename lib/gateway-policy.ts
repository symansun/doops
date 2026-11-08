export class GatewayPolicyError extends Error {}

export interface GatewaySession {
  gateway: string
  token: string
  username?: string
  expiresAt?: string
}

const SESSION_MAX_AGE = 60 * 60 * 24 * 7
const SESSION_GATEWAY_COOKIE = "doops_session_gateway"
const SESSION_TOKEN_COOKIE = "doops_session_token"
const SESSION_USERNAME_COOKIE = "doops_session_username"

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
  const session = readGatewaySession(req)
  if (!session) return { gateway: "", token: "" }
  return {
    gateway: allowedGatewayBase(session.gateway),
    token: session.token,
  }
}

export function readGatewaySession(req: Request): GatewaySession | null {
  const cookies = parseCookieHeader(req.headers.get("cookie"))
  const gateway = (cookies[SESSION_GATEWAY_COOKIE] || "").trim()
  const token = (cookies[SESSION_TOKEN_COOKIE] || "").trim()
  if (!gateway || !token) return null
  const username = (cookies[SESSION_USERNAME_COOKIE] || "").trim()
  return { gateway, token, username: username || undefined }
}

export function setGatewaySession(headers: Headers, session: GatewaySession): void {
  const maxAge = sessionCookieMaxAge(session.expiresAt)
  headers.append("Set-Cookie", sessionCookie(SESSION_GATEWAY_COOKIE, session.gateway, maxAge))
  headers.append("Set-Cookie", sessionCookie(SESSION_TOKEN_COOKIE, session.token, maxAge))
  headers.append("Set-Cookie", sessionCookie(SESSION_USERNAME_COOKIE, session.username || "", maxAge))
}

export function clearGatewaySession(headers: Headers): void {
  headers.append("Set-Cookie", sessionCookie(SESSION_GATEWAY_COOKIE, "", 0))
  headers.append("Set-Cookie", sessionCookie(SESSION_TOKEN_COOKIE, "", 0))
  headers.append("Set-Cookie", sessionCookie(SESSION_USERNAME_COOKIE, "", 0))
}

function parseCookieHeader(header: string | null): Record<string, string> {
  const out: Record<string, string> = {}
  for (const part of (header || "").split(";")) {
    const idx = part.indexOf("=")
    if (idx <= 0) continue
    const key = part.slice(0, idx).trim()
    const value = part.slice(idx + 1).trim()
    if (!key) continue
    try {
      out[key] = decodeURIComponent(value)
    } catch {
      out[key] = value
    }
  }
  return out
}

function sessionCookie(name: string, value: string, maxAge: number): string {
  const secure = process.env.NODE_ENV === "production" ? "; Secure" : ""
  return `${name}=${encodeURIComponent(value)}; Path=/; HttpOnly; SameSite=Lax; Max-Age=${maxAge}${secure}`
}

function sessionCookieMaxAge(expiresAt?: string): number {
  if (!expiresAt) return SESSION_MAX_AGE
  const expiry = Date.parse(expiresAt)
  if (!Number.isFinite(expiry)) throw new Error("Gateway token expiry is invalid")
  return Math.max(0, Math.min(SESSION_MAX_AGE, Math.floor((expiry - Date.now()) / 1000)))
}

export function gatewayPolicyResponse(err: unknown): Response | null {
  if (!(err instanceof GatewayPolicyError)) return null
  return Response.json({ error: err.message }, { status: 403 })
}
