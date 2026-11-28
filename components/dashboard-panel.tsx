"use client"

import { useEffect, useMemo, useState } from "react"
import { AlertTriangle, Clock, Gauge, Search, Zap } from "lucide-react"
import {
  fetchAudit,
  sessionIsAdmin,
  type Session,
  type Target,
  type AuditEvent,
  type AuditSummary,
} from "@/lib/client"
import { listInstances } from "@/lib/admin"
import { commandPreview, summarizeInstanceScope, type InstanceScope } from "@/lib/admin-views"
import { resolveFleetInspectionSkills } from "@/lib/agent-monitor"
import { assetRequest } from "@/lib/assets"
import type { ConsoleNavEntry, TargetTab } from "@/lib/console-views"
import {
  buildAttentionItems,
  buildClusterGroups,
  buildNodeRows,
  buildTargetModeBreakdown,
  formatDashboardStatusLine,
  formatTagChipLabel,
  indexHostTelemetry,
  indexMachineTags,
  isFailedAuditEvent,
  isFailedAuditStatus,
  isTargetKubernetes,
  resolveDefaultActivityTab,
  summarizeRunTitle,
  type AssetDashboardSummary,
  type ClusterFilterMode,
  type DashboardAttentionItem,
  type DashboardClusterGroup,
  type DashboardMachineTagSource,
  type DashboardNodeRow,
  type MachineTagSummary,
  type TargetTelemetry,
} from "@/lib/dashboard-summary"
import { emptyTargetsCopy } from "@/lib/empty-targets"
import { targetModeLabel } from "@/lib/gateway"
import {
  describeMonitorStatus,
  fetchMonitorOverview,
  TRIGGER_LABELS,
  triggerMonitorRun,
  type MonitorOverviewData,
  type MonitorRunRecord,
} from "@/lib/monitor-engine"
import { isPluginEnabled } from "@/lib/plugins/resolver"
import type { PluginStateResponse } from "@/lib/plugins/types"
import { fetchPlatformSkills } from "@/lib/skills-registry"
import { HomePluginWidgetGrid } from "./plugins/home-widget-grid"
import {
  ServerIcon,
  ActivityIcon,
  RefreshIcon,
  ChevronRightIcon,
  RocketIcon,
  HistoryIcon,
  PlugIcon,
  HelpIcon,
  LayersIcon,
  UsersIcon,
  SparkIcon,
  TerminalIcon,
  ShieldIcon,
  PlusIcon,
} from "./icons"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Grid,
  Input,
  SegmentedControl,
  Stack,
  StatusBanner,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui"

const NODE_VIEW_STORAGE_KEY = "doops.console.homeNodeView.v1"
const DEFAULT_VISIBLE_ALERTS = 3
const DEFAULT_VISIBLE_NODE_ROWS = 8
const DEFAULT_VISIBLE_CLUSTERS = 8
const DEFAULT_VISIBLE_NODES_PER_CLUSTER = 3

type NodeWorkbenchView = "table" | "groups"

function readPreferredNodeView(): NodeWorkbenchView {
  try {
    if (typeof window === "undefined") return "table"
    return window.localStorage.getItem(NODE_VIEW_STORAGE_KEY) === "groups" ? "groups" : "table"
  } catch {
    return "table"
  }
}

