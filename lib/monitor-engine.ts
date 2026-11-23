import type { Session } from "./client.ts"
import type { MonitorRunDiff } from "./monitor-metrics.ts"
import { consoleFetch } from "./network-error.ts"

export type MonitorRange = "1h" | "6h" | "24h" | "7d"

export const MONITOR_RANGES: { value: MonitorRange; label: string }[] = [
  { value: "1h", label: "近 1 小时" },
  { value: "6h", label: "近 6 小时" },
  { value: "24h", label: "近 24 小时" },
  { value: "7d", label: "近 7 天" },
]

export const INTERVAL_PRESETS: { value: number; label: string }[] = [
  { value: 60, label: "每 1 分钟" },
  { value: 300, label: "每 5 分钟" },
  { value: 900, label: "每 15 分钟" },
  { value: 3600, label: "每 1 小时" },
  { value: 21600, label: "每 6 小时" },
  { value: 86400, label: "每天（24 小时）" },
  { value: 604800, label: "每周（7 天）" },
]

export interface MonitorTargetRef {
  cluster: string
  instance: string
}

export interface MonitorThresholds {
  cpu_warn_percent?: number
  cpu_crit_percent?: number
  mem_warn_percent?: number
  mem_crit_percent?: number
  disk_warn_percent?: number
  disk_crit_percent?: number
  disk_latency_warn_ms?: number
  latency_warn_ms?: number
  error_rate_warn_pct?: number
  cert_warn_days?: number
  min_uptime_seconds?: number
  denied_processes?: string[]
}

export interface MonitorScheduleRecord {
  id: string
  name: string
  description: string
  enabled: boolean
  scope_mode: "all_online" | "targets" | "tags"
  targets: MonitorTargetRef[]
  tags: string[]
  skills?: string[]
  prompt?: string
  command?: string
  thresholds: MonitorThresholds
  interval_seconds: number
  next_run_at_ms: number
  last_run_at_ms: number
  last_run_id?: string
  last_status?: string
  created_by?: string
  created_at: string
  updated_at: string
}

export interface MonitorRunRecord {
  id: string
  schedule_id?: string
  schedule_name?: string
  trigger_kind: "scheduled" | "manual" | "retest" | string
  triggered_by?: string
  status: "running" | "success" | "warning" | "critical" | "partial" | "failed" | string
  skills?: string[]
  prompt?: string
  command?: string
  total_targets: number
  total_assets: number
  passed_checks: number
  warning_checks: number
  failed_checks: number
  skipped_checks: number
  offline_targets: number
  metrics_written: number
  started_at_ms: number
  finished_at_ms: number
  duration_ms: number
  summary?: string
}

export interface MonitorInspectCheck {
  id: string
  name: string
  category: string
  status: "pass" | "warning" | "critical" | "skipped" | string
  value?: string
  threshold?: string
  detail?: string
}

export interface MonitorInspectMetrics {
  cpu_cores?: number | null
  cpu_total_cores?: number | null
  cpu_percent?: number | null
  kernel_cpu_cores?: number | null
  memory_used_bytes?: number | null
  memory_total_bytes?: number | null
  memory_percent?: number | null
  kernel_slab_bytes?: number | null
  disk_used_bytes?: number | null
  disk_total_bytes?: number | null
  disk_percent?: number | null
  net_bytes_per_sec?: number | null
  disk_iops?: number | null
  disk_latency_ms?: number | null
  gpu_percent?: number | null
  response_time_ms?: number | null
  error_rate?: number | null
  active_connections?: number | null
  requests_per_sec?: number | null
  uptime_seconds?: number | null
  load_1m?: number | null
  os_info?: string
  kernel_info?: string
}

