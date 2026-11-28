"use client"

import type { Session } from "./client"
import { consoleFetch } from "./network-error.ts"
import type { ProxyRoute, ProxyRouteBody } from "./proxy-routes"
import type {
  SensitiveFilterView,
  SensitiveRule,
  SensitiveRuleInput,
  SensitiveWhitelistEntry,
  SensitiveWhitelistInput,
} from "./sensitive-filter"

export type { ProxyRoute, ProxyRouteBody }
export type {
  SensitiveFilterView,
  SensitiveRule,
  SensitiveRuleInput,
  SensitiveWhitelistEntry,
  SensitiveWhitelistInput,
}

export interface AdminUser {
  id: string
  name: string
  disabled: boolean
  has_password: boolean
  grant_count: number
  is_admin: boolean
  created_at?: string
}

export interface AdminGrant {
  id: number
  user_id: string
  user_name?: string
  cluster: string
  instance: string
  created_at?: string
}

export interface AdminToken {
  id: string
  kind: string
  user_id?: string
  user_name?: string
  name: string
  prefix: string
  revoked: boolean
  expires_at?: string
  created_at?: string
}

export interface AdminInstance {
  cluster: string
  instance: string
  status: string
  remote?: string
  busy: boolean
  active_ops: number
  queued_ops: number
  connected_at?: string
  last_seen?: string
  archived?: boolean
  archived_at?: string
  archived_by?: string
}

/** A redacted Gateway Admin Webhook endpoint. The URL and secret are never included. */
export interface AdminWebhookEndpoint {
  id: string
  name: string
  description: string
  url_host: string
  url_sha256: string
  enabled: boolean
  scope_cluster: string
  scope_instance: string
  event_types: string[]
  created_by: string
  created_at: string
  updated_at: string
  last_success_at_ms: number
  last_failure_at_ms: number
  last_error_category: string
}

export interface AdminWebhookDelivery {
  id: string
  endpoint_id: string
  event_id: string
  status: string
  attempt: number
  next_attempt_at_ms: number
  response_status: number
  error_category: string
  created_at_ms: number
  updated_at_ms: number
  delivered_at_ms: number
}

/**
 * Gateway 目前把投递记录按 Go 字段名编码（ID/EventID/...），端点接口却是 snake_case。
 * 这里同时接受两种形态，避免任一端的字段命名变化让控制台退化成“未知 / —”。
 */
type AdminWebhookDeliveryWire = Partial<AdminWebhookDelivery> & {
  ID?: string
  EndpointID?: string
  EventID?: string
  Status?: string
  Attempt?: number
  NextAttemptAtMs?: number
  ResponseStatus?: number
  ErrorCategory?: string
  CreatedAtMs?: number
  UpdatedAtMs?: number
  DeliveredAtMs?: number
}

function wireString(a: unknown, b: unknown): string {
  if (typeof a === "string") return a
  if (typeof b === "string") return b
  return ""
}

function wireNumber(a: unknown, b: unknown): number {
  if (typeof a === "number" && Number.isFinite(a)) return a
  if (typeof b === "number" && Number.isFinite(b)) return b
  return 0
}

function normalizeWebhookDelivery(raw: AdminWebhookDeliveryWire): AdminWebhookDelivery {
  return {
    id: wireString(raw.id, raw.ID),
    endpoint_id: wireString(raw.endpoint_id, raw.EndpointID),
    event_id: wireString(raw.event_id, raw.EventID),
    status: wireString(raw.status, raw.Status),
    attempt: wireNumber(raw.attempt, raw.Attempt),
    next_attempt_at_ms: wireNumber(raw.next_attempt_at_ms, raw.NextAttemptAtMs),
    response_status: wireNumber(raw.response_status, raw.ResponseStatus),
    error_category: wireString(raw.error_category, raw.ErrorCategory),
    created_at_ms: wireNumber(raw.created_at_ms, raw.CreatedAtMs),
    updated_at_ms: wireNumber(raw.updated_at_ms, raw.UpdatedAtMs),
    delivered_at_ms: wireNumber(raw.delivered_at_ms, raw.DeliveredAtMs),
  }
}

export interface AdminWebhookCreateInput {
  name: string
  description?: string
  url: string
  event_types: string[]
  scope_cluster?: string
  scope_instance?: string
  enabled?: boolean
}