export function DashboardPanel({
  session,
  targets,
  directory,
  loading,
  onRefresh,
  onDeployAgent,
  pluginState,
  onOpenDestination,
  onOpenTargetTool,
  onOpenInspectionRun,
}: {
  session: Session
  targets: Target[]
  directory?: Target[]
  loading: boolean
  onRefresh: () => void
  onDeployAgent: () => void
  pluginState?: PluginStateResponse | null
  onOpenDestination?: (dest: ConsoleNavEntry["destination"]) => void
  onOpenTargetTool?: (target: Target, tab: TargetTab) => void
  onOpenInspectionRun?: (runId: string) => void
}) {
  const [refreshVersion, setRefreshVersion] = useState(0)
  const [auditLoading, setAuditLoading] = useState(true)
  const [audit, setAudit] = useState<AuditEvent[]>([])
  const [auditSummary, setAuditSummary] = useState<AuditSummary | null>(null)
  const [managed, setManaged] = useState<InstanceScope | null>(null)
  const [auditDenied, setAuditDenied] = useState(false)
  const [auditError, setAuditError] = useState("")
  const [managedError, setManagedError] = useState("")
  const [monitorOverview, setMonitorOverview] = useState<MonitorOverviewData | null>(null)
  const [monitorLoading, setMonitorLoading] = useState(false)
  const [assetDashboard, setAssetDashboard] = useState<AssetDashboardSummary | null>(null)
  const [assetDashboardError, setAssetDashboardError] = useState("")
  const [machineTags, setMachineTags] = useState<MachineTagSummary>({
    byTarget: new Map(),
    totalMachines: 0,
    taggedMachines: 0,
    untaggedMachines: 0,
  })
  const [websiteCount, setWebsiteCount] = useState<number | null>(null)
  const [databaseCount, setDatabaseCount] = useState<number | null>(null)

  const [collectingFleet, setCollectingFleet] = useState(false)
  const [fleetNotice, setFleetNotice] = useState<{ tone: "success" | "error"; text: string; runId?: string } | null>(null)

  const [nodeFilter, setNodeFilter] = useState<ClusterFilterMode>("all")
  const [nodeQuery, setNodeQuery] = useState("")
  const [nodeView, setNodeView] = useState<NodeWorkbenchView>(() => readPreferredNodeView())
  const [showAllNodes, setShowAllNodes] = useState(false)
  const [activityTabOverride, setActivityTabOverride] = useState<"audit" | "runs" | null>(null)

  const isAdmin = sessionIsAdmin(session)
  const inspectionEnabled = isPluginEnabled(pluginState, "doops.inspection")
  const automationEnabled = isPluginEnabled(pluginState, "doops.automation")
  const cmdbEnabled = isPluginEnabled(pluginState, "doops.cmdb")
  const k8sEnabled = isPluginEnabled(pluginState, "doops.k8s")
  const websiteEnabled = isPluginEnabled(pluginState, "doops.website")
  const databaseEnabled = isPluginEnabled(pluginState, "doops.database")
  const shouldLoadMonitor = inspectionEnabled || automationEnabled
  const shouldLoadAssets = cmdbEnabled || k8sEnabled

  useEffect(() => {
    let alive = true
    const ctrl = new AbortController()
    setAuditLoading(true)
    setAuditError("")
    setManagedError("")
    setAssetDashboardError("")
    setMonitorLoading(shouldLoadMonitor)

    Promise.allSettled([
      fetchAudit(session, { limit: "10" }),
      isAdmin ? listInstances(session) : Promise.resolve(null),
      shouldLoadMonitor ? fetchMonitorOverview(session, { range: "24h" }, ctrl.signal) : Promise.resolve(null),
      shouldLoadAssets ? assetRequest<AssetDashboardSummary>("dashboard", undefined, undefined, ctrl.signal) : Promise.resolve(null),
      cmdbEnabled ? assetRequest<{ machines?: DashboardMachineTagSource[] }>("machines", undefined, undefined, ctrl.signal) : Promise.resolve(null),
      websiteEnabled ? assetRequest<{ records?: { id: string }[] }>("catalog", undefined, new URLSearchParams({ kind: "website" }), ctrl.signal) : Promise.resolve(null),
      databaseEnabled ? assetRequest<{ records?: { id: string }[] }>("catalog", undefined, new URLSearchParams({ kind: "database" }), ctrl.signal) : Promise.resolve(null),
    ]).then(([auditResult, instancesResult, monitorResult, assetResult, machinesResult, websiteResult, databaseResult]) => {
      if (!alive || ctrl.signal.aborted) return
      setAuditLoading(false)
      setMonitorLoading(false)

      if (auditResult.status === "rejected") {
        setAuditError(auditResult.reason instanceof Error ? auditResult.reason.message : "查询审计失败")
        setAuditDenied(false)
        setAudit([])
        setAuditSummary(null)
      } else if (auditResult.value.error && auditResult.value.status === 403) {
        setAuditDenied(true)
        setAudit([])
        setAuditSummary(null)
      } else if (auditResult.value.error) {
        setAuditDenied(false)
        setAuditError(auditResult.value.error)
        setAudit([])
        setAuditSummary(null)
      } else {
        setAuditDenied(false)
        setAudit((auditResult.value.events || []).slice(0, 10))
        setAuditSummary(auditResult.value.summary || null)
      }

      if (instancesResult.status === "rejected") {
        setManagedError(instancesResult.reason instanceof Error ? instancesResult.reason.message : "查询实例状态失败")
        setManaged(null)
      } else if (!instancesResult.value) {
        setManaged(null)
      } else {
        setManaged(summarizeInstanceScope(instancesResult.value, Date.now()))
      }

      if (monitorResult.status === "fulfilled" && monitorResult.value) {
        setMonitorOverview(monitorResult.value)
      } else {
        setMonitorOverview(null)
      }

      if (assetResult.status === "fulfilled" && assetResult.value) {
        setAssetDashboard(assetResult.value)
      } else if (assetResult.status === "rejected") {
        setAssetDashboard(null)
        setAssetDashboardError(assetResult.reason instanceof Error ? assetResult.reason.message : "台账统计暂不可用")
      } else {
        setAssetDashboard(null)
      }

      if (machinesResult.status === "fulfilled" && machinesResult.value?.machines) {
        setMachineTags(indexMachineTags(machinesResult.value.machines))
      } else {
        setMachineTags({ byTarget: new Map(), totalMachines: 0, taggedMachines: 0, untaggedMachines: 0 })
      }

      if (websiteResult.status === "fulfilled" && websiteResult.value && Array.isArray(websiteResult.value.records)) {
        setWebsiteCount(websiteResult.value.records.length)
      } else {
        setWebsiteCount(null)
      }

      if (databaseResult.status === "fulfilled" && databaseResult.value && Array.isArray(databaseResult.value.records)) {
        setDatabaseCount(databaseResult.value.records.length)
      } else {
        setDatabaseCount(null)
      }
    })

    return () => {
      alive = false
      ctrl.abort()
    }
  }, [session, isAdmin, shouldLoadMonitor, shouldLoadAssets, cmdbEnabled, websiteEnabled, databaseEnabled, refreshVersion])

  async function handleRunFleetHealthCheck() {
    if (collectingFleet || targets.length === 0) return
    setCollectingFleet(true)
    setFleetNotice(null)
    try {
      let activeSkills: { slug: string; enabled?: boolean }[] = []
      try {
        const fetched = await fetchPlatformSkills(session, {})
        activeSkills = fetched.skills || []
      } catch {
        // Fallback to default core skill slugs inside resolveFleetInspectionSkills.
      }
      const fleetSkills = resolveFleetInspectionSkills(targets, activeSkills)
      if (fleetSkills.length === 0) {
        setFleetNotice({
          tone: "error",
          text: "当前没有已启用的系统巡检 Skill，请先在「插件中心 → Skill 能力包」启用。",
        })
        return
      }
      const res = await triggerMonitorRun(session, {
        scope_mode: "all_online",
        skills: fleetSkills,
      })
      setFleetNotice({
        tone: "success",
        text: `全网体检已完成（${res.run.summary || `写入 ${res.run.metrics_written} 项指标`}）`,
        runId: res.run.id,
      })
      onRefresh()
      setRefreshVersion((v) => v + 1)
    } catch (err) {
      setFleetNotice({
        tone: "error",
        text: err instanceof Error ? err.message : "发起全网体检失败",
      })
    } finally {
      setCollectingFleet(false)
    }
  }

  function changeNodeView(next: NodeWorkbenchView) {
    setNodeView(next)
    try {
      window.localStorage.setItem(NODE_VIEW_STORAGE_KEY, next)
    } catch {
      // ignore storage errors
    }
  }

  const metrics = useMemo(() => computeTargetMetrics(targets), [targets])
  const modeBreakdown = useMemo(() => buildTargetModeBreakdown(targets), [targets])
  const telemetryByTarget = useMemo(
    () => indexHostTelemetry(monitorOverview?.resources),
    [monitorOverview?.resources],
  )
  const nodeRows = useMemo(
    () => buildNodeRows(targets, telemetryByTarget, machineTags.byTarget, nodeFilter, nodeQuery),
    [targets, telemetryByTarget, machineTags.byTarget, nodeFilter, nodeQuery],
  )
  const filteredClusters = useMemo(
    () => buildClusterGroups(targets, telemetryByTarget, nodeFilter, nodeQuery, machineTags.byTarget),
    [targets, telemetryByTarget, nodeFilter, nodeQuery, machineTags.byTarget],
  )
  const attention = useMemo(
    () => buildAttentionItems({ onlineTargets: targets, directoryTargets: directory, monitorOverview, maxItems: 12 }),
    [targets, directory, monitorOverview],
  )

  const recentRuns = auditSummary?.total ?? audit.length
  const recentFailed = auditSummary?.failed ?? audit.filter(isFailedAuditEvent).length
  const inspectionRuns = monitorOverview?.recent_runs ?? []
  const monitorSummary = monitorOverview?.summary ?? null

  const activeActivityTab =
    activityTabOverride && (activityTabOverride === "audit" || shouldLoadMonitor)
      ? activityTabOverride
      : resolveDefaultActivityTab({ auditDenied, hasInspectionRuns: shouldLoadMonitor && inspectionRuns.length > 0 })

  const totalIssueCount = attention.total
  const hasCriticalIssue = attention.items.some((i) => i.severity === "critical")
  const statusPillText = formatDashboardStatusLine({
    onlineAgents: metrics.agentConnections,
    activeOps: metrics.activeOps,
    issueCount: totalIssueCount,
  })
  const statusDotCls = hasCriticalIssue
    ? "bg-destructive"
    : totalIssueCount > 0 || metrics.onlineZero
      ? "bg-warning"
      : "bg-success"

  const hasResourceSample = Boolean(monitorSummary && monitorSummary.covered_assets > 0)
  const issueTone: "success" | "warning" | "danger" | "muted" = hasCriticalIssue
    ? "danger"
    : totalIssueCount > 0 || recentFailed > 0
      ? "warning"
      : targets.length > 0
        ? "success"
        : "muted"

  const canRunFleetCheck = inspectionEnabled && targets.length > 0
  const showCoreAssetsCard = cmdbEnabled || k8sEnabled || websiteEnabled || databaseEnabled
  const primaryOnlineTarget = targets[0] ?? null

  return (
    <main aria-label="控制台首页" className="min-h-0 flex-1 overflow-auto">
      <div className="px-4 py-4">
        <header className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-border/60 pb-3">
          <div className="flex min-w-0 flex-wrap items-center gap-2.5">
            <h1 className="text-lg font-semibold text-foreground">运行总览</h1>
            <span className="inline-flex items-center gap-2 rounded-full border border-border/80 bg-card px-2.5 py-1 text-xs text-muted-foreground">
              <span className={`size-2 shrink-0 rounded-full ${statusDotCls}`} aria-hidden="true" />
              <span>{statusPillText}</span>
            </span>
          </div>

          <div className="flex flex-wrap items-center gap-1.5">
            {canRunFleetCheck && (
              <Button
                size="sm"
                variant="primary"
                disabled={collectingFleet}
                onClick={() => void handleRunFleetHealthCheck()}
              >
                <Zap size={13} className={collectingFleet ? "animate-pulse" : ""} aria-hidden="true" />
                {collectingFleet ? "全网体检中…" : "一键全网体检"}
              </Button>
            )}
            {isAdmin && (
              <Button size="sm" variant="ghost" onClick={onDeployAgent}>
                <PlusIcon width={14} height={14} aria-hidden="true" />
                接入 Agent
              </Button>
            )}
            <Button
              onClick={() => {
                onRefresh()
                setRefreshVersion((version) => version + 1)
              }}
              disabled={loading}
              size="sm"
            >
              <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} aria-hidden="true" />
              刷新
            </Button>
          </div>
        </header>

        {fleetNotice && (
          <StatusBanner
            tone={fleetNotice.tone === "error" ? "error" : "success"}
            compact
            className="mb-3 flex flex-wrap items-center justify-between gap-2"
          >
            <span>{fleetNotice.text}</span>
            <div className="flex items-center gap-1">
              {fleetNotice.runId && automationEnabled && onOpenInspectionRun && (
                <Button size="sm" variant="ghost" onClick={() => onOpenInspectionRun(fleetNotice.runId!)}>
                  查看报告
                </Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => setFleetNotice(null)}>
                关闭
              </Button>
            </div>
          </StatusBanner>
        )}

        <Grid className="mb-4" cols={2} lgCols={4} gap={3}>
          <KpiCard
            label="在线节点"
            value={managed ? `${metrics.agentConnections}/${managed.active}` : metrics.agentConnections}
            icon={PlugIcon}
            tone={metrics.agentConnections > 0 ? "primary" : "muted"}
            hint={managedError ? "实例状态暂不可用" : modeBreakdown.label}
            onClick={
              onOpenDestination && inspectionEnabled
                ? () => onOpenDestination({ view: "assets", area: "board" })
                : onOpenDestination && cmdbEnabled
                  ? () => onOpenDestination({ view: "assets", area: "machines" })
                  : undefined
            }
          />
          <KpiCard
            label="健康与告警"
            value={totalIssueCount}
            valueSuffix={totalIssueCount > 0 ? "项异常" : targets.length > 0 ? "全部正常" : undefined}
            valueTone={hasCriticalIssue ? "danger" : totalIssueCount > 0 ? "warning" : targets.length > 0 ? "success" : undefined}
            icon={ShieldIcon}
            tone={issueTone}
            hint={
              totalIssueCount > 0
                ? `${attention.items.filter((i) => i.severity === "critical").length} 严重 · ${attention.items.filter((i) => i.severity !== "critical").length} 告警/离线`
                : recentFailed > 0
                  ? `近期操作失败 ${recentFailed} 次`
                  : "无活跃阈值告警或失败任务"
            }
            onClick={
              onOpenDestination && inspectionEnabled
                ? () => onOpenDestination({ view: "assets", area: "monitor", monitorSub: "fleet" })
                : undefined
            }
          />
          {inspectionEnabled ? (
            <ResourceWaterlineKpiCard
              loading={monitorLoading && !monitorSummary}
              summary={monitorSummary}
              hasSample={hasResourceSample}
              onlineCount={metrics.agentConnections}
              collecting={collectingFleet}
              onRunCheck={canRunFleetCheck ? () => void handleRunFleetHealthCheck() : undefined}
              onOpenMonitor={
                onOpenDestination
                  ? () => onOpenDestination({ view: "assets", area: "monitor", monitorSub: "fleet" })
                  : undefined
              }
            />
          ) : (
            <KpiCard
              label="活跃会话"
              value={metrics.activeSessions}
              icon={UsersIcon}
              tone={metrics.activeSessions > 0 ? "primary" : "muted"}
              hint="控制台与终端 RPC 会话"
            />
          )}
          <KpiCard
            label="任务与调度"
            value={metrics.activeOps}
            valueSuffix="执行中"
            icon={RocketIcon}
            tone={metrics.activeOps > 0 ? "warning" : "muted"}
            hint={
              metrics.queuedOps > 0
                ? `排队 ${metrics.queuedOps} · 定时 ${monitorSummary?.active_schedules ?? 0}`
                : automationEnabled && monitorSummary
                  ? `定时任务 ${monitorSummary.active_schedules} · 近期操作 ${auditLoading || auditDenied || auditError ? "—" : recentRuns}`
                  : `活跃会话 ${metrics.activeSessions} · 近期操作 ${auditLoading || auditDenied || auditError ? "—" : recentRuns}`
            }
            onClick={
              onOpenDestination && automationEnabled
                ? () => onOpenDestination({ view: "console", tab: "tasks" })
                : undefined
            }
          />
        </Grid>

        {!loading && targets.length === 0 && (
          <div className="mb-4">
            <EmptyState isAdmin={isAdmin} onDeployAgent={onDeployAgent} />
          </div>
        )}

        {attention.total > 0 && (
          <AttentionSection
            items={attention.items}
            total={attention.total}
            inspectionEnabled={inspectionEnabled}
            automationEnabled={automationEnabled}
            onOpenDestination={onOpenDestination}
            onOpenTargetTool={onOpenTargetTool}
            onOpenInspectionRun={onOpenInspectionRun}
          />
        )}

        <div className="grid grid-cols-1 gap-4 xl:grid-cols-12">
          <Stack gap={3} className="xl:col-span-8">
            <NodeWorkbenchSection
              totalTargets={targets.length}
              rows={nodeRows}
              clusters={filteredClusters}
              telemetryByTarget={telemetryByTarget}
              tagsByTarget={machineTags.byTarget}
              filter={nodeFilter}
              onFilterChange={setNodeFilter}
              query={nodeQuery}
              onQueryChange={setNodeQuery}
              viewMode={nodeView}
              onViewModeChange={changeNodeView}
              showAll={showAllNodes}
              onToggleShowAll={() => setShowAllNodes((v) => !v)}
              inspectionEnabled={inspectionEnabled}
              onOpenDestination={onOpenDestination}
              onOpenTargetTool={onOpenTargetTool}
            />

            {pluginState && onOpenDestination && (
              <HomePluginWidgetGrid
                session={session}
                pluginState={pluginState}
                refreshVersion={refreshVersion}
                onOpenDestination={onOpenDestination}
              />
            )}
          </Stack>

          <Stack gap={3} className="xl:col-span-4">
            {showCoreAssetsCard && onOpenDestination && (
              <CoreAssetsCard
                dashboard={assetDashboard}
                dashboardError={assetDashboardError}
                machineTags={machineTags}
                websiteCount={websiteCount}
                databaseCount={databaseCount}
                cmdbEnabled={cmdbEnabled}
                k8sEnabled={k8sEnabled}
                websiteEnabled={websiteEnabled}
                databaseEnabled={databaseEnabled}
                onOpenDestination={onOpenDestination}
              />
            )}

            <ActivityFeedCard
              activeTab={activeActivityTab}
              onSelectTab={(next) => setActivityTabOverride(next)}
              showRunsTab={shouldLoadMonitor}
              auditLoading={auditLoading}
              audit={audit}
              auditDenied={auditDenied}
              auditError={auditError}
              auditSummary={auditSummary}
              failedCount={recentFailed}
              monitorLoading={monitorLoading}
              runs={inspectionRuns}
              automationEnabled={automationEnabled}
              primaryTarget={primaryOnlineTarget}
              onOpenInspectionRun={onOpenInspectionRun}
              onOpenDestination={onOpenDestination}
              onOpenTargetTool={onOpenTargetTool}
            />
          </Stack>
        </div>
      </div>
    </main>
  )
}

