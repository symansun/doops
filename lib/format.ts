// 展示用格式化：时间、数量、字节。
//
// 每个面板过去各写一份 `new Date(x).toLocaleString(...)`，区域设置、12/24 小时制、
// 秒/毫秒单位各不相同，同一个时间在不同页面显示格式不一致。这里只保留一处实现，
// 单位由调用方用 TimeUnit 显式声明，避免 "v * 1000" 这种隐式换算散落在组件里。

export type TimeUnit = "iso" | "ms" | "s"

// 中文界面统一用 zh-CN、24 小时制；各页面保持一致，便于对照日志和审计记录。
const DATE_TIME: Intl.DateTimeFormatOptions = { hour12: false }

function parse(value: string | number | Date | null | undefined, unit: TimeUnit): Date | null {
  if (value === null || value === undefined || value === "") return null
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value
  // 秒级时间戳（后端 inspections/周报的 unix 秒）和毫秒时间戳分开声明，不做猜测。
  const numeric = typeof value === "number" ? (unit === "s" ? value * 1000 : value) : Number.NaN
  const date = typeof value === "number" ? new Date(numeric) : new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

/**
 * 格式化为本地日期时间。无法解析时原样返回字符串，空值返回 fallback。
 *
 * unit 默认 "iso"（ISO 字符串或毫秒已能区分）；传 "s" 表示入参是 unix 秒。
 */
export function formatDateTime(
  value: string | number | Date | null | undefined,
  options: { unit?: TimeUnit; timeZone?: string; fallback?: string } = {},
): string {
  const { unit = "iso", timeZone, fallback = "—" } = options
  const date = parse(value, unit)
  if (!date) {
    if (value === null || value === undefined || value === "") return fallback
    return typeof value === "string" ? value : fallback
  }
  return date.toLocaleString("zh-CN", timeZone ? { ...DATE_TIME, timeZone } : DATE_TIME)
}

/** 只格式化日期部分，用于到期日这类不需要时分秒的展示。 */
export function formatDate(
  value: string | number | Date | null | undefined,
  options: { unit?: TimeUnit; fallback?: string } = {},
): string {
  const { unit = "iso", fallback = "—" } = options
  const date = parse(value, unit)
  if (!date) {
    if (value === null || value === undefined || value === "") return fallback
    return typeof value === "string" ? value : fallback
  }
  return date.toLocaleDateString("zh-CN")
}

/** 千分位数字。可空时返回 fallback，而不是 "NaN"。 */
export function formatNumber(
  value: number | null | undefined,
  options: { digits?: number; fallback?: string } = {},
): string {
  const { digits, fallback = "—" } = options
  if (typeof value !== "number" || !Number.isFinite(value)) return fallback
  return value.toLocaleString("zh-CN", digits === undefined ? undefined : { maximumFractionDigits: digits })
}

/** 字节数按 MiB 展示，保留两位小数（诊断面板的既有约定）。 */
export function formatMiB(value: number | null | undefined, fallback = "—"): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return fallback
  return (value / 1048576).toLocaleString("zh-CN", { maximumFractionDigits: 2 })
}
