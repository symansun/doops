import type { AuditEvent, Target } from "./client.ts"
import type { MonitorOverviewData, MonitorResourceSnapshot, MonitorRunRecord } from "./monitor-engine.ts"
import type { MonitorLatestMetricItem } from "./monitor-metrics.ts"
import type { PluginView } from "./plugins/types.ts"

export interface AssetDashboardCluster {
  asset_id: string
  name: string
  sources: number
  online: number
  namespaces: number
  machines: number
  systems: number
  verified_at: string
}

export interface AssetDashboardSummary {
  machines: number
  systems: number
  clusters: AssetDashboardCluster[]
  unassigned: { asset_id: string; name: string }[]
}

export interface DashboardMachineTagSource {
  asset_id: string
  cluster?: string
  instance?: string
  tags?: string[]
}

export interface MachineTagSummary {
  byTarget: Map<string, string[]>
  totalMachines: number
  taggedMachines: number
  untaggedMachines: number
}

export interface TargetModeBreakdown {
  k8s: number
  linux: number
  docker: number
  other: number
  label: string
}

export interface TargetTelemetry {
  targetKey: string
  status: string
  cpuPercent: number | null
  memoryPercent: number | null
  diskPercent: number | null
  stale: boolean
  collectedAtMs: number
}

export type ClusterFilterMode = "all" | "k8s" | "host" | "busy"

export interface DashboardNodeRow {
  target: Target
  telemetry?: TargetTelemetry
  tags: string[]
  isK8s: boolean
  priority: number
}

export interface DashboardClusterGroup {
  name: string
  machines: Target[]
  connections: number
  sessions: number
  busy: number
  activeOps: number
  queuedOps: number
  k8sCount: number
  warningCount: number
  criticalCount: number
}

export type AttentionSeverity = "critical" | "warning" | "offline"

export interface DashboardAttentionItem {
  id: string
  severity: AttentionSeverity
  title: string
  detail: string
  targetKey?: string
  target?: Target
  runId?: string
  kind: "threshold_alert" | "resource_health" | "failed_run" | "offline_target"
}

export interface HomeWidgetMetricMatch {
  key: string
  label: string
  unit?: string
  aggregate?: "max" | "avg" | "latest"
  point?: MonitorLatestMetricItem
}

export interface EvaluatedHomeWidget {
  plugin: PluginView
  metrics: HomeWidgetMetricMatch[]
  reportingTargets: number
  hasData: boolean
  severity: "critical" | "warning" | "ok" | "none"
}

export interface HomeWidgetPartition {
  active: EvaluatedHomeWidget[]
  pending: EvaluatedHomeWidget[]
}

const SEVERITY_RANK: Record<EvaluatedHomeWidget["severity"], number> = {
  critical: 0,
  warning: 1,
  ok: 2,
  none: 3,
}

const ATTENTION_RANK: Record<AttentionSeverity, number> = {
  critical: 0,
  warning: 1,
  offline: 2,
}

const THRESHOLD_METRIC_LABELS: Record<string, string> = {
  cpu_percent: "CPU 使用率",
  memory_percent: "内存使用率",
  disk_percent: "磁盘占比",
  load_1m: "1 分钟负载",
  disk_latency_ms: "磁盘延迟",
  response_time_ms: "响应延迟",
  error_rate: "错误率",
  gpu_percent: "GPU 利用率",
  gpu_temp_max_c: "GPU 温度",
}

const HIGH_SIGNAL_TAG_KEYS = new Set([
  "app",
  "service",
  "system",
  "biz",
  "business",
  "db",
  "database",
  "runtime",
  "stack",
  "middleware",
  "gpu",
  "role",
])

const LOW_SIGNAL_TAG_KEYS = new Set(["env", "rack", "zone", "region", "owner", "dept"])

