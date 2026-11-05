// Gateway 错误响应只用于服务端判断，原始响应体（英文 message、next_action、纯文本错误页）
// 不回给浏览器。浏览器只拿到中文说明和 Gateway 的稳定 code。

// 以 Gateway 源码为准：tunnel_hub.go 的 HandleAuthLogin 与 writeGatewayAuthError。
// 登录失败不区分用户不存在、已停用和密码错误，避免枚举账号。
const CODE_MESSAGES: Record<string, string> = {
  gateway_login_invalid_credentials: "用户名或密码错误",
  gateway_auth_failed: "Gateway token 无效或已失效，请重新登录",
}

const STABLE_CODE = /^[a-z][a-z0-9_]{0,63}$/

export interface GatewayErrorBody {
  error: string
  code?: string
}

// 只接受 lower_snake 形式的 code；其它形态（句子、HTML、超长值）都当作没有 code。
export function gatewayErrorCode(text: string): string | undefined {
  try {
    const parsed: unknown = JSON.parse(text)
    if (!parsed || typeof parsed !== "object") return undefined
    const code = (parsed as { code?: unknown }).code
    return typeof code === "string" && STABLE_CODE.test(code) ? code : undefined
  } catch {
    return undefined
  }
}

export function gatewayStatusMessage(status: number): string {
  if (status === 400) return "请求格式无效，请检查填写内容"
  if (status === 401) return "身份验证失败，请检查登录信息"
  if (status === 403) return "没有访问该 Gateway 的权限"
  if (status === 404) return "Gateway 没有这个接口，请检查 Gateway 地址或版本"
  if (status === 429) return "请求过于频繁，请稍后再试"
  if (status >= 500) return "Gateway 暂时不可用，请稍后再试"
  return `Gateway 请求失败（HTTP ${status}）`
}

export function gatewayErrorBody(status: number, text: string): GatewayErrorBody {
  const code = gatewayErrorCode(text)
  const error = (code && CODE_MESSAGES[code]) || gatewayStatusMessage(status)
  return code ? { error, code } : { error }
}

// 状态码保持 Gateway 的原值；超出 Response 允许范围时按 502 处理。
export async function gatewayErrorResponse(response: Response): Promise<Response> {
  const text = await response.text().catch(() => "")
  const status = response.status >= 200 && response.status <= 599 ? response.status : 502
  return Response.json(gatewayErrorBody(status, text), { status })
}
