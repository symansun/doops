"use client"

import { Fragment, useCallback, useEffect, useMemo, useState } from "react"
import { resolveFleetInspectionSkills, type MonitorAssetFocus } from "@/lib/agent-monitor"
import type { Session, Target } from "@/lib/client"
import type { TargetTab } from "@/lib/console-views"
import {
  ASSET_KIND_LABELS,
  COLLECTOR_METHOD_LABELS,
  MONITOR_RANGES,
  describeMonitorStatus,
  fetchMonitorOverview,
  fetchMonitorResourceHistory,
  formatBytesCompact,
  formatPercent,
  formatRateBytes,
  triggerMonitorRun,
  type MonitorHistoryData,
  type MonitorOverviewData,
  type MonitorRange,
  type MonitorResourceSnapshot,
} from "@/lib/monitor-engine"
import { fetchPlatformSkills, type PlatformSkillRecord } from "@/lib/skills-registry"
import { ActivityIcon, RefreshIcon, RocketIcon, ServerIcon } from "./icons"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Grid,
  Inline,
  Input,
  SegmentedControl,
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

function fmtTime(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function fmtRelative(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "尚无采集"
  const diff = Math.max(0, Date.now() - ms)
  if (diff < 10_000) return "刚刚"
  if (diff < 60_000) return `${Math.floor(diff / 1000)} 秒前`
  const min = Math.floor(diff / 60_000)
  if (min < 60) return `${min} 分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小时前`
  return `${Math.floor(hr / 24)} 天前`
}

function fmtAxis(ms: number, range: MonitorRange): string {
  const d = new Date(ms)
  if (range === "1h" || range === "6h" || range === "24h") {
    return d.toLocaleTimeString("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit" })
  }
  return d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })
}

