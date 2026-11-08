import { gatewayErrorResponse } from "./gateway-errors"

export async function readGatewayIdentity(gateway: string, token: string) {
  const response = await fetch(`${gateway}/v1/auth/me`, {
    headers: { Authorization: `Bearer ${token}` },
    cache: "no-store",
    redirect: "manual",
  })
  if (!response.ok) return { error: await gatewayErrorResponse(response) }

  const identity = await response.json().catch(() => null)
  if (!identity || typeof identity.user_id !== "string" || !identity.user_id ||
      typeof identity.username !== "string" || !identity.username ||
      typeof identity.is_admin !== "boolean" ||
      typeof identity.token_id !== "string" || !identity.token_id ||
      typeof identity.issued_at !== "string" || !Number.isFinite(Date.parse(identity.issued_at)) ||
      (identity.expires_at !== undefined &&
        (typeof identity.expires_at !== "string" || !Number.isFinite(Date.parse(identity.expires_at))))) {
    return { error: Response.json({ error: "Gateway 身份响应无效，请确认控制台与 Gateway 版本一致" }, { status: 502 }) }
  }
  if (identity.expires_at && Date.parse(identity.expires_at) <= Date.now()) {
    return { error: Response.json({ error: "Gateway token 已过期，请重新登录", code: "gateway_auth_expired" }, { status: 401 }) }
  }
  return {
    session: {
      gateway,
      username: identity.username as string,
      isAdmin: identity.is_admin as boolean,
      tokenId: identity.token_id as string,
      issuedAt: identity.issued_at as string,
      expiresAt: identity.expires_at as string | undefined,
    },
  }
}
