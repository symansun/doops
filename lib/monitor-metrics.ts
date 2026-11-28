import type { Session } from "./client.ts"
import { consoleFetch } from "./network-error.ts"

// SKILL_FIRST_INSPECTION_SPEC.md §4.2/§4.3/§4.4：指标时序与跨次 diff 的前端纯函数。
// 这里只做纯计算 + 只读请求，渲染与状态机留在组件里，便于 node --test 直接覆盖。

export type MonitorMetricRange = "1h" | "6h" | "24h" | "7d" | "30d"

export const MONITOR_METRIC_RANGES: { value: MonitorMetricRange; label: string }[] = [
  { value: "1h", label: "近 1 小时" },
  { value: "6h", label: "近 6 小时" },
  { value: "24h", label: "近 24 小时" },
  { value: "7d", label: "近 7 天" },
  { value: "30d", label: "近 30 天" },
]

export const DEFAULT_TREND_KEYS = [
  "cpu_percent",
  "memory_percent",
  "disk_percent",
  "load_1m",
  "error_rate",
  "response_time_ms",
]

export interface MonitorMetricPoint {
  at_ms: number
  value: number
  status?: string
  run_id?: string
}

export interface MonitorMetricSeries {
  key: string
  label?: string
  unit?: string
  points: MonitorMetricPoint[]
}

export interface MonitorMetricSeriesResponse {
  skill: string
  cluster: string
  instance: string
  range: string
  from_ms: number
  to_ms: number
  series: MonitorMetricSeries[]
}

export interface MonitorLatestMetricItem {
  skill: string
  key: string
  label?: string
  unit?: string
  max: number
  avg: number
  latest: number
  latest_at_ms: number
  status?: string
  reporting_targets: number
}

export interface MonitorLatestMetricsResponse {
  items: MonitorLatestMetricItem[]
}

export interface MonitorCheckLike {
  id?: string
  name?: string
  category?: string
  status?: string
  value?: string
  detail?: string
}

export interface MonitorRunMetricDelta {
  key: string
  label?: string
  unit?: string
  previous: number
  current: number
  delta: number
  delta_pct: number | null
}

export interface MonitorCheckChange {
  id?: string
  name?: string
  category?: string
  previous_status: string
  current_status: string
}

export interface MonitorRunDiff {
  previous_run_id: string
  previous_at_ms?: number
  previous_status?: string
  status_changed?: boolean
  metric_deltas?: MonitorRunMetricDelta[] | null
  added_checks?: MonitorCheckLike[] | null
  resolved_checks?: MonitorCheckLike[] | null
  changed_checks?: MonitorCheckChange[] | null
}

export interface TrendGeometryPoint {
  x: number
  y: number
  at_ms: number
  value: number
}

export interface TrendGeometry {
  path: string
  areaPath: string
  points: TrendGeometryPoint[]
  min: number
  max: number
}

export function buildMonitorMetricsPath(
  skill: string,
  cluster: string,
  instance: string,
  range: MonitorMetricRange,
): string {
  const qs = new URLSearchParams({
    skill: skill.trim(),
    target: `${cluster.trim()}/${instance.trim()}`,
    range,
  })
  return `metrics?${qs.toString()}`
}

export async function fetchMonitorMetricSeries(
  session: Session,
  opts: { skill: string; cluster: string; instance: string; range: MonitorMetricRange },
  signal?: AbortSignal,
): Promise<MonitorMetricSeriesResponse> {
  const path = buildMonitorMetricsPath(opts.skill, opts.cluster, opts.instance, opts.range)
  if (session.demo) {
    const { demoMonitorEngineRequest } = await import("./demo.ts")
    return (await demoMonitorEngineRequest(path, undefined)) as MonitorMetricSeriesResponse
  }
  const res = await consoleFetch(`/api/monitor/${path}`, { cache: "no-store", signal })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(typeof data?.message === "string" ? data.message : typeof data?.error === "string" ? data.error : `指标接口返回 HTTP ${res.status}`)
  }
  return data as MonitorMetricSeriesResponse
}

