"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { Session, Target } from "@/lib/client"
import { assetRequest } from "@/lib/assets"
import type { TargetTab } from "@/lib/console-views"
import {
  buildRunReportCSV,
  describeMonitorStatus,
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
import {
  getInspectionMcpServices,
  mapMcpServicesToStrategies,
  recordInspectionMcpRun,
} from "@/lib/inspection-mcp-store"
import { ActivityIcon, RefreshIcon, RocketIcon, SparkIcon } from "./icons"
import { AssetTopologyPanel } from "./asset-topology-panel"
import { SystemResourceMonitor } from "./system-resource-monitor"
import {
  HolographicCore3D,
  LayeredArchitecture3D,
  ResourceBars3D,
  TiltCard3D,
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
  PageTabs,
  Select,
  Stack,
  StatusBanner,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui"

type SituationTab = "dashboard" | "topology" | "resources"

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

interface CoverageMetricRow {
  total: number
  covered: number
  percent: number | null
  passed: boolean | null
}

interface CoverageSnapshot {
  state?: string
  baseline?: { id: string } | null
  metrics?: Record<string, CoverageMetricRow | undefined>
}

const REPORT_SECTIONS: { key: ReportSectionKey; label: string }[] = [
  { key: "topology_coverage", label: "业务 → 集群 → 机器整体架构与覆盖率" },
  { key: "system_gpu", label: "机器与集群立体化资源监控（含 GPU）" },
  { key: "app_database", label: "应用级与数据库级运行观测" },
  { key: "inspection_vuln", label: "综合巡检执行与 MCP 服务状态" },
  { key: "security_events", label: "安全态势与告警记录" },
  { key: "ai_remediation", label: "多源关联分析与优先处置建议" },
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
        {diskPath && <path d={diskPath} fill="none" stroke="var(--color-success, #16a34a)" strokeWidth="1.8" strokeDasharray="4 3" />}
        {memPath && <path d={memPath} fill="none" stroke="var(--color-warning, #d97706)" strokeWidth="2.2" />}
        {cpuPath && <path d={cpuPath} fill="none" stroke="var(--color-primary, #2563eb)" strokeWidth="2.4" />}
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
  onOpenPlatformMonitor,
  onOpenArea,
}: {
  session: Session
  targets: Target[]
  clusterFilter?: string
  onOpenTarget?: (cluster: string, instance: string, tab: TargetTab) => void
  onOpenInspection?: (runId?: string) => void
  onOpenPlatformMonitor?: () => void
  onOpenArea?: (area: "systems" | "clusters" | "machines") => void
}) {
  const [subTab, setSubTab] = useState<SituationTab>("dashboard")
  const [overview, setOverview] = useState<MonitorOverviewData | null>(null)
  const [latestDetail, setLatestDetail] = useState<MonitorRunDetailData | null>(null)
  const [systems, setSystems] = useState<BusinessSystemRow[]>([])
  const [clusters, setClusters] = useState<ClusterRow[]>([])
  const [machines, setMachines] = useState<MachineRow[]>([])
  const [coverage, setCoverage] = useState<CoverageSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [inspecting, setInspecting] = useState(false)
  const [inspectRounds, setInspectRounds] = useState(0)
  const [notice, setNotice] = useState("")
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [selectedNodeId, setSelectedNodeId] = useState<string>("")
  const [chartMode, setChartMode] = useState<"3d" | "trend">("3d")

  // Custom security report drawer
  const [reportOpen, setReportOpen] = useState(false)
  const [reportTitle, setReportTitle] = useState(
    session.demo ? "浙江音乐学院智能综合感知运营与安全巡检综合报告" : "DoOps 业务→集群→机器综合态势与监控报告",
  )
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
      assetRequest<CoverageSnapshot>("coverage").catch(() => null),
    ])
      .then(async ([ov, sysRes, cluRes, machRes, covRes]) => {
        if (ctrl.signal.aborted) return
        setOverview(ov)
        setSystems(Array.isArray(sysRes?.systems) ? sysRes.systems : [])
        setClusters(Array.isArray(cluRes?.clusters) ? cluRes.clusters : [])
        setMachines(Array.isArray(machRes?.machines) ? machRes.machines : [])
        setCoverage(covRes)
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
      const enabledMcps = getInspectionMcpServices().filter((s) => s.enabled)
      const strategies = session.demo
        ? mapMcpServicesToStrategies(enabledMcps)
        : ["vm_host", "ssh_host", "k8s_cluster", "container_runtime", "app_service", "database_service"]
      const res = await triggerMonitorRun(session, {
        scope_mode: "all_online",
        strategies,
        ...(session.demo
          ? {
              mcp_services: enabledMcps.map((m) => m.id),
              mcp_title: `全网态势联动巡检（已启用 ${enabledMcps.length} 个 MCP 服务）`,
            }
          : {}),
      })
      if (session.demo && enabledMcps.length > 0) {
        const statusMap: Record<string, "pass" | "warning" | "critical"> = {}
        for (const m of enabledMcps) {
          statusMap[m.id] = res.run.failed_checks > 0 ? "critical" : res.run.warning_checks > 0 ? "warning" : "pass"
        }
        recordInspectionMcpRun(
          enabledMcps.map((m) => m.id),
          statusMap,
        )
      }
      const nextRound = inspectRounds + 1
      setInspectRounds(nextRound)
      setNotice(`第 ${nextRound} 轮全网采集巡检完成（#${res.run.id} · 耗时 ${(res.run.duration_ms / 1000).toFixed(1)}s）：${res.run.summary}`)
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

    // L1: Machines / GPU / VM / SSH Resources (from /v1/assets/machines + observed vm/physical snapshots)
    const seenMachines = new Set<string>()
    for (const m of machines.slice(0, 8)) {
      seenMachines.add(m.asset_id)
      const snap =
        resByAsset.get(m.asset_id) ||
        resources.find(
          (r) =>
            (r.asset_kind === "vm" || r.asset_kind === "physical") &&
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
            ? `已绑定目标 ${m.cluster}/${m.instance}（${boundOnline ? "在线" : "离线"}），可点击发起巡检或进入 Agent 监控采集。`
            : "尚未绑定 Agent 目标，可在「机器台账」中完成绑定。",
      })
    }
    // Also surface real observed host snapshots when machine ledger has no entry for that host yet (e.g. dev-agent)
    for (const r of resources.filter((item) => item.asset_kind === "vm" || item.asset_kind === "physical")) {
      if (seenMachines.has(r.asset_id) || out.filter((n) => n.layer === "infra").length >= 8) continue
      seenMachines.add(r.asset_id)
      out.push({
        id: `mach:${r.asset_id}`,
        layer: "infra",
        title: r.asset_name || r.asset_id,
        subtitle: `${r.cluster}/${r.instance} · ${r.collector_method}`,
        metricLine: `CPU ${r.cpu_percent != null ? `${r.cpu_percent.toFixed(1)}%` : "—"} · 内存 ${r.memory_percent != null ? `${r.memory_percent.toFixed(1)}%` : "—"} · 盘 ${r.disk_percent != null ? `${r.disk_percent.toFixed(1)}%` : "—"}${r.gpu_percent != null ? ` · GPU ${r.gpu_percent.toFixed(0)}%` : ""}`,
        status: toNodeStatus(r.status),
        cluster: r.cluster,
        instance: r.instance,
        assetId: r.asset_id,
        impactNote: `由在线目标 ${r.cluster}/${r.instance} 实时采集（${r.os_info || r.asset_kind}），最近同步 ${fmtClock(r.collected_at_ms)}。`,
      })
    }

    return out
  }, [systems, clusters, machines, resources, onlineTargetSet])

  const selectedNode = archNodes.find((n) => n.id === selectedNodeId) || archNodes[0]

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

  const serverCov = coverage?.metrics?.servers || coverage?.metrics?.machines
  const systemCov = coverage?.metrics?.systems || coverage?.metrics?.business_systems
  const serverCovText =
    serverCov && serverCov.percent != null ? `${serverCov.percent.toFixed(0)}% (${serverCov.covered}/${serverCov.total})` : ""
  const systemCovText =
    systemCov && systemCov.percent != null ? `${systemCov.percent.toFixed(0)}% (${systemCov.covered}/${systemCov.total})` : ""

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
        serverCoverageText: serverCovText || undefined,
        systemCoverageText: systemCovText || undefined,
        isDemo: session.demo,
      },
    )
    setGeneratedMarkdown(md)
  }

  return (
    <section aria-label="态势感知" className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">态势感知</h1>
            <Badge variant="primary">业务 → 集群 → 机器整体视角</Badge>
            <Badge variant={session.demo ? "warning" : "success"}>
              {session.demo ? "演示数据模式" : "实时采集模式"}
            </Badge>
            {inspectRounds > 0 && <Badge variant="primary">本会话已巡检 {inspectRounds} 轮</Badge>}
          </div>
          <p className="text-sm text-muted-foreground">
            自上而下打通业务系统、应用与数据库、Kubernetes 集群到物理机/GPU/虚机/SSH 机器资源的全局立体架构与实时运行态势。
          </p>
        </div>

        <Inline gap={2} className="flex-wrap">
          <Button size="sm" variant="primary" disabled={inspecting} onClick={() => void triggerGlobalInspection()}>
            <RocketIcon width={14} height={14} />
            {inspecting ? "正在采集巡检…" : "立即发起全网巡检"}
          </Button>
          <Button size="sm" onClick={() => setReportOpen((v) => !v)}>
            {reportOpen ? "收起安全报表" : "多维度安全报表"}
          </Button>
          {onOpenInspection && (
            <Button size="sm" variant="ghost" onClick={() => onOpenInspection()}>
              前往智能巡检
            </Button>
          )}
          {onOpenPlatformMonitor && (
            <Button size="sm" variant="ghost" onClick={onOpenPlatformMonitor}>
              平台监控 (Agent)
            </Button>
          )}
          <Button size="sm" disabled={loading} onClick={reload}>
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
        </Inline>
      </header>

      <PageTabs
        ariaLabel="态势感知视图"
        value={subTab}
        onChange={(next) => setSubTab(next as SituationTab)}
        items={[
          { id: "dashboard", label: "全局态势大屏" },
          { id: "topology", label: "业务→集群→机器拓扑" },
          { id: "resources", label: `机器与集群资源 (${resources.length})` },
        ]}
      />

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

      {subTab === "topology" ? (
        <AssetTopologyPanel clusterId={clusterFilter} />
      ) : subTab === "resources" ? (
        <SystemResourceMonitor
          session={session}
          targets={targets}
          clusterFilter={clusterFilter}
          onOpenTarget={onOpenTarget}
          onOpenRunDetail={onOpenInspection ? (runId) => onOpenInspection(runId) : undefined}
        />
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

          {/* Top Hero: Business -> Cluster -> Machine 3-Tier KPIs + 3D Holographic Core */}
          <Card>
            <CardContent className="p-4">
              <div className="grid items-stretch gap-4 lg:grid-cols-12">
                <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3 lg:col-span-8">
                  <TiltCard3D glowTone="primary" className="rounded-lg border border-primary/25 bg-background/70 p-3">
                    <div className="text-2xs text-muted-foreground">① 业务系统层</div>
                    <div className="mt-1 font-mono text-xl font-semibold text-primary tabular-nums">
                      {systems.filter((x) => x.active !== false).length} <span className="text-xs font-normal text-muted-foreground">套在役</span>
                    </div>
                    <div className="mt-0.5 text-2xs text-muted-foreground">
                      {systemCovText ? `存量覆盖 ${systemCovText}` : "纳管覆盖基线待冻结"}
                    </div>
                  </TiltCard3D>

                  <TiltCard3D glowTone="primary" className="rounded-lg border border-indigo-400/25 bg-background/70 p-3">
                    <div className="text-2xs text-muted-foreground">② 集群与编排层</div>
                    <div className="mt-1 font-mono text-xl font-semibold text-foreground tabular-nums">
                      {clusters.length} <span className="text-xs font-normal text-muted-foreground">个集群</span>
                    </div>
                    <div className="mt-0.5 text-2xs text-muted-foreground">
                      K8s 观测 {resources.filter((r) => r.asset_kind === "k8s").length} · 容器节点 {resources.filter((r) => r.asset_kind === "container").length}
                    </div>
                  </TiltCard3D>

                  <TiltCard3D glowTone="success" className="rounded-lg border border-success/25 bg-background/70 p-3">
                    <div className="text-2xs text-muted-foreground">③ 机器资源与探针覆盖</div>
                    <div className="mt-1 font-mono text-xl font-semibold text-success tabular-nums">
                      {s ? `${s.probe_coverage_pct.toFixed(0)}%` : "—"}
                    </div>
                    <div className="mt-0.5 text-2xs text-muted-foreground">
                      机器 {machines.length} 台 · 在线 Agent {s?.online_agents ?? targets.filter((t) => t.status !== "offline").length}
                      {serverCovText ? ` · 存量 ${serverCovText}` : ""}
                    </div>
                  </TiltCard3D>

                  <TiltCard3D glowTone="primary" className="rounded-lg border bg-background/70 p-3 sm:col-span-2">
                    <div className="text-2xs text-muted-foreground">全网实时流量、I/O 与活跃告警</div>
                    <div className="mt-1 flex flex-wrap items-baseline justify-between gap-2">
                      <span className="font-mono text-xl font-semibold text-foreground tabular-nums">
                        {totalNetBps > 0 ? formatRateBytes(totalNetBps) : "暂无网卡速率样本"}
                      </span>
                      <span className="font-mono text-xs text-muted-foreground">
                        告警 {s?.open_threshold_alerts ?? 0} 条 · 同步 {fmtClock(s?.last_collected_at_ms)}
                        {totalIops > 0 ? ` · ${totalIops.toFixed(0)} IOPS` : ""}
                        {gpuRes?.gpu_percent != null ? ` · GPU ${gpuRes.gpu_percent.toFixed(0)}%` : ""}
                      </span>
                    </div>
                  </TiltCard3D>

                  <TiltCard3D
                    glowTone={!latestRun ? "primary" : ai.score >= 85 ? "success" : ai.score >= 70 ? "warning" : "danger"}
                    className="rounded-lg border bg-background/70 p-3"
                  >
                    <div className="text-2xs text-muted-foreground">
                      {session.demo ? "浙音大模型态势评分" : "多源巡检健康评分"}
                    </div>
                    <div className="mt-1 font-mono text-xl font-semibold tabular-nums">
                      {latestRun ? (
                        <>
                          <span className={ai.score >= 85 ? "text-success" : ai.score >= 70 ? "text-warning" : "text-destructive"}>
                            {ai.score}
                          </span>
                          <span className="text-xs font-normal text-muted-foreground"> / 100</span>
                        </>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </div>
                    <div className="mt-0.5 text-2xs text-muted-foreground">
                      累计巡检 {s?.total_runs ?? 0} 轮 · 周期 {s?.active_schedules ?? 0} 启用
                    </div>
                  </TiltCard3D>
                </div>

                <div className="lg:col-span-4">
                  <HolographicCore3D
                    nodes={coreNodes}
                    scanning={inspecting}
                    title="3D 业务→集群→机器态势核心"
                    metrics={[
                      { label: "业务系统", value: String(systems.filter((x) => x.active !== false).length), tone: "accent" },
                      { label: "集群 / 机器", value: `${clusters.length} / ${machines.length}`, tone: "success" },
                      { label: "活跃告警", value: String(s?.open_threshold_alerts ?? 0), tone: (s?.open_threshold_alerts ?? 0) > 0 ? "warning" : "success" },
                    ]}
                  />
                </div>
              </div>
            </CardContent>
          </Card>

          {/* 3D Floating 4-Layer Business -> App/DB -> Cluster -> Machine Architecture */}
          <Card>
            <CardContent className="space-y-3 p-4">
              <LayeredArchitecture3D
                nodes={archNodes}
                selectedNodeId={selectedNode?.id || ""}
                onSelectNode={setSelectedNodeId}
                scanning={inspecting}
                onOpenResources={() => setSubTab("resources")}
                onOpenLayerAction={(layer) => {
                  if (layer === "business") onOpenArea?.("systems")
                  else if (layer === "platform") onOpenArea?.("clusters")
                  else if (layer === "infra") onOpenArea?.("machines")
                  else setSubTab("resources")
                }}
              />

              {selectedNode && (
                <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-background/70 px-3.5 py-2.5 text-xs">
                  <div className="min-w-0 space-y-0.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant={selectedNode.status === "critical" ? "danger" : selectedNode.status === "warning" ? "warning" : "success"}>
                        {selectedNode.status === "critical" ? "严重" : selectedNode.status === "warning" ? "告警" : "健康"}
                      </Badge>
                      <strong className="text-foreground">{selectedNode.title}</strong>
                      <span className="font-mono text-muted-foreground">{selectedNode.metricLine}</span>
                    </div>
                    <p className="text-muted-foreground">关联链路与观测详情：{selectedNode.impactNote}</p>
                  </div>
                  <Inline gap={2} className="flex-wrap">
                    {onOpenTarget && selectedNode.cluster && selectedNode.instance && (
                      <Button size="sm" onClick={() => onOpenTarget(selectedNode.cluster!, selectedNode.instance!, "monitor")}>
                        查看 Agent 监控曲线
                      </Button>
                    )}
                    <Button size="sm" variant="ghost" onClick={() => setSubTab("topology")}>
                      查看多跳依赖拓扑
                    </Button>
                    {latestRun && onOpenInspection && (
                      <Button size="sm" variant="ghost" onClick={() => onOpenInspection(latestRun.id)}>
                        查看巡检报告
                      </Button>
                    )}
                  </Inline>
                </div>
              )}
            </CardContent>
          </Card>

          {/* 3D Isometric Resource Prism Array / Trend + Real 4-Domain Telemetry Matrix */}
          <div className="grid gap-4 lg:grid-cols-5">
            <Card className="lg:col-span-3">
              <CardContent className="space-y-3 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b pb-2">
                  <span className="text-xs font-medium text-muted-foreground">机器与集群资源可视化</span>
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
                  <ResourceBars3D items={bar3DItems} />
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
              </CardContent>
            </Card>

            <Card className="lg:col-span-2">
              <CardContent className="space-y-2.5 p-4">
                <div className="flex items-center justify-between">
                  <h2 className="text-sm font-semibold text-foreground">四维运行监控观测态</h2>
                  <Badge variant="outline">{session.demo ? "演示全量" : "仅展示实采"}</Badge>
                </div>
                <div className="grid gap-2 text-xs">
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="flex items-center justify-between font-medium text-foreground">
                      <span>系统级（CPU / 内存 / GPU / 磁盘 / 网络）</span>
                      <Badge variant={resources.length > 0 ? "success" : "outline"}>
                        {resources.length > 0 ? "已采集" : "待采集"}
                      </Badge>
                    </div>
                    <div className="mt-1 font-mono text-2xs text-muted-foreground">
                      {s && resources.length > 0
                        ? `CPU ${s.avg_cpu_percent.toFixed(1)}% · 内存 ${s.avg_memory_percent.toFixed(1)}% · 磁盘 ${s.avg_disk_percent.toFixed(1)}%${gpuRes?.gpu_percent != null ? ` · GPU ${gpuRes.gpu_percent.toFixed(1)}%` : ""}`
                        : "尚未收到机器采样快照"}
                    </div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="flex items-center justify-between font-medium text-foreground">
                      <span>应用级（响应时间 / 并发 / 请求速率 / 错误率）</span>
                      <Badge variant={appRes ? "success" : "outline"}>{appRes ? "已采集" : "待登记网站"}</Badge>
                    </div>
                    <div className="mt-1 font-mono text-2xs text-muted-foreground">
                      {appRes
                        ? `均耗 ${appRes.response_time_ms?.toFixed(1) ?? "—"}ms · 并发 ${appRes.active_connections ?? "—"} · ${appRes.requests_per_sec?.toFixed(1) ?? "—"} req/s · 错误 ${appRes.error_rate ?? "—"}%`
                        : "在「资产 → 网站」登记端点或开启 Nginx stub_status 后自动采集"}
                    </div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="flex items-center justify-between font-medium text-foreground">
                      <span>数据库级（连通性 / 连接数 / TPS / 锁等待）</span>
                      <Badge variant={dbStats ? "success" : dbRes ? "primary" : "outline"}>
                        {dbStats ? "全维已采" : dbRes ? "TCP 探活" : "待登记库"}
                      </Badge>
                    </div>
                    <div className="mt-1 font-mono text-2xs text-muted-foreground">
                      {dbStats
                        ? `连接 ${dbStats.connections}/${dbStats.max_connections} · 均耗 ${dbStats.query_ms}ms · ${dbStats.tps} TPS · 锁等待 ${dbStats.lock_waits}`
                        : dbRes
                          ? `${dbRes.asset_name} TCP 建连 ${dbRes.response_time_ms?.toFixed(1) ?? "—"}ms（深度连接/TPS/锁等待需配置只读库采集）`
                          : "在「资产 → 数据库」登记实例后自动探测连通性"}
                    </div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="flex items-center justify-between font-medium text-foreground">
                      <span>安全级（异常登录 / 权限基线 / 审计操作）</span>
                      <Badge variant={secStats ? "warning" : "outline"}>
                        {secStats ? "持续监测" : "主机 M5 基线"}
                      </Badge>
                    </div>
                    <div className="mt-1 font-mono text-2xs text-muted-foreground">
                      {secStats
                        ? `拦截异常登录 ${secStats.failed_logins_blocked} 次 · 权限变更 ${secStats.privilege_changes} 次 · 敏感操作审计 ${secStats.sensitive_ops} 条`
                        : "主机登录日志与账户指纹由 Agent M5 策略巡检；边界安防需对接外部安全 MCP"}
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
          </div>

          {/* Risk & Root-Cause Summary + Link to Standalone Inspection Workspace */}
          <Card>
            <CardContent className="space-y-3 p-4">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <SparkIcon width={15} height={15} className="text-primary" />
                    <h2 className="text-sm font-semibold text-foreground">
                      {session.demo ? "浙音大模型 · 多源风险关联与处置建议" : "多源巡检风险关联与处置建议"}
                    </h2>
                    {latestRun && (
                      <Badge variant={describeMonitorStatus(latestRun.status).variant}>
                        最新巡检 #{latestRun.id}
                      </Badge>
                    )}
                  </div>
                  <p className="text-xs text-muted-foreground">{ai.headline}</p>
                </div>
                <Inline gap={2} className="flex-wrap">
                  {onOpenInspection && (
                    <Button size="sm" variant="primary" onClick={() => onOpenInspection(latestRun?.id)}>
                      前往「智能巡检」查看全量报告与 MCP
                    </Button>
                  )}
                  {onOpenTarget && targets[0] && (
                    <Button size="sm" onClick={() => onOpenTarget(targets[0].cluster, targets[0].instance, "ask")}>
                      进入 AI 助手深度诊断
                    </Button>
                  )}
                </Inline>
              </div>

              {ai.findings.length > 0 ? (
                <div className="overflow-x-auto rounded-md border bg-card">
                  <Table className="min-w-[780px]">
                    <TableHeader>
                      <TableRow>
                        <TableHead>优先级 / 评级</TableHead>
                        <TableHead>风险项与受影响业务</TableHead>
                        <TableHead>关联根因分析</TableHead>
                        <TableHead>处置建议</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {ai.findings.slice(0, 6).map((f, idx) => (
                        <TableRow key={`${f.priority}-${idx}`}>
                          <TableCell className="whitespace-nowrap">
                            <Badge variant={priorityVariant(f.priority)}>
                              {f.priority} · {f.riskLevel}
                            </Badge>
                          </TableCell>
                          <TableCell>
                            <div className="text-xs font-medium text-foreground">{f.title}</div>
                            <div className="mt-0.5 text-2xs text-muted-foreground">影响：{f.businessImpact}</div>
                          </TableCell>
                          <TableCell className="max-w-xs text-xs text-muted-foreground">{f.rootCause}</TableCell>
                          <TableCell className="max-w-xs font-mono text-2xs text-foreground">{f.remediation}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              ) : (
                <p className="rounded-md border bg-background/50 p-3 text-xs text-muted-foreground">
                  {latestRun ? "最近一次巡检未发现异常风险项。" : "尚无巡检执行记录，可点击上方「立即发起全网巡检」或前往「智能巡检」创建巡检计划。"}
                </p>
              )}

              {overview && overview.recent_runs.length > 0 && (
                <div className="border-t pt-3">
                  <div className="mb-2 flex items-center gap-2 text-xs font-medium text-foreground">
                    <ActivityIcon width={14} height={14} className="text-primary" />
                    最近巡检轨迹（最新 {Math.min(4, overview.recent_runs.length)} 次）
                  </div>
                  <div className="divide-y text-xs">
                    {overview.recent_runs.slice(0, 4).map((run) => {
                      const st = describeMonitorStatus(run.status)
                      return (
                        <div key={run.id} className="flex flex-wrap items-center justify-between gap-2 py-1.5">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge variant={st.variant}>{st.label}</Badge>
                            <span className="font-mono text-foreground">#{run.id}</span>
                            <span className="text-foreground">{run.schedule_name || "手动全网巡检"}</span>
                            <span className="text-muted-foreground">{fmtDateTime(run.started_at_ms)}</span>
                          </div>
                          {onOpenInspection && (
                            <Button size="sm" variant="ghost" onClick={() => onOpenInspection(run.id)}>
                              查看报告
                            </Button>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>
              )}
            </CardContent>
          </Card>
        </Stack>
      )}
    </section>
  )
}
