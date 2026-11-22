import { sessionIsAdmin, type Session } from "./client.ts"
import { consoleFetch } from "./network-error.ts"

export type PlatformMonitorRange = "1h" | "24h" | "7d" | "30d"

export const PLATFORM_MONITOR_RANGES: { value: PlatformMonitorRange; label: string }[] = [
  { value: "1h", label: "近 1 小时" },
  { value: "24h", label: "近 24 小时" },
  { value: "7d", label: "近 7 天" },
  { value: "30d", label: "近 30 天" },
]

export interface PlatformClusterOption {
  name: string
  online: number
  total: number
}

export interface PlatformVersionCount {
  release_commit: string
  count: number
}

export interface PlatformAgentSummary {
  online: number
  active: number
  busy: number
  recent_offline: number
  history: number
  archived: number
  enrolled_online: number
  unenrolled_online: number
  flapping_agents: number
  active_ops: number
  queued_ops: number
  active_sessions: number
  resource_locks: number
  versions: PlatformVersionCount[]
}

export type PlatformAgentActivity = "online" | "recent_offline" | "history" | "archived"

export interface PlatformMonitorAgent {
  cluster: string
  instance: string
  key: string
  activity: PlatformAgentActivity
  status: string
  busy: boolean
  busy_reason?: string
  enrolled: boolean
  release_commit?: string
  remote?: string
  connected_at?: string
  last_seen?: string
  heartbeat_age_ms?: number
  active_ops: number
  queued_ops: number
  sessions: string[]
  resources: string[]
  window_connects: number
  window_disconnects: number
  window_calls: number
  window_failed: number
  window_bytes_in: number
  window_bytes_out: number
  window_tokens: number
}

export interface PlatformConnectionEvent {
  id: number
  cluster: string
  instance: string
  key: string
  generation: number
  event: "online" | "offline" | string
  release_commit?: string
  remote?: string
  enrolled: boolean
  occurred_at: string
}

export interface PlatformLoadBucket {
  start_ms: number
  end_ms: number
  samples: number
  max_online_agents: number
  max_busy_agents: number
  max_active_ops: number
  max_queued_ops: number
  max_active_sessions: number
}

export interface PlatformUsageSummary {
  total: number
  running: number
  success: number
  failed: number
  outcome_unknown: number
  avg_duration_ms: number
  bytes_in: number
  bytes_out: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  active_users: number
  active_targets: number
  active_models: number
}

export interface PlatformUsageBucket {
  start_ms: number
  end_ms: number
  total: number
  success: number
  failed: number
  bytes_in: number
  bytes_out: number
  total_tokens: number
}

export interface PlatformActionUsage {
  action: string
  total: number
  success: number
  failed: number
  running: number
  avg_duration_ms: number
  bytes_in: number
  bytes_out: number
  total_tokens: number
}

export interface PlatformModelUsage {
  model: string
  total: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
}

export interface PlatformTargetUsage {
  cluster: string
  instance: string
  key: string
  online: boolean
  status: string
  total: number
  success: number
  failed: number
  outcome_unknown: number
  running: number
  avg_duration_ms: number
  bytes_in: number
  bytes_out: number
  total_tokens: number
  last_used_at?: string
}

export interface PlatformUserUsage {
  user_id: string
  user_name: string
  is_admin: boolean
  total: number
  success: number
  failed: number
  active_targets: number
  token_count: number
  bytes_in: number
  bytes_out: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  last_active_at?: string
}

export interface PlatformAuditItem {
  id: number
  user_id?: string
  user_name?: string
  cluster?: string
  instance?: string
  action: string
  session?: string
  command_summary?: string
  status: string
  error?: string
  correlation_id?: string
  model?: string
  total_tokens?: number
  bytes_in: number
  bytes_out: number
  started_at: string
  ended_at?: string
  duration_ms?: number
}

export interface PlatformDetachedTaskItem {
  audit_id: number
  operation_id: string
  user_id?: string
  user_name?: string
  is_own?: boolean
  cluster: string
  instance: string
  session: string
  command_summary: string
  status: string
  started_at: string
  finished_at?: string
  task_id?: string
  cancel_requested: boolean
}

