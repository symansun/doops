"use client"

import type { Session, Target } from "./client"

export type CredentialType = "registry" | "tls" | "opaque" | "helmRepository" | "gitToken" | "modelProvider"
export type CredentialScope = "personal" | "platform"
export type CredentialUse =
  | "imagePull"
  | "gitCheckout"
  | "helmPull"
  | "tls"
  | "opaqueSecret"
  | "database"
  | "modelProvider"
export type CredentialVersionState = "staged" | "active" | "superseded" | "revoked"

export interface CredentialBindingScope {
  cluster: string
  instance: string
  namespace: string
  use: CredentialUse
  workload: { kind: string; name: string }
  database_asset_id?: string
  environment_id?: string
  database_engine?: "mysql" | "postgres" | "mongodb"
  database_name?: string
  image_repository?: string
  image_digest?: string
  helm_release?: string
  helm_chart?: string
  required_keys?: string[]
}

export interface CredentialBinding {
  id: string
  credential_id: string
  scope: CredentialBindingScope
  state: string
  created_at?: string
}

export interface CredentialVersion {
  id: string
  credential_id: string
  payload_digest: string
  state: CredentialVersionState
  created_at?: string
  promoted_at?: string
  revoked_at?: string
}

export interface CredentialVerificationEvidence {
  status: string
  resourceVersion?: string
  digest?: string
  errorCategory?: string
}

export interface CredentialVerificationResponse {
  status: string
  evidence: CredentialVerificationEvidence[]
}

export interface CredentialResource {
  id: string
  name: string
  scope: CredentialScope
  owner_id?: string
  type: CredentialType
  state: string
  created_at?: string
  updated_at?: string
  bindings?: CredentialBinding[]
}

function headers(s: Session): HeadersInit {
  void s
  return {
    "Content-Type": "application/json",
  }
}