export interface MonitorLatestMetricPair {
  skill: string
  key: string
}

export const HOME_METRIC_BATCH_PAIR_LIMIT = 32

export function buildMonitorLatestMetricsPath(skills: string[], keys: string[]): string {
  const qs = new URLSearchParams({
    skills: Array.from(new Set(skills.map((s) => s.trim().toLowerCase()).filter(Boolean))).join(","),
    keys: Array.from(new Set(keys.map((k) => k.trim()).filter(Boolean))).join(","),
  })
  return `metrics/latest?${qs.toString()}`
}

export function buildMonitorLatestMetricPairsPath(pairs: MonitorLatestMetricPair[]): string {
  const seen = new Set<string>()
  const tokens: string[] = []
  for (const p of pairs) {
    const skill = p.skill.trim().toLowerCase()
    const key = p.key.trim()
    if (!skill || !key) continue
    const token = `${skill}:${key}`
    if (seen.has(token)) continue
    seen.add(token)
    tokens.push(token)
  }
  const qs = new URLSearchParams({ pairs: tokens.join(",") })
  return `metrics/latest?${qs.toString()}`
}

export function chunkMonitorMetricPairs(
  pairs: MonitorLatestMetricPair[],
  batchLimit: number = HOME_METRIC_BATCH_PAIR_LIMIT,
): MonitorLatestMetricPair[][] {
  const size = Math.max(1, Math.min(64, Math.floor(batchLimit)))
  const seen = new Set<string>()
  const unique: MonitorLatestMetricPair[] = []
  for (const p of pairs) {
    const skill = p.skill.trim().toLowerCase()
    const key = p.key.trim()
    if (!skill || !key) continue
    const id = `${skill}:${key}`
    if (seen.has(id)) continue
    seen.add(id)
    unique.push({ skill, key })
  }
  const batches: MonitorLatestMetricPair[][] = []
  for (let i = 0; i < unique.length; i += size) {
    batches.push(unique.slice(i, i + size))
  }
  return batches
}

