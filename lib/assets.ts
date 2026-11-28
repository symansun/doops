import { consoleFetch, httpGatewayErrorMessage, readJsonObject } from "./network-error.ts"
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
  readonly status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = "AssetRequestError"
    this.status = status
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
  const data = await readJsonObject(response)
  if (!response.ok) {
    const message =
      (typeof data?.message === "string" && data.message) ||
      (typeof data?.error === "string" && data.error) ||
      httpGatewayErrorMessage(response.status, `读取资产失败（HTTP ${response.status}）`)
    throw new AssetRequestError(message, response.status)
  }
  if (!data) {
    throw new AssetRequestError("读取资产返回了无效响应", 502)
  }
  return data as T
}