export interface AdminWebhookUpdateInput {
  name?: string
  description?: string
  url?: string
  event_types?: string[]
  scope_cluster?: string
  scope_instance?: string
  enabled?: boolean
}

/** The create response contains the secret exactly once; callers must not persist or log it. */
export interface AdminWebhookCreateResult {
  id: string
  endpoint: AdminWebhookEndpoint & {
    /** One-time secret; never log or persist this value. */
    secret: string
  }
}

/** Secret rotation returns a new one-time secret; callers must not persist or log it. */
export interface AdminWebhookSecretResult {
  id: string
  /** One-time secret; never log or persist this value. */
  secret: string
}

export interface AdminWebhookTestResult {
  ok?: boolean
  event_id: string
}

export interface AdminWebhookDeliveryOptions {
  limit?: number
  status?: string
  before?: number | string
}


function headers(s: Session): HeadersInit {
  void s
  return {
    "Content-Type": "application/json",
  }
}

async function req<T>(s: Session, path: string, init?: RequestInit, expectedStatus?: number): Promise<T> {
  const res = await consoleFetch(`/api/admin/${path}`, { ...init, headers: headers(s) })
  const text = await res.text()
  // Gateway 的管理接口多数返回 JSON；认证/权限错误和反向代理错误页是纯文本。
  // 保留纯文本错误，页面才能按 status 给出 401/403 文案，而不是把它当成网络错误。
  let data: { message?: string; error?: string } = {}
  try {
    data = text ? JSON.parse(text) : {}
  } catch {
    data = { error: text.trim().slice(0, 200) }
  }
  // message 是给页面展示的中文说明；category 保留 Gateway 类别原文，给代理路由页等调用方解析。
  if (!res.ok) {
    throw Object.assign(new Error(data.message || data.error || `请求失败 (${res.status})`), {
      status: res.status,
      category: typeof data.error === "string" ? data.error : undefined,
    })
  }
  if (expectedStatus !== undefined && res.status !== expectedStatus) {
    throw new Error(`请求失败：预期 HTTP ${expectedStatus}，实际 HTTP ${res.status}`)
  }
  return data as T
}

// Webhook 错误体可能是 Gateway 的纯文本类别、JSON，或反向代理返回的 HTML 错误页。
// 页面只能展示固定文案：绝不回显响应体、URL、Secret 或其他上游细节。
const WEBHOOK_SAFE_CATEGORY = /^[a-z][a-z0-9_]{1,63}$/

const WEBHOOK_ERROR_MESSAGES: Record<string, string> = {
  webhook_not_found: "Webhook 不存在或已被删除",
  webhook_secret_unavailable: "Gateway 主密钥不可用，Webhook 密钥操作暂不可用",
  webhook_limit_exceeded: "Webhook 端点数量已达上限",
  webhook_endpoint_disabled: "Webhook 端点已停用，无法执行该操作",
  webhook_name_invalid: "Webhook 名称不合法",
  webhook_event_unknown: "包含不支持的事件类型",
  webhook_scope_invalid: "作用域不合法",
  webhook_url_too_long: "Webhook URL 过长",
  webhook_blocked_destination: "目标地址被安全策略拒绝",
  webhook_invalid_url: "Webhook URL 必须是合法的 HTTPS 地址",
  webhook_internal_error: "Gateway 内部错误，请稍后重试",
  gateway_auth_expired: "登录状态已过期，请重新登录",
  gateway_auth_invalid: "登录状态无效，请重新登录",
  unauthenticated: "登录状态已过期，请重新登录",
  unauthorized: "登录状态已过期，请重新登录",
  forbidden: "当前账号没有管理员权限",
  gateway_internal_error: "Gateway 内部错误，请稍后重试",
  gateway_bad_gateway: "Gateway 网关不可用，请稍后重试",
  gateway_unavailable: "Gateway 暂时不可用，请稍后重试",
}

function webhookErrorMessage(status: number, category?: string): string {
  if (category && WEBHOOK_ERROR_MESSAGES[category]) return WEBHOOK_ERROR_MESSAGES[category]
  return `Webhook 请求失败（HTTP ${status}）`
}

