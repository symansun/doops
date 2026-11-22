"use client"

import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { callTool, extractText, sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { commandPreview } from "@/lib/admin-views"
import { TOOLS } from "@/lib/gateway"
import type { TargetTab } from "@/lib/console-views"
import {
  PLATFORM_MONITOR_RANGES,
  describeAuditStatus,
  fetchPlatformMonitor,
  filterPlatformAgents,
  formatActionLabel,
  formatBytes,
  formatDurationMs,
  formatSuccessRate,
  formatTokens,
  parseAdmissionSnapshot,
  summarizeVersionConsistency,
  type AgentVisibilityScope,
  type AdmissionSnapshot,
  type PlatformAuditItem,
  type PlatformDetachedTaskItem,
  type PlatformLoadBucket,
  type PlatformMonitorAgent,
  type PlatformMonitorData,
  type PlatformMonitorRange,
  type PlatformUsageBucket,
} from "@/lib/platform-monitor"
import { ActivityIcon, HistoryIcon, PlugIcon, RefreshIcon, RocketIcon, ServerIcon, SparkIcon, UsersIcon } from "./icons"
import { InspectionCenterPanel } from "./inspection-center-panel"
import { SystemResourceMonitor } from "./system-resource-monitor"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Grid,
  Inline,
  Input,
  PageTabs,
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

type MonitorTab = "overview" | "resources" | "inspections" | "agents" | "usage" | "operations"
type TrendMode = "calls" | "load"

type ProbeState = {
  loading: boolean
  error?: string
  snapshot?: AdmissionSnapshot | null
  raw?: string
}

function fmtStamp(iso?: string): string {
  if (!iso) return "—"
  const ms = Date.parse(iso)
  if (!Number.isFinite(ms) || ms <= 0) return "—"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function fmtRelativeAge(ageMs?: number): string {
  if (ageMs === undefined || !Number.isFinite(ageMs) || ageMs < 0) return "未知"
  if (ageMs < 5000) return "刚刚"
  if (ageMs < 60_000) return `${Math.floor(ageMs / 1000)} 秒前`
  const min = Math.floor(ageMs / 60_000)
  if (min < 60) return `${min} 分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小时前`
  return `${Math.floor(hr / 24)} 天前`
}

function fmtConnectedSpan(iso?: string): string {
  if (!iso) return "—"
  const ms = Date.parse(iso)
  if (!Number.isFinite(ms) || ms <= 0) return "—"
  const diff = Math.max(0, Date.now() - ms)
  return formatDurationMs(diff)
}

function busyReasonText(reason?: string): string {
  if (!reason) return ""
  if (reason === "exclusive_operation") return "独占操作执行中"
  if (reason === "target_queue") return "目标队列等待中"
  return reason
}

function isFlapping(agent: PlatformMonitorAgent): boolean {
  return agent.window_connects >= 2 || (agent.window_connects >= 1 && agent.window_disconnects >= 1)
}

function agentStateBadge(agent: PlatformMonitorAgent) {
  if (agent.activity === "archived") return <Badge variant="default">已归档</Badge>
  if (agent.activity === "history") return <Badge variant="outline">历史离线</Badge>
  if (agent.activity === "recent_offline") return <Badge variant="warning">近期离线</Badge>
  if (agent.busy) return <Badge variant="warning">忙碌独占</Badge>
  if (agent.status === "active" || agent.active_ops > 0 || agent.queued_ops > 0) {
    return <Badge variant="primary">执行中</Badge>
  }
  return <Badge variant="success">在线空闲</Badge>
}

function toTargetModel(
  cluster: string,
  instance: string,
  liveTargets: Target[],
  fallback?: Partial<PlatformMonitorAgent>,
): Target {
  const key = `${cluster}/${instance}`
  const live = liveTargets.find((item) => item.key === key)
  if (live) return live
  return {
    cluster,
    instance,
    key,
    status: fallback?.activity === "online" ? fallback.status || "idle" : "offline",
    remote: fallback?.remote,
    connected_at: fallback?.connected_at,
    last_seen: fallback?.last_seen,
    busy: fallback?.busy,
    busy_reason: fallback?.busy_reason,
    active_ops: fallback?.active_ops,
    queued_ops: fallback?.queued_ops,
    resources: fallback?.resources,
    sessions: fallback?.sessions,
  }
}

function fmtAxisTime(ms: number, range: PlatformMonitorRange): string {
  const d = new Date(ms)
  if (range === "1h" || range === "24h") {
    return d.toLocaleTimeString("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit" })
  }
  return d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })
}

function UsageTrendChart({
  buckets,
  range,
}: {
  buckets: PlatformUsageBucket[]
  range: PlatformMonitorRange
}) {
  const maxTotal = Math.max(1, ...buckets.map((b) => b.total))
  const width = 720
  const height = 168
  const padTop = 12
  const padBottom = 24
  const plotH = height - padTop - padBottom
  const count = Math.max(1, buckets.length)
  const slotW = width / count
  const barW = Math.max(6, Math.min(28, slotW * 0.64))

  const first = buckets[0]
  const mid = buckets[Math.floor(buckets.length / 2)]
  const last = buckets[buckets.length - 1]
  const peak = Math.max(0, ...buckets.map((b) => b.total))

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>单桶峰值 <strong className="font-mono text-foreground">{peak}</strong> 次</span>
        <span className="inline-flex items-center gap-3">
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2.5 w-2.5 rounded-xs bg-primary" aria-hidden="true" />
            成功 / 执行中
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2.5 w-2.5 rounded-xs bg-destructive" aria-hidden="true" />
            异常 / 未完成
          </span>
        </span>
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="h-44 w-full overflow-visible rounded-md border bg-background/60 p-2"
        role="img"
        aria-label={`调用趋势图，共 ${buckets.length} 个时间分桶，单桶最高 ${peak} 次`}
      >
        <line x1={0} y1={padTop + plotH} x2={width} y2={padTop + plotH} stroke="var(--border)" strokeWidth="1" />
        <line x1={0} y1={padTop + plotH / 2} x2={width} y2={padTop + plotH / 2} stroke="var(--border)" strokeDasharray="3 3" strokeWidth="0.75" />
        {buckets.map((bucket, idx) => {
          const x = idx * slotW + (slotW - barW) / 2
          const totalH = bucket.total > 0 ? Math.max(3, (bucket.total / maxTotal) * plotH) : 0
          const failedH = bucket.failed > 0 ? Math.max(3, (bucket.failed / maxTotal) * plotH) : 0
          const okH = Math.max(0, totalH - failedH)
          const baseY = padTop + plotH
          return (
            <g key={bucket.start_ms}>
              <title>
                {`${fmtAxisTime(bucket.start_ms, range)} – ${fmtAxisTime(bucket.end_ms, range)}：共 ${bucket.total} 次，成功 ${bucket.success}，异常 ${bucket.failed}，Token ${formatTokens(bucket.total_tokens)}`}
              </title>
              {bucket.total === 0 ? (
                <rect x={x} y={baseY - 2} width={barW} height={2} rx={1} fill="var(--muted)" />
              ) : (
                <>
                  {okH > 0 && (
                    <rect x={x} y={baseY - totalH} width={barW} height={okH} rx={2} fill="var(--primary)" />
                  )}
                  {failedH > 0 && (
                    <rect x={x} y={baseY - failedH} width={barW} height={failedH} rx={2} fill="var(--destructive)" />
                  )}
                </>
              )}
            </g>
          )
        })}
        {first && (
          <text x={4} y={height - 5} fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
            {fmtAxisTime(first.start_ms, range)}
          </text>
        )}
        {mid && buckets.length > 4 && (
          <text x={width / 2} y={height - 5} textAnchor="middle" fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
            {fmtAxisTime(mid.start_ms, range)}
          </text>
        )}
        {last && (
          <text x={width - 4} y={height - 5} textAnchor="end" fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
            {fmtAxisTime(last.end_ms, range)}
          </text>
        )}
      </svg>
    </div>
  )
}

