"use client"

import type { Session, Target } from "./client"

export type CredentialType = "registry" | "tls" | "opaque" | "helmRepository" | "gitToken"
export type CredentialScope = "personal" | "platform"
export type CredentialUse = "imagePull" | "gitCheckout" | "helmPull" | "tls" | "opaqueSecret"

export interface CredentialGrantScope {
  cluster: string
  instance: string
  namespace: string
  use: CredentialUse
  workload: { kind: string; name: string }
  image_repository?: string
  image_digest?: string
  helm_release?: string
  helm_chart?: string
  required_keys?: string[]
}

export interface CredentialGrant {
  id: string
  credential_id: string
  grantee_id: string
  scope: CredentialGrantScope
  state: string
  created_at?: string
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
  grants?: CredentialGrant[]
}

function headers(s: Session): HeadersInit {
  return {
    "Content-Type": "application/json",
    Authorization: `Bearer ${s.token}`,
    "x-doops-gateway": s.gateway,
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
        grants: [
          {
            id: "credgrant_demo_registry",
            credential_id: managed[0].id,
            grantee_id: "user_demo",
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
  const response = await request<{ credentials: CredentialResource[] }>(s, `?${qs}`)
  return response.credentials || []
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
): Promise<{ id: string; credential_id: string; state: string }> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request(s, `/${encodeURIComponent(credentialID)}/payload`, {
    method: "PUT",
    body: payload,
  })
}

export async function grantCredentialToSelf(
  s: Session,
  credentialID: string,
  scope: CredentialGrantScope,
): Promise<CredentialGrant> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  return request(s, `/${encodeURIComponent(credentialID)}/grants`, {
    method: "POST",
    body: JSON.stringify({ scope }),
  })
}

export async function revokeCredential(s: Session, credentialID: string): Promise<void> {
  if (s.demo) throw new Error("演示模式不执行凭据写操作")
  await request(s, `/${encodeURIComponent(credentialID)}/revoke`, { method: "POST" })
}