function UsageBar({ pct, sub }: { pct?: number | null; sub?: string }) {
  if (pct == null || !Number.isFinite(pct)) {
    return <span className="font-mono text-xs text-muted-foreground">{sub || "—"}</span>
  }
  const clamped = Math.max(0, Math.min(100, pct))
  const tone = clamped >= 90 ? "bg-destructive" : clamped >= 80 ? "bg-warning" : "bg-primary"
  return (
    <div className="min-w-24 space-y-1">
      <div className="flex items-center justify-between gap-2 font-mono text-xs tabular-nums">
        <span className="font-medium text-foreground">{clamped.toFixed(1)}%</span>
        {sub && <span className="truncate text-2xs text-muted-foreground">{sub}</span>}
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.max(2, clamped)}%` }} />
      </div>
    </div>
  )
}

function FleetTrendChart({ data }: { data: MonitorOverviewData }) {
  const width = 720
  const height = 164
  const padTop = 12
  const padBottom = 24
  const plotH = height - padTop - padBottom
  const buckets = data.trends
  const n = Math.max(1, buckets.length)

  const buildPolyline = (pick: (b: MonitorOverviewData["trends"][number]) => number | null | undefined) => {
    const pts: string[] = []
    buckets.forEach((b, idx) => {
      const v = pick(b)
      if (v == null || !Number.isFinite(v)) return
      const x = n === 1 ? width / 2 : (idx / (n - 1)) * (width - 16) + 8
      const y = padTop + plotH - (Math.max(0, Math.min(100, v)) / 100) * plotH
      pts.push(`${x.toFixed(1)},${y.toFixed(1)}`)
    })
    return pts.join(" ")
  }

  const cpuLine = buildPolyline((b) => b.avg_cpu_percent)
  const memLine = buildPolyline((b) => b.avg_memory_percent)
  const diskLine = buildPolyline((b) => b.avg_disk_percent)
  const hasAny = Boolean(cpuLine || memLine || diskLine)

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>全系统主机与集群平均使用率趋势（0%–100%）</span>
        <span className="inline-flex flex-wrap items-center gap-3">
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2.5 w-2.5 rounded-xs bg-primary" /> CPU
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2.5 w-2.5 rounded-xs bg-success" /> 内存
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2.5 w-2.5 rounded-xs bg-warning" /> 磁盘
          </span>
        </span>
      </div>
      {!hasAny ? (
        <div className="flex h-40 items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground">
          所选时间窗内暂无时序样本。点击上方「立即全量采集」或配置定时任务后即可生成趋势曲线。
        </div>
      ) : (
        <svg viewBox={`0 0 ${width} ${height}`} className="h-44 w-full overflow-visible rounded-md border bg-background/60 p-2">
          {[0, 50, 100].map((mark) => {
            const y = padTop + plotH - (mark / 100) * plotH
            return (
              <g key={mark}>
                <line x1={8} y1={y} x2={width - 8} y2={y} stroke="var(--border)" strokeDasharray={mark === 0 ? undefined : "3 3"} strokeWidth="0.75" />
                <text x={8} y={y - 3} fill="var(--muted-foreground)" fontSize="10" fontFamily="var(--font-mono)">
                  {mark}%
                </text>
              </g>
            )
          })}
          {diskLine && <polyline points={diskLine} fill="none" stroke="var(--warning)" strokeWidth="1.75" />}
          {memLine && <polyline points={memLine} fill="none" stroke="var(--success)" strokeWidth="2" />}
          {cpuLine && <polyline points={cpuLine} fill="none" stroke="var(--primary)" strokeWidth="2" />}
          {buckets[0] && (
            <text x={8} y={height - 4} fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
              {fmtAxis(buckets[0].start_ms, data.range)}
            </text>
          )}
          {buckets[Math.floor(buckets.length / 2)] && buckets.length > 3 && (
            <text x={width / 2} y={height - 4} textAnchor="middle" fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
              {fmtAxis(buckets[Math.floor(buckets.length / 2)].start_ms, data.range)}
            </text>
          )}
          {buckets[buckets.length - 1] && (
            <text x={width - 8} y={height - 4} textAnchor="end" fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
              {fmtAxis(buckets[buckets.length - 1].end_ms, data.range)}
            </text>
          )}
        </svg>
      )}
    </div>
  )
}

function MiniSparkline({
  points,
  pick,
  unit,
  label,
  maxBound,
}: {
  points: MonitorResourceSnapshot[]
  pick: (p: MonitorResourceSnapshot) => number | null | undefined
  unit: string
  label: string
  maxBound?: number
}) {
  const vals = points
    .map((p) => ({ ts: p.collected_at_ms, v: pick(p) }))
    .filter((item): item is { ts: number; v: number } => item.v != null && Number.isFinite(item.v))
  const latest = vals[vals.length - 1]?.v
  const maxV = Math.max(maxBound ?? 1, ...vals.map((x) => x.v), 1)
  const poly = vals
    .map((item, i) => {
      const x = vals.length === 1 ? 150 : 6 + (i / (vals.length - 1)) * 288
      const y = 54 - (item.v / maxV) * 46
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(" ")

  return (
    <div className="rounded-md border bg-card p-3">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="font-medium text-foreground">{label}</span>
        <span className="font-mono text-muted-foreground">
          {latest != null ? `${latest.toFixed(1)} ${unit}` : "—"}
        </span>
      </div>
      {vals.length === 0 ? (
        <p className="mt-3 text-2xs text-muted-foreground">暂无该指标样本</p>
      ) : (
        <svg viewBox="0 0 300 60" className="mt-2 h-14 w-full text-primary">
          <line x1={6} y1={54} x2={294} y2={54} stroke="var(--border)" strokeWidth="1" />
          {vals.length > 1 && <polyline points={poly} fill="none" stroke="currentColor" strokeWidth="1.75" />}
          {vals.length === 1 && <circle cx={150} cy={54 - (vals[0].v / maxV) * 46} r="3" fill="currentColor" />}
        </svg>
      )}
    </div>
  )
}

function snapshotToAssetFocus(resource: MonitorResourceSnapshot): MonitorAssetFocus {
  const type =
    resource.asset_kind === "vm" || resource.asset_kind === "physical"
      ? "machine"
      : resource.asset_kind === "container"
        ? "container"
        : resource.asset_kind === "app"
          ? "app"
          : resource.asset_kind === "database"
            ? "database"
            : undefined
  return {
    type,
    assetId: resource.asset_kind === "container" || resource.asset_kind === "skill" ? undefined : resource.asset_id,
    origin: resource.collector_method === "agent_host" || resource.collector_method === "container_stats" || resource.collector_method === "app_probe"
      ? resource.collector_method
      : undefined,
  }
}

function AssetDetailDrawer({
  session,
  resource,
  range,
  onClose,
  onOpenTarget,
}: {
  session: Session
  resource: MonitorResourceSnapshot
  range: MonitorRange
  onClose: () => void
  onOpenTarget?: (cluster: string, instance: string, tab: TargetTab, focus?: MonitorAssetFocus) => void
}) {
  const [history, setHistory] = useState<MonitorHistoryData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    fetchMonitorResourceHistory(
      session,
      {
        cluster: resource.cluster,
        instance: resource.instance,
        assetKind: resource.asset_kind,
        assetId: resource.asset_id,
        range,
      },
      ctrl.signal,
    )
      .then((res) => {
        if (!ctrl.signal.aborted) {
          setHistory(res)
          setLoading(false)
        }
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error).message)
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, resource.cluster, resource.instance, resource.asset_kind, resource.asset_id, range])

  const details = (history?.latest?.details ?? resource.details ?? {}) as Record<string, unknown>
  const mounts = Array.isArray(details.mounts) ? (details.mounts as Record<string, unknown>[]) : []
  const disks = Array.isArray(details.disks) ? (details.disks as Record<string, unknown>[]) : []
  const networks = Array.isArray(details.networks) ? (details.networks as Record<string, unknown>[]) : []
  const gpus = Array.isArray(details.gpus) ? (details.gpus as Record<string, unknown>[]) : []
  const nodes = Array.isArray(details.nodes) ? (details.nodes as Record<string, unknown>[]) : []
  const abnormalPods = Array.isArray(details.abnormal_pods) ? (details.abnormal_pods as Record<string, unknown>[]) : []
  const containers = Array.isArray(details.containers) ? (details.containers as Record<string, unknown>[]) : []

  return (
    <div className="space-y-4 rounded-lg border bg-muted/20 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="space-y-0.5">
          <div className="flex flex-wrap items-center gap-2">
            <h4 className="text-sm font-semibold text-foreground">
              {resource.asset_name} <span className="font-mono text-xs text-muted-foreground">({resource.asset_id})</span>
            </h4>
            <Badge variant="outline">{ASSET_KIND_LABELS[resource.asset_kind] || resource.asset_kind}</Badge>
            <Badge variant="outline">{COLLECTOR_METHOD_LABELS[resource.collector_method] || resource.collector_method}</Badge>
            {resource.os_info && <span className="text-xs text-muted-foreground">{resource.os_info}</span>}
            {resource.kernel_info && <span className="font-mono text-2xs text-muted-foreground">{resource.kernel_info}</span>}
          </div>
          <p className="text-xs text-muted-foreground">
            所属 Agent：<span className="font-mono">{resource.cluster}/{resource.instance}</span> · 最近采集：{fmtTime(resource.collected_at_ms)}
            {resource.load_1m != null && ` · 1m 负载 ${resource.load_1m.toFixed(2)}`}
            {resource.uptime_seconds != null && ` · 运行时长 ${Math.floor(resource.uptime_seconds / 3600)}h`}
          </p>
        </div>
        <Inline gap={2}>
          {onOpenTarget && (
            <Button size="sm" onClick={() => onOpenTarget(resource.cluster, resource.instance, "monitor", snapshotToAssetFocus(resource))}>
              细粒度曲线与告警
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={onClose}>
            收起详情
          </Button>
        </Inline>
      </div>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {loading ? (
        <p className="text-xs text-muted-foreground">正在加载历史时序与设备明细…</p>
      ) : (
        <>
          <Grid cols={1} smCols={2} lgCols={4} gap={3}>
            <MiniSparkline points={history?.points ?? []} pick={(p) => p.cpu_percent} unit="%" label="CPU 使用率" maxBound={100} />
            <MiniSparkline points={history?.points ?? []} pick={(p) => p.memory_percent} unit="%" label="内存使用率" maxBound={100} />
            <MiniSparkline points={history?.points ?? []} pick={(p) => p.disk_percent} unit="%" label="磁盘峰值占用" maxBound={100} />
            {resource.asset_kind === "app" || resource.asset_kind === "database" ? (
              <MiniSparkline points={history?.points ?? []} pick={(p) => p.response_time_ms} unit="ms" label="响应延迟" />
            ) : (
              <MiniSparkline points={history?.points ?? []} pick={(p) => (p.net_bytes_per_sec != null ? p.net_bytes_per_sec / 1024 : null)} unit="KiB/s" label="网络吞吐" />
            )}
          </Grid>

          {mounts.length > 0 && (
            <div className="space-y-1.5">
              <h5 className="text-xs font-semibold text-foreground">文件系统挂载点 ({mounts.length})</h5>
              <Table wrapperClassName="rounded-md border bg-card">
                <TableHeader>
                  <TableRow>
                    <TableHead>挂载点</TableHead>
                    <TableHead>已用 / 总量</TableHead>
                    <TableHead>可用</TableHead>
                    <TableHead>使用率</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {mounts.map((m, idx) => (
                    <TableRow key={String(m.mount ?? idx)}>
                      <TableCell className="font-mono text-xs">{String(m.mount ?? "")}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {formatBytesCompact(Number(m.used_bytes))} / {formatBytesCompact(Number(m.total_bytes))}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{formatBytesCompact(Number(m.avail_bytes))}</TableCell>
                      <TableCell><UsageBar pct={Number(m.used_pct)} /></TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          {disks.length > 0 && (
            <div className="space-y-1.5">
              <h5 className="text-xs font-semibold text-foreground">物理磁盘 I/O ({disks.length})</h5>
              <Table wrapperClassName="rounded-md border bg-card">
                <TableHeader>
                  <TableRow>
                    <TableHead>块设备</TableHead>
                    <TableHead>读 / 写 IOPS</TableHead>
                    <TableHead>读 / 写吞吐</TableHead>
                    <TableHead>读 / 写延迟</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {disks.map((d, idx) => (
                    <TableRow key={String(d.device_id ?? idx)}>
                      <TableCell className="font-mono text-xs">{String(d.device_id ?? "")}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {d.read_iops != null ? Number(d.read_iops).toFixed(1) : "—"} / {d.write_iops != null ? Number(d.write_iops).toFixed(1) : "—"} ops/s
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {formatRateBytes(d.read_bytes_per_sec as number | null)} / {formatRateBytes(d.write_bytes_per_sec as number | null)}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {d.read_latency_ms != null ? `${Number(d.read_latency_ms).toFixed(1)}ms` : "—"} / {d.write_latency_ms != null ? `${Number(d.write_latency_ms).toFixed(1)}ms` : "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          {(networks.length > 0 || gpus.length > 0) && (
            <Grid cols={1} lgCols={2} gap={3}>
              {networks.length > 0 && (
                <div className="space-y-1.5">
                  <h5 className="text-xs font-semibold text-foreground">网卡实时吞吐 ({networks.length})</h5>
                  <div className="rounded-md border bg-card p-3 text-xs space-y-1">
                    {networks.map((n, idx) => (
                      <div key={String(n.device_id ?? idx)} className="flex items-center justify-between font-mono">
                        <span>{String(n.device_id ?? "")}</span>
                        <span>{formatRateBytes(Number(n.bytes_per_sec))}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              {gpus.length > 0 && (
                <div className="space-y-1.5">
                  <h5 className="text-xs font-semibold text-foreground">NVIDIA GPU ({gpus.length})</h5>
                  <div className="rounded-md border bg-card p-3 text-xs space-y-1.5">
                    {gpus.map((g, idx) => (
                      <div key={String(g.uuid ?? idx)} className="flex items-center justify-between gap-2 font-mono">
                        <span className="truncate">{String(g.name || g.uuid)}</span>
                        <span>
                          {Number(g.util_percent).toFixed(0)}%
                          {g.mem_used_mib != null && g.mem_total_mib != null && ` · ${g.mem_used_mib}/${g.mem_total_mib} MiB`}
                          {g.temp_c != null && ` · ${g.temp_c}°C`}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </Grid>
          )}

          {nodes.length > 0 && (
            <div className="space-y-1.5">
              <h5 className="text-xs font-semibold text-foreground">Kubernetes 节点 ({nodes.length})</h5>
              <div className="flex flex-wrap gap-2">
                {nodes.map((n, idx) => (
                  <Badge key={String(n.name ?? idx)} variant={n.ready ? "success" : "danger"}>
                    {String(n.name)} · {n.ready ? "Ready" : "NotReady"}
                    {Array.isArray(n.pressures) && n.pressures.length > 0 ? ` (${n.pressures.join(",")})` : ""}
                  </Badge>
                ))}
              </div>
            </div>
          )}

          {abnormalPods.length > 0 && (
            <div className="space-y-1.5">
              <h5 className="text-xs font-semibold text-destructive">异常 Pod 清单 ({abnormalPods.length})</h5>
              <Table wrapperClassName="rounded-md border bg-card">
                <TableHeader>
                  <TableRow>
                    <TableHead>命名空间 / Pod</TableHead>
                    <TableHead>状态原因</TableHead>
                    <TableHead>重启次数</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {abnormalPods.map((p, idx) => (
                    <TableRow key={`${p.namespace}/${p.name}/${idx}`}>
                      <TableCell className="font-mono text-xs">{String(p.namespace)}/{String(p.name)}</TableCell>
                      <TableCell><Badge variant="danger">{String(p.reason || p.phase)}</Badge></TableCell>
                      <TableCell className="font-mono text-xs">{String(p.restarts ?? 0)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          {containers.length > 0 && (
            <div className="space-y-1.5">
              <h5 className="text-xs font-semibold text-foreground">运行中容器 ({containers.length})</h5>
              <Table wrapperClassName="rounded-md border bg-card">
                <TableHeader>
                  <TableRow>
                    <TableHead>容器名</TableHead>
                    <TableHead>CPU</TableHead>
                    <TableHead>内存 / 上限</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {containers.map((c, idx) => (
                    <TableRow key={String(c.name ?? idx)}>
                      <TableCell className="font-mono text-xs">{String(c.name ?? "")}</TableCell>
                      <TableCell className="font-mono text-xs">{c.cpu_cores != null ? `${Number(c.cpu_cores).toFixed(2)} cores` : "—"}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {formatBytesCompact(c.memory_bytes as number | null)} / {formatBytesCompact(c.memory_limit_bytes as number | null)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </>
      )}
    </div>
  )
}

export function SystemResourceMonitor({
  session,
  targets,
  clusterFilter,
  onOpenTarget,
  onOpenRunDetail,
}: {
  session: Session
  targets: Target[]
  clusterFilter?: string
  onOpenTarget?: (cluster: string, instance: string, tab: TargetTab, focus?: MonitorAssetFocus) => void
  onOpenRunDetail?: (runId: string) => void
}) {
  const [range, setRange] = useState<MonitorRange>("1h")
  const [kindFilter, setKindFilter] = useState("")
  const [onlyAttention, setOnlyAttention] = useState(false)
  const [query, setQuery] = useState("")
  const [data, setData] = useState<MonitorOverviewData | null>(null)
  const [skills, setSkills] = useState<PlatformSkillRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [collecting, setCollecting] = useState(false)
  const [notice, setNotice] = useState("")
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [expandedKey, setExpandedKey] = useState<string | null>(null)

  const reload = useCallback(() => setRevision((v) => v + 1), [])

  const onlineTargets = useMemo(
    () =>
      targets.filter(
        (t) => t.status !== "offline" && (!clusterFilter || t.cluster === clusterFilter),
      ),
    [targets, clusterFilter],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    fetchPlatformSkills(session, {}, ctrl.signal)
      .then((res) => {
        if (!ctrl.signal.aborted) setSkills(res.skills || [])
      })
      .catch(() => {})
    return () => ctrl.abort()
  }, [session])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    fetchMonitorOverview(session, { range, cluster: clusterFilter || undefined, kind: kindFilter || undefined }, ctrl.signal)
      .then((res) => {
        if (!ctrl.signal.aborted) {
          setData(res)
          setLoading(false)
        }
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error).message)
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, range, clusterFilter, kindFilter, revision])

  async function collectAllNow() {
    if (collecting || onlineTargets.length === 0) return
    setCollecting(true)
    setNotice("")
    setError("")
    try {
      let activeSkills = skills
      if (activeSkills.length === 0) {
        try {
          const fetched = await fetchPlatformSkills(session, {})
          activeSkills = fetched.skills || []
          setSkills(activeSkills)
        } catch {
          // Fall back to default core skill slugs below if skill listing is temporarily unavailable.
        }
      }
      const fleetSkills = resolveFleetInspectionSkills(onlineTargets, activeSkills)
      if (!fleetSkills.length) {
        setError("当前没有已启用的系统巡检 Skill，请先在「场景与扩展 → 插件中心 → Skill 能力包」启用。")
        return
      }
      const targetRefs = onlineTargets.map((t) => ({ cluster: t.cluster, instance: t.instance }))
      const res = await triggerMonitorRun(session, {
        scope_mode: clusterFilter ? "targets" : "all_online",
        targets: clusterFilter ? targetRefs : undefined,
        skills: fleetSkills,
      })
      setNotice(
        `全量巡检采集完成（#${res.run.id}，Skill: ${fleetSkills.join("、")}）：${res.run.summary || `写入 ${res.run.metrics_written} 条指标`}`,
      )
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setCollecting(false)
    }
  }

  const attentionCount = useMemo(
    () => (data?.resources ?? []).filter((r) => !r.online || r.stale || r.status !== "ok").length,
    [data],
  )

  const filteredResources = useMemo(() => {
    const list = data?.resources ?? []
    const q = query.trim().toLowerCase()
    return list.filter((r) => {
      if (onlyAttention && r.online && !r.stale && r.status === "ok") return false
      if (!q) return true
      return [r.asset_id, r.asset_name, r.cluster, r.instance, r.os_info, r.collector_method, ...(r.tags ?? [])]
        .some((field) => field?.toLowerCase().includes(q))
    })
  }, [data, query, onlyAttention])

  const s = data?.summary

  return (
    <Stack gap={4}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="space-y-0.5">
          <h2 className="text-base font-semibold text-foreground">全系统机器与资产资源监控</h2>
          <p className="text-xs text-muted-foreground">
            汇总各 Agent 经系统巡检 Skill、本机直采、K8s API、HTTP 与数据库探针收集的实时与历史资源状态。
          </p>
        </div>
        <Inline gap={2} className="flex-wrap">
          <SegmentedControl ariaLabel="资源监控时间窗" value={range} options={MONITOR_RANGES} onChange={setRange} />
          <Button
            size="sm"
            variant="primary"
            disabled={collecting || onlineTargets.length === 0}
            title={onlineTargets.length === 0 ? "当前范围内暂无在线 Agent，无法下发实时采集" : undefined}
            onClick={() => void collectAllNow()}
          >
            <RocketIcon width={14} height={14} />
            {collecting ? "下发采集中…" : "立即全量采集"}
          </Button>
          <Button size="sm" disabled={loading} onClick={reload}>
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
        </Inline>
      </div>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

      {data?.threshold_alerts && data.threshold_alerts.length > 0 && (
        <Card>
          <CardContent className="space-y-2.5 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                <Badge variant="danger">超阈值告警 · {data.threshold_alerts.length}</Badge>
                <span className="text-xs text-muted-foreground">来自已配置阈值规则并持续超限的时序指标（可在「单目标时序与告警」调整规则）</span>
              </div>
            </div>
            <div className="divide-y text-xs">
              {data.threshold_alerts.slice(0, 6).map((a) => (
                <div key={`${a.cluster}/${a.instance}/${a.type}/${a.asset_id}/${a.metric}/${a.device_id || ""}/${a.opened_ms}`} className="flex flex-wrap items-center justify-between gap-2 py-2">
                  <div className="min-w-0">
                    <strong className="text-foreground">
                      {a.asset_id} · {a.metric}{a.device_id ? ` (${a.device_id})` : ""}
                    </strong>
                    <span className="ml-2 font-mono text-2xs text-muted-foreground">
                      {a.cluster}/{a.instance} · 阈值 &gt; {a.threshold} / {a.duration_seconds}s · 触发于 {fmtTime(a.opened_ms)}
                    </span>
                  </div>
                  {onOpenTarget && (
                    <Button
                      size="sm"
                      onClick={() =>
                        onOpenTarget(a.cluster, a.instance, "monitor", {
                          type: a.type,
                          assetId: a.asset_id,
                          namespace: a.namespace,
                          origin: a.origin,
                          metric: a.metric,
                        })
                      }
                    >
                      查看曲线与规则
                    </Button>
                  )}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {!data && loading ? (
        <p role="status" className="py-8 text-sm text-muted-foreground">正在汇总全系统资源快照…</p>
      ) : data && s ? (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
            <Card>
              <CardContent className="p-3.5">
                <div className="text-xs text-muted-foreground">已采集资产</div>
                <div className="mt-1 font-mono text-xl font-semibold tabular-nums">
                  {s.total_assets}
                </div>
                <div className="mt-1 text-2xs text-muted-foreground">
                  在线 Agent {s.online_agents} · 离线 {s.offline_agents} · 活跃计划 {s.active_schedules}
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-3.5">
                <div className="text-xs text-muted-foreground">全网平均 CPU</div>
                <div className="mt-1 font-mono text-xl font-semibold tabular-nums">{s.avg_cpu_percent.toFixed(1)}%</div>
                <div className="mt-1 text-2xs text-muted-foreground">
                  已用 {s.used_cpu_cores.toFixed(2)} / 总 {s.total_cpu_cores.toFixed(0)} 核
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-3.5">
                <div className="text-xs text-muted-foreground">全网平均内存</div>
                <div className="mt-1 font-mono text-xl font-semibold tabular-nums">{s.avg_memory_percent.toFixed(1)}%</div>
                <div className="mt-1 text-2xs text-muted-foreground">
                  {formatBytesCompact(s.used_memory_bytes)} / {formatBytesCompact(s.total_memory_bytes)}
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-3.5">
                <div className="text-xs text-muted-foreground">全网磁盘峰值均值</div>
                <div className="mt-1 font-mono text-xl font-semibold tabular-nums">{s.avg_disk_percent.toFixed(1)}%</div>
                <div className="mt-1 text-2xs text-muted-foreground">
                  {formatBytesCompact(s.used_disk_bytes)} / {formatBytesCompact(s.total_disk_bytes)}
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-3.5">
                <div className="text-xs text-muted-foreground">资产健康状态</div>
                <div className="mt-1 flex items-center gap-1.5 font-mono text-lg font-semibold tabular-nums">
                  <span className="text-success">{s.healthy_assets}</span>
                  <span className="text-muted-foreground">/</span>
                  <span className="text-warning">{s.warning_assets}</span>
                  <span className="text-muted-foreground">/</span>
                  <span className="text-destructive">{s.critical_assets + s.offline_assets}</span>
                </div>
                <div className="mt-1 text-2xs text-muted-foreground">
                  正常 / 告警 / 严重·离线 · {fmtRelative(s.last_collected_at_ms)}
                </div>
              </CardContent>
            </Card>
          </div>

          <div className="grid gap-4 lg:grid-cols-5">
            <Card className="lg:col-span-3">
              <CardContent className="p-4">
                <FleetTrendChart data={data} />
              </CardContent>
            </Card>
            <Card className="lg:col-span-2">
              <CardContent className="space-y-3 p-4">
                <div className="flex items-center justify-between">
                  <h3 className="text-sm font-semibold text-foreground">资源消耗 Top 节点</h3>
                  <ActivityIcon width={15} height={15} className="text-muted-foreground" />
                </div>
                {data.top_cpu.length === 0 && data.top_memory.length === 0 ? (
                  <p className="text-xs text-muted-foreground">暂无节点排行数据</p>
                ) : (
                  <div className="space-y-2.5 text-xs">
                    {data.top_cpu.slice(0, 4).map((r) => (
                      <div key={`top-${r.cluster}/${r.instance}/${r.asset_kind}/${r.asset_id}`} className="flex items-center justify-between gap-2">
                        <div className="min-w-0">
                          <div className="truncate font-medium text-foreground">{r.asset_name}</div>
                          <div className="truncate font-mono text-2xs text-muted-foreground">{r.cluster}/{r.instance}</div>
                        </div>
                        <div className="flex shrink-0 items-center gap-2 font-mono text-2xs">
                          <Badge variant={(r.cpu_percent ?? 0) >= 90 ? "danger" : (r.cpu_percent ?? 0) >= 80 ? "warning" : "outline"}>
                            CPU {formatPercent(r.cpu_percent)}
                          </Badge>
                          <Badge variant={(r.memory_percent ?? 0) >= 90 ? "danger" : (r.memory_percent ?? 0) >= 80 ? "warning" : "outline"}>
                            MEM {formatPercent(r.memory_percent)}
                          </Badge>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
                {data.recent_runs.length > 0 && (
                  <div className="border-t pt-2.5">
                    <div className="mb-1.5 text-2xs font-medium text-muted-foreground">最近执行记录</div>
                    <div className="space-y-1 text-xs">
                      {data.recent_runs.slice(0, 3).map((r) => {
                        const st = describeMonitorStatus(r.status)
                        return (
                          <div key={r.id} className="flex items-center justify-between gap-2">
                            <button
                              type="button"
                              onClick={() => onOpenRunDetail?.(r.id)}
                              className="truncate text-left text-primary hover:underline"
                            >
                              {r.schedule_name || r.id} · {fmtRelative(r.started_at_ms)}
                            </button>
                            <Badge variant={st.variant}>{st.label}</Badge>
                          </div>
                        )
                      })}
                    </div>
                  </div>
                )}
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardContent className="space-y-3 p-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <ServerIcon width={16} height={16} className="text-muted-foreground" />
                  <h3 className="text-sm font-semibold text-foreground">资产资源明细 ({filteredResources.length})</h3>
                </div>
                <Inline gap={2} className="flex-wrap">
                  <Button
                    size="sm"
                    variant={onlyAttention ? "primary" : "default"}
                    aria-pressed={onlyAttention}
                    onClick={() => setOnlyAttention((v) => !v)}
                  >
                    仅看异常 / 离线{attentionCount > 0 ? ` (${attentionCount})` : ""}
                  </Button>
                  <Select scale="sm" className="w-36" value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}>
                    <option value="">全部类型</option>
                    {Object.entries(ASSET_KIND_LABELS).map(([k, label]) => (
                      <option key={k} value={k}>
                        {label}{data.by_kind[k] ? ` (${data.by_kind[k]})` : ""}
                      </option>
                    ))}
                  </Select>
                  <Input
                    scale="sm"
                    type="search"
                    className="w-56"
                    placeholder="搜索资产名、目标、标签…"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                </Inline>
              </div>

              {filteredResources.length === 0 ? (
                <div className="rounded-md border border-dashed py-10 text-center text-sm text-muted-foreground">
                  暂无已收集的资产资源快照。请点击右上角「立即全量采集」向在线 Agent 下发首次采集任务。
                </div>
              ) : (
                <Table wrapperClassName="rounded-lg border">
                  <TableHeader className="bg-muted/40">
                    <TableRow>
                      <TableHead>资产 / 所属 Agent</TableHead>
                      <TableHead>类型 / 采集方式</TableHead>
                      <TableHead>CPU</TableHead>
                      <TableHead>内存</TableHead>
                      <TableHead>磁盘</TableHead>
                      <TableHead>网络 / I/O / 延迟</TableHead>
                      <TableHead>状态 / 采集时间</TableHead>
                      <TableHead className="text-right">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {filteredResources.map((r) => {
                      const rowKey = `${r.cluster}/${r.instance}/${r.asset_kind}/${r.asset_id}`
                      const isExpanded = expandedKey === rowKey
                      const st = describeMonitorStatus(!r.online ? "offline" : r.status)
                      return (
                        <Fragment key={rowKey}>
                          <TableRow>
                            <TableCell>
                              <div className="font-medium text-foreground">{r.asset_name}</div>
                              <div className="font-mono text-2xs text-muted-foreground">
                                {r.asset_id} · {r.cluster}/{r.instance}
                              </div>
                              {r.tags?.length > 0 && (
                                <div className="mt-1 flex flex-wrap gap-1">
                                  {r.tags.map((tg) => (
                                    <Badge key={tg} variant="outline">{tg}</Badge>
                                  ))}
                                </div>
                              )}
                            </TableCell>
                            <TableCell className="text-xs">
                              <div>{ASSET_KIND_LABELS[r.asset_kind] || r.asset_kind}</div>
                              <div className="mt-0.5 text-2xs text-muted-foreground">
                                {COLLECTOR_METHOD_LABELS[r.collector_method] || r.collector_method}
                              </div>
                            </TableCell>
                            <TableCell>
                              <UsageBar
                                pct={r.cpu_percent}
                                sub={r.cpu_cores != null ? `${r.cpu_cores.toFixed(2)}c` : undefined}
                              />
                            </TableCell>
                            <TableCell>
                              <UsageBar
                                pct={r.memory_percent}
                                sub={r.memory_used_bytes != null ? formatBytesCompact(r.memory_used_bytes) : undefined}
                              />
                            </TableCell>
                            <TableCell>
                              <UsageBar
                                pct={r.disk_percent}
                                sub={r.disk_used_bytes != null ? formatBytesCompact(r.disk_used_bytes) : undefined}
                              />
                            </TableCell>
                            <TableCell className="font-mono text-2xs text-muted-foreground">
                              {r.net_bytes_per_sec != null && <div>网 {formatRateBytes(r.net_bytes_per_sec)}</div>}
                              {r.disk_iops != null && (
                                <div>
                                  I/O {r.disk_iops.toFixed(0)} ops/s
                                  {r.disk_latency_ms != null ? ` · ${r.disk_latency_ms.toFixed(1)}ms` : ""}
                                </div>
                              )}
                              {r.gpu_percent != null && <div>GPU {r.gpu_percent.toFixed(0)}%</div>}
                              {r.response_time_ms != null && (
                                <div>
                                  耗时 {r.response_time_ms.toFixed(1)}ms
                                  {r.error_rate != null ? ` · 错误 ${r.error_rate.toFixed(0)}%` : ""}
                                </div>
                              )}
                              {r.active_connections != null && <div>并发 {r.active_connections.toFixed(0)}</div>}
                              {r.net_bytes_per_sec == null && r.disk_iops == null && r.gpu_percent == null && r.response_time_ms == null && r.active_connections == null && "—"}
                            </TableCell>
                            <TableCell>
                              <div className="flex flex-wrap items-center gap-1">
                                <Badge variant={st.variant}>{st.label}</Badge>
                                {r.stale && <Badge variant="outline">已过期</Badge>}
                              </div>
                              <div className="mt-1 font-mono text-2xs text-muted-foreground">
                                {fmtRelative(r.collected_at_ms)}
                              </div>
                            </TableCell>
                            <TableCell className="text-right">
                              <Button size="sm" onClick={() => setExpandedKey(isExpanded ? null : rowKey)}>
                                {isExpanded ? "收起" : "曲线与明细"}
                              </Button>
                            </TableCell>
                          </TableRow>
                          {isExpanded && (
                            <TableRow>
                              <TableCell colSpan={8} className="p-3">
                                <AssetDetailDrawer
                                  session={session}
                                  resource={r}
                                  range={range}
                                  onClose={() => setExpandedKey(null)}
                                  onOpenTarget={onOpenTarget}
                                />
                              </TableCell>
                            </TableRow>
                          )}
                        </Fragment>
                      )
                    })}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </>
      ) : null}
    </Stack>
  )
}
