import { GATEWAY_AUTH_MESSAGES } from "../gateway-auth.ts"

// Gateway 错误响应只用于服务端判断，原始响应体（英文 message、next_action、纯文本错误页）
// 不回给浏览器。浏览器只拿到中文说明和 Gateway 的稳定 code。

// 以 Gateway 源码为准：tunnel_hub.go 的 HandleAuthLogin 与 writeGatewayAuthError。
// 登录失败不区分用户不存在、已停用和密码错误，避免枚举账号。
const CODE_MESSAGES: Record<string, string> = {
  gateway_login_invalid_credentials: "用户名或密码错误",
  ...GATEWAY_AUTH_MESSAGES,
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
  if (status === 409) return "请求与当前状态冲突，请刷新后重试"
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
  const status = responseStatus(response.status)
  return Response.json(gatewayErrorBody(status, text), { status })
}

function responseStatus(status: number): number {
  return status >= 200 && status <= 599 ? status : 502
}

/* ============================ 代理路由 ============================ */

// lib/credentials.ts、lib/proxy-routes.ts 和凭据组按 error 里的类别原文判断错误，
// 所以代理只保留 Gateway 的 lower_snake 类别和下面列出的固定短语（以 Gateway 源码的
// http.Error 为准）。JSON 错误只保留 code。其它文本（Go/SQLite 错误、HTML 错误页、空响应）
// 可能带内部细节，换成按状态码的中文说明。message 始终是中文，给直接展示错误的页面用。
const GATEWAY_PHRASES: Record<string, string> = {
  "Forbidden": "没有权限执行该操作",
  "Method Not Allowed": "Gateway 不支持该请求方法，请确认控制台与 Gateway 版本一致",
  "404 page not found": "Gateway 没有这个接口，请确认控制台与 Gateway 版本一致",
  "invalid request": "请求格式无效，请检查填写内容",
  "invalid request body": "请求格式无效，请检查填写内容",
  "invalid id": "ID 无效，请刷新后重试",
  "missing id": "缺少 ID，请刷新后重试",
  "invalid include": "查询参数无效，请刷新页面后重试",
  "operation canceled": "操作已取消，请刷新后核对状态再重试",
  // 用户
  "user not found": "用户不存在，请刷新用户列表",
  "name is required": "请填写用户名",
  "user name is required": "请填写用户名",
  "user id is required": "缺少用户 ID，请刷新后重试",
  "password is required": "请填写密码",
  // 授权
  "grant not found": "授权不存在，可能已被删除，请刷新列表",
  "per-action grants were removed; a grant covers the whole target": "授权按集群/实例整体生效，不再支持按操作授权",
  // 令牌
  "token not found": "令牌不存在，可能已被撤销，请刷新列表",
  "invalid token request": "签发令牌的请求无效，请检查所属用户和备注名",
  "invalid expires duration": "有效期格式无效",
  "expires duration must be positive": "有效期必须大于 0",
  "failed to create token": "Gateway 签发令牌失败，请稍后重试",
  // 实例
  "instance not found": "实例不存在，请刷新实例列表",
  "instance is online": "实例在线，不能归档",
  "cluster and instance are required": "请指定集群和实例",
  "failed to archive instance": "Gateway 归档实例失败，请稍后重试",
  "failed to unarchive instance": "Gateway 取消归档失败，请稍后重试",
  // 审计
  "missing before": "缺少清理时间点",
  "invalid before; use RFC3339": "清理时间点格式无效，请使用 RFC3339 格式",
  "audit_completion_failed; mutation outcome requires verification": "Gateway 审计未写完，修改可能已经生效，请刷新后核对",
  // 凭据
  "invalid credential request": "Gateway 拒绝了请求格式，请刷新页面并确认控制台与 Gateway 版本一致",
  "credential request must contain exactly one JSON object": "Gateway 拒绝了请求格式，请刷新页面并确认控制台与 Gateway 版本一致",
  "invalid credential verify request": "Gateway 拒绝了请求格式，请刷新页面并确认控制台与 Gateway 版本一致",
  "invalid credential grant revoke request": "Gateway 拒绝了请求格式，请刷新页面并确认控制台与 Gateway 版本一致",
  "invalid credential list view": "Gateway 拒绝了请求格式，请刷新页面并确认控制台与 Gateway 版本一致",
  "credential target list requires cluster and instance": "Gateway 拒绝了请求格式，请刷新页面并确认控制台与 Gateway 版本一致",
  "credential payload must be valid JSON": "凭据内容必须是合法 JSON",
  "credential versions must be verified before promotion": "版本需要先验证才能激活",
  "failed to list managed credentials": "Gateway 暂时无法列出凭据，请稍后刷新",
  "failed to list target credentials": "Gateway 暂时无法列出凭据，请稍后刷新",
  // 凭据组
  "credential bundle name is required": "请填写凭据组名称",
  "credential bundle metadata and items are required": "凭据组名称、归属和凭据列表都必须填写",
  "credential bundle contains a duplicate item": "凭据组里有重复的凭据",
  // 代理路由
  "proxy route not found": "代理路由不存在，可能已被删除，请刷新列表",
  "proxy host already in use": "该域名已被其它路由使用",
  "proxy route changed since it was read": "该路由刚被其他管理员修改，请刷新后重试",
  "proxy host is required": "请填写域名",
  "proxy host must be a hostname without scheme or path": "域名只填主机名，不要带协议或路径",
  "proxy host port is invalid": "域名中的端口无效",
  "proxy host must not contain a port": "域名不能带端口",
  "proxy host is invalid": "域名格式无效",
  "proxy upstream must be an http(s) URL without credentials, query, or fragment": "上游地址必须是不带账号密码、查询参数和片段的 http(s) 地址",
  "proxy route cluster and instance are required": "请指定集群和实例",
  "proxy route id is required": "缺少路由 ID",
  "invalid proxy route request": "请求格式无效",
  "invalid proxy route updated_at": "路由版本（updated_at）格式无效，请刷新列表后重试",
}