function demoLatestMetricCatalog(now: number): MonitorLatestMetricItem[] {
  return [
    { skill: "inspect-vm-host", key: "cpu_percent", label: "CPU 使用率", unit: "%", max: 64.5, avg: 41.2, latest: 64.5, latest_at_ms: now - 90_000, status: "pass", reporting_targets: 3 },
    { skill: "inspect-vm-host", key: "memory_percent", label: "内存使用率", unit: "%", max: 72.8, avg: 58.1, latest: 72.8, latest_at_ms: now - 90_000, status: "pass", reporting_targets: 3 },
    { skill: "inspect-vm-host", key: "disk_percent", label: "磁盘最高占比", unit: "%", max: 68.0, avg: 52.4, latest: 68.0, latest_at_ms: now - 90_000, status: "pass", reporting_targets: 3 },
    { skill: "inspect-vm-host", key: "load_1m", label: "1 分钟负载", unit: "", max: 3.12, avg: 1.85, latest: 3.12, latest_at_ms: now - 90_000, status: "pass", reporting_targets: 3 },
    { skill: "gpu-governance", key: "gpu_count", label: "检出卡数", unit: "张", max: 8, avg: 4, latest: 8, latest_at_ms: now - 120_000, status: "pass", reporting_targets: 2 },
    { skill: "gpu-governance", key: "gpu_util_max_pct", label: "最高算力利用", unit: "%", max: 86.4, avg: 62.1, latest: 86.4, latest_at_ms: now - 120_000, status: "pass", reporting_targets: 2 },
    { skill: "gpu-governance", key: "gpu_mem_max_pct", label: "最高显存占用", unit: "%", max: 81.2, avg: 58.4, latest: 81.2, latest_at_ms: now - 120_000, status: "pass", reporting_targets: 2 },
    { skill: "gpu-governance", key: "gpu_temp_max_c", label: "最高卡温", unit: "°C", max: 74, avg: 66.5, latest: 74, latest_at_ms: now - 120_000, status: "pass", reporting_targets: 2 },
    { skill: "inspect-k8s-cluster", key: "k8s_nodes_total", label: "集群节点数", unit: "个", max: 6, avg: 6, latest: 6, latest_at_ms: now - 150_000, status: "pass", reporting_targets: 1 },
    { skill: "inspect-k8s-cluster", key: "k8s_nodes_not_ready", label: "异常节点", unit: "个", max: 0, avg: 0, latest: 0, latest_at_ms: now - 150_000, status: "pass", reporting_targets: 1 },
    { skill: "inspect-k8s-cluster", key: "k8s_pods_abnormal", label: "异常 Pod", unit: "个", max: 1, avg: 1, latest: 1, latest_at_ms: now - 150_000, status: "warning", reporting_targets: 1 },
    { skill: "inspect-k8s-cluster", key: "k8s_warning_events", label: "Warning 事件", unit: "条", max: 4, avg: 4, latest: 4, latest_at_ms: now - 150_000, status: "pass", reporting_targets: 1 },
    { skill: "host-security", key: "sec_risk_total", label: "风险项合计", unit: "项", max: 1, avg: 0.5, latest: 1, latest_at_ms: now - 300_000, status: "warning", reporting_targets: 2 },
    { skill: "host-security", key: "sec_mining_suspects", label: "疑似挖矿进程", unit: "个", max: 0, avg: 0, latest: 0, latest_at_ms: now - 300_000, status: "pass", reporting_targets: 2 },
    { skill: "host-security", key: "sec_proxy_suspects", label: "违规代理线索", unit: "条", max: 0, avg: 0, latest: 0, latest_at_ms: now - 300_000, status: "pass", reporting_targets: 2 },
    { skill: "host-security", key: "sec_pending_updates", label: "待更新补丁", unit: "个", max: 12, avg: 6, latest: 12, latest_at_ms: now - 300_000, status: "warning", reporting_targets: 2 },
    { skill: "inspect-container-runtime", key: "containers_running", label: "运行中容器", unit: "个", max: 14, avg: 10, latest: 14, latest_at_ms: now - 160_000, status: "pass", reporting_targets: 2 },
    { skill: "inspect-container-runtime", key: "container_max_mem_percent", label: "最高内存占比", unit: "%", max: 71.4, avg: 54.0, latest: 71.4, latest_at_ms: now - 160_000, status: "pass", reporting_targets: 2 },
    { skill: "inspect-app-web", key: "tcp_listen_ports", label: "TCP 监听端口", unit: "个", max: 9, avg: 7, latest: 9, latest_at_ms: now - 200_000, status: "pass", reporting_targets: 2 },
    { skill: "attack-surface-scan", key: "surface_risk_total", label: "暴露面风险", unit: "项", max: 2, avg: 1, latest: 2, latest_at_ms: now - 240_000, status: "warning", reporting_targets: 2 },
    { skill: "attack-surface-scan", key: "surface_exposed_paths", label: "敏感路径命中", unit: "个", max: 0, avg: 0, latest: 0, latest_at_ms: now - 240_000, status: "pass", reporting_targets: 2 },
    { skill: "attack-surface-scan", key: "surface_missing_csp", label: "缺 CSP 站点", unit: "个", max: 2, avg: 1, latest: 2, latest_at_ms: now - 240_000, status: "warning", reporting_targets: 2 },
    { skill: "inspect-database", key: "active_connections", label: "活跃连接", unit: "个", max: 46, avg: 31, latest: 46, latest_at_ms: now - 260_000, status: "pass", reporting_targets: 2 },
    { skill: "inspect-database", key: "db_lock_waits", label: "锁等待", unit: "个", max: 0, avg: 0, latest: 0, latest_at_ms: now - 260_000, status: "pass", reporting_targets: 2 },
  ]
}

