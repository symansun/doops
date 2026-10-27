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
