import { consoleFetch } from "./network-error.ts"
import type { Target } from "./gateway"

export type AssetEndpoint = "catalog" | "cluster-profiles" | "records" | "workspace" | "machines" | "clusters" | "node-links" | "dashboard" | "systems" | "topology" | "coverage" | "monitor" | "inspections" | "machine-diagnostics" | "target-manifest"

export interface ManifestResource {
  ref: string
  kind: string
  name: string
  state: "verified" | "discovered" | "unverified" | "lost" | "conflict"
  source: string[]
  observed_at?: string
  reason?: string
  can_register?: boolean
}

export interface TargetManifestResponse {
  manifest?: {
    target: Pick<Target, "cluster" | "instance" | "mode" | "kubernetes_available">
    revision: number
    digest: string
    generated_at: string
    resources: ManifestResource[]
    counts?: Record<string, number>
    truncated?: boolean
  } | null
  last_observed_at?: string
  applied_revision?: number
  online: boolean
  supported: boolean
}

export class AssetRequestError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = "AssetRequestError"
  }
}

let demoAssetsActive = false

export function setDemoAssetsActive(active: boolean) {
  demoAssetsActive = active
}

export function isDemoAssetsActive(): boolean {
  return demoAssetsActive
}

export async function assetRequest<T>(kind: AssetEndpoint, body?: object, query?: URLSearchParams, signal?: AbortSignal): Promise<T> {
  if (demoAssetsActive) {
    const { demoAssetRequest } = await import("./demo-assets")
    return (await demoAssetRequest(kind, body, query)) as T
  }
  const response = await consoleFetch(`/api/assets/${kind}${query?.size ? `?${query}` : ""}`, body ? {
    method: "POST", signal,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  } : { cache: "no-store", signal })
  const data = await response.json()
  if (!response.ok) {
    throw new AssetRequestError(data.message || data.error || `读取资产失败（HTTP ${response.status}）`, response.status)
  }
  return data as T
}
