import type { AdminInstance } from "./admin"

// Keep these aligned with ActiveInstanceWindow and OfflineInstanceRetention
// in backend/agent/internal/server/instance_retention.go. The gateway document
// DOOPS_GATEWAY_TUNNEL.md owns the rule.
export const ACTIVE_INSTANCE_WINDOW_DAYS = 7
export const OFFLINE_INSTANCE_RETENTION_DAYS = 90
const DAY_MS = 24 * 60 * 60 * 1000

// Gateway emits Go's zero time as 0001-01-01T00:00:00Z. Treat that, blanks,
// and unparseable values as "no heartbeat".
export function gatewayTime(value?: string): number | null {
  if (!value) return null
  const ms = Date.parse(value)
  if (!Number.isFinite(ms) || ms <= 0) return null
  return ms
}

export type InstanceActivity = "online" | "recent" | "history"

// Online, and offline for at most 7 days (the boundary included), are active.
// Offline with no heartbeat is history.
export function instanceActivity(row: AdminInstance, now: number): InstanceActivity {
  if (row.status === "online") return "online"
  const lastSeen = gatewayTime(row.last_seen)
  if (lastSeen === null) return "history"
  return now - lastSeen <= ACTIVE_INSTANCE_WINDOW_DAYS * DAY_MS ? "recent" : "history"
}

export function offlineDays(row: AdminInstance, now: number): number | null {
  if (row.status === "online") return null
  const lastSeen = gatewayTime(row.last_seen)
  if (lastSeen === null) return null
  return Math.max(0, Math.floor((now - lastSeen) / DAY_MS))
}

export function offlineLabel(row: AdminInstance, now: number): string {
  if (row.status === "online") return ""
  const days = offlineDays(row, now)
  if (days === null) return "离线时间未知"
  if (days < 1) return "离线不到 1 天"
  return `离线 ${days} 天`
}

export function compareInstances(a: AdminInstance, b: AdminInstance): number {
  const onlineA = a.status === "online" ? 0 : 1
  const onlineB = b.status === "online" ? 0 : 1
  if (onlineA !== onlineB) return onlineA - onlineB
  const seenA = gatewayTime(a.last_seen) ?? -Infinity
  const seenB = gatewayTime(b.last_seen) ?? -Infinity
  if (seenA !== seenB) return seenB > seenA ? 1 : -1
  return a.cluster.localeCompare(b.cluster) || a.instance.localeCompare(b.instance)
}

export interface InstanceGroups {
  active: AdminInstance[]
  history: AdminInstance[]
}

export function groupInstances(rows: AdminInstance[], now: number, cluster = ""): InstanceGroups {
  const groups: InstanceGroups = { active: [], history: [] }
  for (const row of [...rows].sort(compareInstances)) {
    if (cluster && row.cluster !== cluster) continue
    if (instanceActivity(row, now) === "history") groups.history.push(row)
    else groups.active.push(row)
  }
  return groups
}

export interface InstanceClusterOption {
  name: string
  online: number
  total: number
}

export function instanceClusterOptions(rows: AdminInstance[]): InstanceClusterOption[] {
  const byName = new Map<string, InstanceClusterOption>()
  for (const row of rows) {
    const option = byName.get(row.cluster) || { name: row.cluster, online: 0, total: 0 }
    option.total++
    if (row.status === "online") option.online++
    byName.set(row.cluster, option)
  }
  return [...byName.values()].sort((a, b) => a.name.localeCompare(b.name))
}

export interface InstanceScope {
  active: number
  online: number
  history: number
}

export function summarizeInstanceScope(rows: AdminInstance[], now: number): InstanceScope {
  const scope: InstanceScope = { active: 0, online: 0, history: 0 }
  for (const row of rows) {
    const activity = instanceActivity(row, now)
    if (activity === "history") {
      scope.history++
      continue
    }
    scope.active++
    if (activity === "online") scope.online++
  }
  return scope
}