async function webhookReq<T>(s: Session, path: string, init?: RequestInit, expectedStatus?: number): Promise<T> {
  const res = await consoleFetch(`/api/admin/${path}`, { ...init, headers: headers(s) })
  const text = await res.text()
  let data: Record<string, unknown> = {}
  let parsed = false
  if (text) {
    try {
      const candidate: unknown = JSON.parse(text)
      if (candidate && typeof candidate === "object" && !Array.isArray(candidate)) {
        data = candidate as Record<string, unknown>
        parsed = true
      }
    } catch {
      // 纯文本错误类别（Gateway 的 webhook_* 错误就是这种形态）。
    }
  }
  if (!res.ok) {
    const field = typeof data.error === "string" ? data.error.trim() : ""
    // 只在整段文本本身就是一个类别标识符时才采用，避免把 HTML 错误页当成类别。
    const candidate = field || (!parsed ? text.trim() : "")
    const category = WEBHOOK_SAFE_CATEGORY.test(candidate) ? candidate : undefined
    throw Object.assign(new Error(webhookErrorMessage(res.status, category)), {
      status: res.status,
      category,
    })
  }
  if (expectedStatus !== undefined && res.status !== expectedStatus) {
    throw new Error(`Webhook 请求失败：预期 HTTP ${expectedStatus}，实际 HTTP ${res.status}`)
  }
  if (!parsed) throw new Error("Gateway 返回了无效的 Webhook 响应")
  return data as unknown as T
}

// ---------- 用户 ----------
export async function listUsers(s: Session): Promise<AdminUser[]> {
  if (s.demo) return (await import("./demo")).demoListUsers()
  return (await req<{ users: AdminUser[] }>(s, "users")).users || []
}

export async function createUser(
  s: Session,
  body: { name: string; password: string; admin?: boolean },
): Promise<void> {
  if (s.demo) return (await import("./demo")).demoCreateUser(body)
  await req(s, "users", { method: "POST", body: JSON.stringify(body) })
}

export async function setUserPassword(
  s: Session,
  body: { user_id: string; password: string },
): Promise<void> {
  if (s.demo) return
  await req(s, "users/password", { method: "POST", body: JSON.stringify(body) })
}

export async function setUserDisabled(
  s: Session,
  body: { user_id: string; disabled: boolean },
): Promise<void> {
  if (s.demo) return (await import("./demo")).demoSetUserDisabled(body)
  await req(s, "users/disable", { method: "POST", body: JSON.stringify(body) })
}

// ---------- 授权 ----------
export async function listGrants(s: Session, user?: string): Promise<AdminGrant[]> {
  if (s.demo) return (await import("./demo")).demoListGrants(user)
  const qs = user ? `?user=${encodeURIComponent(user)}` : ""
  return (await req<{ grants: AdminGrant[] }>(s, `grants${qs}`)).grants || []
}

export async function createGrant(
  s: Session,
  body: { user_id: string; cluster: string; instance: string },
): Promise<void> {
  if (s.demo) return (await import("./demo")).demoCreateGrant(body)
  await req(s, "grants", { method: "POST", body: JSON.stringify(body) })
}

export async function deleteGrant(s: Session, id: number): Promise<void> {
  if (s.demo) return (await import("./demo")).demoDeleteGrant(id)
  await req(s, `grants?id=${id}`, { method: "DELETE" })
}

// ---------- 令牌 ----------
export async function listTokens(s: Session): Promise<AdminToken[]> {
  if (s.demo) return (await import("./demo")).demoListTokens()
  return (await req<{ tokens: AdminToken[] }>(s, "tokens")).tokens || []
}

export async function createToken(
  s: Session,
  body: {
    user: string
    name?: string
    expires?: string
  },
): Promise<{ token: string; token_id: string }> {
  if (s.demo) return (await import("./demo")).demoCreateToken(body)
  return req<{ token: string; token_id: string }>(s, "tokens", {
    method: "POST",
    body: JSON.stringify(body),
  })
}

export async function revokeToken(s: Session, id: string): Promise<void> {
  if (s.demo) return (await import("./demo")).demoRevokeToken(id)
  await req(s, `tokens?id=${encodeURIComponent(id)}`, { method: "DELETE" })
}

// ---------- 代理路由 ----------
export async function listProxyRoutes(s: Session): Promise<ProxyRoute[]> {
  if (s.demo) return (await import("./demo")).demoListProxyRoutes()
  return (await req<{ routes: ProxyRoute[] | null }>(s, "proxy-routes")).routes || []
}

