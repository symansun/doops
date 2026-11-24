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

export async function archiveInstance(s: Session, body: { cluster: string; instance: string }): Promise<AdminInstance> {
  if (s.demo) return (await import("./demo")).demoArchiveInstance(body)
  return req<AdminInstance>(s, "instances/archive", { method: "POST", body: JSON.stringify(body) })
}

export async function unarchiveInstance(s: Session, body: { cluster: string; instance: string }): Promise<AdminInstance> {
  if (s.demo) return (await import("./demo")).demoUnarchiveInstance(body)
  return req<AdminInstance>(s, "instances/unarchive", { method: "POST", body: JSON.stringify(body) })
}
