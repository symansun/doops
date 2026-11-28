import type { PluginView, SkillPluginView } from "./plugins/types.ts"

export interface GpuDeviceRecord {
  index: string
  name: string
  uuid: string
  utilPercent: number | null
  memUsedMib: number | null
  memTotalMib: number | null
  memPercent: number | null
  tempC: number | null
  powerW: number | null
  eccUncorrected: number
  health: "ok" | "warning" | "critical"
}

export interface GpuProcessRecord {
  gpuUuid: string
  gpuIndex: string
  gpuName: string
  pid: string
  process: string
  usedMib: number | null
  zombieSuspect: boolean
}

export interface ParsedGpuArtifact {
  gpus: GpuDeviceRecord[]
  processes: GpuProcessRecord[]
  totalGpus: number
  avgUtilPercent: number | null
  maxMemPercent: number | null
  totalUsedMib: number
  totalMemMib: number
  maxTempC: number | null
  eccTotal: number
  zombieCount: number
  warnTempC: number
  warnMemPct: number
  probeStatus: string
  summary: string
}

function asNum(v: unknown): number | null {
  if (typeof v === "number" && Number.isFinite(v)) return v
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v)
    if (Number.isFinite(n)) return n
  }
  return null
}

function asStr(v: unknown, fallback = ""): string {
  if (typeof v === "string") return v.trim()
  if (typeof v === "number" && Number.isFinite(v)) return String(v)
  return fallback
}

/**
 * Parses `gpu-governance` inspection artifact JSON into structured per-GPU cards
 * and compute process rows, joining processes by UUID and flagging idle VRAM hogs.
 */
export function parseGpuArtifact(artifactJson: unknown): ParsedGpuArtifact | null {
  if (!artifactJson || typeof artifactJson !== "object" || Array.isArray(artifactJson)) return null
  const obj = artifactJson as Record<string, unknown>
  const applied = (obj.applied_inputs && typeof obj.applied_inputs === "object"
    ? obj.applied_inputs
    : {}) as Record<string, unknown>
  const warnTempC = asNum(applied.WARN_TEMP_C) ?? 85
  const critTempC = Math.min(112, warnTempC + 7)
  const warnMemPct = asNum(applied.WARN_MEM_PCT) ?? 90
  const critMemPct = Math.max(warnMemPct + 2, 98)

  const rawGpus = Array.isArray(obj.gpus) ? obj.gpus : []
  const rawProcs = Array.isArray(obj.gpu_processes) ? obj.gpu_processes : []

  const gpus: GpuDeviceRecord[] = []
  const gpuByUuid = new Map<string, GpuDeviceRecord>()

  for (let i = 0; i < rawGpus.length; i++) {
    const row = rawGpus[i]
    if (!row || typeof row !== "object" || Array.isArray(row)) continue
    const r = row as Record<string, unknown>
    const index = asStr(r.index, String(i))
    const name = asStr(r.name, `GPU ${index}`)
    const uuid = asStr(r.uuid, `gpu-${index}`)
    const utilPercent = asNum(r.util_percent)
    const memUsedMib = asNum(r.mem_used_mib)
    const memTotalMib = asNum(r.mem_total_mib)
    const memPercent =
      asNum(r.mem_percent) ??
      (memUsedMib != null && memTotalMib != null && memTotalMib > 0
        ? Math.round((memUsedMib / memTotalMib) * 1000) / 10
        : null)
    const tempC = asNum(r.temp_c)
    const powerW = asNum(r.power_w)
    const eccUncorrected = Math.max(0, Math.round(asNum(r.ecc_uncorrected) ?? 0))

    let health: "ok" | "warning" | "critical" = "ok"
    if (
      eccUncorrected > 0 ||
      (tempC != null && tempC >= critTempC) ||
      (memPercent != null && memPercent >= critMemPct)
    ) {
      health = "critical"
    } else if (
      (tempC != null && tempC >= warnTempC) ||
      (memPercent != null && memPercent >= warnMemPct)
    ) {
      health = "warning"
    }

    const gpu: GpuDeviceRecord = {
      index,
      name,
      uuid,
      utilPercent,
      memUsedMib,
      memTotalMib,
      memPercent,
      tempC,
      powerW,
      eccUncorrected,
      health,
    }
    gpus.push(gpu)
    gpuByUuid.set(uuid, gpu)
  }

  const processes: GpuProcessRecord[] = []
  for (const raw of rawProcs) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue
    const p = raw as Record<string, unknown>
    const gpuUuid = asStr(p.gpu_uuid)
    const owner = gpuByUuid.get(gpuUuid)
    const usedMib = asNum(p.used_mib)
    const zombieSuspect = Boolean(
      owner &&
        (owner.memPercent ?? 0) >= 50 &&
        (owner.utilPercent ?? 0) < 5 &&
        (usedMib ?? 0) >= 512,
    )
    processes.push({
      gpuUuid,
      gpuIndex: owner ? owner.index : "—",
      gpuName: owner ? owner.name : "未知 GPU",
      pid: asStr(p.pid, "—"),
      process: asStr(p.process, "unknown"),
      usedMib,
      zombieSuspect,
    })
  }

  processes.sort((a, b) => {
    if (a.zombieSuspect !== b.zombieSuspect) return a.zombieSuspect ? -1 : 1
    return (b.usedMib ?? 0) - (a.usedMib ?? 0)
  })

  const utils = gpus.map((g) => g.utilPercent).filter((v): v is number => v != null)
  const mems = gpus.map((g) => g.memPercent).filter((v): v is number => v != null)
  const temps = gpus.map((g) => g.tempC).filter((v): v is number => v != null)

  return {
    gpus,
    processes,
    totalGpus: gpus.length,
    avgUtilPercent: utils.length > 0 ? Math.round((utils.reduce((a, b) => a + b, 0) / utils.length) * 10) / 10 : null,
    maxMemPercent: mems.length > 0 ? Math.max(...mems) : null,
    totalUsedMib: gpus.reduce((sum, g) => sum + (g.memUsedMib ?? 0), 0),
    totalMemMib: gpus.reduce((sum, g) => sum + (g.memTotalMib ?? 0), 0),
    maxTempC: temps.length > 0 ? Math.max(...temps) : null,
    eccTotal: gpus.reduce((sum, g) => sum + g.eccUncorrected, 0),
    zombieCount: processes.filter((p) => p.zombieSuspect).length,
    warnTempC,
    warnMemPct,
    probeStatus: asStr(obj.status, "pass"),
    summary: asStr(obj.summary),
  }
}

