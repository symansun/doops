import type { ManifestResource, TargetManifestResponse } from "./assets"

// 资源 kind 的中文标签。kind 的取值以 backend/agent/internal/server 为准：
// - Agent 观测（agent_resource_probe.go、agent_resource_inventory.go）：machine / cluster / namespace / container / site / database
// - 台账登记（agent_resource_store.go 的 declaredAgentResourcesSQL）：system / credential / image（catalog 的 kind 原样透传，website 归一成 site）
// - container_engine 是历史写法，保留兼容，避免老快照回退成英文。
const resourceKindLabels: Record<string, string> = {
  machine: "机器",
  cluster: "集群",
  namespace: "命名空间",
  system: "业务系统",
  database: "数据库",
  site: "网站",
  credential: "凭据",
  image: "镜像",
  container: "容器",
  container_engine: "容器引擎",
}

/** 未知 kind 原样返回，绝不抛错：后端新增 kind 时界面最多显示英文，不会白屏。 */
export function resourceKindLabel(kind: string): string {
  return resourceKindLabels[kind] ?? kind
}

export type ManifestSyncVariant = "success" | "warning"

export interface ManifestSyncStatus {
  label: string
  variant: ManifestSyncVariant
}

export function truncatedResourceSummary(manifest: NonNullable<TargetManifestResponse["manifest"]>): string {
  const counts = Object.entries(manifest.counts ?? {})
  const shown = manifest.resources.length
  if (!counts.length) return `已显示 ${shown} 条资源记录，清单已截断。`
  const total = counts.reduce((sum, [, count]) => sum + count, 0)
  const kinds = counts.map(([kind, count]) => `${resourceKindLabel(kind)} ${count} 条`).join("、")
  return `共 ${total} 条资源，已显示 ${shown} 条（${kinds}）。`
}

export function manifestStatus(data: TargetManifestResponse): ManifestSyncStatus {
  if (!data.online) return { label: "Agent 离线", variant: "warning" }
  if (!data.supported) return { label: "Agent 不支持同步", variant: "warning" }
  if (!data.last_observed_at) return { label: "等待首次观测", variant: "warning" }
  if (!data.manifest || (data.applied_revision ?? 0) < data.manifest.revision) {
    return { label: "等待同步", variant: "warning" }
  }
  return { label: "已同步", variant: "success" }
}

/** 不支持同步时 Agent 不上报 applied_revision，这一行必须隐藏，否则「已应用版本 8」与徽章自相矛盾。 */
export function showsAppliedRevision(data: TargetManifestResponse): boolean {
  return data.supported
}

export interface ManifestKindCount {
  kind: string
  label: string
  count: number
}

export interface ManifestResourceSummary {
  total: number
  byKind: ManifestKindCount[]
  byState: Record<ManifestResource["state"], number>
}

export function summarizeManifestResources(resources: readonly ManifestResource[]): ManifestResourceSummary {
  const kindCounts = new Map<string, number>()
  const byState: Record<ManifestResource["state"], number> = {
    verified: 0,
    discovered: 0,
    unverified: 0,
    lost: 0,
    conflict: 0,
  }
  for (const r of resources) {
    kindCounts.set(r.kind, (kindCounts.get(r.kind) ?? 0) + 1)
    if (r.state in byState) {
      byState[r.state] += 1
    }
  }
  const byKind = Array.from(kindCounts.entries())
    .map(([kind, count]) => ({ kind, label: resourceKindLabel(kind), count }))
    .sort((a, b) => b.count - a.count || a.kind.localeCompare(b.kind))
  return { total: resources.length, byKind, byState }
}

export function filterManifestResources(
  resources: readonly ManifestResource[],
  opts: { kind?: string; state?: string; query?: string },
): ManifestResource[] {
  const kind = (opts.kind ?? "").trim()
  const state = (opts.state ?? "").trim()
  const term = (opts.query ?? "").trim().toLowerCase()
  return resources.filter((r) => {
    if (kind && kind !== "all" && r.kind !== kind) return false
    if (state && state !== "all" && r.state !== state) return false
    if (!term) return true
    return [r.name, r.ref, r.kind, resourceKindLabel(r.kind), r.reason ?? ""]
      .some((field) => field.toLowerCase().includes(term))
  })
}

export function extractManifestNamespaces(resources: readonly ManifestResource[]): string[] {
  const set = new Set<string>()
  for (const r of resources) {
    if (r.kind !== "namespace") continue
    const name = r.name.trim()
    if (name) set.add(name)
  }
  return Array.from(set).sort((a, b) => a.localeCompare(b))
}

export type WorkspaceAssetKind = "machine" | "cluster" | "system"

export interface WorkspaceAssetRow {
  kind: WorkspaceAssetKind
  asset_id: string
  name: string
  address?: string
  linked: boolean
  assigned: boolean
}

export function filterWorkspaceAssets(
  assets: readonly WorkspaceAssetRow[],
  opts: { kind?: string; query?: string },
): WorkspaceAssetRow[] {
  const kind = (opts.kind ?? "").trim()
  const term = (opts.query ?? "").trim().toLowerCase()
  return assets.filter((row) => {
    if (kind && kind !== "all" && row.kind !== kind) return false
    if (!term) return true
    return [row.name, row.asset_id, row.address ?? "", resourceKindLabel(row.kind)]
      .some((field) => field.toLowerCase().includes(term))
  })
}
