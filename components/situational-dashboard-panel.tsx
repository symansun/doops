"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { Session, Target } from "@/lib/client"
import { assetRequest } from "@/lib/assets"
import type { TargetTab } from "@/lib/console-views"
import {
  buildRunReportCSV,
  fetchMonitorOverview,
  fetchMonitorRunDetail,
  formatBytesCompact,
  formatRateBytes,
  triggerMonitorRun,
  type MonitorOverviewData,
  type MonitorResourceSnapshot,
  type MonitorRunDetailData,
} from "@/lib/monitor-engine"
import {
  analyzeInspectionWithAi,
  buildCustomReportHtml,
  buildCustomReportMarkdown,
  type ReportSectionKey,
} from "@/lib/inspection-report"
import { ActivityIcon, RefreshIcon, RocketIcon, SparkIcon } from "./icons"
import { AssetTopologyPanel } from "./asset-topology-panel"
import {
  HolographicCore3D,
  LayeredArchitecture3D,
  ResourceBars3D,
  TiltCard3D,
  type ArchLink3D,
  type ArchNode3D,
  type CoreNode3D,
  type ResourceBar3DItem,
} from "./viz-3d"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Checkbox,
  Inline,
  Input,
  PageHeader,
  PageTabs,
  Select,
  Stack,
  StatusBanner,
} from "./ui"

type SituationTab = "dashboard" | "topology"

interface BusinessDeploymentRow {
  cluster: string
  instance: string
  namespace: string
  application: string
  database: string
  verified_at?: string
}

interface BusinessSystemRow {
  asset_id: string
  name: string
  owner: string
  active: boolean
  deployments: BusinessDeploymentRow[]
}

interface ClusterRow {
  asset_id: string
  name: string
  environment?: string
  owner?: string
  sources?: { cluster: string; instance: string; online?: boolean }[]
  namespaces?: { name: string }[]
}

interface MachineRow {
  asset_id: string
  name: string
  kind: "physical" | "vm"
  address?: string
  cpu_count?: number | null
  memory_gib?: number | null
  disk_gib?: number | null
  cluster?: string
  instance?: string
  tags?: string[]
}

const REPORT_SECTIONS: { key: ReportSectionKey; label: string }[] = [
  { key: "topology_coverage", label: "业务 → 集群 → 机器整体架构" },
  { key: "system_gpu", label: "机器与集群资源水位（含 GPU）" },
  { key: "app_database", label: "应用与数据库运行观测" },
  { key: "inspection_vuln", label: "巡检执行与产物概览" },
  { key: "security_events", label: "实时阈值告警记录" },
  { key: "ai_remediation", label: "异常项与处置建议" },
]

function downloadFile(filename: string, content: string, mime: string) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function fmtClock(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  return new Date(ms).toLocaleTimeString("zh-CN", { hour12: false })
}

function fmtDateTime(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function priorityVariant(p: "P0" | "P1" | "P2" | "P3"): "danger" | "warning" | "primary" | "outline" {
  if (p === "P0") return "danger"
  if (p === "P1") return "warning"
  if (p === "P2") return "primary"
  return "outline"
}

function toNodeStatus(st?: string): "ok" | "warning" | "critical" {
  if (st === "critical" || st === "error" || st === "offline") return "critical"
  if (st === "warning" || st === "partial") return "warning"
  return "ok"
}

function worstStatus(list: ("ok" | "warning" | "critical")[]): "ok" | "warning" | "critical" {
  if (list.includes("critical")) return "critical"
  if (list.includes("warning")) return "warning"
  return "ok"
}

function SituationalTrendChart({ overview }: { overview: MonitorOverviewData }) {
  const buckets = overview.trends
  if (!buckets.length) {
    return (
      <p className="py-12 text-center text-xs text-muted-foreground">
        所选时间窗内暂无时序采样点，可点击「立即发起全网巡检」触发实时采集。
      </p>
    )
  }
  const W = 680
  const H = 190
  const padL = 36
  const padR = 14
  const padT = 16
  const padB = 26
  const innerW = W - padL - padR
  const innerH = H - padT - padB

  const buildPath = (pick: (b: MonitorOverviewData["trends"][number]) => number | null | undefined) => {
    const pts: string[] = []
    buckets.forEach((b, idx) => {
      const v = pick(b)
      if (v == null || !Number.isFinite(v)) return
      const x = padL + (buckets.length <= 1 ? innerW / 2 : (innerW * idx) / (buckets.length - 1))
      const y = padT + innerH - (Math.max(0, Math.min(100, v)) / 100) * innerH
      pts.push(`${pts.length === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`)
    })
    return pts.join(" ")
  }

  const cpuPath = buildPath((b) => b.avg_cpu_percent)
  const memPath = buildPath((b) => b.avg_memory_percent)
  const diskPath = buildPath((b) => b.avg_disk_percent)

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <span className="font-semibold text-foreground">全网机器与集群资源水位演进</span>
        <Inline gap={3} className="text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <span className="h-2 w-2 rounded-full bg-primary" /> CPU %
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-2 w-2 rounded-full bg-warning" /> 内存 %
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-2 w-2 rounded-full bg-success" /> 磁盘 %
          </span>
        </Inline>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="h-44 w-full select-none" role="img" aria-label="全网资源水位趋势图">
        {[0, 25, 50, 75, 100].map((pct) => {
          const y = padT + innerH - (pct / 100) * innerH
          return (
            <g key={pct}>
              <line x1={padL} y1={y} x2={W - padR} y2={y} stroke="currentColor" strokeOpacity={0.1} strokeDasharray="3 3" />
              <text x={padL - 6} y={y + 3} textAnchor="end" fontSize="10" fill="currentColor" opacity={0.5}>
                {pct}%
              </text>
            </g>
          )
        })}
        {diskPath && <path d={diskPath} fill="none" stroke="var(--success)" strokeWidth="1.8" strokeDasharray="4 3" />}
        {memPath && <path d={memPath} fill="none" stroke="var(--warning)" strokeWidth="2.2" />}
        {cpuPath && <path d={cpuPath} fill="none" stroke="var(--primary)" strokeWidth="2.4" />}
        <text x={padL} y={H - 6} fontSize="10" fill="currentColor" opacity={0.55}>
          {fmtClock(buckets[0].start_ms)}
        </text>
        <text x={W - padR} y={H - 6} textAnchor="end" fontSize="10" fill="currentColor" opacity={0.55}>
          {fmtClock(buckets[buckets.length - 1].end_ms)}
        </text>
      </svg>
    </div>
  )
}