export function isTargetKubernetes(target: Target): boolean {
  if (target.kubernetes_available === true) return true
  if (target.kubernetes_available === false) return false
  const mode = (target.mode || "").trim().toLowerCase()
  return mode === "kubernetes" || mode === "k8s" || mode.includes("kube")
}

export function isFailedAuditStatus(status?: string): boolean {
  return status === "error" || status === "failed" || status === "outcome_unknown"
}

export function isFailedAuditEvent(event: AuditEvent): boolean {
  return isFailedAuditStatus(event.status)
}

/**
 * Classifies online targets by runtime mode so KPI #1 reports runtime distribution
 * ("K8s 4 · Linux 5 · Docker 2") without conflating Agent grouping with K8s clusters.
 */
export function buildTargetModeBreakdown(targets: Target[]): TargetModeBreakdown {
  let k8s = 0
  let linux = 0
  let docker = 0
  let other = 0
  for (const t of targets) {
    if (isTargetKubernetes(t)) {
      k8s++
      continue
    }
    const mode = (t.mode || "").trim().toLowerCase()
    if (mode === "docker" || mode.includes("container")) docker++
    else if (mode === "linux" || mode === "host" || mode === "standalone" || mode === "vm" || mode === "ssh") linux++
    else other++
  }
  const parts: string[] = []
  if (k8s > 0) parts.push(`K8s ${k8s}`)
  if (linux > 0) parts.push(`Linux ${linux}`)
  if (docker > 0) parts.push(`Docker ${docker}`)
  if (other > 0) parts.push(`其他 ${other}`)
  return {
    k8s,
    linux,
    docker,
    other,
    label: parts.length > 0 ? parts.join(" · ") : "暂无在线节点",
  }
}

function tagPriority(tag: string): number {
  const eq = tag.indexOf("=")
  if (eq <= 0) return 1
  const key = tag.slice(0, eq).trim().toLowerCase()
  if (HIGH_SIGNAL_TAG_KEYS.has(key)) return 0
  if (LOW_SIGNAL_TAG_KEYS.has(key)) return 2
  return 1
}

export function formatTagChipLabel(tag: string): string {
  const trimmed = tag.trim()
  const eq = trimmed.indexOf("=")
  if (eq <= 0) return trimmed
  const key = trimmed.slice(0, eq).trim().toLowerCase()
  const val = trimmed.slice(eq + 1).trim()
  if (!val) return trimmed
  if (HIGH_SIGNAL_TAG_KEYS.has(key)) return val
  return trimmed
}

/**
 * Aggregates CMDB machine tags per `cluster/instance` for the homepage live node workbench
 * and computes tagging coverage for the Core Assets card.
 */
export function indexMachineTags(machines?: DashboardMachineTagSource[] | null): MachineTagSummary {
  const byTarget = new Map<string, string[]>()
  if (!Array.isArray(machines)) {
    return { byTarget, totalMachines: 0, taggedMachines: 0, untaggedMachines: 0 }
  }
  let taggedMachines = 0
  for (const m of machines) {
    const cleanTags = (m.tags ?? []).map((t) => String(t).trim()).filter(Boolean)
    if (cleanTags.length > 0) taggedMachines++
    if (!m.cluster || !m.instance || cleanTags.length === 0) continue
    const key = `${m.cluster}/${m.instance}`
    const existing = byTarget.get(key) ?? []
    const merged = new Set<string>(existing)
    for (const tag of cleanTags) merged.add(tag)
    const sorted = [...merged].sort((a, b) => {
      const pDiff = tagPriority(a) - tagPriority(b)
      if (pDiff !== 0) return pDiff
      return a.localeCompare(b)
    })
    byTarget.set(key, sorted)
  }
  return {
    byTarget,
    totalMachines: machines.length,
    taggedMachines,
    untaggedMachines: Math.max(0, machines.length - taggedMachines),
  }
}

function isHostSnapshotKind(kind: string): boolean {
  return kind === "vm" || kind === "physical"
}