export async function setProxyRoute(s: Session, body: ProxyRouteBody): Promise<ProxyRoute> {
  if (s.demo) return (await import("./demo")).demoSetProxyRoute(body)
  return req<ProxyRoute>(s, "proxy-routes", { method: "POST", body: JSON.stringify(body) })
}

export async function deleteProxyRoute(s: Session, id: string): Promise<void> {
  if (s.demo) return (await import("./demo")).demoDeleteProxyRoute(id)
  await req(s, `proxy-routes?id=${encodeURIComponent(id)}`, { method: "DELETE" })
}

// ---------- 敏感词 ----------
export async function listSensitiveFilter(s: Session): Promise<SensitiveFilterView> {
  if (s.demo) return (await import("./demo")).demoListSensitiveFilter()
  const view = await req<SensitiveFilterView>(s, "sensitive-filter")
  return { enabled: view.enabled === true, rules: view.rules || [], whitelist: view.whitelist || [] }
}

// 总开关默认关闭；只有管理员显式开启后，规则才会拦截新的请求。
export async function setSensitiveFilterEnabled(s: Session, enabled: boolean): Promise<boolean> {
  if (s.demo) return (await import("./demo")).demoSetSensitiveFilterEnabled(enabled)
  const data = await req<{ enabled?: boolean }>(s, "sensitive-filter", {
    method: "PATCH",
    body: JSON.stringify({ enabled }),
  })
  return data.enabled === true
}

export async function addSensitiveRules(
  s: Session,
  rules: SensitiveRuleInput[],
): Promise<{ rules: SensitiveRule[]; skipped: string[] }> {
  if (s.demo) return (await import("./demo")).demoAddSensitiveRules(rules)
  const data = await req<{ rules?: SensitiveRule[]; skipped?: string[] }>(s, "sensitive-filter/rules", {
    method: "POST",
    body: JSON.stringify({ rules }),
  })
  return { rules: data.rules || [], skipped: data.skipped || [] }
}

export async function setSensitiveRuleEnabled(s: Session, id: string, enabled: boolean): Promise<SensitiveRule> {
  if (s.demo) return (await import("./demo")).demoSetSensitiveRuleEnabled(id, enabled)
  return req<SensitiveRule>(s, "sensitive-filter/rules", {
    method: "PATCH",
    body: JSON.stringify({ id, enabled }),
  })
}

export async function deleteSensitiveRule(s: Session, id: string): Promise<void> {
  if (s.demo) return (await import("./demo")).demoDeleteSensitiveRule(id)
  await req(s, `sensitive-filter/rules?id=${encodeURIComponent(id)}`, { method: "DELETE" })
}

export async function addSensitiveWhitelist(
  s: Session,
  input: SensitiveWhitelistInput,
): Promise<SensitiveWhitelistEntry> {
  if (s.demo) return (await import("./demo")).demoAddSensitiveWhitelist(input)
  return req<SensitiveWhitelistEntry>(s, "sensitive-filter/whitelist", {
    method: "POST",
    body: JSON.stringify(input),
  })
}

export async function deleteSensitiveWhitelist(s: Session, input: SensitiveWhitelistInput): Promise<void> {
  if (s.demo) return (await import("./demo")).demoDeleteSensitiveWhitelist(input)
  const query = new URLSearchParams()
  query.set("kind", input.kind)
  if (input.user_id) query.set("user_id", input.user_id)
  if (input.cluster) query.set("cluster", input.cluster)
  if (input.instance) query.set("instance", input.instance)
  await req(s, `sensitive-filter/whitelist?${query.toString()}`, { method: "DELETE" })
}

// ---------- 实例 ----------
export async function listInstances(
  s: Session,
  options?: { includeHistory?: boolean },
): Promise<AdminInstance[]> {
  if (s.demo) return (await import("./demo")).demoListInstances()
  const query = options?.includeHistory ? "?include=history" : ""
  return (await req<{ instances: AdminInstance[] }>(s, `instances${query}`)).instances || []
}

export interface AdminInstanceDeleteResult {
  cluster: string
  instance: string
  deleted: boolean
  remaining: {
    active_credential_grants: number
    revoking_credential_grants: number
    proxy_routes: number
    user_grants: number
  }
}