function computeTargetMetrics(targets: Target[]) {
  return {
    agentConnections: targets.length,
    onlineZero: targets.length === 0,
    activeSessions: targets.reduce((sum, target) => sum + (target.sessions?.length || 0), 0),
    busyCount: targets.filter((target) => target.busy).length,
    activeOps: targets.reduce((sum, target) => sum + (target.active_ops || 0), 0),
    queuedOps: targets.reduce((sum, target) => sum + (target.queued_ops || 0), 0),
  }
}

function ResourceWaterlineKpiCard({
  loading,
  summary,
  hasSample,
  onlineCount,
  collecting,
  onRunCheck,
  onOpenMonitor,
}: {
  loading: boolean
  summary: MonitorOverviewData["summary"] | null
  hasSample: boolean
  onlineCount: number
  collecting: boolean
  onRunCheck?: () => void
  onOpenMonitor?: () => void
}) {
  const rows = hasSample && summary
    ? [
        { label: "CPU", value: summary.avg_cpu_percent },
        { label: "内存", value: summary.avg_memory_percent },
        { label: "磁盘", value: summary.avg_disk_percent },
      ]
    : []

  return (
    <Card className={onOpenMonitor ? "transition-colors hover:border-primary/40" : ""}>
      <CardContent className="flex h-full flex-col justify-between gap-2 p-3.5">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Gauge size={14} className="text-primary" aria-hidden="true" />
            <span>全网资源水位</span>
          </div>
          {onOpenMonitor && (
            <button
              type="button"
              onClick={onOpenMonitor}
              className="inline-flex items-center gap-0.5 text-2xs text-muted-foreground hover:text-foreground"
            >
              {hasSample && summary ? `覆盖 ${summary.covered_assets}/${summary.total_assets}` : "监控"}
              <ChevronRightIcon width={11} height={11} />
            </button>
          )}
        </div>

        {loading ? (
          <div role="status" className="text-xs text-muted-foreground">读取采集中…</div>
        ) : hasSample ? (
          <div className="space-y-1.5">
            {rows.map((r) => {
              const clamped = Math.max(0, Math.min(100, Number.isFinite(r.value) ? r.value : 0))
              const barCls = clamped >= 90 ? "bg-destructive" : clamped >= 80 ? "bg-warning" : "bg-primary"
              const txtCls = clamped >= 90 ? "text-destructive" : clamped >= 80 ? "text-warning" : "text-foreground"
              return (
                <div key={r.label} className="grid grid-cols-[2rem_1fr_2.4rem] items-center gap-2 text-2xs">
                  <span className="text-muted-foreground">{r.label}</span>
                  <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div className={`h-full rounded-full ${barCls}`} style={{ width: `${Math.max(2, clamped)}%` }} />
                  </div>
                  <span className={`text-right font-mono tabular-nums ${txtCls}`}>{clamped.toFixed(0)}%</span>
                </div>
              )
            })}
          </div>
        ) : (
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <div className="text-sm font-semibold text-muted-foreground">尚未采样</div>
              <div className="truncate text-2xs text-muted-foreground/80">
                {onlineCount > 0 ? `覆盖 0/${onlineCount} 节点` : "接入节点后可采样"}
              </div>
            </div>
            {onRunCheck && (
              <Button size="sm" variant="primary" disabled={collecting} onClick={onRunCheck}>
                {collecting ? "采集中…" : "立即体检"}
              </Button>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export function CurrentMachineSection({ selected }: { selected: Target }) {
  return (
    <section className="card-surface p-5">
      <SectionTitle icon={ServerIcon} title="当前机器" />
      <div className="flex items-start gap-3">
        <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-primary/15 text-primary">
          <ServerIcon width={20} height={20} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-base font-semibold text-foreground">{selected.instance}</h3>
            <Badge>{targetModeLabel(selected.mode)}</Badge>
            <StatusPill busy={selected.busy} activeOps={selected.active_ops} status={selected.status} />
          </div>
          <p className="mt-0.5 font-mono text-xs text-muted-foreground">{selected.cluster}</p>
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span>在线连接 {selected.status === "offline" ? 0 : 1}</span>
            <span>活跃会话 {selected.sessions?.length || 0}</span>
            <span>执行中 {selected.active_ops || 0}</span>
            <span>排队 {selected.queued_ops || 0}</span>
            {selected.connected_at ? (
              <span>已连接 {fmtConnectedDuration(selected.connected_at)}</span>
            ) : null}
            {selected.last_seen ? <span>最近心跳 {fmtRelative(selected.last_seen)}</span> : null}
          </div>
          {selected.remote ? (
            <p className="mt-1 font-mono text-2xs text-muted-foreground/80">{selected.remote}</p>
          ) : null}
          {selected.busy && selected.busy_reason ? (
            <p className="mt-1 text-xs text-warning">{busyReasonLabel(selected.busy_reason)}</p>
          ) : null}
        </div>
      </div>
    </section>
  )
}

function AttentionSection({
  items,
  total,
  inspectionEnabled,
  automationEnabled,
  onOpenDestination,
  onOpenTargetTool,
  onOpenInspectionRun,
}: {
  items: DashboardAttentionItem[]
  total: number
  inspectionEnabled: boolean
  automationEnabled: boolean
  onOpenDestination?: (dest: ConsoleNavEntry["destination"]) => void
  onOpenTargetTool?: (target: Target, tab: TargetTab) => void
  onOpenInspectionRun?: (runId: string) => void
}) {
  const [expanded, setExpanded] = useState(false)
  const hasCritical = items.some((i) => i.severity === "critical")
  const displayed = expanded ? items : items.slice(0, DEFAULT_VISIBLE_ALERTS)
  const hiddenCount = Math.max(0, items.length - displayed.length)

  return (
    <section
      aria-label="待处理异常"
      className={`mb-4 rounded-xl border p-3 ${
        hasCritical ? "border-destructive/40 bg-destructive/5" : "border-warning/40 bg-warning/5"
      }`}
    >
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <AlertTriangle
            size={15}
            aria-hidden="true"
            className={hasCritical ? "text-destructive" : "text-warning"}
          />
          <h2 className="text-sm font-semibold text-foreground">待处理异常</h2>
          <Badge variant={hasCritical ? "danger" : "warning"}>{total} 项</Badge>
        </div>
        <div className="flex items-center gap-2">
          {items.length > DEFAULT_VISIBLE_ALERTS && (
            <Button size="sm" variant="ghost" onClick={() => setExpanded((v) => !v)}>
              {expanded ? "收起" : `展开其余 ${hiddenCount} 项`}
            </Button>
          )}
          {inspectionEnabled && onOpenDestination && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => onOpenDestination({ view: "assets", area: "monitor", monitorSub: "fleet" })}
            >
              全部监控
              <ChevronRightIcon width={13} height={13} />
            </Button>
          )}
        </div>
      </div>
      <div className="divide-y divide-border/50">
        {displayed.map((item) => (
          <div key={item.id} className="flex flex-wrap items-center justify-between gap-2 py-1.5 first:pt-0 last:pb-0">
            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 text-xs">
              <Badge
                variant={
                  item.severity === "critical"
                    ? "danger"
                    : item.severity === "warning"
                      ? "warning"
                      : "default"
                }
              >
                {item.severity === "critical" ? "严重" : item.severity === "warning" ? "告警" : "离线"}
              </Badge>
              <span className="truncate font-mono font-medium text-foreground">{item.title}</span>
              <span className="min-w-0 truncate text-muted-foreground">{item.detail}</span>
            </div>
            <div className="flex shrink-0 items-center gap-1">
              {item.targetKey && inspectionEnabled && onOpenDestination && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    onOpenDestination({
                      view: "assets",
                      area: "monitor",
                      monitorSub: "target",
                      targetKey: item.targetKey,
                    })
                  }
                >
                  监控
                </Button>
              )}
              {item.target && onOpenTargetTool && (
                <Button size="sm" variant="ghost" onClick={() => onOpenTargetTool(item.target!, "ask")}>
                  AI 排查
                </Button>
              )}
              {item.runId && automationEnabled && onOpenInspectionRun && (
                <Button size="sm" variant="ghost" onClick={() => onOpenInspectionRun(item.runId!)}>
                  查看报告
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}

function NodeWorkbenchSection({
  totalTargets,
  rows,
  clusters,
  telemetryByTarget,
  tagsByTarget,
  filter,
  onFilterChange,
  query,
  onQueryChange,
  viewMode,
  onViewModeChange,
  showAll,
  onToggleShowAll,
  inspectionEnabled,
  onOpenDestination,
  onOpenTargetTool,
}: {
  totalTargets: number
  rows: DashboardNodeRow[]
  clusters: DashboardClusterGroup[]
  telemetryByTarget: Map<string, TargetTelemetry>
  tagsByTarget: Map<string, string[]>
  filter: ClusterFilterMode
  onFilterChange: (next: ClusterFilterMode) => void
  query: string
  onQueryChange: (next: string) => void
  viewMode: NodeWorkbenchView
  onViewModeChange: (next: NodeWorkbenchView) => void
  showAll: boolean
  onToggleShowAll: () => void
  inspectionEnabled: boolean
  onOpenDestination?: (dest: ConsoleNavEntry["destination"]) => void
  onOpenTargetTool?: (target: Target, tab: TargetTab) => void
}) {
  const [expandedClusters, setExpandedClusters] = useState<Record<string, boolean>>({})
  const displayedRows = showAll ? rows : rows.slice(0, DEFAULT_VISIBLE_NODE_ROWS)
  const displayedClusters = showAll ? clusters : clusters.slice(0, DEFAULT_VISIBLE_CLUSTERS)

  return (
    <section aria-label="实时节点工作台" className="card-surface p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <ServerIcon width={15} height={15} className="text-primary" aria-hidden="true" />
            实时节点工作台
          </h2>
          <Badge variant="outline" className="font-mono">
            {totalTargets} 在线
          </Badge>
        </div>

        {totalTargets > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedControl<ClusterFilterMode>
              ariaLabel="按类型筛选在线节点"
              value={filter}
              onChange={onFilterChange}
              options={[
                { value: "all", label: "全部" },
                { value: "busy", label: "异常/忙碌" },
                { value: "k8s", label: "K8s" },
                { value: "host", label: "主机" },
              ]}
            />
            {totalTargets >= 4 && (
              <div className="relative">
                <Search
                  size={13}
                  aria-hidden="true"
                  className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground"
                />
                <Input
                  scale="sm"
                  value={query}
                  onChange={(e) => onQueryChange(e.target.value)}
                  placeholder="搜索节点 / 标签 / IP"
                  aria-label="搜索节点、标签或 IP"
                  className="w-44 pl-7"
                />
              </div>
            )}
            <SegmentedControl<NodeWorkbenchView>
              ariaLabel="切换节点显示方式"
              value={viewMode}
              onChange={onViewModeChange}
              options={[
                { value: "table", label: "列表" },
                { value: "groups", label: "分组" },
              ]}
            />
          </div>
        )}
      </div>

      {totalTargets === 0 ? (
        <p className="py-4 text-center text-sm text-muted-foreground">当前没有已连接的在线节点。</p>
      ) : rows.length === 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-background/50 px-3 py-4 text-xs text-muted-foreground">
          <span>当前筛选条件下没有匹配的在线节点。</span>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              onFilterChange("all")
              onQueryChange("")
            }}
          >
            重置筛选
          </Button>
        </div>
      ) : viewMode === "table" ? (
        <div className="space-y-2.5">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>节点（实例 / 分组）</TableHead>
                  <TableHead className="hidden md:table-cell">业务与栈标签</TableHead>
                  <TableHead>实时水位 / 状态</TableHead>
                  <TableHead className="text-right">快捷操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {displayedRows.map(({ target, telemetry, tags, isK8s }) => {
                  const dotCls =
                    telemetry?.status === "critical"
                      ? "bg-destructive"
                      : telemetry?.status === "warning" || target.busy
                        ? "bg-warning"
                        : "bg-success"
                  const visibleTags = tags.slice(0, 2)
                  const extraTags = Math.max(0, tags.length - visibleTags.length)
                  const loadTone =
                    telemetry?.status === "critical"
                      ? "text-destructive"
                      : telemetry?.status === "warning"
                        ? "text-warning"
                        : "text-muted-foreground"
                  return (
                    <TableRow key={target.key} className="group">
                      <TableCell className="py-2">
                        <div className="flex items-center gap-2">
                          <span className={`size-2 shrink-0 rounded-full ${dotCls}`} aria-hidden="true" />
                          <div className="min-w-0">
                            <div className="flex items-center gap-1.5">
                              {onOpenTargetTool ? (
                                <button
                                  type="button"
                                  onClick={() => onOpenTargetTool(target, "overview")}
                                  className="truncate font-mono text-xs font-semibold text-foreground hover:text-primary hover:underline"
                                  title={`打开 ${target.key} 概览`}
                                >
                                  {target.instance}
                                </button>
                              ) : (
                                <span className="truncate font-mono text-xs font-semibold text-foreground">
                                  {target.instance}
                                </span>
                              )}
                              <Badge variant="outline">{isK8s ? "K8s" : targetModeLabel(target.mode)}</Badge>
                            </div>
                            <div className="truncate font-mono text-2xs text-muted-foreground">
                              {target.cluster}
                              {target.remote ? ` · ${target.remote}` : ""}
                            </div>
                          </div>
                        </div>
                      </TableCell>

                      <TableCell className="hidden py-2 md:table-cell">
                        {visibleTags.length > 0 ? (
                          <div className="flex flex-wrap items-center gap-1">
                            {visibleTags.map((tag) => (
                              <Badge key={tag} variant="primary" title={tag}>
                                {formatTagChipLabel(tag)}
                              </Badge>
                            ))}
                            {extraTags > 0 && (
                              <Badge variant="default" title={tags.slice(2).join(" · ")}>
                                +{extraTags}
                              </Badge>
                            )}
                          </div>
                        ) : (
                          <span className="text-2xs text-muted-foreground/70">—</span>
                        )}
                      </TableCell>

                      <TableCell className="py-2">
                        <div className="flex flex-wrap items-center gap-1.5">
                          {telemetry && (telemetry.cpuPercent != null || telemetry.memoryPercent != null) ? (
                            <span className={`font-mono text-xs tabular-nums ${loadTone}`}>
                              {telemetry.cpuPercent != null ? `CPU ${telemetry.cpuPercent.toFixed(0)}%` : ""}
                              {telemetry.cpuPercent != null && telemetry.memoryPercent != null ? " · " : ""}
                              {telemetry.memoryPercent != null ? `内存 ${telemetry.memoryPercent.toFixed(0)}%` : ""}
                            </span>
                          ) : (target.active_ops || 0) > 0 ? (
                            <Badge variant="warning">{target.active_ops} 执行中</Badge>
                          ) : target.busy ? (
                            <Badge variant="warning">独占中</Badge>
                          ) : (
                            <span className="text-xs text-muted-foreground">空闲</span>
                          )}
                          {(target.active_ops || 0) > 0 && telemetry && (
                            <Badge variant="warning">{target.active_ops} 运行</Badge>
                          )}
                        </div>
                      </TableCell>

                      <TableCell className="py-2 text-right">
                        <div className="inline-flex items-center justify-end gap-0.5 opacity-75 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
                          {onOpenTargetTool && (
                            <>
                              <Button
                                size="sm"
                                variant="ghost"
                                className="px-2"
                                title={`在 ${target.instance} 打开 AI 助手`}
                                aria-label={`在 ${target.instance} 打开 AI 助手`}
                                onClick={() => onOpenTargetTool(target, "ask")}
                              >
                                <SparkIcon width={13} height={13} aria-hidden="true" />
                                <span className="hidden sm:inline">AI</span>
                              </Button>
                              <Button
                                size="sm"
                                variant="ghost"
                                className="px-2"
                                title={`在 ${target.instance} 打开终端`}
                                aria-label={`在 ${target.instance} 打开终端`}
                                onClick={() => onOpenTargetTool(target, "terminal")}
                              >
                                <TerminalIcon width={13} height={13} aria-hidden="true" />
                                <span className="hidden sm:inline">终端</span>
                              </Button>
                            </>
                          )}
                          {inspectionEnabled && onOpenDestination && (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="px-2"
                              title={`查看 ${target.instance} 资源监控`}
                              aria-label={`查看 ${target.instance} 资源监控`}
                              onClick={() =>
                                onOpenDestination({
                                  view: "assets",
                                  area: "monitor",
                                  monitorSub: "target",
                                  targetKey: target.key,
                                })
                              }
                            >
                              <ActivityIcon width={13} height={13} aria-hidden="true" />
                            </Button>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>

          {rows.length > DEFAULT_VISIBLE_NODE_ROWS && (
            <div className="flex items-center justify-between pt-1 text-xs text-muted-foreground">
              <span>
                显示 {displayedRows.length} / {rows.length} 台节点（异常与高负载自动置顶）
              </span>
              <Button size="sm" variant="ghost" onClick={onToggleShowAll}>
                {showAll ? "收起节点列表" : `展开其余 ${rows.length - DEFAULT_VISIBLE_NODE_ROWS} 台节点`}
              </Button>
            </div>
          )}
        </div>
      ) : (
        <div className="space-y-3">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {displayedClusters.map((cluster) => {
              const isExpanded = Boolean(expandedClusters[cluster.name])
              const visibleMachines = isExpanded
                ? cluster.machines
                : cluster.machines.slice(0, DEFAULT_VISIBLE_NODES_PER_CLUSTER)
              const hiddenCount = cluster.machines.length - visibleMachines.length
              return (
                <div
                  key={cluster.name}
                  className="flex flex-col rounded-lg border bg-background/60 p-3 transition-colors hover:border-primary/35"
                >
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-mono text-sm font-semibold text-foreground">
                        {cluster.name}
                      </span>
                      <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                        {cluster.connections} 台
                      </span>
                    </div>
                    <ClusterHealthBadge cluster={cluster} />
                  </div>

                  <div className="divide-y divide-border/50">
                    {visibleMachines.map((machine) => {
                      const telemetry = telemetryByTarget.get(machine.key)
                      const tags = (tagsByTarget.get(machine.key) ?? []).slice(0, 1)
                      const isK8s = isTargetKubernetes(machine)
                      const dotCls =
                        telemetry?.status === "critical"
                          ? "bg-destructive"
                          : telemetry?.status === "warning" || machine.busy
                            ? "bg-warning"
                            : "bg-success"
                      return (
                        <div
                          key={machine.key}
                          className="group flex items-center justify-between gap-2 py-1.5 first:pt-0 last:pb-0"
                        >
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-1.5">
                              <span className={`size-2 shrink-0 rounded-full ${dotCls}`} aria-hidden="true" />
                              {onOpenTargetTool ? (
                                <button
                                  type="button"
                                  onClick={() => onOpenTargetTool(machine, "overview")}
                                  className="truncate font-mono text-xs font-medium text-foreground hover:text-primary hover:underline"
                                  title={`打开 ${machine.key} 概览`}
                                >
                                  {machine.instance}
                                </button>
                              ) : (
                                <span className="truncate font-mono text-xs font-medium text-foreground">
                                  {machine.instance}
                                </span>
                              )}
                              <Badge variant="outline">{isK8s ? "K8s" : targetModeLabel(machine.mode)}</Badge>
                              {tags.map((tag) => (
                                <Badge key={tag} variant="primary" className="hidden sm:inline-flex" title={tag}>
                                  {formatTagChipLabel(tag)}
                                </Badge>
                              ))}
                            </div>
                            {telemetry && (telemetry.cpuPercent != null || telemetry.memoryPercent != null) && (
                              <div className="mt-0.5 pl-3.5 font-mono text-2xs tabular-nums text-muted-foreground">
                                {telemetry.cpuPercent != null ? `CPU ${telemetry.cpuPercent.toFixed(0)}%` : ""}
                                {telemetry.cpuPercent != null && telemetry.memoryPercent != null ? " · " : ""}
                                {telemetry.memoryPercent != null ? `内存 ${telemetry.memoryPercent.toFixed(0)}%` : ""}
                              </div>
                            )}
                          </div>

                          <div className="flex shrink-0 items-center gap-0.5 opacity-75 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
                            {onOpenTargetTool && (
                              <>
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  className="px-2"
                                  title={`在 ${machine.instance} 打开 AI 助手`}
                                  aria-label={`在 ${machine.instance} 打开 AI 助手`}
                                  onClick={() => onOpenTargetTool(machine, "ask")}
                                >
                                  <SparkIcon width={13} height={13} aria-hidden="true" />
                                </Button>
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  className="px-2"
                                  title={`在 ${machine.instance} 打开终端`}
                                  aria-label={`在 ${machine.instance} 打开终端`}
                                  onClick={() => onOpenTargetTool(machine, "terminal")}
                                >
                                  <TerminalIcon width={13} height={13} aria-hidden="true" />
                                </Button>
                              </>
                            )}
                            {inspectionEnabled && onOpenDestination && (
                              <Button
                                size="sm"
                                variant="ghost"
                                className="px-2"
                                title={`查看 ${machine.instance} 资源监控`}
                                aria-label={`查看 ${machine.instance} 资源监控`}
                                onClick={() =>
                                  onOpenDestination({
                                    view: "assets",
                                    area: "monitor",
                                    monitorSub: "target",
                                    targetKey: machine.key,
                                  })
                                }
                              >
                                <ActivityIcon width={13} height={13} aria-hidden="true" />
                              </Button>
                            )}
                          </div>
                        </div>
                      )
                    })}
                  </div>

                  {hiddenCount > 0 && (
                    <button
                      type="button"
                      onClick={() =>
                        setExpandedClusters((prev) => ({ ...prev, [cluster.name]: !prev[cluster.name] }))
                      }
                      className="mt-2 self-start text-xs text-primary hover:underline"
                    >
                      展开其余 {hiddenCount} 台节点
                    </button>
                  )}
                  {isExpanded && cluster.machines.length > DEFAULT_VISIBLE_NODES_PER_CLUSTER && (
                    <button
                      type="button"
                      onClick={() =>
                        setExpandedClusters((prev) => ({ ...prev, [cluster.name]: false }))
                      }
                      className="mt-2 self-start text-xs text-muted-foreground hover:underline"
                    >
                      收起节点
                    </button>
                  )}
                </div>
              )
            })}
          </div>

          {clusters.length > DEFAULT_VISIBLE_CLUSTERS && (
            <div className="flex justify-center">
              <Button size="sm" variant="ghost" onClick={onToggleShowAll}>
                {showAll ? "收起分组列表" : `展开全部 ${clusters.length} 个分组`}
              </Button>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

function CoreAssetsCard({
  dashboard,
  dashboardError,
  machineTags,
  websiteCount,
  databaseCount,
  cmdbEnabled,
  k8sEnabled,
  websiteEnabled,
  databaseEnabled,
  onOpenDestination,
}: {
  dashboard: AssetDashboardSummary | null
  dashboardError: string
  machineTags: MachineTagSummary
  websiteCount: number | null
  databaseCount: number | null
  cmdbEnabled: boolean
  k8sEnabled: boolean
  websiteEnabled: boolean
  databaseEnabled: boolean
  onOpenDestination: (dest: ConsoleNavEntry["destination"]) => void
}) {
  const totalNamespaces = useMemo(
    () => (dashboard?.clusters ?? []).reduce((sum, c) => sum + (c.namespaces || 0), 0),
    [dashboard],
  )
  const unassignedCount = dashboard?.unassigned?.length ?? 0

  const tiles: {
    id: string
    label: string
    value: string
    hint: string
    dest: ConsoleNavEntry["destination"]
  }[] = []

  if (cmdbEnabled) {
    const machineHint =
      machineTags.totalMachines > 0
        ? `已打标 ${machineTags.taggedMachines} · 未打标 ${machineTags.untaggedMachines}`
        : "物理机与虚拟机"
    tiles.push({
      id: "machines",
      label: "机器台账",
      value: dashboard ? String(dashboard.machines) : "—",
      hint: machineHint,
      dest: { view: "assets", area: "machines" },
    })
  }
  if (k8sEnabled) {
    tiles.push({
      id: "clusters",
      label: "K8s 集群",
      value: dashboard ? String(dashboard.clusters.length) : "—",
      hint: dashboard ? `${totalNamespaces} 命名空间` : "Kubernetes 集群台账",
      dest: { view: "assets", area: "clusters" },
    })
  }
  if (cmdbEnabled) {
    tiles.push({
      id: "systems",
      label: "业务系统",
      value: dashboard ? String(dashboard.systems) : "—",
      hint: "服役系统与依赖拓扑",
      dest: { view: "assets", area: "systems" },
    })
  }
  if (websiteEnabled && databaseEnabled) {
    tiles.push({
      id: "catalog-web-db",
      label: "网站 / 数据库",
      value: `${websiteCount ?? "—"} / ${databaseCount ?? "—"}`,
      hint: "已登记受管资源",
      dest: { view: "catalog", kind: "website" },
    })
  } else if (websiteEnabled) {
    tiles.push({
      id: "catalog-web",
      label: "网站目录",
      value: websiteCount != null ? String(websiteCount) : "—",
      hint: "受管站点与域名",
      dest: { view: "catalog", kind: "website" },
    })
  } else if (databaseEnabled) {
    tiles.push({
      id: "catalog-db",
      label: "数据库目录",
      value: databaseCount != null ? String(databaseCount) : "—",
      hint: "受管数据库实例",
      dest: { view: "catalog", kind: "database" },
    })
  }

  return (
    <section aria-label="核心资产规模" className="card-surface p-4">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <LayersIcon width={15} height={15} className="text-primary" aria-hidden="true" />
          核心资产规模
        </h2>
        {dashboardError ? (
          <span className="truncate text-2xs text-warning" title={dashboardError}>
            统计暂不可用
          </span>
        ) : unassignedCount > 0 && cmdbEnabled ? (
          <button
            type="button"
            onClick={() => onOpenDestination({ view: "assets", area: "machines" })}
            className="text-2xs text-warning hover:underline"
          >
            {unassignedCount} 台待归属
          </button>
        ) : null}
      </div>

      {tiles.length > 0 && (
        <div className="grid grid-cols-2 gap-2">
          {tiles.map((tile) => (
            <button
              key={tile.id}
              type="button"
              onClick={() => onOpenDestination(tile.dest)}
              className="group rounded-lg border bg-background/60 p-3 text-left transition-colors hover:border-primary/40 hover:bg-muted/30"
            >
              <div className="flex items-center justify-between gap-1 text-xs text-muted-foreground">
                <span>{tile.label}</span>
                <ChevronRightIcon
                  width={12}
                  height={12}
                  className="opacity-60 transition-transform group-hover:translate-x-0.5"
                />
              </div>
              <div className="mt-1 font-mono text-lg font-semibold leading-none tabular-nums text-foreground">
                {tile.value}
              </div>
              <div className="mt-1 truncate text-2xs text-muted-foreground">{tile.hint}</div>
            </button>
          ))}
        </div>
      )}
    </section>
  )
}

function ActivityFeedCard({
  activeTab,
  onSelectTab,
  showRunsTab,
  auditLoading,
  audit,
  auditDenied,
  auditError,
  auditSummary,
  failedCount,
  monitorLoading,
  runs,
  automationEnabled,
  primaryTarget,
  onOpenInspectionRun,
  onOpenDestination,
  onOpenTargetTool,
}: {
  activeTab: "audit" | "runs"
  onSelectTab: (next: "audit" | "runs") => void
  showRunsTab: boolean
  auditLoading: boolean
  audit: AuditEvent[]
  auditDenied: boolean
  auditError: string
  auditSummary: AuditSummary | null
  failedCount: number
  monitorLoading: boolean
  runs: MonitorRunRecord[]
  automationEnabled: boolean
  primaryTarget: Target | null
  onOpenInspectionRun?: (runId: string) => void
  onOpenDestination?: (dest: ConsoleNavEntry["destination"]) => void
  onOpenTargetTool?: (target: Target, tab: TargetTab) => void
}) {
  return (
    <section aria-label="近期动态" className="card-surface p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <HistoryIcon width={15} height={15} className="text-primary" aria-hidden="true" />
          近期动态
        </h2>
        {showRunsTab ? (
          <SegmentedControl<"audit" | "runs">
            ariaLabel="切换近期动态视图"
            value={activeTab}
            onChange={onSelectTab}
            options={[
              { value: "audit", label: "操作审计" },
              { value: "runs", label: "巡检记录" },
            ]}
          />
        ) : null}
      </div>

      {activeTab === "runs" ? (
        monitorLoading && runs.length === 0 ? (
          <p role="status" className="py-6 text-center text-xs text-muted-foreground">正在读取巡检记录…</p>
        ) : runs.length === 0 ? (
          <div className="space-y-2 py-6 text-center text-xs text-muted-foreground">
            <p>暂无巡检执行记录</p>
            {automationEnabled && onOpenDestination && (
              <Button size="sm" variant="ghost" onClick={() => onOpenDestination({ view: "console", tab: "tasks" })}>
                前往自动化配置定时任务
              </Button>
            )}
          </div>
        ) : (
          <>
            <div className="flex flex-col divide-y divide-border/60">
              {runs.slice(0, 6).map((run) => {
                const st = describeMonitorStatus(run.status)
                const title = summarizeRunTitle(run)
                const trigger = TRIGGER_LABELS[run.trigger_kind] || run.trigger_kind
                return (
                  <div key={run.id} className="flex items-start justify-between gap-2 py-2 first:pt-0 last:pb-0">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <Badge variant={st.variant}>{st.label}</Badge>
                        {automationEnabled && onOpenInspectionRun ? (
                          <button
                            type="button"
                            onClick={() => onOpenInspectionRun(run.id)}
                            className="truncate text-left text-xs font-medium text-foreground hover:text-primary hover:underline"
                            title={title}
                          >
                            {title}
                          </button>
                        ) : (
                          <span className="truncate text-xs font-medium text-foreground" title={title}>
                            {title}
                          </span>
                        )}
                      </div>
                      <div className="mt-1 flex flex-wrap items-center gap-2 text-2xs text-muted-foreground">
                        <span>{trigger}</span>
                        <span className="font-mono">
                          通过 {run.passed_checks}
                          {run.warning_checks > 0 ? ` · 告警 ${run.warning_checks}` : ""}
                          {run.failed_checks > 0 ? ` · 失败 ${run.failed_checks}` : ""}
                        </span>
                        {run.started_at_ms > 0 && (
                          <span className="inline-flex items-center gap-0.5">
                            <Clock size={10} aria-hidden="true" />
                            {fmtRelativeMs(run.started_at_ms)}
                          </span>
                        )}
                      </div>
                    </div>
                    {automationEnabled && onOpenInspectionRun && (
                      <Button size="sm" variant="ghost" className="shrink-0 px-2" onClick={() => onOpenInspectionRun(run.id)}>
                        详情
                      </Button>
                    )}
                  </div>
                )
              })}
            </div>
            {automationEnabled && onOpenDestination && (
              <div className="mt-2.5 border-t border-border/60 pt-2 text-right">
                <button
                  type="button"
                  onClick={() => onOpenDestination({ view: "console", tab: "tasks" })}
                  className="inline-flex items-center gap-0.5 text-xs text-primary hover:underline"
                >
                  查看全部巡检记录
                  <ChevronRightIcon width={12} height={12} />
                </button>
              </div>
            )}
          </>
        )
      ) : auditLoading ? (
        <p role="status" className="py-6 text-center text-xs text-muted-foreground">正在读取近期活动…</p>
      ) : auditError ? (
        <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-6 text-center text-xs text-destructive">
          审计数据暂时不可用：{auditError}
        </p>
      ) : auditDenied ? (
        <p className="py-6 text-center text-xs text-muted-foreground">
          全局审计日志仅管理员可见；可在各节点的 AI 助手 / 终端中查看本人操作记录。
        </p>
      ) : audit.length === 0 ? (
        <p className="py-6 text-center text-xs text-muted-foreground">暂无操作记录</p>
      ) : (
        <>
          {auditSummary ? (
            <div className="mb-2 flex flex-wrap gap-3 text-xs text-muted-foreground">
              <span>共 {auditSummary.total} 次</span>
              <span className="text-success">成功 {auditSummary.success}</span>
              {failedCount > 0 ? <span className="text-destructive">失败 {failedCount}</span> : null}
              {auditSummary.running > 0 ? <span className="text-warning">进行中 {auditSummary.running}</span> : null}
            </div>
          ) : null}
          <div className="flex flex-col divide-y divide-border/60">
            {audit.slice(0, 6).map((event) => (
              <RecentActivityRow key={event.id} event={event} />
            ))}
          </div>
          {(onOpenTargetTool && primaryTarget) || onOpenDestination ? (
            <div className="mt-2.5 border-t border-border/60 pt-2 text-right">
              <button
                type="button"
                onClick={() => {
                  if (primaryTarget && onOpenTargetTool) onOpenTargetTool(primaryTarget, "audit")
                  else onOpenDestination?.({ view: "console", tab: "audit" })
                }}
                className="inline-flex items-center gap-0.5 text-xs text-primary hover:underline"
              >
                查看全部审计记录
                <ChevronRightIcon width={12} height={12} />
              </button>
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}

// 命令摘要只显示一行；超长或含 base64 时悬停看完整内容，点击展开。
function RecentActivityRow({ event }: { event: AuditEvent }) {
  const [expanded, setExpanded] = useState(false)
  const preview = commandPreview(event.command_summary || event.action || "操作")
  const targetLabel =
    event.cluster && event.instance ? `${event.cluster}/${event.instance}` : event.instance || ""
  const actorAndTarget = [event.user_id, targetLabel].filter(Boolean).join(" · ")
  return (
    <div className="py-2 text-xs first:pt-0 last:pb-0">
      <div className="flex items-center gap-1.5">
        <ActionBadge action={event.action} />
        {preview.shortened ? (
          <button
            type="button"
            title={preview.title}
            aria-expanded={expanded}
            onClick={() => setExpanded((v) => !v)}
            className="min-w-0 flex-1 truncate text-left text-foreground hover:underline"
          >
            {preview.text}
          </button>
        ) : (
          <span title={preview.full} className="min-w-0 flex-1 truncate text-foreground">
            {preview.text}
          </span>
        )}
        <StatusTag status={event.status} />
      </div>
      {(actorAndTarget || event.started_at) && (
        <div className="mt-0.5 flex items-center justify-between gap-2 text-2xs text-muted-foreground">
          <span className="truncate font-mono">{actorAndTarget}</span>
          {event.started_at ? <span className="shrink-0">{fmtRelative(event.started_at)}</span> : null}
        </div>
      )}
      {expanded && (
        <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/60 px-3 py-2 font-mono text-xs text-muted-foreground">
          {preview.full}
        </pre>
      )}
    </div>
  )
}

function SectionTitle({
  icon: Icon,
  title,
}: {
  icon: typeof ServerIcon
  title: string
}) {
  return (
    <h2 className="mb-3 flex items-center gap-2 text-sm font-medium text-foreground">
      <Icon width={16} height={16} className="text-primary" />
      {title}
    </h2>
  )
}

function ClusterHealthBadge({ cluster }: { cluster: DashboardClusterGroup }) {
  if (cluster.criticalCount > 0) {
    return <Badge variant="danger" className="rounded-full">{cluster.criticalCount} 严重</Badge>
  }
  if (cluster.warningCount > 0) {
    return <Badge variant="warning" className="rounded-full">{cluster.warningCount} 告警</Badge>
  }
  if (cluster.busy > 0) {
    return (
      <Badge variant={cluster.busy < cluster.connections ? "warning" : "danger"} className="rounded-full">
        {cluster.busy}/{cluster.connections} 独占
      </Badge>
    )
  }
  if (cluster.activeOps > 0) {
    return <Badge variant="primary" className="rounded-full">{cluster.activeOps} 执行中</Badge>
  }
  if (cluster.queuedOps > 0) {
    return <Badge variant="warning" className="rounded-full">{cluster.queuedOps} 排队中</Badge>
  }
  return <Badge variant="success" className="rounded-full">空闲</Badge>
}

function KpiCard({
  label,
  value,
  valueSuffix,
  valueTone,
  icon: Icon,
  tone,
  hint,
  onClick,
}: {
  label: string
  value: number | string
  valueSuffix?: string
  valueTone?: "success" | "warning" | "danger"
  icon: typeof ServerIcon
  tone: "primary" | "success" | "warning" | "danger" | "muted"
  hint?: string
  onClick?: () => void
}) {
  const toneCls =
    tone === "primary"
      ? "bg-primary/15 text-primary"
      : tone === "success"
        ? "bg-success/15 text-success"
        : tone === "warning"
          ? "bg-warning/20 text-warning"
          : tone === "danger"
            ? "bg-destructive/15 text-destructive"
            : "bg-muted text-muted-foreground"
  const valCls =
    valueTone === "danger"
      ? "text-destructive"
      : valueTone === "warning"
        ? "text-warning"
        : valueTone === "success"
          ? "text-success"
          : "text-foreground"

  const inner = (
    <CardContent className="flex items-center gap-3 p-3.5">
      <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl ring-1 ring-inset ring-foreground/5 ${toneCls}`}>
        <Icon width={19} height={19} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-1">
          <span className="text-xs text-muted-foreground">{label}</span>
          {onClick && <ChevronRightIcon width={12} height={12} className="shrink-0 text-muted-foreground/60" />}
        </div>
        <div className="mt-0.5 flex items-baseline gap-1.5">
          <span className={`truncate font-mono text-xl font-semibold leading-tight tabular-nums ${valCls}`}>
            {value}
          </span>
          {valueSuffix && <span className={`shrink-0 text-2xs ${valueTone ? valCls : "text-muted-foreground"}`}>{valueSuffix}</span>}
        </div>
        {hint ? <div className="mt-0.5 truncate text-2xs text-muted-foreground/90">{hint}</div> : null}
      </div>
    </CardContent>
  )

  if (onClick) {
    return (
      <Card className="transition-colors hover:border-primary/40">
        <button type="button" onClick={onClick} className="block w-full text-left">
          {inner}
        </button>
      </Card>
    )
  }

  return <Card>{inner}</Card>
}

// One badge component, so a status reads the same in every panel.
function StatusPill({ busy, activeOps = 0, status }: { busy?: boolean; activeOps?: number; status?: string }) {
  if (status === "offline") return <Badge className="rounded-full">离线</Badge>
  if (busy) return <Badge variant="warning" className="rounded-full">忙碌</Badge>
  if (status === "active" || activeOps > 0) return <Badge variant="primary" className="rounded-full">活动</Badge>
  return <Badge className="rounded-full">空闲</Badge>
}

function ActionBadge({ action }: { action?: string }) {
  const map: Record<string, string> = {
    exec: "命令",
    shell: "命令",
    ask: "AI",
    agent_prompt: "AI",
    read: "读取",
    file_read: "读取",
    write: "写入",
    file_write: "写入",
    push: "上传",
    pull: "下载",
    info: "信息",
    node_info: "信息",
    check: "检查",
    check_deployment: "检查",
    clean: "清理",
    clean_workspace: "清理",
  }
  const key = (action || "").replace(/^doops_/, "")
  return (
    <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground">
      {map[key] || key || "操作"}
    </span>
  )
}

function StatusTag({ status }: { status?: string }) {
  if (!status) return null
  const ok = status === "success" || status === "ok" || status === "completed"
  const fail = isFailedAuditStatus(status)
  const cls = ok ? "text-success" : fail ? "text-destructive" : "text-muted-foreground"
  const label = ok ? "成功" : fail ? "失败" : status
  return <span className={`shrink-0 text-2xs ${cls}`}>{label}</span>
}

function busyReasonLabel(reason: string): string {
  const map: Record<string, string> = {
    exclusive_operation: "独占操作中，其他会话需等待",
    target_queue: "任务排队中，等待当前操作完成",
  }
  return map[reason] || reason
}

function fmtConnectedDuration(iso: string): string {
  const hours = Math.floor((Date.now() - new Date(iso).getTime()) / 3_600_000)
  if (hours < 1) return "不足 1 小时"
  if (hours < 24) return `${hours} 小时`
  return `${Math.floor(hours / 24)} 天`
}

function fmtRelativeMs(ms: number): string {
  const diff = Math.max(0, Date.now() - ms)
  const min = Math.floor(diff / 60000)
  if (min < 1) return "刚刚"
  if (min < 60) return `${min} 分钟前`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}

function fmtRelative(iso: string): string {
  const parsed = new Date(iso).getTime()
  if (!Number.isFinite(parsed)) return ""
  return fmtRelativeMs(parsed)
}

function EmptyState({ isAdmin, onDeployAgent }: { isAdmin: boolean; onDeployAgent: () => void }) {
  const copy = emptyTargetsCopy(isAdmin)
  return (
    <div className="card-surface p-8 text-center">
      <span className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <PlugIcon width={26} height={26} />
      </span>
      <h2 className="text-base font-semibold text-foreground">{copy.title}</h2>
      <p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground text-pretty">
        {copy.description}
      </p>

      <div className={`mx-auto mt-5 grid gap-3 text-left ${copy.showDeployAgent ? "max-w-lg sm:grid-cols-2" : "max-w-sm"}`}>
        {copy.showDeployAgent && (
          <button
            onClick={onDeployAgent}
            className="group flex flex-col rounded-lg border bg-background p-4 text-left transition-colors hover:border-primary/50 hover:bg-muted"
          >
            <span className="mb-1.5 flex items-center gap-2 text-sm font-medium text-foreground">
              <RocketIcon width={16} height={16} className="text-primary" />
              部署 DoOps Agent
            </span>
            <span className="text-xs leading-relaxed text-muted-foreground">
              查看首次接入步骤并连接 Agent。
            </span>
            <span className="mt-2 flex items-center gap-1 text-xs font-medium text-primary">
              查看部署指南
              <ChevronRightIcon
                width={13}
                height={13}
                className="transition-transform group-hover:translate-x-0.5"
              />
            </span>
          </button>
        )}
        <div className="flex flex-col rounded-lg border bg-background p-4">
          <span className="mb-1.5 flex items-center gap-2 text-sm font-medium text-foreground">
            <HelpIcon width={16} height={16} className="text-primary" />
            {copy.contactTitle}
          </span>
          <span className="text-xs leading-relaxed text-muted-foreground">
            {copy.contactHint}
          </span>
        </div>
      </div>
    </div>
  )
}