export interface MonitorRunItemRecord {
  id: number
  run_id: string
  cluster: string
  instance: string
  asset_id: string
  asset_name: string
  asset_kind: string
  strategy: string
  collector_method: string
  status: "pass" | "warning" | "critical" | "offline" | "error" | "skipped" | string
  reason?: string
  started_at_ms: number
  finished_at_ms: number
  duration_ms: number
  metrics: MonitorInspectMetrics
  checks: MonitorInspectCheck[]
  evidence?: Record<string, unknown>
  artifact_format?: "json" | "markdown+json" | "markdown" | "text" | string
  artifact_markdown?: string
  artifact_json?: unknown
  raw_output?: string
  /** 跨次对比，仅 GET /v1/monitor/runs/<id> 详情响应提供；创建响应不含该字段。 */
  diff?: MonitorRunDiff | null
}

export interface MonitorResourceSnapshot {
  id?: number
  cluster: string
  instance: string
  asset_id: string
  asset_name: string
  asset_kind: string
  strategy: string
  collector_method: string
  collected_at_ms: number
  run_id?: string
  status: "ok" | "warning" | "critical" | "offline" | "error" | string
  online: boolean
  stale: boolean
  cpu_cores?: number | null
  cpu_total_cores?: number | null
  cpu_percent?: number | null
  memory_used_bytes?: number | null
  memory_total_bytes?: number | null
  memory_percent?: number | null
  disk_used_bytes?: number | null
  disk_total_bytes?: number | null
  disk_percent?: number | null
  net_bytes_per_sec?: number | null
  disk_iops?: number | null
  disk_latency_ms?: number | null
  gpu_percent?: number | null
  response_time_ms?: number | null
  error_rate?: number | null
  active_connections?: number | null
  requests_per_sec?: number | null
  uptime_seconds?: number | null
  load_1m?: number | null
  os_info?: string
  kernel_info?: string
  tags: string[]
  details?: Record<string, unknown>
}

export interface MonitorOverviewSummary {
  total_assets: number
  online_agents: number
  offline_agents: number
  covered_assets: number
  probe_coverage_pct: number
  healthy_assets: number
  warning_assets: number
  critical_assets: number
  offline_assets: number
  avg_cpu_percent: number
  avg_memory_percent: number
  avg_disk_percent: number
  total_cpu_cores: number
  used_cpu_cores: number
  total_memory_bytes: number
  used_memory_bytes: number
  total_disk_bytes: number
  used_disk_bytes: number
  active_schedules: number
  total_runs: number
  open_threshold_alerts?: number
  last_collected_at_ms: number
}

export interface MonitorThresholdAlert {
  cluster: string
  instance: string
  type: string
  asset_id: string
  namespace?: string
  origin: string
  metric: string
  device_id?: string
  threshold: number
  duration_seconds: number
  opened_ms: number
}

export interface MonitorTrendBucket {
  start_ms: number
  end_ms: number
  samples: number
  avg_cpu_percent?: number | null
  avg_memory_percent?: number | null
  avg_disk_percent?: number | null
  max_cpu_percent?: number | null
  max_memory_percent?: number | null
}

export interface MonitorOverviewData {
  scope: "global" | "self"
  range: MonitorRange
  from_ms: number
  to_ms: number
  summary: MonitorOverviewSummary
  by_kind: Record<string, number>
  resources: MonitorResourceSnapshot[]
  top_cpu: MonitorResourceSnapshot[]
  top_memory: MonitorResourceSnapshot[]
  top_disk: MonitorResourceSnapshot[]
  trends: MonitorTrendBucket[]
  threshold_alerts?: MonitorThresholdAlert[]
  recent_runs: MonitorRunRecord[]
}

export interface MonitorHistoryData {
  cluster: string
  instance: string
  asset_kind: string
  asset_id: string
  range: MonitorRange
  from_ms: number
  to_ms: number
  latest?: MonitorResourceSnapshot
  points: MonitorResourceSnapshot[]
}

export interface MonitorSchedulesData {
  schedules: MonitorScheduleRecord[]
  defaults: MonitorThresholds
}

export interface MonitorRunDetailData {
  run: MonitorRunRecord
  items: MonitorRunItemRecord[]
}