async function request<T>(s: Session, path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/credentials${path}`, { ...init, headers: headers(s) })
  const text = await res.text()
  let data: Record<string, unknown> = {}
  try {
    data = text ? (JSON.parse(text) as Record<string, unknown>) : {}
  } catch {
    throw new Error(`凭据服务返回了无效响应 (${res.status})`)
  }
  if (!res.ok) throw new Error(String(data.error || `凭据请求失败 (${res.status})`).trim())
  return data as T
}

export async function listCredentials(
  s: Session,
  target: Target,
  view: "target" | "managed",
): Promise<CredentialResource[]> {
  if (s.demo) {
    const managed: CredentialResource[] = [
      {
        id: "cred_demo_registry",
        name: "registry-production",
        scope: "personal",
        type: "registry",
        state: "active",
        updated_at: "2026-07-18T03:20:00Z",
      },
      {
        id: "cred_demo_tls",
        name: "gateway-tls",
        scope: "platform",
        type: "tls",
        state: "active",
        updated_at: "2026-07-17T08:00:00Z",
      },
    ]
    if (view === "managed") return managed.filter((credential) => credential.scope === "personal")
    return [
      {
        ...managed[0],
        bindings: [
          {
            id: "credgrant_demo_registry",
            credential_id: managed[0].id,
            state: "active",
            scope: {
              cluster: target.cluster,
              instance: target.instance,
              namespace: "apps",
              use: "imagePull",
              workload: { kind: "deployment", name: "oilan-api" },
              image_repository: "registry.example.com/oilan/api",
              image_digest: `sha256:${"a".repeat(64)}`,
            },
          },
        ],
      },
    ]
  }
  const qs = new URLSearchParams({ view })
  if (view === "target") {
    qs.set("cluster", target.cluster)
    qs.set("instance", target.instance)
  }
  const response = await request<{ credentials: Array<CredentialResource & { grants?: CredentialBinding[] }> }>(s, `?${qs}`)
  return (response.credentials || []).map(({ grants, ...credential }) => ({
    ...credential,
    ...(grants ? { bindings: grants } : {}),
  }))
}

export async function listCredentialVersions(
  s: Session,
  credentialID: string,
): Promise<CredentialVersion[]> {
  if (s.demo) throw new Error("演示模式不查询凭据版本")
  const response = await request<{ versions?: unknown }>(
    s,
    `/${encodeURIComponent(credentialID)}/versions`,
  )
  if (!Array.isArray(response.versions)) {
    throw new Error("凭据版本响应缺少 versions")
  }
  return response.versions as CredentialVersion[]
}

export async function listCredentialBindings(
  s: Session,
  credentialID: string,
): Promise<CredentialBinding[]> {
  if (s.demo) throw new Error("演示模式不查询凭据绑定")
  const response = await request<{ grants?: unknown }>(
    s,
    `/${encodeURIComponent(credentialID)}/grants`,
  )
  if (!Array.isArray(response.grants)) {
    throw new Error("凭据绑定响应缺少 grants")
  }
  return response.grants as CredentialBinding[]
}

export async function createCredential(
  s: Session,
  body: { name: string; scope: CredentialScope; type: CredentialType },
): Promise<CredentialResource> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request<CredentialResource>(s, "", {
    method: "POST",
    body: JSON.stringify(body),
  })
}

export async function putCredentialPayload(
  s: Session,
  credentialID: string,
  payload: string,
): Promise<CredentialVersion> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request<CredentialVersion>(s, `/${encodeURIComponent(credentialID)}/payload`, {
    method: "PUT",
    body: payload,
  })
}

export async function bindCredentialToTarget(
  s: Session,
  credentialID: string,
  scope: CredentialBindingScope,
): Promise<CredentialBinding> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request<CredentialBinding>(s, `/${encodeURIComponent(credentialID)}/grants`, {
    method: "POST",
    body: JSON.stringify({ scope }),
  })
}

export async function verifyCredentialVersion(
  s: Session,
  credentialID: string,
  body: {
    version_id: string
    session_id: string
    scope: CredentialBindingScope
  },
): Promise<CredentialVerificationResponse> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request<CredentialVerificationResponse>(s, `/${encodeURIComponent(credentialID)}/verify`, {
    method: "POST",
    body: JSON.stringify(body),
  })
}

export async function promoteCredentialVersion(
  s: Session,
  credentialID: string,
  versionID: string,
): Promise<{ credential_id: string; version_id: string; promoted: boolean }> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request<{ credential_id: string; version_id: string; promoted: boolean }>(
    s,
    `/${encodeURIComponent(credentialID)}/promote`,
    {
      method: "POST",
      body: JSON.stringify({ version_id: versionID }),
    },
  )
}

export async function revokeCredentialBinding(
  s: Session,
  credentialID: string,
  bindingID: string,
): Promise<CredentialBinding> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request<CredentialBinding>(s, `/${encodeURIComponent(credentialID)}/grants`, {
    method: "DELETE",
    body: JSON.stringify({ grant_id: bindingID }),
  })
}

export async function revokeCredential(s: Session, credentialID: string): Promise<void> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  await request(s, `/${encodeURIComponent(credentialID)}/revoke`, { method: "POST" })
}

export interface CredentialBundleItem {
  credential_id: string
}

export interface CredentialBundle {
  id: string
  name: string
  scope: CredentialScope
  state: string
  created_at?: string
  updated_at?: string
  items: CredentialBundleItem[]
}

export interface CredentialBundleCreateInput {
  name: string
  scope: CredentialScope
  credentialIDs: string[]
}

const BUNDLE_ERROR_MESSAGES: Record<string, string> = {
  credential_bundle_not_found: "未找到该凭据组，或当前账号无权查看",
  credential_reference_ambiguous: "存在同名的个人与平台凭据组，无法确定要查看哪一个",
  credential_forbidden: "无权限：只能把自己可管理的凭据放进凭据组，平台凭据组仅平台管理员可创建",
  Forbidden: "无权限：平台凭据组仅平台管理员可创建",
  credential_audit_unavailable: "凭据审计或所含凭据不可用，Gateway 已拒绝本次操作",
  credential_key_unavailable: "Gateway 密钥不可用，暂时无法处理凭据",
}

// Gateway errors are stable categories; anything else is replaced by a generic
// message so unexpected response text is never shown in the console.
function bundleErrorMessage(status: number, raw: unknown): string {
  const text = String(raw || "").trim()
  if (BUNDLE_ERROR_MESSAGES[text]) return BUNDLE_ERROR_MESSAGES[text]
  if (text === "未建立 Gateway 会话") return text
  if (text.startsWith("无法连接 gateway")) return "无法连接 Gateway，请检查目标是否可用"
  if (status === 401) return "Gateway 会话已失效，请重新登录"
  if (status === 403) return "无权限执行该凭据组操作"
  if (status === 404) return BUNDLE_ERROR_MESSAGES.credential_bundle_not_found
  if (status === 400) return "Gateway 拒绝了凭据组请求：名称、归属或凭据列表无效"
  if (status >= 500) return `Gateway 暂时不可用 (${status})`
  return `凭据组请求失败 (${status})`
}

// Only metadata fields are kept; any other field in the response is dropped.
function bundleMetadata(raw: unknown): CredentialBundle {
  const value = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>
  const id = typeof value.id === "string" ? value.id.trim() : ""
  if (!id) throw new Error("凭据组响应缺少 id")
  const items = Array.isArray(value.items) ? value.items : []
  return {
    id,
    name: String(value.name ?? ""),
    scope: value.scope === "platform" ? "platform" : "personal",
    state: String(value.state ?? ""),
    ...(typeof value.created_at === "string" ? { created_at: value.created_at } : {}),
    ...(typeof value.updated_at === "string" ? { updated_at: value.updated_at } : {}),
    items: items
      .map((item) => (item && typeof item === "object" ? (item as Record<string, unknown>).credential_id : ""))
      .filter((credentialID): credentialID is string => typeof credentialID === "string" && credentialID !== "")
      .map((credential_id) => ({ credential_id })),
  }
}

async function bundleRequest(query: string, init?: RequestInit): Promise<CredentialBundle> {
  const res = await fetch(`/api/credential-bundles${query}`, {
    ...init,
    headers: { "Content-Type": "application/json" },
    cache: "no-store",
  })
  const text = await res.text()
  let data: unknown = {}
  try {
    data = text ? JSON.parse(text) : {}
  } catch {
    throw new Error(`凭据服务返回了无效响应 (${res.status})`)
  }
  if (!res.ok) {
    throw new Error(bundleErrorMessage(res.status, (data as Record<string, unknown>)?.error))
  }
  return bundleMetadata(data)
}

export function validateCredentialBundleInput(input: CredentialBundleCreateInput): string {
  if (!input.name.trim()) return "请填写凭据组名称"
  if (input.scope !== "personal" && input.scope !== "platform") return "请选择凭据组归属"
  const ids = input.credentialIDs.map((id) => id.trim())
  if (!ids.length || ids.some((id) => !id)) return "至少选择一个凭据"
  if (new Set(ids).size !== ids.length) return "凭据组不能包含重复凭据"
  return ""
}

export async function createCredentialBundle(
  s: Session,
  input: CredentialBundleCreateInput,
): Promise<CredentialBundle> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  const invalid = validateCredentialBundleInput(input)
  if (invalid) throw new Error(invalid)
  return bundleRequest("", {
    method: "POST",
    body: JSON.stringify({
      name: input.name.trim(),
      scope: input.scope,
      items: input.credentialIDs.map((id) => ({ credential_id: id.trim() })),
    }),
  })
}

export async function getCredentialBundle(s: Session, name: string): Promise<CredentialBundle> {
  const trimmed = name.trim()
  if (!trimmed) throw new Error("请填写凭据组名称")
  if (s.demo) {
    if (trimmed !== "release-demo") throw new Error(BUNDLE_ERROR_MESSAGES.credential_bundle_not_found)
    return {
      id: "credbundle_demo_release",
      name: "release-demo",
      scope: "personal",
      state: "active",
      created_at: "2026-07-18T03:30:00Z",
      updated_at: "2026-07-18T03:30:00Z",
      items: [{ credential_id: "cred_demo_registry" }],
    }
  }
  return bundleRequest(`?${new URLSearchParams({ name: trimmed })}`)
}
