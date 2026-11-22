import { consoleFetch } from "./network-error.ts"

export type AssetEndpoint = "catalog" | "cluster-profiles" | "records" | "workspace" | "machines" | "clusters" | "node-links" | "dashboard" | "systems" | "topology" | "coverage" | "monitor" | "inspections" | "machine-diagnostics"

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
    throw new Error(data.message || data.error || `读取资产失败（HTTP ${response.status}）`)
  }
  return data as T
}
