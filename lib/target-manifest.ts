import type { TargetManifestResponse } from "./assets"

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

/** 四态判据，顺序固定：离线 → 不支持同步 → 等待同步 → 已同步。 */
export function manifestStatus(data: TargetManifestResponse): ManifestSyncStatus {
  if (!data.online) return { label: "Agent 离线", variant: "warning" }
  if (!data.supported) return { label: "Agent 不支持同步", variant: "warning" }
  if (!data.manifest || (data.applied_revision ?? 0) < data.manifest.revision) {
    return { label: "等待同步", variant: "warning" }
  }
  return { label: "已同步", variant: "success" }
}

/** 不支持同步时 Agent 不上报 applied_revision，这一行必须隐藏，否则「已应用版本 8」与徽章自相矛盾。 */
export function showsAppliedRevision(data: TargetManifestResponse): boolean {
  return data.supported
}
