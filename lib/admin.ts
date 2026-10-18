"use client"

import type { Session } from "./client"

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
  actions: string[]
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
}

export interface AdminOperation {
  id: string
  state: "waiting" | "running" | "cancel_requested" | "finalizing" | "outcome_unknown"
  audit_id?: number
  resource?: string
  requested_resource?: string
  effective_lock?: string
  blocked_by?: string[]
  user_id: string
  cluster: string
  instance: string
  action: string
  kind: string
  command_summary?: string
  started_at: string
  age_seconds: number
}

export const ALL_ACTIONS = [
  "exec",
  "ask",
  "read",
  "write",
  "push",
  "pull",
  "info",
  "check",
  "clean",
  "agent:upgrade",
  "targets:list",
  "credential:create",
  "credential:metadata",
  "credential:grant",
  "credential:use",
  "credential:verify",
  "credential:rotate",
  "credential:revoke",
  "credential:audit",
  "admin",
] as const

function headers(s: Session): HeadersInit {
  void s
  return {
    "Content-Type": "application/json",
  }
}

async function req<T>(s: Session, path: string, init?: RequestInit, expectedStatus?: number): Promise<T> {
  const res = await fetch(`/api/admin/${path}`, { ...init, headers: headers(s) })
  const text = await res.text()
  const data = text ? JSON.parse(text) : {}
  if (!res.ok) throw new Error(data.error || `请求失败 (${res.status})`)
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
  body: { user_id: string; cluster: string; instance: string; actions: string[] },
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

// ---------- 实例 ----------
export async function listInstances(s: Session): Promise<AdminInstance[]> {
  if (s.demo) return (await import("./demo")).demoListInstances()
  return (await req<{ instances: AdminInstance[] }>(s, "instances")).instances || []
}

// ---------- 运行中操作 ----------
export async function listOperations(s: Session, cluster: string, instance: string): Promise<AdminOperation[]> {
  if (s.demo) return (await import("./demo")).demoListOperations()
  const qs = new URLSearchParams({ cluster, instance })
  return (await req<{ operations: AdminOperation[] }>(s, `operations?${qs.toString()}`)).operations || []
}

export async function optimizeAuditIndexes(s: Session, sessionId: string): Promise<void> {
	if (s.demo) throw new Error("演示模式不执行索引维护")
	const result = await req<{ optimized: boolean; definitions_verified: boolean; query_plans: string[] }>(s, "audit/optimize", { method: "POST", body: JSON.stringify({ session_id: sessionId }) })
	if (!result.optimized || !result.definitions_verified || result.query_plans?.length !== 3) throw new Error("审计索引维护未通过验证")
}

export async function cancelOperation(s: Session, id: string, cluster: string, instance: string): Promise<void> {
  if (s.demo) return (await import("./demo")).demoCancelOperation(id)
  const result = await req<{ id: string; cancel_requested: boolean; state: string } | null>(
    s,
    `operations?cluster=${encodeURIComponent(cluster)}&id=${encodeURIComponent(id)}&instance=${encodeURIComponent(instance)}`,
    { method: "DELETE" },
    202,
  )
  if (!result || result.id !== id || result.cancel_requested !== true || result.state !== "cancel_requested") {
    throw new Error("取消请求响应无效：未确认对应操作的 cancel_requested 状态")
  }
}
