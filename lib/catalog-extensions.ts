import {
  computeMergedMachineTags,
  normalizeSuggestedTags,
  type MachineInventorySummary,
  type SuggestedMachineTag,
} from "./machine-tag-discovery.ts"
import type { SkillPluginCatalogField, SkillPluginCatalogSchema } from "./plugins/types.ts"

export type MachineTagMergePlan = ReturnType<typeof computeMergedMachineTags>

export type PluginCatalogRow = {
  id: string
  kind: string
  name: string
  environment: string
  cluster: string
  instance: string
  details: Record<string, string>
  revision: number
}

const HOST_PART_RE = /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/
const IPV4_RE = /^(?:\d{1,3}\.){3}\d{1,3}$/
const LABEL_RE = /^[\p{L}\p{N}][\p{L}\p{N}._ -]{0,79}$/u

function isValidIPv4(raw: string): boolean {
  if (!IPV4_RE.test(raw)) return false
  return raw.split(".").every((part) => {
    const n = Number(part)
    return Number.isInteger(n) && n >= 0 && n <= 255 && String(n) === part
  })
}

function isValidIP(raw: string): boolean {
  const s = raw.trim()
  if (!s || s.length > 64) return false
  if (isValidIPv4(s)) return true
  return s.includes(":") && /^[0-9a-fA-F:]+$/.test(s)
}

function isValidDomain(raw: string): boolean {
  const s = raw.trim().replace(/\.$/, "")
  if (!s || s.length > 253 || isValidIPv4(s) || s.includes(":")) return false
  return s.split(".").every((part) => HOST_PART_RE.test(part))
}

function isValidCatalogHost(raw: string): boolean {
  const s = raw.trim().replace(/\.$/, "")
  if (!s || s.length > 253) return false
  if (isValidIP(s)) return true
  return s.split(".").every((part) => HOST_PART_RE.test(part))
}

function isValidCatalogLabel(raw: string): boolean {
  const s = raw.trim()
  return Boolean(s) && s.length <= 80 && LABEL_RE.test(s)
}

export function pluginCatalogKind(skillSlug: string, schemaKey: string): string {
  return `plugin:${skillSlug.trim().toLowerCase()}:${schemaKey.trim().toLowerCase()}`
}

export function createEmptyPluginCatalogRow(
  skillSlug: string,
  schema: SkillPluginCatalogSchema,
  target?: { cluster: string; instance: string } | null,
): PluginCatalogRow {
  const details: Record<string, string> = {}
  for (const field of schema.fields) {
    if (field.default !== undefined && field.default !== "") {
      details[field.key] = field.default
    } else if (field.type === "enum" && field.options && field.options.length > 0) {
      details[field.key] = field.options[0]
    } else {
      details[field.key] = ""
    }
  }
  return {
    id: "",
    kind: pluginCatalogKind(skillSlug, schema.key),
    name: "",
    environment: "生产",
    cluster: target?.cluster ?? "",
    instance: target?.instance ?? "",
    details,
    revision: 0,
  }
}

export function formatPluginCatalogAddress(
  schema: SkillPluginCatalogSchema,
  row: Pick<PluginCatalogRow, "details">,
): string {
  const d = row.details || {}
  const parts: string[] = []
  const primaryVal = (d[schema.primary_field] ?? "").trim()
  if (primaryVal) {
    parts.push(primaryVal)
  }
  for (const field of schema.fields) {
    if (field.key === schema.primary_field) continue
    const val = (d[field.key] ?? "").trim()
    if (!val) continue
    parts.push(`${field.label}: ${val}`)
  }
  return parts.length > 0 ? parts.join(" · ") : "—"
}

function validateCatalogFieldValue(field: SkillPluginCatalogField, value: string): string | null {
  switch (field.type) {
    case "domain":
      if (!isValidDomain(value)) {
        return `「${field.label}」必须为合法域名（例如 example.org）`
      }
      return null
    case "host":
      if (!isValidCatalogHost(value)) {
        return `「${field.label}」必须为合法主机名或 IP 地址`
      }
      return null
    case "ip":
      if (!isValidIP(value)) {
        return `「${field.label}」必须为合法 IPv4 或 IPv6 地址`
      }
      return null
    case "port": {
      if (!/^\d{1,5}$/.test(value)) {
        return `「${field.label}」必须为 1–65535 的端口号`
      }
      const n = Number(value)
      if (!Number.isInteger(n) || n < 1 || n > 65535) {
        return `「${field.label}」必须为 1–65535 的端口号`
      }
      return null
    }
    case "enum":
      if (!field.options || !field.options.includes(value)) {
        return `「${field.label}」取值不在允许列表中`
      }
      return null
    case "label":
    default:
      if (!isValidCatalogLabel(value)) {
        return `「${field.label}」仅支持字母、数字、中文、._- 字符（最多 80 字符）`
      }
      return null
  }
}

