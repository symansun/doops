/**
 * 通用图表原语：把巡检产物 JSON 里的 charts[] 归一化成可直接绘制的几何数据。
 *
 * 契约见 backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md §4.4 第 3 条：
 *   charts[] = [{ title, type: "bar" | "gauge" | "line", unit?, max?, series: [{ label, value, status? }] }]
 * 本文件只做归一化与几何计算，不依赖 React；渲染在 components/inspection-artifact-viewer.tsx。
 * 颜色只由 series[].status 决定，与数值高低无关，因此这里不出现任何业务字段名。
 */

export type InspectionChartTone = "success" | "warning" | "danger" | "neutral"
export type InspectionChartKind = "bar" | "gauge" | "line"
export type InspectionChartMaxSource = "declared" | "series"

const TONE_BY_STATUS: Record<string, InspectionChartTone> = {
  pass: "success",
  ok: "success",
  healthy: "success",
  success: "success",
  good: "success",
  normal: "success",
  warning: "warning",
  warn: "warning",
  degraded: "warning",
  minor: "warning",
  critical: "danger",
  crit: "danger",
  fail: "danger",
  failed: "danger",
  error: "danger",
  danger: "danger",
  fatal: "danger",
}

/** status 缺失或无法识别时用中性色，绝不按数值推断健康度。 */
export function inspectionChartTone(status: unknown): InspectionChartTone {
  if (typeof status !== "string") return "neutral"
  return TONE_BY_STATUS[status.trim().toLowerCase()] ?? "neutral"
}

/** 仪表盘弧线扫过的角度：左半圆（180°）到右半圆（360°），顺时针经过顶部。 */
export const GAUGE_SWEEP_DEGREES = 180

export interface InspectionChartPoint {
  label: string
  value: number
  status?: string
  tone: InspectionChartTone
  /** value / max，已 clamp 到 0..1。 */
  ratio: number
  /** value 超过声明的 max（渲染时把刻度标为超额）。 */
  overMax: boolean
}

export interface InspectionChartGaugeArc extends InspectionChartPoint {
  startAngle: number
  endAngle: number
}

export interface InspectionChartLinePoint extends InspectionChartPoint {
  /** SVG 百分比坐标，x 与 y 都在 0..100；y=0 在顶部。 */
  x: number
  y: number
}

export interface InspectionBarChart {
  kind: "bar"
  title: string
  unit: string
  max: number
  maxSource: InspectionChartMaxSource
  series: InspectionChartPoint[]
}

export interface InspectionGaugeChart {
  kind: "gauge"
  title: string
  unit: string
  max: number
  maxSource: InspectionChartMaxSource
  arcs: InspectionChartGaugeArc[]
}

export interface InspectionLineChart {
  kind: "line"
  title: string
  unit: string
  max: number
  maxSource: InspectionChartMaxSource
  points: InspectionChartLinePoint[]
  /** 直接可用于 <polyline points="..."> 的 0..100 坐标串。 */
  polyline: string
  /** 按 series 顺序的横轴刻度。 */
  ticks: string[]
}

export type InspectionChart = InspectionBarChart | InspectionGaugeChart | InspectionLineChart

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function round2(value: number): number {
  return Math.round(value * 100) / 100
}

function clamp01(value: number): number {
  if (!Number.isFinite(value)) return 0
  if (value <= 0) return 0
  if (value >= 1) return 1
  return value
}

interface RawPoint {
  label: string
  value: number
  status?: string
}

function readPoints(raw: unknown): RawPoint[] {
  if (!Array.isArray(raw)) return []
  const points: RawPoint[] = []
  for (const entry of raw) {
    if (!isRecord(entry)) continue
    const value = entry.value
    // 非数值（字符串、null、NaN、Infinity）一律跳过，避免把采集缺口画成 0。
    if (typeof value !== "number" || !Number.isFinite(value)) continue
    const label = typeof entry.label === "string" ? entry.label.trim() : ""
    const status = typeof entry.status === "string" ? entry.status.trim() : undefined
    points.push(status ? { label, value, status } : { label, value })
  }
  return points
}

function scalePoint(point: RawPoint, max: number): InspectionChartPoint {
  const ratio = clamp01(point.value / max)
  const base: InspectionChartPoint = {
    label: point.label,
    value: point.value,
    tone: inspectionChartTone(point.status),
    ratio,
    overMax: point.value > max,
  }
  return point.status ? { ...base, status: point.status } : base
}