// 有中文说明的 lower_snake 类别；其它类别由调用方自己的映射解释，message 按状态码给。
const CATEGORY_MESSAGES: Record<string, string> = {
  ...CODE_MESSAGES,
  audit_unavailable: "Gateway 审计存储不可用，本次操作未执行，请稍后重试",
}

// 旧版 Gateway 把 SQLite 唯一约束错误原样返回；只保留约束名，丢掉其余文本。
const UNIQUE_CONSTRAINTS: { category: string; message: string }[] = [
  { category: "UNIQUE constraint failed: proxy_routes.host", message: "该域名已被其它路由使用" },
  { category: "UNIQUE constraint failed: users.name", message: "用户名已存在" },
]

const CATEGORY = /^[a-z][a-z0-9_]{0,79}$/

// 资源吊销清理失败时 Gateway 返回「类别: cluster/instance, cluster/instance」
// （credential_http.go 的 strings.Join(unfinished, ", ")）。目标部分只接受标识符字符，
// 有任何其它文本就整体按未知文本处理。
const TARGET = "[A-Za-z0-9_.:-]{1,253}/[A-Za-z0-9_.:-]{1,253}"
const CATEGORY_WITH_TARGETS = new RegExp(`^([a-z][a-z0-9_]{0,79}): (${TARGET}(?:, ${TARGET})*)$`)

export interface GatewayProxyErrorBody {
  // 给调用方解析：Gateway 类别原文、JSON code，或未知文本时的中文说明。
  error: string
  // 给页面展示：中文说明。
  message: string
  status: number
  code?: string
  // 类别后面带的目标（cluster/instance），按 Gateway 返回的顺序。
  targets?: string[]
}

// label 是调用方的操作名（如「管理请求」），只用在按状态码给出的说明里。
export function gatewayProxyErrorBody(status: number, raw: string, label = ""): GatewayProxyErrorBody {
  const text = raw.trim()
  const statusMessage = gatewayStatusMessage(status)
  const byStatus = label ? `${label}失败：${statusMessage}` : statusMessage
  const code = gatewayErrorCode(text)
  // 调用方按 JSON 解析 code；只留 code，丢掉英文 message 和 next_action。
  if (code) return { error: JSON.stringify({ code }), message: CATEGORY_MESSAGES[code] || byStatus, status, code }
  const withTargets = CATEGORY_WITH_TARGETS.exec(text)
  if (withTargets) {
    const category = withTargets[1]
    return { error: category, message: CATEGORY_MESSAGES[category] || byStatus, status, targets: withTargets[2].split(", ") }
  }
  if (CATEGORY.test(text)) return { error: text, message: CATEGORY_MESSAGES[text] || byStatus, status }
  if (Object.hasOwn(GATEWAY_PHRASES, text)) return { error: text, message: GATEWAY_PHRASES[text], status }
  const unique = UNIQUE_CONSTRAINTS.find((item) => text.includes(item.category))
  if (unique) return { error: unique.category, message: unique.message, status }
  // error 不带 label：调用方（如代理路由页）会加上自己的操作名。
  return { error: statusMessage, message: byStatus, status }
}

export async function gatewayProxyErrorResponse(response: Response, label = ""): Promise<Response> {
  const text = await response.text().catch(() => "")
  const status = responseStatus(response.status)
  return Response.json(gatewayProxyErrorBody(status, text, label), { status })
}

// 网络层错误信息是英文且可能带内部地址。error 保留「无法连接 gateway:」前缀给调用方识别。
export function gatewayUnreachableResponse(): Response {
  return Response.json(
    { error: "无法连接 gateway: 请检查 Gateway 地址或网络", message: "无法连接 Gateway，请检查地址或网络", status: 502 },
    { status: 502 },
  )
}