export async function fetchMonitorLatestMetrics(
  session: Session,
  opts: { skills: string[]; keys: string[] },
  signal?: AbortSignal,
): Promise<MonitorLatestMetricsResponse> {
  const cleanSkills = Array.from(new Set(opts.skills.map((s) => s.trim().toLowerCase()).filter(Boolean)))
  const cleanKeys = Array.from(new Set(opts.keys.map((k) => k.trim()).filter(Boolean)))
  if (cleanSkills.length === 0 || cleanKeys.length === 0) {
    return { items: [] }
  }
  if (session.demo) {
    const demoItems = demoLatestMetricCatalog(Date.now())
    return {
      items: demoItems.filter((item) => cleanSkills.includes(item.skill) && cleanKeys.includes(item.key)),
    }
  }
  const path = buildMonitorLatestMetricsPath(cleanSkills, cleanKeys)
  const res = await consoleFetch(`/api/monitor/${path}`, { cache: "no-store", signal })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(typeof data?.message === "string" ? data.message : typeof data?.error === "string" ? data.error : `聚合指标接口返回 HTTP ${res.status}`)
  }
  return { items: Array.isArray(data?.items) ? data.items : [] }
}

/**
 * Unbounded pair-scoped batched fetcher for pluggable Home Widgets.
 * Automatically chunks arbitrary numbers of plugin metrics into bounded
 * pair-scoped requests (<= 32 pairs/batch) with per-batch fault isolation so
 * installing 10, 20, or 50+ plugins never silently truncates any card.
 */
export async function fetchMonitorLatestMetricsForWidgets(
  session: Session,
  pairs: MonitorLatestMetricPair[],
  signal?: AbortSignal,
  batchLimit: number = HOME_METRIC_BATCH_PAIR_LIMIT,
): Promise<{ items: MonitorLatestMetricItem[]; partialErrors: string[]; batchCount: number }> {
  const batches = chunkMonitorMetricPairs(pairs, batchLimit)
  if (batches.length === 0) {
    return { items: [], partialErrors: [], batchCount: 0 }
  }
  if (session.demo) {
    const allowed = new Set(batches.flat().map((p) => `${p.skill}:${p.key}`))
    const items = demoLatestMetricCatalog(Date.now()).filter((it) => allowed.has(`${it.skill}:${it.key}`))
    return { items, partialErrors: [], batchCount: batches.length }
  }
  const results = await Promise.allSettled(
    batches.map(async (batch) => {
      const path = buildMonitorLatestMetricPairsPath(batch)
      const res = await consoleFetch(`/api/monitor/${path}`, { cache: "no-store", signal })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        throw new Error(typeof data?.message === "string" ? data.message : typeof data?.error === "string" ? data.error : `聚合指标接口返回 HTTP ${res.status}`)
      }
      return Array.isArray(data?.items) ? (data.items as MonitorLatestMetricItem[]) : []
    }),
  )
  const merged = new Map<string, MonitorLatestMetricItem>()
  const partialErrors: string[] = []
  for (const r of results) {
    if (r.status === "fulfilled") {
      for (const item of r.value) {
        merged.set(`${item.skill}:${item.key}`, item)
      }
    } else {
      partialErrors.push(r.reason instanceof Error ? r.reason.message : "部分指标批次加载失败")
    }
  }
  return { items: [...merged.values()], partialErrors, batchCount: batches.length }
}

/** 优先返回标准键中第一个有 >=2 个点的序列；否则取点数最多的序列。 */
export function pickTrendSeries(
  series: MonitorMetricSeries[] | null | undefined,
  preferredKeys: string[] = DEFAULT_TREND_KEYS,
): MonitorMetricSeries | null {
  const usable = (series || []).filter((s) => s && s.key && Array.isArray(s.points) && s.points.length > 0)
  if (usable.length === 0) return null
  for (const key of preferredKeys) {
    const found = usable.find((s) => s.key === key && s.points.length >= 2)
    if (found) return found
  }
  return [...usable].sort((a, b) => b.points.length - a.points.length)[0]
}