function snapshotSeverityRank(status: string): number {
  if (status === "critical" || status === "error") return 0
  if (status === "warning") return 1
  if (status === "ok" || status === "pass") return 2
  return 3
}

/**
 * Indexes latest host telemetry per `cluster/instance` from monitor overview snapshots.
 * Prefers vm/physical host snapshots over app/container rows, and newer samples on ties.
 */
export function indexHostTelemetry(resources?: MonitorResourceSnapshot[] | null): Map<string, TargetTelemetry> {
  const best = new Map<string, { snap: MonitorResourceSnapshot; host: boolean }>()
  if (!Array.isArray(resources)) return new Map()
  for (const snap of resources) {
    if (!snap.cluster || !snap.instance) continue
    const hasMetrics =
      snap.cpu_percent != null || snap.memory_percent != null || snap.disk_percent != null
    const host = isHostSnapshotKind(snap.asset_kind)
    if (!host && !hasMetrics) continue
    const key = `${snap.cluster}/${snap.instance}`
    const prev = best.get(key)
    if (!prev) {
      best.set(key, { snap, host })
      continue
    }
    if (host && !prev.host) {
      best.set(key, { snap, host })
      continue
    }
    if (host === prev.host) {
      const curRank = snapshotSeverityRank(snap.status)
      const prevRank = snapshotSeverityRank(prev.snap.status)
      if (curRank < prevRank || (curRank === prevRank && snap.collected_at_ms > prev.snap.collected_at_ms)) {
        best.set(key, { snap, host })
      }
    }
  }
  const result = new Map<string, TargetTelemetry>()
  for (const [key, { snap }] of best.entries()) {
    result.set(key, {
      targetKey: key,
      status: snap.status,
      cpuPercent: snap.cpu_percent ?? null,
      memoryPercent: snap.memory_percent ?? null,
      diskPercent: snap.disk_percent ?? null,
      stale: Boolean(snap.stale),
      collectedAtMs: snap.collected_at_ms,
    })
  }
  return result
}

export function isTargetBusyOrAlerting(target: Target, telemetry?: TargetTelemetry): boolean {
  if (target.busy || (target.active_ops || 0) > 0 || (target.queued_ops || 0) > 0) return true
  return telemetry?.status === "critical" || telemetry?.status === "warning"
}

function targetMatchesQuery(target: Target, tags: string[], query: string): boolean {
  if (!query) return true
  if (
    target.cluster.toLowerCase().includes(query) ||
    target.instance.toLowerCase().includes(query) ||
    target.key.toLowerCase().includes(query) ||
    (target.remote || "").toLowerCase().includes(query) ||
    (target.mode || "").toLowerCase().includes(query)
  ) {
    return true
  }
  return tags.some((tag) => tag.toLowerCase().includes(query))
}

function nodePriority(target: Target, telemetry?: TargetTelemetry): number {
  if (telemetry?.status === "critical") return 0
  if (telemetry?.status === "warning") return 1
  if (target.busy || (target.active_ops || 0) > 0 || (target.queued_ops || 0) > 0) return 2
  return 3
}

/**
 * Produces a flat, high-density node list for the default homepage Node Workbench table,
 * sorted by operational priority (critical -> warning -> busy/active -> alphabetical)
 * and filterable by mode, busy/alerting status, or search across name/group/IP/tags.
 */