export async function archiveInstance(s: Session, body: { cluster: string; instance: string }): Promise<AdminInstance> {
  if (s.demo) return (await import("./demo")).demoArchiveInstance(body)
  return req<AdminInstance>(s, "instances/archive", { method: "POST", body: JSON.stringify(body) })
}

export async function unarchiveInstance(s: Session, body: { cluster: string; instance: string }): Promise<AdminInstance> {
  if (s.demo) return (await import("./demo")).demoUnarchiveInstance(body)
  return req<AdminInstance>(s, "instances/unarchive", { method: "POST", body: JSON.stringify(body) })
}

export async function deleteInstance(
  s: Session,
  body: { cluster: string; instance: string },
): Promise<AdminInstanceDeleteResult> {
  if (s.demo) return (await import("./demo")).demoDeleteInstance(body)
  return req<AdminInstanceDeleteResult>(s, "instances/delete", { method: "POST", body: JSON.stringify(body) })
}
const ADMIN_WEBHOOKS_READ_ONLY_ERROR = "Admin webhooks are read-only in demo sessions"

function demoWebhookMutation(): never {
  throw new Error(ADMIN_WEBHOOKS_READ_ONLY_ERROR)
}

function webhookIdPath(id: string): string {
  return encodeURIComponent(id)
}

/** List webhook endpoints. Demo sessions intentionally expose an empty read-only view. */
export async function listWebhooks(s: Session): Promise<AdminWebhookEndpoint[]> {
  if (s.demo) return []
  const data = await webhookReq<{ endpoints?: AdminWebhookEndpoint[] }>(s, "webhooks")
  return data.endpoints || []
}

/**
 * Create a webhook endpoint. The response secret is one-time-only: callers must use it
 * immediately and must never log or persist it.
 */
export async function createWebhook(
  s: Session,
  input: AdminWebhookCreateInput,
): Promise<AdminWebhookCreateResult> {
  if (s.demo) return demoWebhookMutation()
  return webhookReq<AdminWebhookCreateResult>(s, "webhooks", {
    method: "POST",
    body: JSON.stringify(input),
  }, 201)
}

export async function updateWebhook(
  s: Session,
  id: string,
  input: AdminWebhookUpdateInput,
): Promise<AdminWebhookEndpoint> {
  if (s.demo) return demoWebhookMutation()
  const data = await webhookReq<{ endpoint?: AdminWebhookEndpoint }>(s, `webhooks/${webhookIdPath(id)}`, {
    method: "PATCH",
    body: JSON.stringify(input),
  })
  if (!data.endpoint) throw new Error("Gateway returned no webhook endpoint")
  return data.endpoint
}

export async function deleteWebhook(s: Session, id: string): Promise<void> {
  if (s.demo) return demoWebhookMutation()
  await webhookReq(s, `webhooks/${webhookIdPath(id)}`, { method: "DELETE" })
}

/** Rotate the endpoint secret. The returned secret is one-time-only: never log or persist it. */
export async function rotateWebhookSecret(s: Session, id: string): Promise<AdminWebhookSecretResult> {
  if (s.demo) return demoWebhookMutation()
  return webhookReq<AdminWebhookSecretResult>(s, `webhooks/${webhookIdPath(id)}/rotate-secret`, { method: "POST" })
}

export async function testWebhook(s: Session, id: string): Promise<AdminWebhookTestResult> {
  if (s.demo) return demoWebhookMutation()
  return webhookReq<AdminWebhookTestResult>(s, `webhooks/${webhookIdPath(id)}/test`, { method: "POST" })
}

export async function listWebhookDeliveries(
  s: Session,
  id: string,
  options?: AdminWebhookDeliveryOptions,
): Promise<AdminWebhookDelivery[]> {
  if (s.demo) return []
  const query = new URLSearchParams()
  if (options?.limit !== undefined) query.set("limit", String(options.limit))
  if (options?.status !== undefined) query.set("status", options.status)
  if (options?.before !== undefined) query.set("before", String(options.before))
  const suffix = query.toString() ? `?${query.toString()}` : ""
  const data = await webhookReq<{ deliveries?: AdminWebhookDeliveryWire[] }>(s, `webhooks/${webhookIdPath(id)}/deliveries${suffix}`)
  if (!Array.isArray(data.deliveries)) return []
  return data.deliveries.map(normalizeWebhookDelivery)
}