export interface PlatformMonitorData {
  scope: "global" | "self"
  range: PlatformMonitorRange
  cluster?: string
  from_ms: number
  to_ms: number
  clusters: PlatformClusterOption[]
  agent_summary: PlatformAgentSummary
  agents: PlatformMonitorAgent[]
  connection_events: PlatformConnectionEvent[]
  load_trend: PlatformLoadBucket[]
  usage_summary: PlatformUsageSummary
  trend: PlatformUsageBucket[]
  by_action: PlatformActionUsage[]
  by_model: PlatformModelUsage[]
  by_target: PlatformTargetUsage[]
  by_user: PlatformUserUsage[]
  running_ops: PlatformAuditItem[]
  detached_tasks: PlatformDetachedTaskItem[]
  recent_issues: PlatformAuditItem[]
}

export type AgentVisibilityScope = "active" | "with_history" | "all"

export interface AdmissionSnapshot {
  limit: number
  inUse: number
  available: number
  holders: number
  queues: number
  raw: string
}

const ACTION_LABELS: Record<string, string> = {
  ask: "AI 助手",
  agent_prompt: "AI 助手",
  exec: "终端命令",
  shell: "终端命令",
  read: "文件读取",
  file_read: "文件读取",
  write: "文件写入",
  file_write: "文件写入",
  push: "工作区上传",
  pull: "工作区拉取",
  info: "节点信息",
  node_info: "节点信息",
  check: "部署核查",
  check_deployment: "部署核查",
  clean: "工作区清理",
  clean_workspace: "工作区清理",
  proxy: "HTTP 代理",
  "agent:upgrade": "Agent 升级",
  "audit:purge": "审计清理",
  "targets:list": "目标查询",
  "credential:create": "创建凭据",
  "credential:metadata": "凭据元数据",
  "credential:grant": "凭据授权",
  "credential:use": "使用凭据",
  "credential:verify": "验证凭据",
  "credential:rotate": "轮换凭据",
  "credential:revoke": "吊销凭据",
  "credential:audit": "凭据审计",
  "inventory:asset:assign": "资产范围分配",
  "monitor:run": "策略巡检采集",
  "monitor:schedule:create": "新建定时巡检",
  "monitor:schedule:update": "更新定时巡检",
  "monitor:schedule:toggle": "启停定时巡检",
  "monitor:schedule:delete": "删除定时巡检",
  "asset_monitor:collect": "监控隧道采集",
  "asset_monitor:ingest": "监控指标上报",
  "asset_monitor:rule": "监控告警规则",
  admin: "平台管理",
  "edge:admin": "边缘路由管理",
  "edge:view": "边缘路由查看",
}

const STATUS_META: Record<string, { label: string; variant: "success" | "warning" | "danger" | "primary" | "default" | "outline" }> = {
  success: { label: "成功", variant: "success" },
  running: { label: "执行中", variant: "warning" },
  executing: { label: "执行中", variant: "warning" },
  accepted: { label: "已受理", variant: "warning" },
  observing: { label: "观测中", variant: "warning" },
  outcome_unknown: { label: "结果未知", variant: "warning" },
  canceled: { label: "已取消", variant: "default" },
  busy: { label: "目标忙碌", variant: "warning" },
  offline: { label: "目标离线", variant: "warning" },
  rate_limited: { label: "触发限流", variant: "warning" },
  forbidden: { label: "权限拒绝", variant: "danger" },
  blocked: { label: "安全阻断", variant: "danger" },
  error: { label: "执行失败", variant: "danger" },
  failed: { label: "执行失败", variant: "danger" },
  timeout: { label: "执行超时", variant: "danger" },
}

export function formatActionLabel(action: string): string {
  const normalized = action.replace(/^doops_/, "")
  return ACTION_LABELS[normalized] || ACTION_LABELS[action] || action || "未分类"
}

export function describeAuditStatus(status: string): { label: string; variant: "success" | "warning" | "danger" | "primary" | "default" | "outline" } {
  return STATUS_META[status] || { label: status || "未知", variant: "default" }
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  const digits = index === 0 ? 0 : value >= 100 ? 0 : value >= 10 ? 1 : 2
  return `${value.toFixed(digits)} ${units[index]}`
}

export function formatTokens(tokens?: number | null): string {
  if (tokens === undefined || tokens === null || !Number.isFinite(tokens) || tokens <= 0) return "0"
  if (tokens < 1000) return String(Math.round(tokens))
  if (tokens < 1_000_000) {
    const k = tokens / 1000
    return `${k.toFixed(k >= 100 ? 0 : k >= 10 ? 1 : 2)}K`
  }
  const m = tokens / 1_000_000
  return `${m.toFixed(m >= 100 ? 0 : m >= 10 ? 1 : 2)}M`
}