function LoadTrendChart({
  buckets,
  range,
}: {
  buckets: PlatformLoadBucket[]
  range: PlatformMonitorRange
}) {
  const maxLoad = Math.max(1, ...buckets.map((b) => b.max_active_ops + b.max_queued_ops))
  const peakActive = Math.max(0, ...buckets.map((b) => b.max_active_ops))
  const peakQueue = Math.max(0, ...buckets.map((b) => b.max_queued_ops))
  const peakOnline = Math.max(0, ...buckets.map((b) => b.max_online_agents))
  const width = 720
  const height = 168
  const padTop = 12
  const padBottom = 24
  const plotH = height - padTop - padBottom
  const count = Math.max(1, buckets.length)
  const slotW = width / count
  const barW = Math.max(6, Math.min(28, slotW * 0.64))

  const first = buckets[0]
  const mid = buckets[Math.floor(buckets.length / 2)]
  const last = buckets[buckets.length - 1]

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>
          峰值在线 <strong className="font-mono text-foreground">{peakOnline}</strong> 台 · 并发峰值{" "}
          <strong className="font-mono text-foreground">{peakActive}</strong> · 排队峰值{" "}
          <strong className="font-mono text-foreground">{peakQueue}</strong>
        </span>
        <span className="inline-flex items-center gap-3">
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2.5 w-2.5 rounded-xs bg-primary" aria-hidden="true" />
            并发执行峰值
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2.5 w-2.5 rounded-xs bg-warning" aria-hidden="true" />
            队列等待峰值
          </span>
        </span>
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="h-44 w-full overflow-visible rounded-md border bg-background/60 p-2"
        role="img"
        aria-label={`并发与排队水位趋势图，最高并发 ${peakActive}，最高排队 ${peakQueue}`}
      >
        <line x1={0} y1={padTop + plotH} x2={width} y2={padTop + plotH} stroke="var(--border)" strokeWidth="1" />
        <line x1={0} y1={padTop + plotH / 2} x2={width} y2={padTop + plotH / 2} stroke="var(--border)" strokeDasharray="3 3" strokeWidth="0.75" />
        {buckets.map((bucket, idx) => {
          const x = idx * slotW + (slotW - barW) / 2
          const combined = bucket.max_active_ops + bucket.max_queued_ops
          const totalH = combined > 0 ? Math.max(3, (combined / maxLoad) * plotH) : 0
          const queueH = bucket.max_queued_ops > 0 ? Math.max(3, (bucket.max_queued_ops / maxLoad) * plotH) : 0
          const activeH = Math.max(0, totalH - queueH)
          const baseY = padTop + plotH
          return (
            <g key={bucket.start_ms}>
              <title>
                {`${fmtAxisTime(bucket.start_ms, range)} – ${fmtAxisTime(bucket.end_ms, range)}：在线峰值 ${bucket.max_online_agents} 台，并发峰值 ${bucket.max_active_ops}，排队峰值 ${bucket.max_queued_ops}，会话峰值 ${bucket.max_active_sessions}`}
              </title>
              {combined === 0 ? (
                <rect x={x} y={baseY - 2} width={barW} height={2} rx={1} fill="var(--muted)" />
              ) : (
                <>
                  {activeH > 0 && (
                    <rect x={x} y={baseY - totalH} width={barW} height={activeH} rx={2} fill="var(--primary)" />
                  )}
                  {queueH > 0 && (
                    <rect x={x} y={baseY - queueH} width={barW} height={queueH} rx={2} fill="var(--warning)" />
                  )}
                </>
              )}
            </g>
          )
        })}
        {first && (
          <text x={4} y={height - 5} fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
            {fmtAxisTime(first.start_ms, range)}
          </text>
        )}
        {mid && buckets.length > 4 && (
          <text x={width / 2} y={height - 5} textAnchor="middle" fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
            {fmtAxisTime(mid.start_ms, range)}
          </text>
        )}
        {last && (
          <text x={width - 4} y={height - 5} textAnchor="end" fill="var(--muted-foreground)" fontSize="11" fontFamily="var(--font-mono)">
            {fmtAxisTime(last.end_ms, range)}
          </text>
        )}
      </svg>
    </div>
  )
}

