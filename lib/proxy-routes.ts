// Gateway 网站代理路由（/v1/admin/proxy-routes）的类型与表单校验。
// 校验规则与 backend/agent/internal/server/proxy_route_store.go 保持一致，
// 只用于在提交前给出中文提示；最终以 Gateway 返回为准。

export interface ProxyRoute {
  id: string
  host: string
  cluster: string
  instance: string
  upstream: string
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface ProxyRouteBody {
  id?: string
  host: string
  cluster: string
  instance: string
  upstream: string
  enabled: boolean
}

export interface ProxyRouteForm {
  id: string
  host: string
  cluster: string
  instance: string
  upstream: string
  enabled: boolean
}

export const EMPTY_PROXY_ROUTE_FORM: ProxyRouteForm = {
  id: "",
  host: "",
  cluster: "",
  instance: "",
  upstream: "",
  enabled: true,
}

export function proxyRouteForm(route: ProxyRoute): ProxyRouteForm {
  return {
    id: route.id,
    host: route.host,
    cluster: route.cluster,
    instance: route.instance,
    upstream: route.upstream,
    enabled: route.enabled,
  }
}

type Result<T> = { ok: true; value: T } | { ok: false; error: string }

const HOST_INVALID = "域名格式无效"

// 对应 Go net.SplitHostPort：成功返回 [host, port]，失败返回 null。
function splitHostPort(raw: string): [string, string] | null {
  if (raw.startsWith("[")) {
    const end = raw.indexOf("]")
    if (end < 0 || raw[end + 1] !== ":") return null
    const port = raw.slice(end + 2)
    if (port.includes(":")) return null
    return [raw.slice(1, end), port]
  }
  const first = raw.indexOf(":")
  if (first < 0 || first !== raw.lastIndexOf(":")) return null
  return [raw.slice(0, first), raw.slice(first + 1)]
}

function isIPv4(raw: string): boolean {
  const parts = raw.split(".")
  return parts.length === 4 && parts.every((p) => /^(0|[1-9][0-9]{0,2})$/.test(p) && Number(p) <= 255)
}

function isIPv6(raw: string): boolean {
  return raw.includes(":") && /^[0-9a-f:.]+$/.test(raw) && (raw.match(/::/g) || []).length <= 1
}

export function normalizeProxyHost(input: string): Result<string> {
  let raw = input.trim()
  if (!raw) return { ok: false, error: "请填写域名" }
  if (raw.includes("://") || /[/?#]/.test(raw)) {
    return { ok: false, error: "域名只填主机名，不要带协议或路径" }
  }
  const split = splitHostPort(raw)
  if (split) {
    if (!split[1]) return { ok: false, error: "域名中的端口无效" }
    raw = split[0]
  } else if (raw.includes(":")) {
    return { ok: false, error: "域名不能带端口" }
  }
  raw = raw.trim().toLowerCase().replace(/\.$/, "")
  if (!raw || raw.length > 253 || /[ \t\r\n]/.test(raw)) return { ok: false, error: HOST_INVALID }
  if (isIPv4(raw) || isIPv6(raw)) return { ok: true, value: raw }
  for (const label of raw.split(".")) {
    if (!label || label.length > 63 || label.startsWith("-") || label.endsWith("-") || !/^[a-z0-9-]+$/.test(label)) {
      return { ok: false, error: HOST_INVALID }
    }
  }
  return { ok: true, value: raw }
}

export function validateProxyUpstream(input: string): Result<string> {
  const raw = input.trim()
  const error = "上游必须是 http(s) 地址，且不能包含账号密码、查询参数或锚点"
  if (/[?#]/.test(raw)) return { ok: false, error }
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    return { ok: false, error }
  }
  if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.host || parsed.username || parsed.password) {
    return { ok: false, error }
  }
  return { ok: true, value: raw }
}

// 表单 → Gateway upsert 请求体。Gateway 的 upsert 会整体替换字段，
// 所以编辑时表单里必须带着全部现值（由 proxyRouteForm 预填）。
export function proxyRouteBody(form: ProxyRouteForm): Result<ProxyRouteBody> {
  const host = normalizeProxyHost(form.host)
  if (!host.ok) return host
  const cluster = form.cluster.trim()
  const instance = form.instance.trim()
  if (!cluster || !instance) return { ok: false, error: "请填写目标集群和实例" }
  const upstream = validateProxyUpstream(form.upstream)
  if (!upstream.ok) return upstream
  const body: ProxyRouteBody = { host: host.value, cluster, instance, upstream: upstream.value, enabled: form.enabled }
  const id = form.id.trim()
  if (id) body.id = id
  return { ok: true, value: body }
}

export function isPermissionError(error: unknown): boolean {
  const status = (error as { status?: number } | null)?.status
  return status === 401 || status === 403
}