/** 把时序点映射为 SVG 折线坐标；所有点相同或只有一个点时退化为水平线。 */
export function buildTrendGeometry(points: MonitorMetricPoint[], width = 100, height = 36, padding = 3): TrendGeometry {
  const clean = (points || []).filter((p) => Number.isFinite(p?.value))
  if (clean.length === 0) {
    return { path: "", areaPath: "", points: [], min: 0, max: 0 }
  }
  const values = clean.map((p) => p.value)
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min
  const innerW = Math.max(1, width - padding * 2)
  const innerH = Math.max(1, height - padding * 2)
  const coords = clean.map((p, i) => {
    const ratio = clean.length === 1 ? 0.5 : i / (clean.length - 1)
    const x = padding + ratio * innerW
    const normalized = span === 0 ? 0.5 : (p.value - min) / span
    const y = height - padding - normalized * innerH
    return { x: round2(x), y: round2(y), at_ms: p.at_ms, value: p.value }
  })
  const path = coords.map((c, i) => `${i === 0 ? "M" : "L"}${c.x},${c.y}`).join(" ")
  const areaPath = coords.length
    ? `${path} L${coords[coords.length - 1].x},${height - padding} L${coords[0].x},${height - padding} Z`
    : ""
  return { path, areaPath, points: coords, min, max }
}

export function formatMetricNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—"
  const abs = Math.abs(value)
  if (abs !== 0 && abs < 0.01) return value.toExponential(2)
  if (Number.isInteger(value)) return value.toLocaleString("en-US")
  return Number(value.toFixed(abs >= 100 ? 1 : 2)).toString()
}

export function formatMetricValue(value: number | null | undefined, unit?: string): string {
  const text = formatMetricNumber(value)
  if (!unit || text === "—") return text
  if (unit === "%") return `${text}%`
  return `${text} ${unit}`
}

/** delta_pct 为空（上次为 0）时返回 null；用于渲染 "new" 而不是伪造百分比。 */
export function formatDeltaPercent(pct: number | null | undefined): string | null {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) return null
  const sign = pct > 0 ? "+" : ""
  return `${sign}${Number(pct.toFixed(1))}%`
}

export function formatSignedNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—"
  const text = formatMetricNumber(Math.abs(value))
  if (value > 0) return `+${text}`
  if (value < 0) return `-${text}`
  return "0"
}

export type DeltaDirection = "up" | "down" | "flat"

export function deltaDirection(delta: number | null | undefined, epsilon = 1e-9): DeltaDirection {
  if (delta === null || delta === undefined || !Number.isFinite(delta)) return "flat"
  if (delta > epsilon) return "up"
  if (delta < -epsilon) return "down"
  return "flat"
}

export function summarizeRunDiff(diff: MonitorRunDiff | null | undefined): {
  added: number
  resolved: number
  changed: number
  improved: number
  degraded: number
} {
  const deltas = diff?.metric_deltas || []
  let improved = 0
  let degraded = 0
  for (const d of deltas) {
    if (deltaDirection(d.delta) === "down") improved += 1
    else if (deltaDirection(d.delta) === "up") degraded += 1
  }
  return {
    added: diff?.added_checks?.length || 0,
    resolved: diff?.resolved_checks?.length || 0,
    changed: diff?.changed_checks?.length || 0,
    improved,
    degraded,
  }
}

/** diff 存在但没有指标变化也没有检查项变化时，页面显示"与上次一致"。 */
export function diffHasChanges(diff: MonitorRunDiff | null | undefined): boolean {
  if (!diff) return false
  return Boolean(
    (diff.metric_deltas?.length || 0) > 0 ||
      (diff.added_checks?.length || 0) > 0 ||
      (diff.resolved_checks?.length || 0) > 0 ||
      (diff.changed_checks?.length || 0) > 0 ||
      diff.status_changed,
  )
}

function round2(n: number): number {
  return Math.round(n * 100) / 100
}

// SKILL_FIRST_INSPECTION_SPEC.md §3.2：artifact_state 的展示模型。
// missing 只表示"正文文件不在了"，不是采集失败；present + truncated 是 present 的子状态，
// 截断提示由后端写进正文的 "... [truncated]"（JSON 为 truncated 信封）承担，前端不替换正文。