export function validatePluginCatalogDraft(
  schema: SkillPluginCatalogSchema,
  row: PluginCatalogRow,
): { ok: true; normalized: PluginCatalogRow } | { ok: false; message: string } {
  const name = row.name.trim()
  const environment = row.environment.trim()
  if (!isValidCatalogLabel(name)) {
    return { ok: false, message: "请填写合法的显示名称（1-80 字符，不含控制字符或特殊符号）" }
  }
  if (environment && !isValidCatalogLabel(environment)) {
    return { ok: false, message: "环境名称格式不合法（最多 80 字符）" }
  }

  const normalizedDetails: Record<string, string> = {}
  for (const field of schema.fields) {
    const val = String(row.details?.[field.key] ?? "").trim()
    if (!val) {
      return { ok: false, message: `请填写「${field.label}」` }
    }
    const fieldErr = validateCatalogFieldValue(field, val)
    if (fieldErr) {
      return { ok: false, message: fieldErr }
    }
    normalizedDetails[field.key] = val
  }

  const cluster = row.cluster.trim()
  const instance = row.instance.trim()
  if ((cluster && !instance) || (!cluster && instance)) {
    return { ok: false, message: "管理 Agent 的集群与实例必须同时填写或同时留空" }
  }

  return {
    ok: true,
    normalized: {
      id: row.id.trim(),
      kind: row.kind.trim().toLowerCase(),
      name,
      environment,
      cluster,
      instance,
      details: normalizedDetails,
      revision: Number.isFinite(row.revision) ? row.revision : 0,
    },
  }
}

export function buildPluginCatalogPayload(
  row: PluginCatalogRow,
  op: "create" | "update" | "delete",
): Record<string, unknown> {
  return {
    op,
    id: row.id,
    kind: row.kind,
    name: row.name,
    environment: row.environment,
    cluster: row.cluster,
    instance: row.instance,
    details: row.details,
    revision: row.revision,
  }
}

export type BatchMachineRunItemLike = {
  cluster: string
  instance: string
  status?: string
  reason?: string
  artifact_json?: unknown
  error?: string
  checks?: { status?: string; detail?: string; name?: string }[]
}

export type BatchMachineTagPlan = {
  machine: MachineInventorySummary
  targetKey: string
  runStatus: string
  runError?: string
  suggested: SuggestedMachineTag[]
  mergePlan: MachineTagMergePlan
}

export function buildBatchMachineTagPlans(
  machines: MachineInventorySummary[],
  runItems: BatchMachineRunItemLike[],
): BatchMachineTagPlan[] {
  const byTarget = new Map<string, BatchMachineRunItemLike>()
  for (const item of runItems || []) {
    if (!item.cluster || !item.instance) continue
    byTarget.set(`${item.cluster}/${item.instance}`, item)
  }
  const plans: BatchMachineTagPlan[] = []
  for (const machine of machines || []) {
    if (!machine.cluster || !machine.instance) continue
    const targetKey = `${machine.cluster}/${machine.instance}`
    const matched = byTarget.get(targetKey)
    if (!matched) continue
    const suggested = normalizeSuggestedTags(matched.artifact_json)
    const mergePlan = computeMergedMachineTags(
      machine.tags ?? [],
      suggested.map((s) => s.tag),
    )
    const failedStatus = matched.status === "error" || matched.status === "offline"
    const checkDetail = matched.checks?.find((c) => c.status === "critical" || c.status === "error")?.detail
    const runError =
      matched.error ||
      (failedStatus ? checkDetail || matched.reason || `采集状态异常 (${matched.status})` : undefined)
    plans.push({
      machine,
      targetKey,
      runStatus: matched.status || "ok",
      runError,
      suggested,
      mergePlan,
    })
  }
  return plans
}