export const ASSET_KIND_LABELS: Record<string, string> = {
  vm: "虚拟机",
  physical: "物理机",
  k8s: "Kubernetes 集群",
  container: "容器运行时",
  app: "应用 / Web",
  database: "数据库",
  skill: "Skill 巡检",
  command: "命令巡检",
  prompt: "提示词巡检",
}

export const COLLECTOR_METHOD_LABELS: Record<string, string> = {
  agent_local: "Agent 本机",
  ssh: "SSH 只读",
  k8s_api: "K8s API",
  http_probe: "HTTP/TLS 探针",
  dosql: "DoSql 只读",
  tcp_probe: "TCP 探针",
  agent_skill: "Agent Skill",
  agent_prompt: "AI 提示词",
  shell_command: "Shell 命令",
  skill_script: "Skill 脚本",
}

export const STRATEGY_LABELS: Record<string, string> = {
  vm_host: "虚机 / 物理机巡检",
  ssh_host: "SSH 远程主机",
  k8s_cluster: "K8s 集群巡检",
  container_runtime: "容器运行时",
  app_service: "应用与 Web/API",
  database_service: "数据库巡检",
  command: "Shell 命令巡检",
  custom_prompt: "提示词巡检",
}

export const TRIGGER_LABELS: Record<string, string> = {
  scheduled: "定时任务",
  manual: "手动执行",
  retest: "异常复检",
}

export function describeMonitorStatus(status: string): {
  label: string
  variant: "success" | "warning" | "danger" | "primary" | "outline" | "default"
} {
  switch (status) {
    case "ok":
    case "pass":
    case "success":
      return { label: "正常", variant: "success" }
    case "warning":
      return { label: "告警", variant: "warning" }
    case "partial":
      return { label: "部分完成", variant: "warning" }
    case "critical":
      return { label: "严重", variant: "danger" }
    case "failed":
    case "error":
      return { label: "失败", variant: "danger" }
    case "offline":
      return { label: "离线", variant: "default" }
    case "running":
      return { label: "执行中", variant: "primary" }
    case "skipped":
      return { label: "跳过", variant: "outline" }
    default:
      return { label: status || "未知", variant: "outline" }
  }
}

export function formatIntervalLabel(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—"
  if (seconds < 3600) return `每 ${Math.round(seconds / 60)} 分钟`
  if (seconds < 86400) return `每 ${Math.round(seconds / 3600)} 小时`
  if (seconds === 86400) return "每天"
  if (seconds === 604800) return "每周"
  return `每 ${Math.round(seconds / 86400)} 天`
}

export function formatBytesCompact(bytes?: number | null): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return "—"
  if (bytes === 0) return "0 B"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)} ${units[i]}`
}

export function formatPercent(val?: number | null): string {
  if (val == null || !Number.isFinite(val)) return "—"
  return `${val.toFixed(1)}%`
}

export function formatRateBytes(bps?: number | null): string {
  if (bps == null || !Number.isFinite(bps) || bps < 0) return "—"
  return `${formatBytesCompact(bps)}/s`
}

export function buildRunReportCSV(detail: MonitorRunDetailData): string {
  const esc = (v: unknown) => `"${String(v ?? "").replaceAll('"', '""')}"`
  const header = ["run_id", "cluster", "instance", "asset_id", "asset_kind", "strategy", "collector", "item_status", "check_id", "check_name", "check_status", "value", "threshold", "detail"]
  const lines = [header.join(",")]
  for (const item of detail.items) {
    if (!item.checks?.length) {
      lines.push([
        esc(detail.run.id), esc(item.cluster), esc(item.instance), esc(item.asset_id), esc(item.asset_kind),
        esc(item.strategy), esc(item.collector_method), esc(item.status),
        "", "", esc(item.status), "", "", esc(item.reason || ""),
      ].join(","))
      continue
    }
    for (const c of item.checks) {
      lines.push([
        esc(detail.run.id), esc(item.cluster), esc(item.instance), esc(item.asset_id), esc(item.asset_kind),
        esc(item.strategy), esc(item.collector_method), esc(item.status),
        esc(c.id), esc(c.name), esc(c.status), esc(c.value || ""), esc(c.threshold || ""), esc(c.detail || ""),
      ].join(","))
    }
  }
  return "\uFEFF" + lines.join("\n")
}