export function formatMibCompact(mib?: number | null): string {
  if (mib == null || !Number.isFinite(mib)) return "—"
  if (mib >= 1024) return `${(mib / 1024).toFixed(1)} GiB`
  return `${Math.round(mib)} MiB`
}

/**
 * Determines whether a domain plugin workbench should automatically run a fast,
 * zero-token read-only probe when opened with no cached run yet.
 * Never auto-runs AI-only plugins or plugins with required credential inputs.
 */
export function shouldAutoLiveProbe(input: {
  plugin: PluginView
  hasCachedRun: boolean
  hasOnlineTarget: boolean
  alreadyProbed: boolean
}): boolean {
  if (input.hasCachedRun || !input.hasOnlineTarget || input.alreadyProbed) return false
  const manifest = input.plugin.manifest
  if (!manifest) return false
  const presets = manifest.ui.presets ?? []
  const hasScriptPreset = presets.some((p) => p.mode === "script")
  if (!hasScriptPreset) return false
  const hasRequiredCred = (manifest.ui.inputs ?? []).some(
    (inp) => inp.type === "credential_ref" && Boolean(inp.required),
  )
  return !hasRequiredCred
}

const REPORT_VIEW_TYPES = new Set<SkillPluginView["type"]>(["trend_series", "checks_table", "artifact_viewer"])

/**
 * Separates live domain overview views (Tab 1: 实时工作台) from historical/audit
 * views (Tab 3: 策略与报告) so plugin workbenches open directly to real resources.
 */
export function splitPluginViewsForTabs(views: SkillPluginView[]): {
  liveViews: SkillPluginView[]
  reportViews: SkillPluginView[]
} {
  const liveViews: SkillPluginView[] = []
  const reportViews: SkillPluginView[] = []
  for (const view of views) {
    if (REPORT_VIEW_TYPES.has(view.type)) reportViews.push(view)
    else liveViews.push(view)
  }
  return { liveViews, reportViews }
}

/**
 * Human-friendly plugin source badge (no internal "L2" / "L2+" jargon).
 */
export function formatPluginOriginLabel(plugin: Pick<PluginView, "scope" | "builtin">): string {
  if (plugin.scope === "system") {
    return plugin.builtin ? "内置扩展" : "已安装插件"
  }
  return "个人扩展"
}