function MetricCard({
  label,
  value,
  hint,
  icon: Icon,
  tone = "default",
}: {
  label: string
  value: string | number
  hint?: string
  icon: typeof ServerIcon
  tone?: "default" | "primary" | "warning"
}) {
  const iconTone =
    tone === "primary"
      ? "bg-primary/15 text-primary"
      : tone === "warning"
        ? "bg-warning/20 text-warning"
        : "bg-muted text-muted-foreground"
  return (
    <Card>
      <CardContent className="flex items-start gap-3 p-4">
        <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ring-1 ring-inset ring-foreground/5 ${iconTone}`}>
          <Icon width={18} height={18} aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-xs text-muted-foreground">{label}</div>
          <div className="mt-1 font-mono text-xl font-semibold leading-tight tabular-nums text-foreground">{value}</div>
          {hint && <div className="mt-1 truncate text-2xs text-muted-foreground" title={hint}>{hint}</div>}
        </div>
      </CardContent>
    </Card>
  )
}

export interface PlatformMonitorPanelProps {
  session: Session
  targets: Target[]
  initialClusterId?: string
  onClusterChange?: (cluster?: string) => void
  onOpenTarget?: (target: Target, tab: TargetTab, operationId?: string) => void
  onDeployAgent?: () => void
  onRunningChange?: (running: boolean) => void
}

export function AssetPlatformPanel({
  session,
  targets,
  initialClusterId,
  onClusterChange,
  onOpenTarget,
  onDeployAgent,
  onRunningChange,
}: PlatformMonitorPanelProps) {
  const isAdmin = sessionIsAdmin(session)
  const [range, setRange] = useState<PlatformMonitorRange>("24h")
  const [cluster, setCluster] = useState(initialClusterId ?? "")
  const [tab, setTab] = useState<MonitorTab>("overview")
  const [inspectionRunId, setInspectionRunId] = useState("")
  const [trendMode, setTrendMode] = useState<TrendMode>("calls")
  const [visibility, setVisibility] = useState<AgentVisibilityScope>("active")
  const [agentQuery, setAgentQuery] = useState("")
  const [data, setData] = useState<PlatformMonitorData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [probes, setProbes] = useState<Record<string, ProbeState>>({})
  const probingRef = useRef(0)

  useEffect(() => {
    setCluster(initialClusterId ?? "")
  }, [initialClusterId])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    fetchPlatformMonitor(session, { range, cluster: cluster || undefined }, ctrl.signal)
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
  }, [session, range, cluster, revision])

  function selectCluster(next: string) {
    setCluster(next)
    onClusterChange?.(next || undefined)
  }

  const probeAdmission = useCallback(async (agent: PlatformMonitorAgent) => {
    if (agent.activity !== "online") return
    setProbes((prev) => ({ ...prev, [agent.key]: { loading: true } }))
    probingRef.current += 1
    onRunningChange?.(true)
    let output = ""
    let toolErr = ""
    try {
      await callTool(
        session,
        {
          cluster: agent.cluster,
          instance: agent.instance,
          tool: TOOLS.nodeInfo,
          arguments: { session_id: "platform-monitor-admission", section: "admission" },
        },
        (ev) => {
          if (ev.type === "output") output += ev.data
          else if (ev.type === "result") output = extractText(ev.result) || output
          else if (ev.type === "error") toolErr = ev.error
        },
      )
      if (toolErr) throw new Error(toolErr)
      setProbes((prev) => ({
        ...prev,
        [agent.key]: {
          loading: false,
          snapshot: parseAdmissionSnapshot(output),
          raw: output.trim(),
        },
      }))
    } catch (err) {
      setProbes((prev) => ({
        ...prev,
        [agent.key]: { loading: false, error: (err as Error).message },
      }))
    } finally {
      probingRef.current = Math.max(0, probingRef.current - 1)
      if (probingRef.current === 0) onRunningChange?.(false)
    }
  }, [session, onRunningChange])

  const filteredAgents = useMemo(
    () => (data ? filterPlatformAgents(data.agents, visibility, agentQuery) : []),
    [data, visibility, agentQuery],
  )

  const versionInfo = useMemo(
    () => summarizeVersionConsistency(data?.agent_summary.versions ?? []),
    [data],
  )

  const runningDetached = useMemo(
    () => (data?.detached_tasks ?? []).filter((t) => t.status === "running").length,
    [data],
  )

  const hasLoadSamples = (data?.load_trend ?? []).some((b) => b.samples > 0)
  const attentionCount = (data?.running_ops.length ?? 0) + runningDetached + (data?.recent_issues.length ?? 0)
  const activeAgentCount = (data?.agent_summary.online ?? 0) + (data?.agent_summary.recent_offline ?? 0)

  const visibilityOptions = useMemo(() => {
    const base: { value: AgentVisibilityScope; label: string }[] = [
      { value: "active", label: `活跃 (${activeAgentCount})` },
      { value: "with_history", label: `含历史 (${activeAgentCount + (data?.agent_summary.history ?? 0)})` },
    ]
    if (isAdmin && (data?.agent_summary.archived ?? 0) > 0) {
      base.push({ value: "all", label: `含归档 (${data?.agents.length ?? 0})` })
    }
    return base
  }, [isAdmin, activeAgentCount, data])

  function jumpToTarget(clusterName: string, instanceName: string, targetTab: TargetTab, operationId?: string) {
    if (!onOpenTarget || !clusterName || !instanceName) return
    const fallback = data?.agents.find((a) => a.cluster === clusterName && a.instance === instanceName)
    onOpenTarget(toTargetModel(clusterName, instanceName, targets, fallback), targetTab, operationId)
  }

  return (
    <section aria-label="平台监控" className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">平台监控</h1>
            <Badge variant={isAdmin ? "primary" : "outline"}>
              {isAdmin ? "全局视角" : "本人授权范围"}
            </Badge>
            {data && !versionInfo.consistent && (
              <Badge variant="warning">{versionInfo.headline}</Badge>
            )}
            {data && data.agent_summary.flapping_agents > 0 && (
              <Badge variant="warning">时窗重连波动 {data.agent_summary.flapping_agents} 台</Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            统一监控 Agent 隧道连接与大模型消耗、全网主机/SSH/K8s/容器/应用资源水位，以及策略化定时巡检与历史记录。
          </p>
        </div>

        <Inline gap={2} className="flex-wrap">
          <Select
            aria-label="按集群筛选"
            scale="sm"
            className="w-48"
            value={cluster}
            disabled={!data && loading}
            onChange={(e) => selectCluster(e.target.value)}
          >
            <option value="">全部集群{data ? ` (${data.clusters.length})` : ""}</option>
            {cluster && data && !data.clusters.some((c) => c.name === cluster) && (
              <option value={cluster}>{cluster}</option>
            )}
            {data?.clusters.map((item) => (
              <option key={item.name} value={item.name}>
                {item.name}（在线 {item.online}/{item.total}）
              </option>
            ))}
          </Select>

          <SegmentedControl
            ariaLabel="统计时间范围"
            value={range}
            options={PLATFORM_MONITOR_RANGES}
            onChange={setRange}
          />

          <Button size="sm" disabled={loading} onClick={() => setRevision((v) => v + 1)}>
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
          {isAdmin && onDeployAgent && (
            <Button size="sm" variant="primary" onClick={onDeployAgent}>
              接入 Agent
            </Button>
          )}
        </Inline>
      </header>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}

      {!data && loading ? (
        <p role="status" className="py-8 text-sm text-muted-foreground">
          正在读取平台监控数据…
        </p>
      ) : data ? (
        <>
          <Grid cols={2} smCols={3} lgCols={3} gap={3}>
            <MetricCard
              label="在线 Agent"
              value={data.agent_summary.online}
              icon={PlugIcon}
              tone={data.agent_summary.unenrolled_online > 0 || data.agent_summary.flapping_agents > 0 || (data.agent_summary.online === 0 && data.agent_summary.recent_offline > 0) ? "warning" : "primary"}
              hint={
                data.agent_summary.unenrolled_online > 0
                  ? `已注册 ${data.agent_summary.enrolled_online} · 未绑定凭证 ${data.agent_summary.unenrolled_online}`
                  : `近期离线 ${data.agent_summary.recent_offline} · 重连波动 ${data.agent_summary.flapping_agents}`
              }
            />
            <MetricCard
              label="执行中 / 排队"
              value={`${data.agent_summary.active_ops} / ${data.agent_summary.queued_ops}`}
              icon={RocketIcon}
              tone={data.agent_summary.queued_ops > 0 || data.agent_summary.busy > 0 ? "warning" : "default"}
              hint={`独占忙碌 ${data.agent_summary.busy} · 资源锁 ${data.agent_summary.resource_locks}`}
            />
            <MetricCard
              label="活跃会话"
              value={data.agent_summary.active_sessions}
              icon={UsersIcon}
              tone={data.agent_summary.active_sessions > 0 ? "primary" : "default"}
              hint={`脱离后台运行中 ${runningDetached}`}
            />
            <MetricCard
              label="时窗调用量"
              value={data.usage_summary.total}
              icon={ActivityIcon}
              tone="primary"
              hint={`活跃目标 ${data.usage_summary.active_targets} · 活跃账号 ${data.usage_summary.active_users}`}
            />
            <MetricCard
              label="大模型 Token"
              value={formatTokens(data.usage_summary.total_tokens)}
              icon={SparkIcon}
              tone={data.usage_summary.total_tokens > 0 ? "primary" : "default"}
              hint={`输入 ${formatTokens(data.usage_summary.input_tokens)} · 输出 ${formatTokens(data.usage_summary.output_tokens)} · ${data.usage_summary.active_models} 个模型`}
            />
            <MetricCard
              label="执行成功率"
              value={formatSuccessRate(data.usage_summary)}
              icon={HistoryIcon}
              tone={data.usage_summary.outcome_unknown > 0 || data.usage_summary.failed > 0 ? "warning" : "primary"}
              hint={
                data.usage_summary.outcome_unknown > 0
                  ? `结果未知 ${data.usage_summary.outcome_unknown} · 均耗 ${formatDurationMs(data.usage_summary.avg_duration_ms)}`
                  : `均耗 ${data.usage_summary.avg_duration_ms ? formatDurationMs(data.usage_summary.avg_duration_ms) : "—"} · 流量 ${formatBytes(data.usage_summary.bytes_in + data.usage_summary.bytes_out)}`
              }
            />
          </Grid>

          <PageTabs
            ariaLabel="平台监控视图"
            value={tab}
            onChange={setTab}
            items={[
              { id: "overview", label: "总览" },
              { id: "resources", label: "系统资源监控" },
              { id: "inspections", label: "巡检任务与记录" },
              { id: "agents", label: `Agent 节点 (${activeAgentCount})` },
              { id: "usage", label: `使用排行 (${data.usage_summary.total})` },
              { id: "operations", label: `运行与异常 (${attentionCount})` },
            ]}
          />

          {tab === "overview" && (
            <Stack gap={4}>
              <div className="grid gap-4 lg:grid-cols-5">
                <Card className="lg:col-span-3">
                  <CardContent className="space-y-3 p-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div>
                        <h2 className="text-base font-semibold text-foreground">
                          {trendMode === "load" && hasLoadSamples ? "并发与排队峰值水位" : "调用与异常趋势"}
                        </h2>
                        <p className="text-xs text-muted-foreground">
                          {trendMode === "load" && hasLoadSamples
                            ? "按分钟水位采样统计各分桶最高在线 Agent、并发操作与排队等待数"
                            : `统计已排除升级观测轮询；出入总流量 ${formatBytes(data.usage_summary.bytes_in + data.usage_summary.bytes_out)}`}
                        </p>
                      </div>
                      <Inline gap={2} className="flex-wrap">
                        {data.usage_summary.outcome_unknown > 0 && (
                          <Badge variant="warning">含 {data.usage_summary.outcome_unknown} 条结果未知</Badge>
                        )}
                        {hasLoadSamples && (
                          <SegmentedControl
                            ariaLabel="趋势图指标"
                            value={trendMode}
                            onChange={setTrendMode}
                            options={[
                              { value: "calls", label: "调用量" },
                              { value: "load", label: "并发水位" },
                            ]}
                          />
                        )}
                      </Inline>
                    </div>
                    {trendMode === "load" && hasLoadSamples ? (
                      <LoadTrendChart buckets={data.load_trend} range={data.range} />
                    ) : (
                      <UsageTrendChart buckets={data.trend} range={data.range} />
                    )}
                  </CardContent>
                </Card>

                <Card className="lg:col-span-2">
                  <CardContent className="space-y-3 p-4">
                    <div className="flex items-center justify-between gap-2">
                      <h2 className="text-base font-semibold text-foreground">工具动作与模型分布</h2>
                      <span className="text-xs text-muted-foreground">共 {data.by_action.length} 类</span>
                    </div>
                    {!data.by_action.length ? (
                      <p className="py-6 text-center text-sm text-muted-foreground">所选时间范围内暂无调用记录</p>
                    ) : (
                      <ul className="space-y-2.5">
                        {data.by_action.slice(0, 5).map((item) => {
                          const share = data.usage_summary.total > 0
                            ? Math.round((item.total / data.usage_summary.total) * 100)
                            : 0
                          return (
                            <li key={item.action} className="space-y-1 text-sm">
                              <div className="flex items-center justify-between gap-2">
                                <span className="min-w-0 truncate">
                                  <span className="font-medium text-foreground">{formatActionLabel(item.action)}</span>{" "}
                                  <span className="font-mono text-2xs text-muted-foreground">{item.action}</span>
                                </span>
                                <span className="shrink-0 font-mono text-xs tabular-nums text-foreground">
                                  {item.total} <span className="text-muted-foreground">({share}%)</span>
                                </span>
                              </div>
                              <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
                                <div
                                  className="h-full rounded-full bg-primary"
                                  style={{ width: `${Math.max(share, item.total > 0 ? 3 : 0)}%` }}
                                />
                              </div>
                              <div className="flex flex-wrap gap-3 text-2xs text-muted-foreground">
                                <span>平均 {formatDurationMs(item.avg_duration_ms)}</span>
                                {item.total_tokens > 0 && <span>Token {formatTokens(item.total_tokens)}</span>}
                                {item.failed > 0 && <span className="text-destructive">异常 {item.failed}</span>}
                              </div>
                            </li>
                          )
                        })}
                      </ul>
                    )}

                    {data.by_model.length > 0 && (
                      <div className="border-t pt-3">
                        <div className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
                          <span className="font-medium text-foreground">大模型消耗</span>
                          <span>总计 {formatTokens(data.usage_summary.total_tokens)} Token</span>
                        </div>
                        <ul className="space-y-1.5 text-xs">
                          {data.by_model.slice(0, 3).map((m) => (
                            <li key={m.model} className="flex items-center justify-between gap-2">
                              <span className="truncate font-mono text-foreground">{m.model}</span>
                              <span className="shrink-0 font-mono text-muted-foreground">
                                {formatTokens(m.total_tokens)} <span className="text-2xs">({m.total} 次)</span>
                              </span>
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}
                  </CardContent>
                </Card>
              </div>

              <div className="grid gap-4 lg:grid-cols-2">
                <Card>
                  <CardContent className="space-y-3 p-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div>
                        <h2 className="text-base font-semibold text-foreground">Agent 运行与版本状态</h2>
                        <p className="text-xs text-muted-foreground">{versionInfo.headline}</p>
                      </div>
                      <Button size="sm" variant="ghost" onClick={() => setTab("agents")}>
                        查看全部节点
                      </Button>
                    </div>

                    {data.agent_summary.versions.length > 0 && (
                      <div className="flex flex-wrap gap-2">
                        {data.agent_summary.versions.map((v) => (
                          <span
                            key={v.release_commit || "unreported"}
                            title={v.release_commit || "Agent 未上报 releaseCommit"}
                            className="inline-flex items-center gap-1.5 rounded-md border bg-background/60 px-2.5 py-1 text-xs"
                          >
                            <span className="font-mono text-foreground">
                              {v.release_commit ? v.release_commit.slice(0, 8) : "未上报版本"}
                            </span>
                            <Badge variant={v.release_commit ? "outline" : "warning"}>{v.count} 台</Badge>
                          </span>
                        ))}
                      </div>
                    )}

                    {!data.agents.length ? (
                      <div className="rounded-lg border bg-background/50 p-4 text-sm text-muted-foreground">
                        <p>{isAdmin ? "当前尚无已登记的 Agent 节点。" : "当前账号暂无获授权的 Agent 节点，请联系平台管理员开通目标授权。"}</p>
                        {isAdmin && onDeployAgent && (
                          <Button size="sm" variant="primary" className="mt-3" onClick={onDeployAgent}>
                            查看首次接入指南
                          </Button>
                        )}
                      </div>
                    ) : (
                      <div className="divide-y">
                        {data.agents.slice(0, 5).map((agent) => (
                          <div key={agent.key} className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
                            <div className="min-w-0 space-y-0.5">
                              <div className="flex flex-wrap items-center gap-2">
                                <span className="font-mono font-medium text-foreground">{agent.key}</span>
                                {agentStateBadge(agent)}
                                {!agent.enrolled && agent.activity === "online" && (
                                  <Badge variant="warning">未绑定注册凭证</Badge>
                                )}
                                {isFlapping(agent) && <Badge variant="warning">闪断重连</Badge>}
                              </div>
                              <div className="flex flex-wrap gap-3 text-xs text-muted-foreground">
                                <span>并发 {agent.active_ops} / 排队 {agent.queued_ops}</span>
                                <span>会话 {agent.sessions.length}</span>
                                <span>心跳 {fmtRelativeAge(agent.heartbeat_age_ms)}</span>
                                <span>调用 {agent.window_calls}</span>
                                {agent.window_tokens > 0 && <span>Token {formatTokens(agent.window_tokens)}</span>}
                              </div>
                            </div>
                            {onOpenTarget && (
                              <Button
                                size="sm"
                                onClick={() => jumpToTarget(agent.cluster, agent.instance, agent.activity === "online" ? "overview" : "monitor")}
                              >
                                {agent.activity === "online" ? "进入工作区" : "历史指标"}
                              </Button>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                  </CardContent>
                </Card>

                <Card>
                  <CardContent className="space-y-3 p-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div>
                        <h2 className="text-base font-semibold text-foreground">近期异常与运行追踪</h2>
                        <p className="text-xs text-muted-foreground">
                          正在执行 {data.running_ops.length} · 脱离后台 {runningDetached} · 时窗异常 {data.recent_issues.length}
                        </p>
                      </div>
                      <Button size="sm" variant="ghost" onClick={() => setTab("operations")}>
                        查看全部记录
                      </Button>
                    </div>

                    {!data.recent_issues.length && !data.running_ops.length ? (
                      <p className="py-8 text-center text-sm text-muted-foreground">
                        当前没有执行中任务或未恢复异常记录
                      </p>
                    ) : (
                      <div className="divide-y">
                        {[...data.running_ops.slice(0, 2), ...data.recent_issues.slice(0, 4)].map((item) => {
                          const meta = describeAuditStatus(item.status)
                          const preview = commandPreview(item.command_summary || item.error || item.action, 84)
                          return (
                            <div key={item.id} className="space-y-1 py-2.5 text-sm">
                              <div className="flex flex-wrap items-center justify-between gap-2">
                                <div className="flex flex-wrap items-center gap-2">
                                  <Badge variant={meta.variant}>{meta.label}</Badge>
                                  <span className="font-medium text-foreground">{formatActionLabel(item.action)}</span>
                                  {item.cluster && item.instance && (
                                    <span className="font-mono text-xs text-muted-foreground">
                                      {item.cluster}/{item.instance}
                                    </span>
                                  )}
                                </div>
                                {onOpenTarget && item.cluster && item.instance && (
                                  <Button
                                    size="sm"
                                    variant="ghost"
                                    onClick={() => jumpToTarget(item.cluster!, item.instance!, "audit", item.correlation_id)}
                                  >
                                    审计回执
                                  </Button>
                                )}
                              </div>
                              <p className="truncate font-mono text-xs text-muted-foreground" title={preview.full}>
                                {preview.text}
                              </p>
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </CardContent>
                </Card>
              </div>

              <div className="grid gap-4 lg:grid-cols-2">
                <Card>
                  <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
                    <div className="min-w-0 space-y-1">
                      <h2 className="text-base font-semibold text-foreground">系统资源监控与阈值告警</h2>
                      <p className="text-xs text-muted-foreground">
                        查看各 Agent 汇总的虚机/物理机、SSH 远程主机、K8s 集群、容器与应用资源水位、历史趋势及活跃阈值告警。
                      </p>
                    </div>
                    <Button size="sm" variant="primary" onClick={() => setTab("resources")}>
                      进入系统资源监控
                    </Button>
                  </CardContent>
                </Card>
                <Card>
                  <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
                    <div className="min-w-0 space-y-1">
                      <h2 className="text-base font-semibold text-foreground">策略巡检任务与执行历史</h2>
                      <p className="text-xs text-muted-foreground">
                        按资产类型配置定时巡检计划，由 Gateway 下发给对应 Agent 执行，并可回看每次巡检明细与一键复检异常项。
                      </p>
                    </div>
                    <Button size="sm" onClick={() => setTab("inspections")}>
                      管理巡检与记录
                    </Button>
                  </CardContent>
                </Card>
              </div>
            </Stack>
          )}

          {tab === "agents" && (
            <Stack gap={4}>
              <Card>
                <CardContent className="space-y-3 p-4">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <Inline gap={2} className="flex-wrap">
                      <SegmentedControl
                        ariaLabel="实例显示范围"
                        value={visibility}
                        options={visibilityOptions}
                        onChange={setVisibility}
                      />
                      <Input
                        type="search"
                        scale="sm"
                        aria-label="搜索 Agent"
                        placeholder="搜索 cluster/instance、地址、会话或 commit"
                        value={agentQuery}
                        onChange={(e) => setAgentQuery(e.target.value)}
                        className="w-72"
                      />
                    </Inline>
                    {isAdmin && (
                      <a href="/console/admin?tab=instances" className="text-xs text-primary underline underline-offset-2">
                        打开实例归档管理
                      </a>
                    )}
                  </div>

                  <div className="overflow-x-auto">
                    <Table className="min-w-[960px]">
                      <TableHeader>
                        <TableRow>
                          <TableHead>状态与注册</TableHead>
                          <TableHead>目标 Agent</TableHead>
                          <TableHead>版本提交</TableHead>
                          <TableHead>并发 / 队列 / 锁</TableHead>
                          <TableHead>连接、心跳与上下线</TableHead>
                          <TableHead>调用与 Token ({data.range})</TableHead>
                          <TableHead className="text-right">操作</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {filteredAgents.map((agent) => {
                          const probe = probes[agent.key]
                          const staleHeartbeat = agent.activity === "online" && (agent.heartbeat_age_ms ?? 0) > 90_000
                          const flapping = isFlapping(agent)
                          return (
                            <Fragment key={agent.key}>
                              <TableRow className={agent.activity === "history" || agent.activity === "archived" ? "opacity-65" : undefined}>
                                <TableCell>
                                  <div className="flex flex-wrap items-center gap-1.5">
                                    {agentStateBadge(agent)}
                                    {agent.enrolled ? (
                                      <Badge variant="outline">已注册</Badge>
                                    ) : agent.activity === "online" ? (
                                      <Badge variant="warning">开放接入</Badge>
                                    ) : null}
                                    {flapping && <Badge variant="warning">闪断重连</Badge>}
                                  </div>
                                  {agent.busy_reason && (
                                    <div className="mt-1 text-2xs text-warning">{busyReasonText(agent.busy_reason)}</div>
                                  )}
                                </TableCell>
                                <TableCell>
                                  <div className="font-mono font-medium text-foreground">{agent.key}</div>
                                  <div className="font-mono text-2xs text-muted-foreground">{agent.remote || "—"}</div>
                                </TableCell>
                                <TableCell>
                                  {agent.release_commit ? (
                                    <div>
                                      <span className="font-mono text-xs text-foreground" title={agent.release_commit}>
                                        {agent.release_commit.slice(0, 8)}
                                      </span>
                                      {agent.activity !== "online" && (
                                        <span className="block text-2xs text-muted-foreground">最后上报</span>
                                      )}
                                    </div>
                                  ) : (
                                    <span className="text-xs text-muted-foreground">
                                      {agent.activity === "online" ? "未上报" : "—"}
                                    </span>
                                  )}
                                </TableCell>
                                <TableCell>
                                  <div className="font-mono text-xs">
                                    执行 <span className={agent.active_ops > 0 ? "text-primary" : "text-foreground"}>{agent.active_ops}</span>
                                    {" · "}排队 <span className={agent.queued_ops > 0 ? "text-warning" : "text-foreground"}>{agent.queued_ops}</span>
                                    {" · "}会话 {agent.sessions.length}
                                  </div>
                                  {agent.resources.length > 0 && (
                                    <div className="mt-1 truncate font-mono text-2xs text-muted-foreground" title={agent.resources.join(", ")}>
                                      锁：{agent.resources.slice(0, 2).join(", ")}{agent.resources.length > 2 ? ` +${agent.resources.length - 2}` : ""}
                                    </div>
                                  )}
                                </TableCell>
                                <TableCell className="text-xs">
                                  <div className={staleHeartbeat ? "text-warning" : "text-foreground"}>
                                    心跳 {fmtRelativeAge(agent.heartbeat_age_ms)}
                                  </div>
                                  <div className="text-2xs text-muted-foreground">
                                    {agent.activity === "online"
                                      ? `已连接 ${fmtConnectedSpan(agent.connected_at)}`
                                      : `最后在线 ${fmtStamp(agent.last_seen)}`}
                                  </div>
                                  {(agent.window_connects > 0 || agent.window_disconnects > 0) && (
                                    <div className="font-mono text-2xs text-muted-foreground">
                                      上线 {agent.window_connects} · 离线 {agent.window_disconnects}
                                    </div>
                                  )}
                                </TableCell>
                                <TableCell className="text-xs">
                                  <div className="font-mono text-foreground">
                                    {agent.window_calls} 次
                                    {agent.window_failed > 0 && (
                                      <span className="ml-1.5 text-destructive">异常 {agent.window_failed}</span>
                                    )}
                                  </div>
                                  <div className="font-mono text-2xs text-muted-foreground">
                                    {agent.window_tokens > 0 ? `${formatTokens(agent.window_tokens)} tok · ` : ""}
                                    {formatBytes(agent.window_bytes_in + agent.window_bytes_out)}
                                  </div>
                                </TableCell>
                                <TableCell className="text-right">
                                  <div className="inline-flex flex-wrap justify-end gap-1.5">
                                    {agent.activity === "online" && (
                                      <Button
                                        size="sm"
                                        disabled={probe?.loading}
                                        onClick={() => void probeAdmission(agent)}
                                      >
                                        {probe?.loading ? "读取中…" : "准入队列"}
                                      </Button>
                                    )}
                                    {onOpenTarget && (
                                      <>
                                        <Button
                                          size="sm"
                                          onClick={() => jumpToTarget(agent.cluster, agent.instance, "monitor")}
                                        >
                                          指标
                                        </Button>
                                        <Button
                                          size="sm"
                                          onClick={() => jumpToTarget(agent.cluster, agent.instance, "audit")}
                                        >
                                          审计
                                        </Button>
                                        {agent.activity === "online" && (
                                          <Button
                                            size="sm"
                                            variant="primary"
                                            onClick={() => jumpToTarget(agent.cluster, agent.instance, "overview")}
                                          >
                                            进入工作区
                                          </Button>
                                        )}
                                      </>
                                    )}
                                  </div>
                                </TableCell>
                              </TableRow>
                              {probe && !probe.loading && (
                                <TableRow className="bg-muted/30">
                                  <TableCell colSpan={7}>
                                    {probe.error ? (
                                      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-destructive">
                                        <span>读取 do-agent 准入状态失败：{probe.error}</span>
                                        <Button size="sm" variant="ghost" onClick={() => setProbes((p) => { const next = { ...p }; delete next[agent.key]; return next })}>
                                          收起
                                        </Button>
                                      </div>
                                    ) : probe.snapshot ? (
                                      <div className="space-y-2 text-xs">
                                        <div className="flex flex-wrap items-center justify-between gap-2">
                                          <div className="flex flex-wrap items-center gap-3 font-mono">
                                            <span>do-agent 准入名额：<strong className="text-foreground">{probe.snapshot.inUse} / {probe.snapshot.limit}</strong>（可用 {probe.snapshot.available}）</span>
                                            <span>占用 Turn：{probe.snapshot.holders}</span>
                                            <span className={probe.snapshot.queues > 0 ? "text-warning" : undefined}>排队会话：{probe.snapshot.queues}</span>
                                          </div>
                                          <Button size="sm" variant="ghost" onClick={() => setProbes((p) => { const next = { ...p }; delete next[agent.key]; return next })}>
                                            收起
                                          </Button>
                                        </div>
                                        <details className="text-2xs text-muted-foreground">
                                          <summary className="cursor-pointer">查看原始准入快照 JSON</summary>
                                          <pre className="mt-1.5 max-h-40 overflow-auto rounded-md border bg-background p-2 font-mono text-foreground">
                                            {probe.snapshot.raw}
                                          </pre>
                                        </details>
                                      </div>
                                    ) : (
                                      <pre className="max-h-36 overflow-auto font-mono text-xs text-foreground">{probe.raw || "无返回内容"}</pre>
                                    )}
                                  </TableCell>
                                </TableRow>
                              )}
                            </Fragment>
                          )
                        })}
                        {!filteredAgents.length && (
                          <TableRow>
                            <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                              没有匹配当前筛选条件的 Agent 节点
                            </TableCell>
                          </TableRow>
                        )}
                      </TableBody>
                    </Table>
                  </div>
                </CardContent>
              </Card>

              <Card>
                <CardContent className="space-y-3 p-4">
                  <div>
                    <h2 className="text-base font-semibold text-foreground">Agent 上下线事件（{data.connection_events.length}）</h2>
                    <p className="text-xs text-muted-foreground">所选时间窗口内持久化的隧道连接与断开记录，用于排查频繁重连或版本切换</p>
                  </div>
                  {!data.connection_events.length ? (
                    <p className="py-6 text-center text-sm text-muted-foreground">所选时间范围内无上下线状态变化记录</p>
                  ) : (
                    <div className="overflow-x-auto">
                      <Table className="min-w-[680px]">
                        <TableHeader>
                          <TableRow>
                            <TableHead>事件</TableHead>
                            <TableHead>目标 Agent</TableHead>
                            <TableHead>连接代次</TableHead>
                            <TableHead>版本提交</TableHead>
                            <TableHead>远端地址</TableHead>
                            <TableHead>发生时间</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.connection_events.map((ev) => (
                            <TableRow key={ev.id}>
                              <TableCell>
                                <Badge variant={ev.event === "online" ? "success" : "warning"}>
                                  {ev.event === "online" ? "上线连接" : "断开离线"}
                                </Badge>
                              </TableCell>
                              <TableCell className="font-mono text-xs font-medium text-foreground">{ev.key}</TableCell>
                              <TableCell className="font-mono text-xs text-muted-foreground">#{ev.generation}</TableCell>
                              <TableCell className="font-mono text-xs text-muted-foreground" title={ev.release_commit}>
                                {ev.release_commit ? ev.release_commit.slice(0, 8) : "—"}
                              </TableCell>
                              <TableCell className="font-mono text-xs text-muted-foreground">{ev.remote || "—"}</TableCell>
                              <TableCell className="text-xs text-muted-foreground">{fmtStamp(ev.occurred_at)}</TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </div>
                  )}
                </CardContent>
              </Card>
            </Stack>
          )}

          {tab === "usage" && (
            <Stack gap={4}>
              <div className="grid gap-4 lg:grid-cols-2">
                <Card>
                  <CardContent className="space-y-3 p-4">
                    <div>
                      <h2 className="text-base font-semibold text-foreground">目标 Agent 使用与异常排行</h2>
                      <p className="text-xs text-muted-foreground">按所选时间窗内调用次数与异常数排序（前 20）</p>
                    </div>
                    <div className="overflow-x-auto">
                      <Table className="min-w-[600px]">
                        <TableHeader>
                          <TableRow>
                            <TableHead>目标 Agent</TableHead>
                            <TableHead>调用 / 成功率</TableHead>
                            <TableHead>异常 / 未知</TableHead>
                            <TableHead>Token / 流量</TableHead>
                            <TableHead className="text-right">追溯</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.by_target.map((row) => (
                            <TableRow key={row.key}>
                              <TableCell>
                                <div className="flex items-center gap-2">
                                  <span className={`inline-block h-2 w-2 rounded-full ${row.online ? "bg-success" : "bg-muted-foreground/40"}`} aria-hidden="true" />
                                  <span className="font-mono font-medium text-foreground">{row.key}</span>
                                </div>
                                <div className="text-2xs text-muted-foreground">最近调用 {fmtStamp(row.last_used_at)}</div>
                              </TableCell>
                              <TableCell className="font-mono text-xs">
                                <div>{row.total} 次</div>
                                <div className="text-2xs text-muted-foreground">{formatSuccessRate(row)} · {formatDurationMs(row.avg_duration_ms)}</div>
                              </TableCell>
                              <TableCell className="font-mono text-xs">
                                <span className={row.failed > 0 ? "text-destructive" : "text-muted-foreground"}>
                                  {row.failed}
                                </span>
                                {row.outcome_unknown > 0 && (
                                  <Badge variant="warning" className="ml-1.5">未知 {row.outcome_unknown}</Badge>
                                )}
                              </TableCell>
                              <TableCell className="font-mono text-xs">
                                <div>{row.total_tokens > 0 ? `${formatTokens(row.total_tokens)} tok` : "—"}</div>
                                <div className="text-2xs text-muted-foreground">{formatBytes(row.bytes_in + row.bytes_out)}</div>
                              </TableCell>
                              <TableCell className="text-right">
                                {onOpenTarget && (
                                  <div className="inline-flex gap-1.5">
                                    <Button size="sm" onClick={() => jumpToTarget(row.cluster, row.instance, "audit")}>
                                      审计
                                    </Button>
                                    {row.online && (
                                      <Button size="sm" onClick={() => jumpToTarget(row.cluster, row.instance, "overview")}>
                                        进入
                                      </Button>
                                    )}
                                  </div>
                                )}
                              </TableCell>
                            </TableRow>
                          ))}
                          {!data.by_target.length && (
                            <TableRow>
                              <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                                所选范围内暂无目标调用记录
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </Table>
                    </div>
                  </CardContent>
                </Card>

                <Card>
                  <CardContent className="space-y-3 p-4">
                    <div>
                      <h2 className="text-base font-semibold text-foreground">账号与令牌使用排行</h2>
                      <p className="text-xs text-muted-foreground">
                        {isAdmin ? "统计各账号调用次数、覆盖目标数、令牌数与大模型 Token" : "普通账号仅展示本人及自有凭据范围内的调用统计"}
                      </p>
                    </div>
                    <div className="overflow-x-auto">
                      <Table className="min-w-[560px]">
                        <TableHeader>
                          <TableRow>
                            <TableHead>账号</TableHead>
                            <TableHead>调用 / 异常</TableHead>
                            <TableHead>目标 / 令牌</TableHead>
                            <TableHead>Token / 流量</TableHead>
                            <TableHead>最近活跃</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.by_user.map((user) => (
                            <TableRow key={user.user_id}>
                              <TableCell>
                                <div className="flex items-center gap-2">
                                  <span className="font-medium text-foreground">{user.user_name}</span>
                                  {user.is_admin && <Badge variant="primary">admin</Badge>}
                                </div>
                              </TableCell>
                              <TableCell className="font-mono text-xs">
                                {user.total} 次
                                {user.failed > 0 && <span className="ml-1.5 text-destructive">异常 {user.failed}</span>}
                              </TableCell>
                              <TableCell className="font-mono text-xs text-muted-foreground">
                                {user.active_targets} 目标 · {user.token_count} 令牌
                              </TableCell>
                              <TableCell className="font-mono text-xs">
                                <div>{user.total_tokens > 0 ? `${formatTokens(user.total_tokens)} tok` : "—"}</div>
                                <div className="text-2xs text-muted-foreground">{formatBytes(user.bytes_in + user.bytes_out)}</div>
                              </TableCell>
                              <TableCell className="text-xs text-muted-foreground">
                                {fmtStamp(user.last_active_at)}
                              </TableCell>
                            </TableRow>
                          ))}
                          {!data.by_user.length && (
                            <TableRow>
                              <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                                所选范围内暂无账号活动记录
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </Table>
                    </div>
                  </CardContent>
                </Card>
              </div>

              <div className="grid gap-4 lg:grid-cols-2">
                <Card>
                  <CardContent className="space-y-3 p-4">
                    <h2 className="text-base font-semibold text-foreground">大模型 Token 消耗明细</h2>
                    <div className="overflow-x-auto">
                      <Table className="min-w-[480px]">
                        <TableHeader>
                          <TableRow>
                            <TableHead>模型标识</TableHead>
                            <TableHead>调用轮次</TableHead>
                            <TableHead>输入 Token</TableHead>
                            <TableHead>输出 Token</TableHead>
                            <TableHead>总 Token</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.by_model.map((m) => (
                            <TableRow key={m.model}>
                              <TableCell className="font-mono text-xs font-medium text-foreground">{m.model}</TableCell>
                              <TableCell className="font-mono text-xs">{m.total}</TableCell>
                              <TableCell className="font-mono text-xs text-muted-foreground">{formatTokens(m.input_tokens)}</TableCell>
                              <TableCell className="font-mono text-xs text-muted-foreground">{formatTokens(m.output_tokens)}</TableCell>
                              <TableCell className="font-mono text-xs font-semibold text-foreground">{formatTokens(m.total_tokens)}</TableCell>
                            </TableRow>
                          ))}
                          {!data.by_model.length && (
                            <TableRow>
                              <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                                所选时间窗内暂无大模型 Token 消耗记录
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </Table>
                    </div>
                  </CardContent>
                </Card>

                <Card>
                  <CardContent className="space-y-3 p-4">
                    <h2 className="text-base font-semibold text-foreground">动作调用明细</h2>
                    <div className="overflow-x-auto">
                      <Table className="min-w-[540px]">
                        <TableHeader>
                          <TableRow>
                            <TableHead>动作名称</TableHead>
                            <TableHead>总调用</TableHead>
                            <TableHead>成功 / 异常</TableHead>
                            <TableHead>平均耗时</TableHead>
                            <TableHead>流量 / Token</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.by_action.map((act) => (
                            <TableRow key={act.action}>
                              <TableCell>
                                <span className="font-medium text-foreground">{formatActionLabel(act.action)}</span>{" "}
                                <span className="font-mono text-2xs text-muted-foreground">{act.action}</span>
                              </TableCell>
                              <TableCell className="font-mono text-xs">{act.total}</TableCell>
                              <TableCell className="font-mono text-xs">
                                <span className="text-success">{act.success}</span>
                                {" / "}
                                <span className={act.failed > 0 ? "text-destructive" : "text-muted-foreground"}>{act.failed}</span>
                              </TableCell>
                              <TableCell className="font-mono text-xs">{formatDurationMs(act.avg_duration_ms)}</TableCell>
                              <TableCell className="font-mono text-xs text-muted-foreground">
                                {formatBytes(act.bytes_in + act.bytes_out)}
                                {act.total_tokens > 0 ? ` · ${formatTokens(act.total_tokens)} tok` : ""}
                              </TableCell>
                            </TableRow>
                          ))}
                          {!data.by_action.length && (
                            <TableRow>
                              <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                                暂无动作统计数据
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </Table>
                    </div>
                  </CardContent>
                </Card>
              </div>
            </Stack>
          )}

          {tab === "resources" && (
            <SystemResourceMonitor
              session={session}
              targets={targets}
              clusterFilter={cluster || undefined}
              onOpenTarget={onOpenTarget ? jumpToTarget : undefined}
              onOpenRunDetail={(runId) => {
                setInspectionRunId(runId)
                setTab("inspections")
              }}
            />
          )}

          {tab === "inspections" && (
            <InspectionCenterPanel
              session={session}
              targets={targets}
              initialRunId={inspectionRunId || undefined}
              onClearInitialRun={() => setInspectionRunId("")}
            />
          )}

          {tab === "operations" && (
            <Stack gap={4}>
              <Card>
                <CardContent className="space-y-3 p-4">
                  <div>
                    <h2 className="text-base font-semibold text-foreground">正在执行的操作（{data.running_ops.length}）</h2>
                    <p className="text-xs text-muted-foreground">Gateway 当前尚未收到终态回执的实时调用</p>
                  </div>
                  <AuditItemsTable
                    items={data.running_ops}
                    emptyText="当前没有正在执行的实时操作"
                    onOpenTarget={onOpenTarget ? jumpToTarget : undefined}
                  />
                </CardContent>
              </Card>

              <Card>
                <CardContent className="space-y-3 p-4">
                  <div>
                    <h2 className="text-base font-semibold text-foreground">脱离后台任务（{data.detached_tasks.length}）</h2>
                    <p className="text-xs text-muted-foreground">
                      已持久化至 Gateway SQLite（保留最近 24 小时终态；若 Gateway 重启中断运行中任务，恢复为结果未知）
                    </p>
                  </div>
                  <DetachedTasksTable
                    items={data.detached_tasks}
                    onOpenTarget={onOpenTarget ? jumpToTarget : undefined}
                  />
                </CardContent>
              </Card>

              <Card>
                <CardContent className="space-y-3 p-4">
                  <div>
                    <h2 className="text-base font-semibold text-foreground">近期异常与结果未知（{data.recent_issues.length}）</h2>
                    <p className="text-xs text-muted-foreground">
                      「结果未知」表示网络或隧道在终态回执前中断，请先进入目标或审计核对实际状态，切勿盲目重复提交变更
                    </p>
                  </div>
                  <AuditItemsTable
                    items={data.recent_issues}
                    emptyText="所选时间范围内没有失败、阻断或结果未知事件"
                    showError
                    onOpenTarget={onOpenTarget ? jumpToTarget : undefined}
                  />
                </CardContent>
              </Card>
            </Stack>
          )}
        </>
      ) : null}
    </section>
  )
}

function AuditItemsTable({
  items,
  emptyText,
  showError = false,
  onOpenTarget,
}: {
  items: PlatformAuditItem[]
  emptyText: string
  showError?: boolean
  onOpenTarget?: (cluster: string, instance: string, tab: TargetTab, operationId?: string) => void
}) {
  if (!items.length) {
    return <p className="py-6 text-center text-sm text-muted-foreground">{emptyText}</p>
  }
  return (
    <div className="overflow-x-auto">
      <Table className="min-w-[780px]">
        <TableHeader>
          <TableRow>
            <TableHead>状态 / 动作</TableHead>
            <TableHead>目标与会话</TableHead>
            <TableHead>发起账号</TableHead>
            <TableHead>命令或摘要</TableHead>
            <TableHead>时间 / 耗时</TableHead>
            <TableHead className="text-right">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => {
            const meta = describeAuditStatus(item.status)
            const summary = commandPreview(item.command_summary || item.action, 96)
            return (
              <TableRow key={item.id}>
                <TableCell>
                  <div className="flex flex-wrap items-center gap-1.5">
                    <Badge variant={meta.variant}>{meta.label}</Badge>
                    <span className="text-xs font-medium text-foreground">{formatActionLabel(item.action)}</span>
                  </div>
                  <div className="mt-1 font-mono text-2xs text-muted-foreground">
                    #{item.id}
                    {item.total_tokens ? ` · ${formatTokens(item.total_tokens)} tok` : ""}
                  </div>
                </TableCell>
                <TableCell>
                  <div className="font-mono text-xs text-foreground">
                    {item.cluster && item.instance ? `${item.cluster}/${item.instance}` : "平台资源"}
                  </div>
                  {item.session && <div className="font-mono text-2xs text-muted-foreground">{item.session}</div>}
                </TableCell>
                <TableCell className="text-xs text-foreground">{item.user_name || item.user_id || "—"}</TableCell>
                <TableCell className="max-w-md">
                  <div className="truncate font-mono text-xs text-foreground" title={summary.full}>
                    {summary.text}
                  </div>
                  {item.model && (
                    <div className="mt-0.5 font-mono text-2xs text-muted-foreground">模型 {item.model}</div>
                  )}
                  {showError && item.error && (
                    <div className="mt-1 break-words font-mono text-2xs text-destructive">{item.error}</div>
                  )}
                  {item.correlation_id && (
                    <div className="mt-0.5 font-mono text-2xs text-muted-foreground">操作 {item.correlation_id}</div>
                  )}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  <div>{fmtStamp(item.started_at)}</div>
                  <div className="font-mono text-2xs">{formatDurationMs(item.duration_ms)}</div>
                </TableCell>
                <TableCell className="text-right">
                  {onOpenTarget && item.cluster && item.instance && (
                    <div className="inline-flex gap-1.5">
                      <Button
                        size="sm"
                        onClick={() => onOpenTarget(item.cluster!, item.instance!, "audit", item.correlation_id)}
                      >
                        {item.correlation_id ? "追踪操作" : "查审计"}
                      </Button>
                    </div>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

function DetachedTasksTable({
  items,
  onOpenTarget,
}: {
  items: PlatformDetachedTaskItem[]
  onOpenTarget?: (cluster: string, instance: string, tab: TargetTab, operationId?: string) => void
}) {
  if (!items.length) {
    return <p className="py-6 text-center text-sm text-muted-foreground">当前没有脱离后台任务</p>
  }
  return (
    <div className="overflow-x-auto">
      <Table className="min-w-[760px]">
        <TableHeader>
          <TableRow>
            <TableHead>状态</TableHead>
            <TableHead>目标 / 会话</TableHead>
            <TableHead>账号</TableHead>
            <TableHead>任务与命令摘要</TableHead>
            <TableHead>启动时间</TableHead>
            <TableHead className="text-right">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((task) => {
            const meta = describeAuditStatus(task.status)
            const preview = commandPreview(task.command_summary, 90)
            const canOpenTaskTab = task.is_own !== false
            return (
              <TableRow key={task.audit_id}>
                <TableCell>
                  <Badge variant={meta.variant}>{meta.label}</Badge>
                  {task.cancel_requested && task.status === "running" && (
                    <div className="mt-1 text-2xs text-warning">正在请求停止</div>
                  )}
                </TableCell>
                <TableCell>
                  <div className="font-mono text-xs text-foreground">{task.cluster}/{task.instance}</div>
                  <div className="font-mono text-2xs text-muted-foreground">{task.session}</div>
                </TableCell>
                <TableCell className="text-xs text-foreground">{task.user_name || task.user_id || "—"}</TableCell>
                <TableCell className="max-w-md">
                  <div className="truncate font-mono text-xs text-foreground" title={preview.full}>
                    {preview.text}
                  </div>
                  <div className="mt-0.5 font-mono text-2xs text-muted-foreground">
                    audit #{task.audit_id}{task.task_id ? ` · ${task.task_id}` : ""}
                  </div>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{fmtStamp(task.started_at)}</TableCell>
                <TableCell className="text-right">
                  {onOpenTarget && (
                    <Button
                      size="sm"
                      onClick={() => onOpenTarget(task.cluster, task.instance, canOpenTaskTab ? "tasks" : "audit")}
                    >
                      {canOpenTaskTab ? "查看后台任务" : "查审计"}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