export function buildNodeRows(
  targets: Target[],
  telemetryByTarget: Map<string, TargetTelemetry>,
  tagsByTarget: Map<string, string[]> = new Map(),
  filter: ClusterFilterMode = "all",
  rawQuery = "",
): DashboardNodeRow[] {
  const query = rawQuery.trim().toLowerCase()
  const rows: DashboardNodeRow[] = []

  for (const target of targets) {
    const telemetry = telemetryByTarget.get(target.key)
    const tags = tagsByTarget.get(target.key) ?? []
    const isK8s = isTargetKubernetes(target)
    if (filter === "k8s" && !isK8s) continue
    if (filter === "host" && isK8s) continue
    if (filter === "busy" && !isTargetBusyOrAlerting(target, telemetry)) continue
    if (!targetMatchesQuery(target, tags, query)) continue
    rows.push({
      target,
      telemetry,
      tags,
      isK8s,
      priority: nodePriority(target, telemetry),
    })
  }

  rows.sort((a, b) => {
    if (a.priority !== b.priority) return a.priority - b.priority
    const aLoad = Math.max(a.telemetry?.cpuPercent ?? -1, a.telemetry?.memoryPercent ?? -1)
    const bLoad = Math.max(b.telemetry?.cpuPercent ?? -1, b.telemetry?.memoryPercent ?? -1)
    if (aLoad !== bLoad && (a.priority <= 1 || b.priority <= 1)) return bLoad - aLoad
    return a.target.key.localeCompare(b.target.key)
  })
  return rows
}

export function buildClusterGroups(
  targets: Target[],
  telemetryByTarget: Map<string, TargetTelemetry>,
  filter: ClusterFilterMode = "all",
  rawQuery = "",
  tagsByTarget: Map<string, string[]> = new Map(),
): DashboardClusterGroup[] {
  const query = rawQuery.trim().toLowerCase()
  const byCluster = new Map<string, Target[]>()

  for (const target of targets) {
    const telemetry = telemetryByTarget.get(target.key)
    const tags = tagsByTarget.get(target.key) ?? []
    if (filter === "k8s" && !isTargetKubernetes(target)) continue
    if (filter === "host" && isTargetKubernetes(target)) continue
    if (filter === "busy" && !isTargetBusyOrAlerting(target, telemetry)) continue
    if (!targetMatchesQuery(target, tags, query)) continue

    const list = byCluster.get(target.cluster) || []
    list.push(target)
    byCluster.set(target.cluster, list)
  }

  return [...byCluster.entries()]
    .map(([name, machines]) => {
      const sortedMachines = [...machines].sort((a, b) => {
        const aPri = nodePriority(a, telemetryByTarget.get(a.key))
        const bPri = nodePriority(b, telemetryByTarget.get(b.key))
        if (aPri !== bPri) return aPri - bPri
        return a.instance.localeCompare(b.instance)
      })
      let warningCount = 0
      let criticalCount = 0
      for (const m of sortedMachines) {
        const st = telemetryByTarget.get(m.key)?.status
        if (st === "critical") criticalCount++
        else if (st === "warning") warningCount++
      }
      return {
        name,
        machines: sortedMachines,
        connections: sortedMachines.length,
        sessions: sortedMachines.reduce((sum, m) => sum + (m.sessions?.length || 0), 0),
        busy: sortedMachines.filter((m) => m.busy).length,
        activeOps: sortedMachines.reduce((sum, m) => sum + (m.active_ops || 0), 0),
        queuedOps: sortedMachines.reduce((sum, m) => sum + (m.queued_ops || 0), 0),
        k8sCount: sortedMachines.filter((m) => isTargetKubernetes(m)).length,
        warningCount,
        criticalCount,
      }
    })
    .sort((a, b) => {
      const aHot = a.criticalCount > 0 ? 0 : a.warningCount > 0 ? 1 : a.busy > 0 || a.activeOps > 0 ? 2 : 3
      const bHot = b.criticalCount > 0 ? 0 : b.warningCount > 0 ? 1 : b.busy > 0 || b.activeOps > 0 ? 2 : 3
      if (aHot !== bHot) return aHot - bHot
      return a.name.localeCompare(b.name)
    })
}

