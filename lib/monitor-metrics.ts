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

/** 详情接口 item 上可选的 diff 字段（仅 GET /v1/monitor/runs/<id> 提供）。 */
export interface MonitorDiffSourceItem {
  diff?: MonitorRunDiff | null
  cluster?: string | null
  instance?: string | null
}

/** InspectionArtifactViewer 需要的外部上下文（跨次 diff + 趋势取数目标）。 */
export interface MonitorInspectionViewerProps {
  diff?: MonitorRunDiff | null
  session?: Session
  trendSkill?: string
  trendCluster?: string
  trendInstance?: string
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
  if (!session || !skill || !cluster || !instance) {
    return { diff }
  }
  return { diff, session, trendSkill: skill, trendCluster: cluster, trendInstance: instance }
}
