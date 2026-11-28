// CI/CD 页把网关地址写进可复制的 shell 和 YAML（DOOPS_GATEWAY），用户会把它提交进代码仓库，
// 所以地址里不能带账号密码、查询参数或片段。规则与 CLI 的 enforceSecureGatewayURL 一致：
// 必须 https，只有本机地址可以用 http。地址只允许域名、端口和普通路径字符，避免片段里出现
// 引号、$、反引号等会改变 shell/YAML 含义的字符。
export const CICD_GATEWAY_PLACEHOLDER = "<DOOPS_GATEWAY_URL>"

export type CicdGatewayResult = { ok: true; value: string } | { ok: false; error: string }

const FORMAT_INVALID = "网关地址格式无效，请填写如 https://gateway.example.com 的完整地址"
const UNSAFE_CHARS = "网关地址只能包含域名、端口和路径，不能有空格、引号、$ 等特殊字符"
const CREDENTIALS = "网关地址不能包含用户名或密码（@ 前面的部分）。令牌只保存在 CI/CD 平台的加密 Secrets（DOOPS_API_KEY）"
const INSECURE = "网关地址必须以 https:// 开头；只有本机地址（localhost、127.0.0.1、[::1]）可以用 http://"
const QUERY = "网关地址不能包含查询参数（? 及之后的部分）"
const HASH = "网关地址不能包含片段（# 及之后的部分）"

const HOSTNAME = /^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$/
const IPV6 = /^\[[0-9a-f:.]+\]$/
const PATH = /^[A-Za-z0-9._~/-]*$/

function isLoopback(hostname: string): boolean {
  return hostname === "localhost" || hostname === "[::1]" || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(hostname)
}

export function validateCicdGateway(input: string): CicdGatewayResult {
  const raw = input.trim()
  if (!/^[A-Za-z][A-Za-z0-9+.-]*:\/\//.test(raw)) return { ok: false, error: FORMAT_INVALID }
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return { ok: false, error: FORMAT_INVALID }
  }
  // URL 会把空的 ? 和 # 丢掉，所以同时检查原文。
  if (url.username || url.password || raw.includes("@")) return { ok: false, error: CREDENTIALS }
  if (url.search || raw.includes("?")) return { ok: false, error: QUERY }
  if (url.hash || raw.includes("#")) return { ok: false, error: HASH }
  if (url.protocol !== "https:" && !(url.protocol === "http:" && isLoopback(url.hostname))) {
    return { ok: false, error: INSECURE }
  }
  if (!(HOSTNAME.test(url.hostname) || IPV6.test(url.hostname)) || !PATH.test(url.pathname)) {
    return { ok: false, error: UNSAFE_CHARS }
  }
  const path = url.pathname.replace(/\/+$/, "")
  return { ok: true, value: `${url.protocol}//${url.host}${path}` }
}

// 片段里用的值：未填写或不合法时继续用占位符，error 给页面显示。
export function cicdGatewaySnippetValue(input: string): { value: string; error: string } {
  if (!input.trim()) return { value: CICD_GATEWAY_PLACEHOLDER, error: "" }
  const result = validateCicdGateway(input)
  return result.ok ? { value: result.value, error: "" } : { value: CICD_GATEWAY_PLACEHOLDER, error: result.error }
}