function normalizeChart(raw: unknown): InspectionChart | null {
  if (!isRecord(raw)) return null
  const kind = typeof raw.type === "string" ? raw.type.trim().toLowerCase() : ""
  if (kind !== "bar" && kind !== "gauge" && kind !== "line") return null

  // series 为空或全部非数值时不产生图表，前端也就不会留下空框。
  const rawPoints = readPoints(raw.series)
  if (rawPoints.length === 0) return null

  const declaredMax =
    typeof raw.max === "number" && Number.isFinite(raw.max) && raw.max > 0 ? raw.max : null
  const seriesMax = Math.max(...rawPoints.map((point) => point.value))
  // 缺 max（或 max 非法）时按 series 最大值兜底；全为 0 时用 1 防止除零。
  const max = declaredMax ?? (seriesMax > 0 ? seriesMax : 1)
  const maxSource: InspectionChartMaxSource = declaredMax === null ? "series" : "declared"
  const unit = typeof raw.unit === "string" ? raw.unit.trim() : ""
  const title = typeof raw.title === "string" ? raw.title.trim() : ""
  const points = rawPoints.map((point) => scalePoint(point, max))

  if (kind === "bar") {
    return { kind: "bar", title, unit, max, maxSource, series: points }
  }
  if (kind === "gauge") {
    const arcs = points.map((point) => ({
      ...point,
      startAngle: 180,
      endAngle: 180 + point.ratio * GAUGE_SWEEP_DEGREES,
    }))
    return { kind: "gauge", title, unit, max, maxSource, arcs }
  }

  const total = points.length
  const linePoints = points.map((point, index) => ({
    ...point,
    x: total > 1 ? (index / (total - 1)) * 100 : 50,
    y: 100 - point.ratio * 100,
  }))
  return {
    kind: "line",
    title,
    unit,
    max,
    maxSource,
    points: linePoints,
    polyline: linePoints.map((point) => `${round2(point.x)},${round2(point.y)}`).join(" "),
    ticks: linePoints.map((point) => point.label),
  }
}

/**
 * 归一化 charts[]。接受两种输入：charts 数组本身，或含 charts 字段的整份产物 JSON
 * （后者省略了调用方的解构，未知结构一律返回空数组）。
 */
export function normalizeInspectionCharts(input: unknown): InspectionChart[] {
  const list = Array.isArray(input)
    ? input
    : isRecord(input) && Array.isArray(input.charts)
      ? input.charts
      : []
  const charts: InspectionChart[] = []
  for (const raw of list) {
    const chart = normalizeChart(raw)
    if (chart) charts.push(chart)
  }
  return charts
}

/** 把 0..100 的百分比几何映射成 SVG 坐标串，渲染层只负责画。 */
export function mapChartPointsToViewBox(chart: InspectionLineChart, width = 100, height = 100): string {
  return chart.points
    .map((point) => `${round2((point.x / 100) * width)},${round2((point.y / 100) * height)}`)
    .join(" ")
}

/** 仪表盘弧线 path；ratio 为 0 时返回空串，渲染层跳过。 */
export function gaugeArcPath(
  cx: number,
  cy: number,
  radius: number,
  startAngle: number,
  endAngle: number,
): string {
  const sweep = endAngle - startAngle
  if (!Number.isFinite(sweep) || sweep <= 0) return ""
  const capped = Math.min(sweep, GAUGE_SWEEP_DEGREES)
  const toRad = (deg: number) => (deg * Math.PI) / 180
  const x0 = cx + radius * Math.cos(toRad(startAngle))
  const y0 = cy + radius * Math.sin(toRad(startAngle))
  const x1 = cx + radius * Math.cos(toRad(startAngle + capped))
  const y1 = cy + radius * Math.sin(toRad(startAngle + capped))
  const largeArc = capped > 180 ? 1 : 0
  return `M ${round2(x0)} ${round2(y0)} A ${radius} ${radius} 0 ${largeArc} 1 ${round2(x1)} ${round2(y1)}`
}

/** 图表里的数值标签：最多一位小数，整数不补零。 */
export function formatChartValue(value: number, unit = ""): string {
  if (!Number.isFinite(value)) return "—"
  const text = Number.isInteger(value) ? String(value) : String(round2(value))
  return unit ? `${text}${unit}` : text
}