export function formatDurationMs(ms?: number | null): string {
  if (ms === undefined || ms === null || !Number.isFinite(ms) || ms < 0) return "—"
  if (ms < 1000) return `${Math.round(ms)} ms`
  const sec = ms / 1000
  if (sec < 60) return `${sec.toFixed(sec >= 10 ? 1 : 2)} s`
  const min = Math.floor(sec / 60)
  const remSec = Math.round(sec % 60)
  if (min < 60) return remSec > 0 ? `${min}m ${remSec}s` : `${min}m`
  const hr = Math.floor(min / 60)
  const remMin = min % 60
  return remMin > 0 ? `${hr}h ${remMin}m` : `${hr}h`
}

export function formatSuccessRate(summary: Pick<PlatformUsageSummary, "success" | "failed">): string {
  const completed = summary.success + summary.failed
  if (completed <= 0) return "—"
  return `${((summary.success / completed) * 100).toFixed(1)}%`
}

export function summarizeVersionConsistency(versions: PlatformVersionCount[]): {
  consistent: boolean
  hasUnreported: boolean
  headline: string
} {
  if (!versions.length) {
    return { consistent: true, hasUnreported: false, headline: "当前无在线 Agent" }
  }
  const hasUnreported = versions.some((v) => !v.release_commit)
  if (versions.length === 1 && !hasUnreported) {
    return {
      consistent: true,
      hasUnreported: false,
      headline: `版本一致 · ${versions[0].release_commit.slice(0, 8)}`,
    }
  }
  if (versions.length === 1 && hasUnreported) {
    return {
      consistent: false,
      hasUnreported: true,
      headline: `${versions[0].count} 台在线 Agent 未上报发布提交`,
    }
  }
  return {
    consistent: false,
    hasUnreported,
    headline: `存在 ${versions.length} 个不同运行版本${hasUnreported ? "（含未上报）" : ""}`,
  }
}

export function filterPlatformAgents(
  agents: PlatformMonitorAgent[],
  visibility: AgentVisibilityScope,
  query: string,
): PlatformMonitorAgent[] {
  const needle = query.trim().toLowerCase()
  return agents.filter((agent) => {
    if (visibility === "active" && (agent.activity === "history" || agent.activity === "archived")) {
      return false
    }
    if (visibility === "with_history" && agent.activity === "archived") {
      return false
    }
    if (!needle) return true
    return (
      agent.key.toLowerCase().includes(needle) ||
      (agent.remote || "").toLowerCase().includes(needle) ||
      (agent.release_commit || "").toLowerCase().includes(needle) ||
      agent.sessions.some((s) => s.toLowerCase().includes(needle))
    )
  })
}

export function parseAdmissionSnapshot(raw: string): AdmissionSnapshot | null {
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const limit = Number(parsed.limit)
    const inUse = Number(parsed.inUse)
    const available = Number(parsed.available)
    const holders = Array.isArray(parsed.holders) ? parsed.holders.length : -1
    const queues = Array.isArray(parsed.queues) ? parsed.queues.length : -1
    if (!Number.isFinite(limit) || !Number.isFinite(inUse) || !Number.isFinite(available) || holders < 0 || queues < 0) {
      return null
    }
    return { limit, inUse, available, holders, queues, raw: JSON.stringify(parsed, null, 2) }
  } catch {
    return null
  }
}

export async function fetchPlatformMonitor(
  session: Session,
  options: { range: PlatformMonitorRange; cluster?: string },
  signal?: AbortSignal,
): Promise<PlatformMonitorData> {
  if (session.demo) {
    const { demoPlatformMonitor } = await import("./demo.ts")
    return demoPlatformMonitor(sessionIsAdmin(session), options.range, options.cluster)
  }
  const qs = new URLSearchParams({ range: options.range })
  if (options.cluster) qs.set("cluster", options.cluster)
  const res = await consoleFetch(`/api/platform-monitor?${qs.toString()}`, {
    cache: "no-store",
    signal,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(data.message || data.error || `读取平台监控失败 (${res.status})`)
  }
  return data as PlatformMonitorData
}
