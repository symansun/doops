import { formatDateTime } from "./format.ts"

const DATE_ONLY_UTC_PATTERN = /^(\d{4}-\d{2}-\d{2})(?:T00:00:00(?:\.0+)?Z)?$/

function pad2(n: number): string {
  return String(n).padStart(2, "0")
}

function localYMD(date: Date): string {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`
}

/** 列表列用：把机器录入时间格式化为紧凑的 YYYY-MM-DD（UTC 零点日期值不受时区偏移影响）。 */
export function formatMachineCreatedDate(value?: string | null, fallback = "未登记"): string {
  const raw = typeof value === "string" ? value.trim() : ""
  if (!raw) return fallback
  const exact = DATE_ONLY_UTC_PATTERN.exec(raw)
  if (exact) return exact[1]
  const parsed = new Date(raw)
  if (Number.isNaN(parsed.getTime())) return raw
  return localYMD(parsed)
}

/** 详情与悬浮提示用：日期型值保持 YYYY-MM-DD，带具体时分秒的 RFC3339 按中文 24 小时制展示。 */
export function formatMachineCreatedDetail(value?: string | null, fallback = "未登记"): string {
  const raw = typeof value === "string" ? value.trim() : ""
  if (!raw) return fallback
  const exact = DATE_ONLY_UTC_PATTERN.exec(raw)
  if (exact) return exact[1]
  return formatDateTime(raw, { fallback })
}

/** 将已保存的 created_at 转为 <input type="date"> 可消费的 YYYY-MM-DD。 */
export function toMachineDateInput(value?: string | null): string {
  const raw = typeof value === "string" ? value.trim() : ""
  if (!raw) return ""
  const exact = DATE_ONLY_UTC_PATTERN.exec(raw)
  if (exact) return exact[1]
  const parsed = new Date(raw)
  if (Number.isNaN(parsed.getTime())) return ""
  return localYMD(parsed)
}

/**
 * 计算提交给 Gateway 的 created_at 字段：
 * - 留空：省略字段（新建由服务端填当前时间，编辑保留原录入时间）。
 * - 编辑时日期未改动：省略字段，避免用日期覆盖原有精确时分秒。
 * - 用户显式填写或修改日期：提交 YYYY-MM-DD，由服务端校验并归一化。
 */
export function resolveMachineCreatedAtPayload(submittedDate: string, original?: string | null): string | undefined {
  const trimmed = submittedDate.trim()
  if (!trimmed) return undefined
  if (original && trimmed === toMachineDateInput(original)) return undefined
  return trimmed
}