export function buildAttentionItems(input: {
  onlineTargets: Target[]
  directoryTargets?: Target[]
  monitorOverview?: MonitorOverviewData | null
  maxItems?: number
}): { items: DashboardAttentionItem[]; total: number } {
  const maxItems = input.maxItems ?? 12
  const onlineMap = new Map<string, Target>()
  for (const t of input.onlineTargets) onlineMap.set(t.key, t)

  const items: DashboardAttentionItem[] = []
  const alertedTargetKeys = new Set<string>()

  // 1. Open threshold alerts (highest operational priority), enriched with live host telemetry.
  const worstSnapByTarget = new Map<string, MonitorResourceSnapshot>()
  for (const snap of input.monitorOverview?.resources ?? []) {
    if (snap.status !== "critical" && snap.status !== "warning") continue
    const targetKey = `${snap.cluster}/${snap.instance}`
    const prev = worstSnapByTarget.get(targetKey)
    if (!prev || snapshotSeverityRank(snap.status) < snapshotSeverityRank(prev.status)) {
      worstSnapByTarget.set(targetKey, snap)
    }
  }

  for (const alert of input.monitorOverview?.threshold_alerts ?? []) {
    const targetKey = `${alert.cluster}/${alert.instance}`
    alertedTargetKeys.add(targetKey)
    const metricLabel = THRESHOLD_METRIC_LABELS[alert.metric] || alert.metric
    const deviceSuffix = alert.device_id ? ` (${alert.device_id})` : ""
    const snap = worstSnapByTarget.get(targetKey)
    const liveParts: string[] = []
    if (snap?.cpu_percent != null) liveParts.push(`CPU ${snap.cpu_percent.toFixed(1)}%`)
    if (snap?.memory_percent != null) liveParts.push(`内存 ${snap.memory_percent.toFixed(1)}%`)
    items.push({
      id: `threshold:${targetKey}:${alert.metric}:${alert.device_id || ""}`,
      severity: "critical",
      title: targetKey,
      detail: `${metricLabel}${deviceSuffix} 超过阈值 ${alert.threshold}${liveParts.length > 0 ? `（${liveParts.join(" · ")}）` : ""}`,
      targetKey,
      target: onlineMap.get(targetKey),
      kind: "threshold_alert",
    })
  }

  // 2. Critical / warning resource snapshots (deduplicated per target already covered by an open threshold alert).
  for (const [targetKey, snap] of worstSnapByTarget.entries()) {
    if (alertedTargetKeys.has(targetKey)) continue
    const parts: string[] = []
    if (snap.cpu_percent != null) parts.push(`CPU ${snap.cpu_percent.toFixed(1)}%`)
    if (snap.memory_percent != null) parts.push(`内存 ${snap.memory_percent.toFixed(1)}%`)
    if (snap.disk_percent != null) parts.push(`磁盘 ${snap.disk_percent.toFixed(1)}%`)
    items.push({
      id: `resource:${targetKey}`,
      severity: snap.status === "critical" ? "critical" : "warning",
      title: targetKey,
      detail: parts.length > 0 ? `资源异常 · ${parts.join(" · ")}` : `${snap.asset_name || snap.asset_id} 状态异常`,
      targetKey,
      target: onlineMap.get(targetKey),
      kind: "resource_health",
    })
  }

  // 3. Recent failed or critical inspection runs (top 3 most recent).
  const failedRuns = (input.monitorOverview?.recent_runs ?? [])
    .filter((run) => run.status === "failed" || run.status === "critical")
    .slice(0, 3)
  for (const run of failedRuns) {
    items.push({
      id: `run:${run.id}`,
      severity: run.status === "critical" || run.failed_checks > 0 ? "critical" : "warning",
      title: run.schedule_name || (run.skills?.length ? run.skills.join(", ") : "巡检任务"),
      detail:
        run.failed_checks > 0
          ? `巡检发现 ${run.failed_checks} 项严重异常${run.warning_checks > 0 ? ` · ${run.warning_checks} 项告警` : ""}`
          : run.summary || "巡检执行失败",
      runId: run.id,
      kind: "failed_run",
    })
  }

  // 4. Offline registered targets visible to this user.
  for (const t of input.directoryTargets ?? []) {
    if (t.status !== "offline") continue
    if (onlineMap.has(t.key)) continue
    items.push({
      id: `offline:${t.key}`,
      severity: "offline",
      title: t.key,
      detail: "已登记 Agent 当前离线（可查阅历史监控曲线）",
      targetKey: t.key,
      kind: "offline_target",
    })
  }

  items.sort((a, b) => {
    const rankDiff = ATTENTION_RANK[a.severity] - ATTENTION_RANK[b.severity]
    if (rankDiff !== 0) return rankDiff
    return a.title.localeCompare(b.title)
  })

  return {
    items: items.slice(0, maxItems),
    total: items.length,
  }
}