/** 与后端 artifact_state 取值一致；老响应可能缺省。 */
export type MonitorArtifactState = "present" | "missing"

export const ARTIFACT_MISSING_NOTICE =
  "本次巡检的原始产物已被保留策略清理（默认保留 30 天），正文不可用；指标趋势仍按 90 天保留。原始产物文件已不存在或不可读，这不代表本次采集失败。"

export interface ArtifactStateDisplay {
  /** missing = 正文不可渲染，正文区应替换为 notice。 */
  missing: boolean
  /** present 及其截断子状态下正文照常渲染；missing 为 false。 */
  showBody: boolean
  /** missing 时的正文区提示；present 时为 null。 */
  notice: string | null
  /** present 且被 256 KiB 上限截断；missing 恒为 false（不做状态提升）。 */
  truncated: boolean
}

/**
 * 把详情 item 的 artifact_state / artifact_truncated 收敛成一个展示模型。
 * 字段缺省（老响应）与 present 等价；truncated 只作为 present 的子状态透出。
 */
export function describeArtifactState(
  state: MonitorArtifactState | string | null | undefined,
  truncated?: boolean | null,
): ArtifactStateDisplay {
  if (state === "missing") {
    return { missing: true, showBody: false, notice: ARTIFACT_MISSING_NOTICE, truncated: false }
  }
  return { missing: false, showBody: true, notice: null, truncated: Boolean(truncated) }
}

/** 详情接口 item 上可选的 diff 字段（仅 GET /v1/monitor/runs/<id> 提供）。 */
export interface MonitorDiffSourceItem {
  diff?: MonitorRunDiff | null
  cluster?: string | null
  instance?: string | null
  /** 产物可用状态；老后端不返回该字段。 */
  artifact_state?: MonitorArtifactState | string | null
  /** 仅 present 下的 256 KiB 截断标记；老后端不返回该字段。 */
  artifact_truncated?: boolean | null
}

/** InspectionArtifactViewer 需要的外部上下文（跨次 diff + 趋势取数目标）。 */
export interface MonitorInspectionViewerProps {
  diff?: MonitorRunDiff | null
  session?: Session
  trendSkill?: string
  trendCluster?: string
  trendInstance?: string
  /** 产物可用状态；undefined 表示老响应缺省，viewer 按 present 处理。 */
  artifactState?: MonitorArtifactState
  /** 仅 present 下的截断标记。 */
  artifactTruncated?: boolean
}

/**
 * 把巡检详情的 run item 映射成 InspectionArtifactViewer 的 Diff/趋势 props。
 *
 * 语义要点：只有详情响应真的带了 diff 键才传下去（undefined 表示该响应没有 diff
 * 概念，viewer 不渲染对比区块）；diff: null 表示后端明确回答"没有前次基线"，viewer
 * 渲染空态。创建接口（POST /v1/monitor/runs）不含 diff，不能假设有值。
 * 趋势需要 session 与 skill/cluster/instance 齐全，缺一就不展示。
 */
export function toInspectionViewerProps(
  item: MonitorDiffSourceItem | null | undefined,
  session: Session | null | undefined,
  skillSlug: string | null | undefined,
): MonitorInspectionViewerProps {
  const hasDiff = Boolean(item) && Object.prototype.hasOwnProperty.call(item, "diff")
  const diff = hasDiff ? item?.diff ?? null : undefined
  const cluster = item?.cluster || undefined
  const instance = item?.instance || undefined
  const skill = skillSlug || undefined
  // 只认后端明确给出的 present/missing；缺省或未知取值保持 undefined（= 老响应）。
  const artifactState: MonitorArtifactState | undefined =
    item?.artifact_state === "missing" ? "missing" : item?.artifact_state === "present" ? "present" : undefined
  const artifactTruncated = item?.artifact_truncated === true ? true : undefined
  const base = { diff, artifactState, artifactTruncated }
  if (!session || !skill || !cluster || !instance) {
    return base
  }
  return { ...base, session, trendSkill: skill, trendCluster: cluster, trendInstance: instance }
}
