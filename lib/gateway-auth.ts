export const GATEWAY_AUTH_MESSAGES: Record<string, string> = {
  gateway_auth_missing: "缺少 Gateway token，请登录",
  gateway_auth_invalid: "Gateway token 无效，请核对令牌或重新登录",
  gateway_auth_expired: "Gateway token 已过期，请重新登录",
  gateway_auth_revoked: "Gateway token 已撤销，请使用有效令牌登录",
  gateway_auth_user_disabled: "Gateway 用户已停用，请联系管理员",
  gateway_auth_invalid_expiry: "Gateway token 到期数据异常，请联系管理员",
  gateway_auth_unavailable: "Gateway 认证服务暂时不可用，请稍后重试",
}

export function gatewayAuthCode(raw: string): string | undefined {
  const code = raw.match(/\bgateway_auth_[a-z_]+\b/)?.[0]
  return code && Object.hasOwn(GATEWAY_AUTH_MESSAGES, code) ? code : undefined
}

export function gatewayAuthMessage(raw: string): string | undefined {
  const code = gatewayAuthCode(raw)
  return code ? GATEWAY_AUTH_MESSAGES[code] : undefined
}