/**
 * Partitions home widgets into:
 * - `active`: plugins that have at least one collected metric point (ordered critical -> warning -> ok -> plugin order)
 * - `pending`: plugins with zero collected metric points yet (collapsed into a single compact quick-launch strip)
 * Never truncates or drops enabled plugins; preserves unbounded pluggability while eliminating empty-card spam.
 */
export function partitionHomeWidgets(
  plugins: PluginView[],
  bySkillKey: Map<string, MonitorLatestMetricItem>,
): HomeWidgetPartition {
  const active: EvaluatedHomeWidget[] = []
  const pending: EvaluatedHomeWidget[] = []

  for (const plugin of plugins) {
    const hw = plugin.manifest?.ui.slots.home_widget
    if (!hw) continue
    const metrics: HomeWidgetMetricMatch[] = (hw.metrics ?? []).map((spec) => ({
      key: spec.key,
      label: spec.label,
      unit: spec.unit,
      aggregate: spec.aggregate,
      point: plugin.skill_slug ? bySkillKey.get(`${plugin.skill_slug}:${spec.key}`) : undefined,
    }))
    const reportingTargets = Math.max(0, ...metrics.map((m) => m.point?.reporting_targets ?? 0))
    const hasData = reportingTargets > 0 || metrics.some((m) => Boolean(m.point))
    const hasCritical = metrics.some((m) => m.point?.status === "critical")
    const hasWarning = metrics.some((m) => m.point?.status === "warning")
    const severity: EvaluatedHomeWidget["severity"] = hasCritical
      ? "critical"
      : hasWarning
        ? "warning"
        : hasData
          ? "ok"
          : "none"

    const evaluated: EvaluatedHomeWidget = {
      plugin,
      metrics,
      reportingTargets,
      hasData,
      severity,
    }
    if (hasData) active.push(evaluated)
    else pending.push(evaluated)
  }

  active.sort((a, b) => SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity])
  return { active, pending }
}

export function resolveDefaultActivityTab(input: {
  auditDenied: boolean
  hasInspectionRuns: boolean
}): "audit" | "runs" {
  if (input.auditDenied && input.hasInspectionRuns) return "runs"
  return "audit"
}

export function formatDashboardStatusLine(input: {
  onlineAgents: number
  activeOps: number
  issueCount: number
}): string {
  if (input.onlineAgents === 0) {
    return input.issueCount > 0
      ? `暂无在线 Agent · ${input.issueCount} 项异常待处理`
      : "暂无在线 Agent"
  }
  const parts = [`${input.onlineAgents} 台在线`]
  if (input.issueCount > 0) parts.push(`${input.issueCount} 项异常待处理`)
  else parts.push("运行正常")
  if (input.activeOps > 0) parts.push(`${input.activeOps} 项执行中`)
  return parts.join(" · ")
}

export function summarizeRunTitle(run: MonitorRunRecord): string {
  if (run.schedule_name) return run.schedule_name
  if (run.skills && run.skills.length > 0) return run.skills.join(" / ")
  if (run.command) return run.command
  if (run.prompt) return run.prompt
  return "巡检执行"
}