async function monitorRequest<T>(session: Session, path: string, body?: object, signal?: AbortSignal): Promise<T> {
  if (session.demo) {
    const { demoMonitorEngineRequest } = await import("./demo.ts")
    return demoMonitorEngineRequest(path, body as Record<string, unknown> | undefined) as Promise<T>
  }
  const res = await consoleFetch(`/api/monitor/${path}`, {
    method: body ? "POST" : "GET",
    cache: "no-store",
    signal,
    ...(body ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {}),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new Error(data.message || data.error || `监控接口返回 HTTP ${res.status}`) as Error & { code?: string }
    err.code = data.code || data.error
    throw err
  }
  return data as T
}

export function fetchMonitorOverview(
  session: Session,
  opts: { range: MonitorRange; cluster?: string; kind?: string; tag?: string },
  signal?: AbortSignal,
): Promise<MonitorOverviewData> {
  const qs = new URLSearchParams({ range: opts.range })
  if (opts.cluster) qs.set("cluster", opts.cluster)
  if (opts.kind) qs.set("kind", opts.kind)
  if (opts.tag) qs.set("tag", opts.tag)
  return monitorRequest<MonitorOverviewData>(session, `overview?${qs.toString()}`, undefined, signal)
}

export function fetchMonitorResourceHistory(
  session: Session,
  opts: { cluster: string; instance: string; assetKind: string; assetId: string; range: MonitorRange },
  signal?: AbortSignal,
): Promise<MonitorHistoryData> {
  const qs = new URLSearchParams({
    cluster: opts.cluster,
    instance: opts.instance,
    asset_kind: opts.assetKind,
    asset_id: opts.assetId,
    range: opts.range,
  })
  return monitorRequest<MonitorHistoryData>(session, `resources/history?${qs.toString()}`, undefined, signal)
}

export function fetchMonitorSchedules(session: Session, signal?: AbortSignal): Promise<MonitorSchedulesData> {
  return monitorRequest<MonitorSchedulesData>(session, "schedules", undefined, signal)
}

export function mutateMonitorSchedule(session: Session, body: Record<string, unknown>): Promise<{ schedule?: MonitorScheduleRecord; deleted?: boolean }> {
  return monitorRequest(session, "schedules", body)
}

export function fetchMonitorRuns(
  session: Session,
  opts: { scheduleId?: string; status?: string; limit?: number } = {},
  signal?: AbortSignal,
): Promise<{ runs: MonitorRunRecord[] }> {
  const qs = new URLSearchParams()
  if (opts.scheduleId) qs.set("schedule_id", opts.scheduleId)
  if (opts.status) qs.set("status", opts.status)
  if (opts.limit) qs.set("limit", String(opts.limit))
  const suffix = qs.toString() ? `?${qs.toString()}` : ""
  return monitorRequest(session, `runs${suffix}`, undefined, signal)
}

export function fetchMonitorRunDetail(session: Session, runId: string, signal?: AbortSignal): Promise<MonitorRunDetailData> {
  return monitorRequest<MonitorRunDetailData>(session, `runs/${encodeURIComponent(runId)}`, undefined, signal)
}

export function triggerMonitorRun(
  session: Session,
  body: {
    schedule_id?: string
    retest_run_id?: string
    scope_mode?: string
    targets?: MonitorTargetRef[]
    tags?: string[]
    skills?: string[]
    prompt?: string
    command?: string
    thresholds?: MonitorThresholds
  },
): Promise<{ run: MonitorRunRecord; items: MonitorRunItemRecord[] }> {
  return monitorRequest(session, "runs", body)
}