export function SituationalDashboardPanel({
  session,
  targets,
  clusterFilter,
  onOpenTarget,
  onOpenInspection,
  onOpenResourceMonitor,
  onOpenPlatformMonitor,
  onOpenArea,
}: {
  session: Session
  targets: Target[]
  clusterFilter?: string
  onOpenTarget?: (cluster: string, instance: string, tab: TargetTab) => void
  onOpenInspection?: (runId?: string) => void
  onOpenResourceMonitor?: (targetKey?: string) => void
  onOpenPlatformMonitor?: () => void
  onOpenArea?: (area: "systems" | "clusters" | "machines") => void
}) {
  const [subTab, setSubTab] = useState<SituationTab>("dashboard")
  const [overview, setOverview] = useState<MonitorOverviewData | null>(null)
  const [latestDetail, setLatestDetail] = useState<MonitorRunDetailData | null>(null)
  const [systems, setSystems] = useState<BusinessSystemRow[]>([])
  const [clusters, setClusters] = useState<ClusterRow[]>([])
  const [machines, setMachines] = useState<MachineRow[]>([])
  const [loading, setLoading] = useState(true)
  const [inspecting, setInspecting] = useState(false)
  const [inspectRounds, setInspectRounds] = useState(0)
  const [notice, setNotice] = useState("")
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [selectedNodeId, setSelectedNodeId] = useState<string>("")
  const [onlyAnomalies, setOnlyAnomalies] = useState(false)
  const [chartMode, setChartMode] = useState<"3d" | "trend">("3d")

  // Custom report drawer
  const [reportOpen, setReportOpen] = useState(false)
  const [reportTitle, setReportTitle] = useState("DoOps 业务→集群→机器综合态势与监控报告")
  const [reportPeriod, setReportPeriod] = useState<"24h" | "7d" | "30d">("7d")
  const [reportSections, setReportSections] = useState<ReportSectionKey[]>([
    "topology_coverage",
    "system_gpu",
    "app_database",
    "inspection_vuln",
    "security_events",
    "ai_remediation",
  ])
  const [generatedMarkdown, setGeneratedMarkdown] = useState("")

  const reload = useCallback(() => setRevision((v) => v + 1), [])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    Promise.all([
      fetchMonitorOverview(session, { range: "1h", cluster: clusterFilter }, ctrl.signal),
      assetRequest<{ systems?: BusinessSystemRow[] }>("systems").catch(() => ({ systems: [] })),
      assetRequest<{ clusters?: ClusterRow[] }>("clusters").catch(() => ({ clusters: [] })),
      assetRequest<{ machines?: MachineRow[] }>("machines").catch(() => ({ machines: [] })),
    ])
      .then(async ([ov, sysRes, cluRes, machRes]) => {
        if (ctrl.signal.aborted) return
        setOverview(ov)
        setSystems(Array.isArray(sysRes?.systems) ? sysRes.systems : [])
        setClusters(Array.isArray(cluRes?.clusters) ? cluRes.clusters : [])
        setMachines(Array.isArray(machRes?.machines) ? machRes.machines : [])
        const latestRunId = ov.recent_runs?.[0]?.id
        if (latestRunId) {
          try {
            const det = await fetchMonitorRunDetail(session, latestRunId, ctrl.signal)
            if (!ctrl.signal.aborted) setLatestDetail(det)
          } catch {
            if (!ctrl.signal.aborted) setLatestDetail(null)
          }
        } else {
          setLatestDetail(null)
        }
        if (!ctrl.signal.aborted) setLoading(false)
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error).message)
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, clusterFilter, revision])

  async function triggerGlobalInspection() {
    if (inspecting) return
    setInspecting(true)
    setError("")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, {
        scope_mode: "all_online",
        skills: ["inspect-vm-host"],
      })
      const nextRound = inspectRounds + 1
      setInspectRounds(nextRound)
      setNotice(`第 ${nextRound} 轮主机采集巡检完成（#${res.run.id} · 耗时 ${(res.run.duration_ms / 1000).toFixed(1)}s）：${res.run.summary}`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setInspecting(false)
    }
  }

  const resources = overview?.resources ?? []
  const onlineTargetSet = useMemo(
    () => new Set(targets.filter((t) => t.status !== "offline").map((t) => `${t.cluster}/${t.instance}`)),
    [targets],
  )

  // Dynamically assemble Business -> Workload/App/DB -> Cluster -> Machine 3D nodes strictly from live API responses.
  const archNodes: ArchNode3D[] = useMemo(() => {
    const out: ArchNode3D[] = []
    const resByAsset = new Map<string, MonitorResourceSnapshot>()
    const resByTarget = new Map<string, MonitorResourceSnapshot[]>()
    for (const r of resources) {
      resByAsset.set(r.asset_id, r)
      const tk = `${r.cluster}/${r.instance}`
      const arr = resByTarget.get(tk) ?? []
      arr.push(r)
      resByTarget.set(tk, arr)
    }

    // L4: Business Systems (strictly from /v1/assets/systems)
    const activeSystems = systems.filter((sys) => sys.active !== false)
    for (const sys of activeSystems.slice(0, 10)) {
      const deps = sys.deployments ?? []
      const linkedStatuses: ("ok" | "warning" | "critical")[] = []
      for (const d of deps) {
        const tk = `${d.cluster}/${d.instance}`
        if (!onlineTargetSet.has(tk)) linkedStatuses.push("warning")
        for (const r of resByTarget.get(tk) ?? []) {
          linkedStatuses.push(toNodeStatus(r.status))
        }
      }
      const firstDep = deps[0]
      const appNames = [...new Set(deps.map((d) => d.application).filter(Boolean))]
      const dbNames = [...new Set(deps.map((d) => d.database).filter(Boolean))]
      out.push({
        id: `sys:${sys.asset_id}`,
        layer: "business",
        title: sys.name,
        subtitle: `${sys.asset_id} · ${sys.owner || "未指定负责人"}`,
        metricLine: deps.length
          ? `${deps.length} 处部署 · ${appNames.length} 应用${dbNames.length ? ` · ${dbNames.length} 库` : ""}`
          : "已登记 · 尚未绑定部署",
        status: linkedStatuses.length ? worstStatus(linkedStatuses) : "ok",
        cluster: firstDep?.cluster,
        instance: firstDep?.instance,
        impactNote: deps.length
          ? `部署在 ${deps.map((d) => `${d.cluster}/${d.instance}(${d.namespace})`).join("、")}${dbNames.length ? `，依赖数据库：${dbNames.join("、")}` : ""}`
          : "尚未配置集群部署与数据库依赖，可在「业务系统」中补录。",
      })
    }

    // L3: Applications, Containers & Databases (from real monitor snapshots + deployed apps)
    const appAndDbSnapshots = resources.filter(
      (r) => r.asset_kind === "app" || r.asset_kind === "database" || r.asset_kind === "container",
    )
    const seenAppIds = new Set<string>()
    for (const r of appAndDbSnapshots.slice(0, 8)) {
      seenAppIds.add(r.asset_id)
      const dbStats = (r.details as Record<string, unknown> | undefined)?.db_stats as Record<string, number> | undefined
      const metricLine =
        r.asset_kind === "app"
          ? `延迟 ${r.response_time_ms != null ? `${r.response_time_ms.toFixed(1)}ms` : "—"}${r.requests_per_sec != null ? ` · ${r.requests_per_sec.toFixed(0)} req/s` : ""}${r.error_rate != null ? ` · 错误 ${r.error_rate}%` : ""}`
          : r.asset_kind === "database"
            ? dbStats
              ? `连接 ${dbStats.connections}/${dbStats.max_connections} · ${dbStats.tps} TPS · 锁等待 ${dbStats.lock_waits}`
              : `TCP 建连 ${r.response_time_ms != null ? `${r.response_time_ms.toFixed(1)}ms` : "—"} · 错误 ${r.error_rate ?? 0}%`
            : `CPU ${r.cpu_percent != null ? `${r.cpu_percent.toFixed(1)}%` : "—"} · 内存 ${r.memory_percent != null ? `${r.memory_percent.toFixed(1)}%` : "—"}`
      out.push({
        id: `app:${r.asset_id}`,
        layer: "app",
        title: r.asset_name || r.asset_id,
        subtitle: `${r.cluster}/${r.instance} · ${r.asset_kind}`,
        metricLine,
        status: toNodeStatus(r.status),
        cluster: r.cluster,
        instance: r.instance,
        assetId: r.asset_id,
        impactNote: `由 ${r.cluster}/${r.instance} 经 ${r.collector_method} 实采（状态 ${r.status}），最近更新 ${fmtClock(r.collected_at_ms)}`,
      })
    }
    if (out.filter((n) => n.layer === "app").length === 0) {
      for (const sys of activeSystems) {
        for (const d of sys.deployments ?? []) {
          const key = `${d.namespace}/${d.application}`
          if (!d.application || seenAppIds.has(key) || out.filter((n) => n.layer === "app").length >= 6) continue
          seenAppIds.add(key)
          out.push({
            id: `app:${key}`,
            layer: "app",
            title: d.application,
            subtitle: `${d.cluster}/${d.instance} · ns:${d.namespace}`,
            metricLine: d.database ? `依赖库 ${d.database} · 待 HTTP 探活` : "业务台账已登记 · 待采集指标",
            status: onlineTargetSet.has(`${d.cluster}/${d.instance}`) ? "ok" : "warning",
            cluster: d.cluster,
            instance: d.instance,
            impactNote: `归属业务「${sys.name}」，来源目标 ${d.cluster}/${d.instance}，命名空间 ${d.namespace}。`,
          })
        }
      }
    }

    // L2: Kubernetes Clusters (from /v1/assets/clusters + k8s monitor snapshots)
    const seenClusters = new Set<string>()
    for (const c of clusters.slice(0, 6)) {
      seenClusters.add(c.asset_id)
      const snap =
        resByAsset.get(c.asset_id) ||
        resources.find(
          (r) =>
            r.asset_kind === "k8s" &&
            (r.asset_id === c.asset_id || (c.sources ?? []).some((src) => src.cluster === r.cluster && src.instance === r.instance)),
        )
      const src = c.sources?.[0]
      const nsCount = c.namespaces?.length ?? 0
      out.push({
        id: `clu:${c.asset_id}`,
        layer: "platform",
        title: c.name || c.asset_id,
        subtitle: `${c.asset_id}${c.environment ? ` · ${c.environment}` : ""}${nsCount ? ` · ${nsCount} NS` : ""}`,
        metricLine: snap
          ? `CPU ${snap.cpu_percent != null ? `${snap.cpu_percent.toFixed(1)}%` : "—"} · 内存 ${snap.memory_percent != null ? `${snap.memory_percent.toFixed(1)}%` : "—"}`
          : src
            ? `来源 ${src.cluster}/${src.instance} · 待采 K8s 指标`
            : "已登记待关联在线 Agent",
        status: snap ? toNodeStatus(snap.status) : src?.online === false ? "warning" : "ok",
        cluster: snap?.cluster || src?.cluster,
        instance: snap?.instance || src?.instance,
        assetId: c.asset_id,
        impactNote: snap
          ? `已关联 K8s 巡检快照（${snap.cluster}/${snap.instance}），最近同步 ${fmtClock(snap.collected_at_ms)}。`
          : `集群台账共登记 ${c.sources?.length ?? 0} 个 Agent 来源、${nsCount} 个命名空间。`,
      })
    }
    for (const r of resources.filter((item) => item.asset_kind === "k8s")) {
      if (seenClusters.has(r.asset_id) || out.filter((n) => n.layer === "platform").length >= 6) continue
      seenClusters.add(r.asset_id)
      out.push({
        id: `clu:${r.asset_id}`,
        layer: "platform",
        title: r.asset_name || r.asset_id,
        subtitle: `${r.cluster}/${r.instance} · k8s_api`,
        metricLine: `CPU ${r.cpu_percent != null ? `${r.cpu_percent.toFixed(1)}%` : "—"} · 内存 ${r.memory_percent != null ? `${r.memory_percent.toFixed(1)}%` : "—"}`,
        status: toNodeStatus(r.status),
        cluster: r.cluster,
        instance: r.instance,
        assetId: r.asset_id,
        impactNote: `由在线 Agent ${r.cluster}/${r.instance} 经 kubectl 实采，最近同步 ${fmtClock(r.collected_at_ms)}。`,
      })
    }

    // L1: Machines / GPU / VM / SSH Resources — always prioritize live-sampled hosts first so observed Agents are never truncated
    const seenMachines = new Set<string>()
    const liveHostSnapshots = resources.filter((item) => item.asset_kind === "vm" || item.asset_kind === "physical")
    const sortedMachines = [...machines].sort((a, b) => {
      const aLive =
        resByAsset.has(a.asset_id) ||
        liveHostSnapshots.some((r) => a.cluster && a.instance && r.cluster === a.cluster && r.instance === a.instance)
      const bLive =
        resByAsset.has(b.asset_id) ||
        liveHostSnapshots.some((r) => b.cluster && b.instance && r.cluster === b.cluster && r.instance === b.instance)
      if (aLive !== bLive) return aLive ? -1 : 1
      const aBound = Boolean(a.cluster && a.instance)
      const bBound = Boolean(b.cluster && b.instance)
      if (aBound !== bBound) return aBound ? -1 : 1
      return 0
    })

    // First include live observed host snapshots that aren't yet bound to any ledger machine
    for (const r of liveHostSnapshots) {
      const matchedInLedger = sortedMachines.some(
        (m) => m.asset_id === r.asset_id || (m.cluster === r.cluster && m.instance === r.instance),
      )
      if (matchedInLedger || seenMachines.has(r.asset_id)) continue
      seenMachines.add(r.asset_id)
      out.push({
        id: `mach:${r.asset_id}`,
        layer: "infra",
        title: r.asset_name || r.asset_id,
        subtitle: `${r.cluster}/${r.instance} · 实时主机探针`,
        metricLine: `CPU ${r.cpu_percent != null ? `${r.cpu_percent.toFixed(1)}%` : "—"} · 内存 ${r.memory_percent != null ? `${r.memory_percent.toFixed(1)}%` : "—"} · 盘 ${r.disk_percent != null ? `${r.disk_percent.toFixed(1)}%` : "—"}${r.gpu_percent != null ? ` · GPU ${r.gpu_percent.toFixed(0)}%` : ""}`,
        status: toNodeStatus(r.status),
        cluster: r.cluster,
        instance: r.instance,
        assetId: r.asset_id,
        impactNote: `由在线目标 ${r.cluster}/${r.instance} 实时采集（${r.os_info || r.asset_kind}），最近同步 ${fmtClock(r.collected_at_ms)}。`,
      })
    }

    for (const m of sortedMachines) {
      if (seenMachines.has(m.asset_id) || out.filter((n) => n.layer === "infra").length >= 12) continue
      seenMachines.add(m.asset_id)
      const snap =
        resByAsset.get(m.asset_id) ||
        liveHostSnapshots.find(
          (r) =>
            m.cluster &&
            m.instance &&
            r.cluster === m.cluster &&
            r.instance === m.instance,
        )
      const boundOnline = Boolean(m.cluster && m.instance && onlineTargetSet.has(`${m.cluster}/${m.instance}`))
      const specLine = [
        m.cpu_count ? `${m.cpu_count}C` : "",
        m.memory_gib ? `${m.memory_gib}GiB` : "",
        m.disk_gib ? `${m.disk_gib}G盘` : "",
      ]
        .filter(Boolean)
        .join(" / ")
      out.push({
        id: `mach:${m.asset_id}`,
        layer: "infra",
        title: m.name || m.asset_id,
        subtitle: `${m.asset_id} · ${m.kind === "physical" ? "物理机" : "虚拟机"}${m.address ? ` · ${m.address}` : ""}`,
        metricLine: snap
          ? `CPU ${snap.cpu_percent != null ? `${snap.cpu_percent.toFixed(1)}%` : "—"} · 内存 ${snap.memory_percent != null ? `${snap.memory_percent.toFixed(1)}%` : "—"} · 盘 ${snap.disk_percent != null ? `${snap.disk_percent.toFixed(1)}%` : "—"}${snap.gpu_percent != null ? ` · GPU ${snap.gpu_percent.toFixed(0)}%` : ""}`
          : specLine || (boundOnline ? "已绑定在线 Agent · 待采样" : "未接入 Agent"),
        status: snap ? toNodeStatus(snap.status) : m.cluster && !boundOnline ? "warning" : "ok",
        cluster: snap?.cluster || m.cluster,
        instance: snap?.instance || m.instance,
        assetId: m.asset_id,
        impactNote: snap
          ? `采集方式：${snap.collector_method}（${snap.cluster}/${snap.instance}），最近采样 ${fmtClock(snap.collected_at_ms)}${m.tags?.length ? `；标签：${m.tags.join("、")}` : ""}。`
          : m.cluster
            ? `已绑定目标 ${m.cluster}/${m.instance}（${boundOnline ? "在线" : "离线"}），可点击发起巡检或前往资源监控采集。`
            : "尚未绑定 Agent 目标，可在「机器台账」中完成绑定。",
      })
    }

    return out
  }, [systems, clusters, machines, resources, onlineTargetSet])

  // Build real cross-layer dependency edges (Business -> App/DB -> Cluster -> Machine) for blast-radius tracing
  const archLinks: ArchLink3D[] = useMemo(() => {
    const edges: ArchLink3D[] = []
    const seen = new Set<string>()
    const addEdge = (from: string, to: string, label?: string) => {
      if (!from || !to || from === to) return
      const key = `${from}->${to}`
      if (seen.has(key)) return
      seen.add(key)
      edges.push({ from, to, label })
    }

    const appNodes = archNodes.filter((n) => n.layer === "app")
    const cluNodes = archNodes.filter((n) => n.layer === "platform")
    const machNodes = archNodes.filter((n) => n.layer === "infra")

    // L4 Business -> L3 App/DB & L2 Cluster
    for (const sys of systems.filter((x) => x.active !== false)) {
      const fromId = `sys:${sys.asset_id}`
      if (!archNodes.some((n) => n.id === fromId)) continue
      for (const dep of sys.deployments ?? []) {
        let matchedApp = false
        for (const app of appNodes) {
          const sameTarget = app.cluster === dep.cluster && app.instance === dep.instance
          const nameHit =
            (dep.application &&
              (app.title.toLowerCase().includes(dep.application.toLowerCase()) ||
                (app.assetId || "").toLowerCase().includes(dep.application.toLowerCase()))) ||
            (dep.database &&
              (app.title.toLowerCase().includes(dep.database.toLowerCase()) ||
                (app.assetId || "").toLowerCase().includes(dep.database.toLowerCase())))
          if (sameTarget || nameHit) {
            addEdge(fromId, app.id, "业务依赖")
            matchedApp = true
          }
        }
        if (!matchedApp) {
          for (const clu of cluNodes) {
            if (clu.cluster === dep.cluster && (!dep.instance || clu.instance === dep.instance)) {
              addEdge(fromId, clu.id, "部署于")
            }
          }
        }
      }
    }

    // L3 App/DB -> L2 Cluster (or directly to L1 Machine when no cluster on that target)
    for (const app of appNodes) {
      let linkedCluster = false
      for (const clu of cluNodes) {
        if (app.cluster && clu.cluster === app.cluster && (!app.instance || !clu.instance || clu.instance === app.instance)) {
          addEdge(app.id, clu.id, "运行于集群")
          linkedCluster = true
        }
      }
      if (!linkedCluster) {
        for (const m of machNodes) {
          if (app.cluster && m.cluster === app.cluster && (!app.instance || m.instance === app.instance)) {
            addEdge(app.id, m.id, "运行于主机")
          }
        }
      }
    }

    // L2 Cluster -> L1 Machine
    for (const clu of cluNodes) {
      let linkedExact = false
      for (const m of machNodes) {
        if (clu.cluster && m.cluster === clu.cluster && clu.instance && m.instance === clu.instance) {
          addEdge(clu.id, m.id, "承载宿主机")
          linkedExact = true
        }
      }
      if (!linkedExact && clu.cluster) {
        for (const m of machNodes) {
          if (m.cluster === clu.cluster) {
            addEdge(clu.id, m.id, "同集群宿主")
          }
        }
      }
    }

    return edges
  }, [archNodes, systems])

  const selectedNode = archNodes.find((n) => n.id === selectedNodeId) || archNodes.find((n) => n.status !== "ok") || archNodes[0]

  const selectedChain = useMemo(() => {
    if (!selectedNode) return { upstream: [] as ArchNode3D[], downstream: [] as ArchNode3D[] }
    const byId = new Map(archNodes.map((n) => [n.id, n]))
    const upIds = new Set<string>()
    const downIds = new Set<string>()
    const uq = [selectedNode.id]
    while (uq.length > 0) {
      const cur = uq.shift()!
      for (const lk of archLinks) {
        if (lk.to === cur && !upIds.has(lk.from) && lk.from !== selectedNode.id) {
          upIds.add(lk.from)
          uq.push(lk.from)
        }
      }
    }
    const dq = [selectedNode.id]
    while (dq.length > 0) {
      const cur = dq.shift()!
      for (const lk of archLinks) {
        if (lk.from === cur && !downIds.has(lk.to) && lk.to !== selectedNode.id) {
          downIds.add(lk.to)
          dq.push(lk.to)
        }
      }
    }
    return {
      upstream: [...upIds].map((id) => byId.get(id)).filter(Boolean) as ArchNode3D[],
      downstream: [...downIds].map((id) => byId.get(id)).filter(Boolean) as ArchNode3D[],
    }
  }, [archNodes, archLinks, selectedNode])

  // Build 3D Holographic Core nodes dynamically from real archNodes
  const coreNodes: CoreNode3D[] = useMemo(() => {
    const sample = archNodes.slice(0, 12)
    if (sample.length === 0) {
      return [
        { id: "hub", label: "DoOps Gateway", status: targets.some((t) => t.status !== "offline") ? "ok" : "warning", lat: 8, lon: 0, shell: 1.04 },
        ...targets.slice(0, 8).map((t, idx) => ({
          id: t.key,
          label: `${t.cluster}/${t.instance}`,
          status: (t.status === "offline" ? "warning" : "ok") as "ok" | "warning",
          lat: ((idx % 4) - 1.5) * 28,
          lon: (idx * 72 - 140) % 360,
        })),
      ]
    }
    const coords: [number, number][] = [
      [8, 0], [36, 42], [24, 118], [-12, 68], [46, -42], [-26, -24],
      [16, -108], [-36, 148], [-14, -162], [-48, 22], [50, 158], [-42, -96],
    ]
    return sample.map((n, i) => ({
      id: n.id,
      label: n.title.length > 9 ? `${n.title.slice(0, 8)}…` : n.title,
      status: n.status,
      lat: coords[i % coords.length][0],
      lon: coords[i % coords.length][1],
      shell: i === 0 ? 1.04 : 1,
    }))
  }, [archNodes, targets])

  // Build 3D Isometric Resource Prism Bars strictly from real overview.resources
  const bar3DItems: ResourceBar3DItem[] = useMemo(() => {
    const hostAndCluster = resources.filter(
      (r) =>
        r.cpu_percent != null ||
        r.memory_percent != null ||
        r.disk_percent != null ||
        r.gpu_percent != null,
    )
    return hostAndCluster.slice(0, 7).map((r) => ({
      id: `${r.cluster}/${r.instance}/${r.asset_id}`,
      label: (r.asset_name || r.asset_id).slice(0, 8),
      sublabel: r.asset_id.slice(0, 12),
      cpu: r.cpu_percent ?? 0,
      memory: r.memory_percent ?? 0,
      disk: r.disk_percent ?? 0,
      gpu: r.gpu_percent ?? null,
      status: toNodeStatus(r.status),
    }))
  }, [resources])

  const ai = useMemo(() => analyzeInspectionWithAi(latestDetail, overview), [latestDetail, overview])

  const s = overview?.summary
  const latestRun = latestDetail?.run || overview?.recent_runs?.[0]

  const totalNetBps = resources.reduce((sum, r) => sum + (r.net_bytes_per_sec || 0), 0)
  const totalIops = resources.reduce((sum, r) => sum + (r.disk_iops || 0), 0)
  const appRes = resources.find((r) => r.asset_kind === "app")
  const dbRes = resources.find((r) => r.asset_kind === "database")
  const gpuRes = resources.find((r) => r.gpu_percent != null)
  const dbStats = (dbRes?.details as Record<string, unknown> | undefined)?.db_stats as Record<string, number> | undefined
  const secHost = resources.find((r) => (r.details as Record<string, unknown> | undefined)?.security_events)
  const secStats = (secHost?.details as Record<string, unknown> | undefined)?.security_events as Record<string, number> | undefined

  const activeSystemsCount = systems.filter((x) => x.active !== false).length
  const appLayerCount = archNodes.filter((n) => n.layer === "app").length
  const clusterLayerCount = archNodes.filter((n) => n.layer === "platform").length
  const machineLayerCount = archNodes.filter((n) => n.layer === "infra").length
  const sampledMachineCount = useMemo(() => {
    const sampledIds = new Set(
      resources
        .filter((r) => r.asset_kind === "vm" || r.asset_kind === "physical")
        .map((r) => r.asset_id),
    )
    const sampledTargets = new Set(
      resources
        .filter((r) => r.asset_kind === "vm" || r.asset_kind === "physical")
        .map((r) => `${r.cluster}/${r.instance}`),
    )
    return archNodes.filter(
      (n) =>
        n.layer === "infra" &&
        ((n.assetId && sampledIds.has(n.assetId)) ||
          (n.cluster && n.instance && sampledTargets.has(`${n.cluster}/${n.instance}`))),
    ).length
  }, [archNodes, resources])
  const onlineAgentCount = s?.online_agents ?? targets.filter((t) => t.status !== "offline").length
  const anomalyNodes = archNodes.filter((n) => n.status !== "ok")
  const hasCritical = anomalyNodes.some((n) => n.status === "critical") || ai.findings.some((f) => f.priority === "P0")
  const hasWarning = anomalyNodes.length > 0 || (s?.open_threshold_alerts ?? 0) > 0 || ai.findings.length > 0
  const globalTone: "danger" | "warning" | "success" = hasCritical ? "danger" : hasWarning ? "warning" : "success"
  const globalVerdict = hasCritical ? "存在严重异常" : hasWarning ? "存在运行告警" : "整体运行平稳"

  const toggleReportSection = (key: ReportSectionKey) => {
    setReportSections((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]))
  }

  function handleGenerateReport() {
    const md = buildCustomReportMarkdown(
      { title: reportTitle.trim() || "综合态势与监控报告", period: reportPeriod, sections: reportSections },
      overview,
      latestDetail,
      ai,
      {
        systemsCount: systems.filter((x) => x.active !== false).length,
        clustersCount: clusters.length,
        machinesCount: machines.length,
      },
    )
    setGeneratedMarkdown(md)
  }

  return (
    <section aria-label="态势感知" className="space-y-4">
      <PageHeader
        title="态势感知"
        badge={
          <>
            <Badge variant="primary">业务 → 集群 → 机器整体视角</Badge>
            {inspectRounds > 0 && <Badge variant="primary">本会话已采集 {inspectRounds} 轮</Badge>}
          </>
        }
        description="自上而下打通业务系统、应用与数据库、Kubernetes 集群到物理机/GPU/虚机/SSH 机器资源的全局立体架构与实时运行态势。"
        actions={
          <>
            <Button size="sm" variant="primary" disabled={inspecting} onClick={() => void triggerGlobalInspection()}>
              <RocketIcon width={14} height={14} />
              {inspecting ? "正在采集巡检…" : "立即发起全网巡检"}
            </Button>
            <Button size="sm" onClick={() => setReportOpen((v) => !v)}>
              {reportOpen ? "收起安全报表" : "多维度安全报表"}
            </Button>
            {onOpenResourceMonitor && (
              <Button size="sm" variant="ghost" onClick={() => onOpenResourceMonitor()}>
                资源监控 ({resources.length})
              </Button>
            )}
            {onOpenInspection && (
              <Button size="sm" variant="ghost" onClick={() => onOpenInspection()}>
                前往智能巡检
              </Button>
            )}
            {onOpenPlatformMonitor && (
              <Button size="sm" variant="ghost" onClick={onOpenPlatformMonitor}>
                Agent 运行监控
              </Button>
            )}
            <Button size="sm" disabled={loading} onClick={reload}>
              <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} />
              刷新
            </Button>
          </>
        }
      />

      <PageTabs
        ariaLabel="态势感知视图"
        value={subTab}
        onChange={(next) => setSubTab(next as SituationTab)}
        items={[
          { id: "dashboard", label: "全局态势大屏" },
          { id: "topology", label: "业务→集群→机器拓扑" },
        ]}
      />

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

      {subTab === "topology" ? (
        <AssetTopologyPanel clusterId={clusterFilter} />
      ) : (
        <Stack gap={4}>
          {/* Multi-Dimensional Custom Report Drawer */}
          {reportOpen && (
            <Card>
              <CardContent className="space-y-3 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <h2 className="text-sm font-semibold text-foreground">多维度态势与安全自定义报表</h2>
                    <p className="text-xs text-muted-foreground">
                      基于当前真实采集数据与最近巡检记录自由勾选维度，一键生成并导出可打印报告。
                    </p>
                  </div>
                  <Inline gap={2} className="flex-wrap">
                    <Button size="sm" variant="primary" onClick={handleGenerateReport} disabled={reportSections.length === 0}>
                      <SparkIcon width={14} height={14} /> 生成报表
                    </Button>
                    {generatedMarkdown && (
                      <>
                        <Button
                          size="sm"
                          onClick={() => downloadFile("doops-situational-report.html", buildCustomReportHtml(reportTitle, generatedMarkdown), "text/html;charset=utf-8")}
                        >
                          导出 HTML (可打印 PDF)
                        </Button>
                        <Button
                          size="sm"
                          onClick={() => downloadFile("doops-situational-report.md", generatedMarkdown, "text/markdown;charset=utf-8")}
                        >
                          导出 Markdown
                        </Button>
                        {latestDetail && (
                          <Button
                            size="sm"
                            onClick={() => downloadFile(`inspection-${latestDetail.run.id}.csv`, buildRunReportCSV(latestDetail), "text/csv;charset=utf-8")}
                          >
                            导出巡检 CSV
                          </Button>
                        )}
                      </>
                    )}
                  </Inline>
                </div>

                <div className="grid gap-3 md:grid-cols-3">
                  <label className="space-y-1 text-xs md:col-span-2">
                    <span className="text-muted-foreground">报表标题</span>
                    <Input scale="sm" value={reportTitle} onChange={(e) => setReportTitle(e.target.value)} />
                  </label>
                  <label className="space-y-1 text-xs">
                    <span className="text-muted-foreground">统计周期</span>
                    <Select scale="sm" value={reportPeriod} onChange={(e) => setReportPeriod(e.target.value as "24h" | "7d" | "30d")}>
                      <option value="24h">近 24 小时日报</option>
                      <option value="7d">近 7 天周报</option>
                      <option value="30d">近 30 天月度总览</option>
                    </Select>
                  </label>
                </div>

                <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                  {REPORT_SECTIONS.map((sec) => (
                    <label key={sec.key} className="flex cursor-pointer items-center gap-2 rounded-md border bg-background/60 px-2.5 py-2 text-xs">
                      <Checkbox checked={reportSections.includes(sec.key)} onChange={() => toggleReportSection(sec.key)} />
                      <span>{sec.label}</span>
                    </label>
                  ))}
                </div>

                {generatedMarkdown && (
                  <pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/30 p-3 font-mono text-xs leading-relaxed text-foreground">
                    {generatedMarkdown}
                  </pre>
                )}
              </CardContent>
            </Card>
          )}

          {/* Zone 1: Executive Status Verdict + 4-Tier Architecture Pipeline + Interactive 3D Core */}
          <Card>
            <CardContent className="p-4">
              <div className="grid items-stretch gap-4 lg:grid-cols-12">
                <div className="flex flex-col justify-between gap-3 lg:col-span-8">
                  {/* Top Verdict & Real-Time Health Bar */}
                  <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-background/70 px-3.5 py-2.5">
                    <div className="flex flex-wrap items-center gap-2.5">
                      <Badge variant={globalTone}>{globalVerdict}</Badge>
                      <div className="font-mono text-sm font-semibold text-foreground tabular-nums">
                        {latestRun ? (
                          <>
                            健康评分{" "}
                            <span className={ai.score >= 85 ? "text-success" : ai.score >= 70 ? "text-warning" : "text-destructive"}>
                              {ai.score}
                            </span>
                            <span className="text-xs font-normal text-muted-foreground"> / 100</span>
                          </>
                        ) : (
                          <span className="text-muted-foreground">待首轮巡检评分</span>
                        )}
                      </div>
                      <span className="font-mono text-2xs text-muted-foreground">
                        · 活跃告警 {s?.open_threshold_alerts ?? 0} · 异常节点 {anomalyNodes.length}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-2 font-mono text-2xs text-muted-foreground">
                      {totalNetBps > 0 && <span>流量 {formatRateBytes(totalNetBps)}</span>}
                      {totalIops > 0 && <span>· {totalIops.toFixed(0)} IOPS</span>}
                      {gpuRes?.gpu_percent != null && <span className="text-primary">· GPU {gpuRes.gpu_percent.toFixed(0)}%</span>}
                      <span>· 同步 {fmtClock(s?.last_collected_at_ms)}</span>
                    </div>
                  </div>

                  {/* 4-Tier Business -> App/DB -> Cluster -> Machine Truthful Pipeline Counters */}
                  <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
                    <TiltCard3D glowTone="primary" className="rounded-lg border border-accent/25 bg-background/70 p-3">
                      <button
                        type="button"
                        onClick={() => onOpenArea?.("systems")}
                        className="w-full text-left"
                      >
                        <div className="text-2xs text-accent/90">L4 · 业务系统</div>
                        <div className="mt-1 font-mono text-xl font-semibold text-foreground tabular-nums">
                          {activeSystemsCount} <span className="text-xs font-normal text-muted-foreground">套</span>
                        </div>
                        <div className="mt-0.5 truncate text-2xs text-muted-foreground">
                          {activeSystemsCount > 0 ? "已登记业务台账" : "点击登记业务 →"}
                        </div>
                      </button>
                    </TiltCard3D>

                    <TiltCard3D glowTone="primary" className="rounded-lg border border-primary/25 bg-background/70 p-3">
                      <button
                        type="button"
                        onClick={() => onOpenResourceMonitor?.()}
                        className="w-full text-left"
                      >
                        <div className="text-2xs text-primary/90">L3 · 应用 / 库 / 容器</div>
                        <div className="mt-1 font-mono text-xl font-semibold text-foreground tabular-nums">
                          {appLayerCount} <span className="text-xs font-normal text-muted-foreground">个</span>
                        </div>
                        <div className="mt-0.5 truncate text-2xs text-muted-foreground">
                          {appRes
                            ? `均耗 ${appRes.response_time_ms?.toFixed(0) ?? "—"}ms`
                            : dbRes
                              ? `库探活 ${dbRes.response_time_ms?.toFixed(0) ?? "—"}ms`
                              : "实采工作负载实例"}
                        </div>
                      </button>
                    </TiltCard3D>

                    <TiltCard3D glowTone="primary" className="rounded-lg border border-primary/30 bg-background/70 p-3">
                      <button
                        type="button"
                        onClick={() => onOpenArea?.("clusters")}
                        className="w-full text-left"
                      >
                        <div className="text-2xs text-primary/90">L2 · Kubernetes 集群</div>
                        <div className="mt-1 font-mono text-xl font-semibold text-foreground tabular-nums">
                          {clusterLayerCount} <span className="text-xs font-normal text-muted-foreground">个</span>
                        </div>
                        <div className="mt-0.5 truncate text-2xs text-muted-foreground">
                          台账 {clusters.length} · 实采 {resources.filter((r) => r.asset_kind === "k8s").length}
                        </div>
                      </button>
                    </TiltCard3D>

                    <TiltCard3D
                      glowTone={sampledMachineCount > 0 ? "success" : "warning"}
                      className="rounded-lg border border-success/25 bg-background/70 p-3"
                    >
                      <button
                        type="button"
                        onClick={() => onOpenArea?.("machines")}
                        className="w-full text-left"
                      >
                        <div className="text-2xs text-success/90">L1 · 机器资源</div>
                        <div className="mt-1 font-mono text-xl font-semibold text-foreground tabular-nums">
                          {machineLayerCount} <span className="text-xs font-normal text-muted-foreground">台</span>
                        </div>
                        <div className="mt-0.5 truncate text-2xs text-muted-foreground">
                          实采 {sampledMachineCount}/{machineLayerCount} · Agent {onlineAgentCount}
                        </div>
                      </button>
                    </TiltCard3D>
                  </div>

                  {/* Actionable Risk Focus Strip (Highlights top issues immediately above the fold) */}
                  {ai.findings.length > 0 ? (
                    <div className="rounded-lg border border-warning/30 bg-warning/5 px-3.5 py-2.5 text-xs">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex flex-wrap items-center gap-2 font-medium text-foreground">
                          <SparkIcon width={14} height={14} className="text-warning" />
                          <span>当前聚焦风险（{ai.findings.length} 项）</span>
                          <span className="font-normal text-muted-foreground">{ai.headline}</span>
                        </div>
                        <Inline gap={2}>
                          {anomalyNodes.length > 0 && (
                            <Button size="sm" variant={onlyAnomalies ? "primary" : "ghost"} onClick={() => setOnlyAnomalies((v) => !v)}>
                              {onlyAnomalies ? "查看全部架构" : "聚焦异常链路"}
                            </Button>
                          )}
                          {latestRun && onOpenInspection && (
                            <Button size="sm" variant="ghost" onClick={() => onOpenInspection(latestRun.id)}>
                              巡检详情 →
                            </Button>
                          )}
                        </Inline>
                      </div>
                      <div className="mt-2 grid gap-1.5 sm:grid-cols-2">
                        {ai.findings.slice(0, 2).map((f, idx) => (
                          <div key={`${f.priority}-${idx}`} className="flex items-start gap-2 rounded-md border border-border/60 bg-background/70 px-2.5 py-1.5">
                            <Badge variant={priorityVariant(f.priority)} className="mt-0.5 shrink-0">
                              {f.priority}
                            </Badge>
                            <div className="min-w-0">
                              <div className="truncate font-medium text-foreground">{f.title}</div>
                              <div className="truncate text-2xs text-muted-foreground">{f.rootCause}</div>
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  ) : (
                    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-success/25 bg-success/5 px-3.5 py-2 text-xs">
                      <span className="text-foreground">
                        <strong className="text-success">运行平稳</strong> ·{" "}
                        {latestRun
                          ? `最近巡检 #${latestRun.id} 未检出未闭环告警，实时采集链路正常。`
                          : "当前暂无活跃阈值告警，可点击右上角「立即发起全网巡检」生成实时基线。"}
                      </span>
                      {s && resources.length > 0 && (
                        <span className="font-mono text-2xs text-muted-foreground">
                          均值 CPU {s.avg_cpu_percent.toFixed(1)}% · 内存 {s.avg_memory_percent.toFixed(1)}% · 盘 {s.avg_disk_percent.toFixed(1)}%
                        </span>
                      )}
                    </div>
                  )}
                </div>

                <div className="lg:col-span-4">
                  <HolographicCore3D
                    nodes={coreNodes}
                    selectedNodeId={selectedNode?.id}
                    onSelectNode={setSelectedNodeId}
                    scanning={inspecting}
                    title="3D 态势核心（点击节点追踪链路）"
                    metrics={[
                      { label: "业务 / 应用", value: `${activeSystemsCount} / ${appLayerCount}`, tone: "accent" },
                      { label: "集群 / 机器", value: `${clusterLayerCount} / ${machineLayerCount}`, tone: "success" },
                      { label: "异常节点", value: String(anomalyNodes.length), tone: anomalyNodes.length > 0 ? "warning" : "success" },
                    ]}
                  />
                </div>
              </div>
            </CardContent>
          </Card>

          {/* Zone 2: Business -> App/DB -> Cluster -> Machine Cross-Layer Impact Architecture */}
          <Card>
            <CardContent className="space-y-3 p-4">
              <LayeredArchitecture3D
                nodes={archNodes}
                links={archLinks}
                selectedNodeId={selectedNode?.id || ""}
                onSelectNode={setSelectedNodeId}
                onlyAnomalies={onlyAnomalies}
                onToggleAnomalies={() => setOnlyAnomalies((v) => !v)}
                scanning={inspecting}
                onOpenResources={() => onOpenResourceMonitor?.()}
                onOpenLayerAction={(layer) => {
                  if (layer === "business") onOpenArea?.("systems")
                  else if (layer === "platform") onOpenArea?.("clusters")
                  else if (layer === "infra") onOpenArea?.("machines")
                  else onOpenResourceMonitor?.()
                }}
              />

              {selectedNode && (
                <div className="space-y-2.5 rounded-lg border bg-background/75 px-3.5 py-3 text-xs">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <Badge variant={selectedNode.status === "critical" ? "danger" : selectedNode.status === "warning" ? "warning" : "success"}>
                        {selectedNode.status === "critical" ? "严重" : selectedNode.status === "warning" ? "告警" : "健康"}
                      </Badge>
                      <strong className="text-sm text-foreground">{selectedNode.title}</strong>
                      <span className="font-mono text-2xs text-muted-foreground">{selectedNode.subtitle}</span>
                      <span className="font-mono text-foreground">· {selectedNode.metricLine}</span>
                    </div>
                    <Inline gap={2} className="flex-wrap">
                      {selectedNode.cluster && selectedNode.instance && (
                        <>
                          {(onOpenResourceMonitor || onOpenTarget) && (
                            <Button
                              size="sm"
                              variant="primary"
                              onClick={() => {
                                if (onOpenResourceMonitor) {
                                  onOpenResourceMonitor(`${selectedNode.cluster}/${selectedNode.instance}`)
                                } else {
                                  onOpenTarget?.(selectedNode.cluster!, selectedNode.instance!, "monitor")
                                }
                              }}
                            >
                              资源与告警曲线
                            </Button>
                          )}
                          {onOpenTarget && (
                            <Button size="sm" onClick={() => onOpenTarget(selectedNode.cluster!, selectedNode.instance!, "ask")}>
                              AI 助手诊断
                            </Button>
                          )}
                        </>
                      )}
                      <Button size="sm" variant="ghost" onClick={() => setSubTab("topology")}>
                        多跳拓扑详情
                      </Button>
                      {latestRun && onOpenInspection && (
                        <Button size="sm" variant="ghost" onClick={() => onOpenInspection(latestRun.id)}>
                          最新巡检报告
                        </Button>
                      )}
                    </Inline>
                  </div>

                  <p className="text-muted-foreground">{selectedNode.impactNote}</p>

                  {(selectedChain.upstream.length > 0 || selectedChain.downstream.length > 0) && (
                    <div className="flex flex-wrap gap-4 border-t border-border/50 pt-2">
                      {selectedChain.upstream.length > 0 && (
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="font-medium text-warning">上游受影响 ({selectedChain.upstream.length})：</span>
                          {selectedChain.upstream.map((u) => (
                            <Button
                              key={u.id}
                              type="button"
                              size="sm"
                              variant="outline"
                              onClick={() => setSelectedNodeId(u.id)}
                              className="h-6 border-warning/40 bg-warning/10 px-2 text-2xs hover:border-warning hover:bg-warning/15"
                            >
                              {u.title}
                            </Button>
                          ))}
                        </div>
                      )}
                      {selectedChain.downstream.length > 0 && (
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="font-medium text-accent">下游承载 ({selectedChain.downstream.length})：</span>
                          {selectedChain.downstream.map((d) => (
                            <Button
                              key={d.id}
                              type="button"
                              size="sm"
                              variant="outline"
                              onClick={() => setSelectedNodeId(d.id)}
                              className="h-6 border-accent/40 bg-accent/10 px-2 text-2xs hover:border-accent hover:bg-accent/15"
                            >
                              {d.title}
                            </Button>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}
            </CardContent>
          </Card>

          {/* Zone 3: 3D Resource Visualization + 4-Domain Coverage (Left) | Risk Root-Cause & Action List (Right) */}
          <div className="grid gap-4 lg:grid-cols-12">
            <Card className="lg:col-span-7">
              <CardContent className="space-y-3 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b pb-2">
                  <span className="text-xs font-semibold text-foreground">机器与集群资源水位</span>
                  <Inline gap={1}>
                    <Button size="sm" variant={chartMode === "3d" ? "primary" : "ghost"} onClick={() => setChartMode("3d")}>
                      3D 立体柱阵
                    </Button>
                    <Button size="sm" variant={chartMode === "trend" ? "primary" : "ghost"} onClick={() => setChartMode("trend")}>
                      实时演进曲线
                    </Button>
                  </Inline>
                </div>

                {chartMode === "3d" ? (
                  <ResourceBars3D
                    items={bar3DItems}
                    title="核心资产 3D 立体资源柱阵"
                  />
                ) : overview ? (
                  <SituationalTrendChart overview={overview} />
                ) : (
                  <p className="py-12 text-center text-sm text-muted-foreground">正在加载资源趋势…</p>
                )}

                <div className="grid grid-cols-2 gap-2 border-t pt-3 text-xs sm:grid-cols-4">
                  <div>
                    <div className="text-2xs text-muted-foreground">总 CPU 核数 / 已用</div>
                    <div className="font-mono font-medium text-foreground">
                      {s && s.total_cpu_cores > 0 ? `${s.used_cpu_cores} / ${s.total_cpu_cores} 核` : "—"}
                    </div>
                  </div>
                  <div>
                    <div className="text-2xs text-muted-foreground">总内存 / 已用</div>
                    <div className="font-mono font-medium text-foreground">
                      {s && s.total_memory_bytes > 0
                        ? `${formatBytesCompact(s.used_memory_bytes)} / ${formatBytesCompact(s.total_memory_bytes)}`
                        : "—"}
                    </div>
                  </div>
                  <div>
                    <div className="text-2xs text-muted-foreground">存储容量 / 已用</div>
                    <div className="font-mono font-medium text-foreground">
                      {s && s.total_disk_bytes > 0
                        ? `${formatBytesCompact(s.used_disk_bytes)} / ${formatBytesCompact(s.total_disk_bytes)}`
                        : "—"}
                    </div>
                  </div>
                  <div>
                    <div className="text-2xs text-muted-foreground">GPU 算力观测</div>
                    <div className="font-mono font-medium text-primary">
                      {gpuRes?.gpu_percent != null ? `${gpuRes.gpu_percent.toFixed(1)}%` : "未检测到 GPU"}
                    </div>
                  </div>
                </div>

                {/* Compact 4-Domain Telemetry Readiness Strip */}
                <div className="grid gap-2 border-t pt-3 text-xs sm:grid-cols-2">
                  <div className="flex items-center justify-between gap-2 rounded-md border bg-background/50 px-2.5 py-2">
                    <div className="min-w-0">
                      <div className="font-medium text-foreground">系统级监控</div>
                      <div className="truncate font-mono text-2xs text-muted-foreground">
                        {s && resources.length > 0
                          ? `CPU ${s.avg_cpu_percent.toFixed(1)}% · 内存 ${s.avg_memory_percent.toFixed(1)}% · 盘 ${s.avg_disk_percent.toFixed(1)}%`
                          : "等待机器采样快照"}
                      </div>
                    </div>
                    <Badge variant={resources.length > 0 ? "success" : "outline"}>{resources.length > 0 ? "已采集" : "待采集"}</Badge>
                  </div>
                  <div className="flex items-center justify-between gap-2 rounded-md border bg-background/50 px-2.5 py-2">
                    <div className="min-w-0">
                      <div className="font-medium text-foreground">应用级拨测</div>
                      <div className="truncate font-mono text-2xs text-muted-foreground">
                        {appRes
                          ? `${appRes.response_time_ms?.toFixed(1) ?? "—"}ms · ${appRes.requests_per_sec?.toFixed(0) ?? "—"} req/s · 错 ${appRes.error_rate ?? 0}%`
                          : "在「资产 → 网站」登记后自动拨测"}
                      </div>
                    </div>
                    <Badge variant={appRes ? "success" : "outline"}>{appRes ? "已采集" : "待登记"}</Badge>
                  </div>
                  <div className="flex items-center justify-between gap-2 rounded-md border bg-background/50 px-2.5 py-2">
                    <div className="min-w-0">
                      <div className="font-medium text-foreground">数据库级观测</div>
                      <div className="truncate font-mono text-2xs text-muted-foreground">
                        {dbStats
                          ? `连接 ${dbStats.connections}/${dbStats.max_connections} · ${dbStats.tps} TPS · 锁 ${dbStats.lock_waits}`
                          : dbRes
                            ? `TCP ${dbRes.response_time_ms?.toFixed(1) ?? "—"}ms（探活）`
                            : "在「资产 → 数据库」登记后探活"}
                      </div>
                    </div>
                    <Badge variant={dbStats ? "success" : dbRes ? "primary" : "outline"}>
                      {dbStats ? "全维" : dbRes ? "探活" : "待登记"}
                    </Badge>
                  </div>
                  <div className="flex items-center justify-between gap-2 rounded-md border bg-background/50 px-2.5 py-2">
                    <div className="min-w-0">
                      <div className="font-medium text-foreground">安全基线监测</div>
                      <div className="truncate font-mono text-2xs text-muted-foreground">
                        {secStats
                          ? `拦截登录 ${secStats.failed_logins_blocked} · 权限变更 ${secStats.privilege_changes}`
                          : "Agent M5 登录与账户基线巡检"}
                      </div>
                    </div>
                    <Badge variant={secStats ? "warning" : "outline"}>{secStats ? "监测中" : "M5 基线"}</Badge>
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card className="lg:col-span-5">
              <CardContent className="flex h-full flex-col justify-between gap-3 p-4">
                <div className="space-y-3">
                  <div className="flex flex-wrap items-start justify-between gap-2 border-b pb-2">
                    <div className="flex items-center gap-1.5">
                      <SparkIcon width={15} height={15} className="text-primary" />
                      <h2 className="text-sm font-semibold text-foreground">
                        实时异常与处置建议
                      </h2>
                    </div>
                    {onOpenTarget && targets[0] && (
                      <Button size="sm" variant="ghost" onClick={() => onOpenTarget(targets[0].cluster, targets[0].instance, "ask")}>
                        AI 助手诊断
                      </Button>
                    )}
                  </div>

                  {ai.findings.length > 0 ? (
                    <div className="max-h-96 space-y-2 overflow-y-auto pr-1">
                      {ai.findings.slice(0, 6).map((f, idx) => (
                        <div key={`${f.priority}-${idx}`} className="space-y-1.5 rounded-lg border bg-background/60 p-3 text-xs">
                          <div className="flex flex-wrap items-center justify-between gap-1.5">
                            <Badge variant={priorityVariant(f.priority)}>
                              {f.priority} · {f.riskLevel}
                            </Badge>
                            <span className="truncate text-2xs text-muted-foreground">影响：{f.businessImpact}</span>
                          </div>
                          <div className="font-semibold text-foreground">{f.title}</div>
                          <div className="text-2xs leading-relaxed text-muted-foreground">根因：{f.rootCause}</div>
                          <div className="rounded bg-muted/50 px-2 py-1 font-mono text-2xs text-foreground">
                            建议：{f.remediation}
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="rounded-lg border border-dashed bg-background/40 p-4 text-xs text-muted-foreground">
                      {latestRun
                        ? "最近一轮巡检未检出异常风险项，当前架构各层运行平稳。"
                        : "尚无巡检执行记录，点击顶部「立即发起全网巡检」或前往「智能巡检」启动周期任务。"}
                    </p>
                  )}
                </div>

                {/* Single-line link to Standalone Inspection workspace (no duplicated run history table) */}
                <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-2.5 text-xs">
                  <div className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
                    <ActivityIcon width={14} height={14} className="shrink-0 text-primary" />
                    {latestRun ? (
                      <span className="truncate">
                        最新巡检 <span className="font-mono text-foreground">#{latestRun.id}</span> ·{" "}
                        {fmtDateTime(latestRun.started_at_ms)}
                      </span>
                    ) : (
                      <span>巡检记录由「智能巡检」统一归档</span>
                    )}
                  </div>
                  {onOpenInspection && (
                    <Button size="sm" variant="primary" onClick={() => onOpenInspection(latestRun?.id)}>
                      前往智能巡检 →
                    </Button>
                  )}
                </div>
              </CardContent>
            </Card>
          </div>
        </Stack>
      )}
    </section>
  )
}
