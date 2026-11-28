"use client"

import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react"
import {
  AlertTriangle,
  CalendarPlus,
  Download,
  Layers,
  Play,
  Printer,
  RefreshCw,
  Send,
  Sparkles,
  Square,
  Terminal,
  TrendingUp,
} from "lucide-react"
import { callTool, extractText, sessionIsAdmin, type Session, type Target, type ToolCancel } from "@/lib/client"
import { TOOLS, randomSession } from "@/lib/gateway"
import {
  addAgentRunNote,
  addAgentRunOutput,
  addAgentRunResult,
  startAgentRun,
  type AgentRun,
} from "@/lib/agent-output"
import { listCredentials, type CredentialResource } from "@/lib/credentials"
import {
  describeMonitorStatus,
  fetchMonitorRunDetail,
  fetchMonitorRuns,
  formatIntervalLabel,
  mutateMonitorSchedule,
  triggerMonitorRun,
  type MonitorRunItemRecord,
  type MonitorRunRecord,
} from "@/lib/monitor-engine"
import {
  MONITOR_METRIC_RANGES,
  buildTrendGeometry,
  fetchMonitorMetricSeries,
  formatMetricValue,
  type MonitorMetricRange,
  type MonitorMetricSeries,
} from "@/lib/monitor-metrics"
import { formatChartValue, normalizeInspectionCharts, type InspectionChart } from "@/lib/inspection-charts"
import {
  computeInspectionScore,
  normalizeCoverageGaps,
  normalizeInspectionFindings,
  type FindingSeverity,
} from "@/lib/inspection-scoring"
import { renderFormalInspectionReportHtml } from "@/lib/formal-inspection-report"
import {
  CATALOG_INPUT_LABELS,
  type PluginView,
  type SkillPluginAction,
  type SkillPluginInput,
  type SkillPluginPreset,
  type SkillPluginView,
} from "@/lib/plugins/types"
import { AgentRunOutput } from "@/components/agent-run-output"
import { InspectionArtifactViewer } from "@/components/inspection-artifact-viewer"
import { GpuGovernanceWorkbench } from "@/components/plugins/gpu-workbench-view"
import { InlineCatalogManager } from "@/components/plugins/inline-catalog-manager"
import { MachineTagSyncCard } from "@/components/plugins/machine-tag-sync-card"
import { CONSOLE_NAV_ICONS } from "@/components/plugins/registry"
import { shouldAutoLiveProbe, splitPluginViewsForTabs } from "@/lib/plugin-workbench"
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Input,
  PageTabs,
  Select,
  StatusBanner,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Textarea,
} from "@/components/ui"

type ExtractedMetric = {
  key: string
  value: number
  label?: string
  unit?: string
  status?: string
}

function extractArtifactMetricsMap(artifactJson: unknown): Map<string, ExtractedMetric> {
  const out = new Map<string, ExtractedMetric>()
  if (!artifactJson || typeof artifactJson !== "object" || Array.isArray(artifactJson)) {
    return out
  }
  const obj = artifactJson as Record<string, unknown>
  if (Array.isArray(obj.metrics)) {
    for (const raw of obj.metrics) {
      if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue
      const entry = raw as Record<string, unknown>
      const key = typeof entry.key === "string" ? entry.key.trim() : ""
      const value = typeof entry.value === "number" ? entry.value : Number(entry.value)
      if (!key || !Number.isFinite(value)) continue
      out.set(key, {
        key,
        value,
        label: typeof entry.label === "string" ? entry.label : undefined,
        unit: typeof entry.unit === "string" ? entry.unit : undefined,
        status: typeof entry.status === "string" ? entry.status : undefined,
      })
    }
    return out
  }
  for (const [key, val] of Object.entries(obj)) {
    if (typeof val === "number" && Number.isFinite(val)) {
      out.set(key, { key, value: val })
    }
  }
  return out
}

function buildInitialInputs(inputs: SkillPluginInput[] | undefined): Record<string, string | number | boolean> {
  const values: Record<string, string | number | boolean> = {}
  for (const inp of inputs ?? []) {
    if (inp.default !== undefined) {
      values[inp.key] = inp.default
    } else if (inp.type === "boolean") {
      values[inp.key] = false
    } else if (inp.type === "select" && inp.options && inp.options.length > 0) {
      values[inp.key] = inp.options[0].value
    } else if (inp.type === "number") {
      values[inp.key] = inp.min ?? 0
    } else {
      values[inp.key] = ""
    }
  }
  return values
}

function formatRunStamp(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function kpiToneBadge(
  value: number,
  status?: string,
  warn?: number,
  crit?: number,
): "success" | "warning" | "danger" | "outline" {
  if (status === "critical") return "danger"
  if (status === "warning") return "warning"
  if (crit !== undefined && value >= crit) return "danger"
  if (warn !== undefined && value >= warn) return "warning"
  return "success"
}

export function SkillPluginHost({
  session,
  sessionId,
  plugin,
  targets,
  selectedTarget,
  scopedTarget,
  onOpenAskWithPrompt,
  onFillTerminal,
  onOpenTerminalSplit,
  onOpenInspectionRun,
  onOpenPluginCenter,
}: {
  session: Session
  sessionId?: string
  plugin: PluginView
  targets: Target[]
  selectedTarget?: Target | null
  scopedTarget?: Target | null
  onOpenAskWithPrompt?: (prompt: string) => void
  onFillTerminal?: (command: string) => void
  onOpenTerminalSplit?: (prompt?: string) => void
  onOpenInspectionRun?: (runId: string) => void
  onOpenPluginCenter?: () => void
}) {
  const manifest = plugin.manifest
  const skillSlug = plugin.skill_slug || plugin.id.replace(/^skill\./, "")
  const isAdmin = sessionIsAdmin(session)
  const onlineTargets = useMemo(() => targets.filter((t) => t.status !== "offline"), [targets])

  const [targetScope, setTargetScope] = useState<string>(() => {
    if (scopedTarget) return scopedTarget.key
    if (selectedTarget && selectedTarget.status !== "offline") return selectedTarget.key
    return onlineTargets.length > 0 ? "all_online" : ""
  })

  useEffect(() => {
    if (scopedTarget) {
      setTargetScope(scopedTarget.key)
    }
  }, [scopedTarget])

  const [inputValues, setInputValues] = useState<Record<string, string | number | boolean>>(() =>
    buildInitialInputs(manifest?.ui.inputs),
  )

  useEffect(() => {
    setInputValues(buildInitialInputs(manifest?.ui.inputs))
  }, [plugin.id, manifest])

  const hasCredentialRefInput = useMemo(
    () =>
      Boolean(manifest?.ui.inputs?.some((inp) => inp.type === "credential_ref")) ||
      Boolean(manifest?.ui.actions?.some((act) => act.mode === "apply" || Boolean(act.credential_use))),
    [manifest],
  )
  const [activeTab, setActiveTab] = useState<"workbench" | "actions" | "strategy">("workbench")
  const [selectedResourceContext, setSelectedResourceContext] = useState<string>("")
  const [selectedActionId, setSelectedActionId] = useState<string>("")
  const autoProbedKeyRef = useRef<string>("")

  useEffect(() => {
    setActiveTab("workbench")
    setSelectedResourceContext("")
    setSelectedActionId("")
  }, [plugin.id])

  const resolvedCredentialTarget = useMemo(() => {
    if (scopedTarget) return scopedTarget
    if (targetScope && targetScope !== "all_online") {
      return onlineTargets.find((t) => t.key === targetScope) ?? selectedTarget ?? onlineTargets[0] ?? null
    }
    return selectedTarget ?? onlineTargets[0] ?? null
  }, [scopedTarget, targetScope, onlineTargets, selectedTarget])

  const [targetCredentials, setTargetCredentials] = useState<CredentialResource[]>([])
  useEffect(() => {
    if (!hasCredentialRefInput || !resolvedCredentialTarget) {
      setTargetCredentials([])
      return
    }
    const ctrl = new AbortController()
    listCredentials(session, resolvedCredentialTarget, "target", ctrl.signal)
      .then((list) => {
        if (!ctrl.signal.aborted) {
          setTargetCredentials((list || []).filter((c) => c.state === "active"))
        }
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setTargetCredentials([])
      })
    return () => ctrl.abort()
  }, [session, hasCredentialRefInput, resolvedCredentialTarget])

  const [loadingLatest, setLoadingLatest] = useState(true)
  const [running, setRunning] = useState(false)
  const [scheduleBusy, setScheduleBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [latestRun, setLatestRun] = useState<MonitorRunRecord | null>(null)
  const [runItems, setRunItems] = useState<MonitorRunItemRecord[]>([])
  const [selectedItemKey, setSelectedItemKey] = useState("")

  const loadLatestRun = useCallback(async (signal?: AbortSignal) => {
    setLoadingLatest(true)
    try {
      const { runs } = await fetchMonitorRuns(session, { limit: 40 }, signal)
      if (signal?.aborted) return
      const matching = (runs || []).find((r) => Array.isArray(r.skills) && r.skills.includes(skillSlug))
      if (!matching) {
        setLatestRun(null)
        setRunItems([])
        setSelectedItemKey("")
        setLoadingLatest(false)
        return
      }
      const detail = await fetchMonitorRunDetail(session, matching.id, signal)
      if (signal?.aborted) return
      const filteredItems = (detail.items || []).filter((item) => {
        if (scopedTarget && (item.cluster !== scopedTarget.cluster || item.instance !== scopedTarget.instance)) {
          return false
        }
        return item.asset_id === skillSlug || item.strategy === `skill:${skillSlug}`
      })
      setLatestRun(detail.run)
      setRunItems(filteredItems)
      setSelectedItemKey((prev) => {
        if (prev && filteredItems.some((it) => `${it.cluster}/${it.instance}` === prev)) return prev
        return filteredItems[0] ? `${filteredItems[0].cluster}/${filteredItems[0].instance}` : ""
      })
    } catch (err) {
      if (!signal?.aborted) {
        setError(err instanceof Error ? err.message : "加载最近一次插件运行结果失败")
      }
    } finally {
      if (!signal?.aborted) setLoadingLatest(false)
    }
  }, [session, skillSlug, scopedTarget])

  useEffect(() => {
    const ctrl = new AbortController()
    void loadLatestRun(ctrl.signal)
    return () => ctrl.abort()
  }, [loadLatestRun])

  const activeItem = useMemo(() => {
    if (runItems.length === 0) return null
    return runItems.find((it) => `${it.cluster}/${it.instance}` === selectedItemKey) ?? runItems[0]
  }, [runItems, selectedItemKey])

  const metricsMap = useMemo(
    () => extractArtifactMetricsMap(activeItem?.artifact_json),
    [activeItem?.artifact_json],
  )
  const parsedCharts = useMemo<InspectionChart[]>(
    () => (activeItem?.artifact_json ? normalizeInspectionCharts(activeItem.artifact_json) : []),
    [activeItem?.artifact_json],
  )

  const validateAndBuildInputs = useCallback(
    (source: Record<string, string | number | boolean>): { ok: true; payload: Record<string, string | number | boolean> } | { ok: false; message: string } => {
      const specs = manifest?.ui.inputs ?? []
      const payload: Record<string, string | number | boolean> = {}
      for (const spec of specs) {
        const raw = source[spec.key]
        if (raw === undefined || raw === "") {
          if (spec.required) {
            return { ok: false, message: `请填写必填参数「${spec.label}」（${spec.key}）` }
          }
          continue
        }
        if (spec.type === "number") {
          const num = typeof raw === "number" ? raw : Number(raw)
          if (!Number.isFinite(num)) {
            return { ok: false, message: `参数「${spec.label}」必须为合法数字` }
          }
          if (spec.min !== undefined && num < spec.min) {
            return { ok: false, message: `参数「${spec.label}」不能小于 ${spec.min}` }
          }
          if (spec.max !== undefined && num > spec.max) {
            return { ok: false, message: `参数「${spec.label}」不能大于 ${spec.max}` }
          }
          payload[spec.key] = num
        } else if (spec.type === "boolean") {
          payload[spec.key] = Boolean(raw)
        } else if (spec.type === "select") {
          const str = String(raw)
          if (spec.options && !spec.options.some((opt) => opt.value === str)) {
            return { ok: false, message: `参数「${spec.label}」取值不在允许选项内` }
          }
          payload[spec.key] = str
        } else if (spec.type === "credential_ref") {
          const ref = String(raw).trim()
          if (!/^[a-z0-9][a-z0-9._-]{0,127}$/.test(ref)) {
            return { ok: false, message: `凭据引用「${spec.label}」必须为小写凭据名称` }
          }
          payload[spec.key] = ref
        }
      }
      return { ok: true, payload }
    },
    [manifest],
  )

  const executePluginRun = useCallback(
    async (overrideInputs?: Record<string, string | number | boolean>, label?: string, silent?: boolean) => {
      if (running) return
      const effectiveSource = overrideInputs ? { ...inputValues, ...overrideInputs } : inputValues
      if (overrideInputs) setInputValues(effectiveSource)
      const check = validateAndBuildInputs(effectiveSource)
      if (!check.ok) {
        setError(check.message)
        return
      }
      if (onlineTargets.length === 0 && !scopedTarget) {
        setError("当前暂无在线 Agent 目标可执行该插件")
        return
      }

      setRunning(true)
      setError("")
      if (!silent) setNotice("")
      try {
        let scopeMode: "all_online" | "targets" = "all_online"
        let runTargets: { cluster: string; instance: string }[] | undefined
        if (scopedTarget) {
          scopeMode = "targets"
          runTargets = [{ cluster: scopedTarget.cluster, instance: scopedTarget.instance }]
        } else if (targetScope && targetScope !== "all_online") {
          const found = onlineTargets.find((t) => t.key === targetScope)
          if (found) {
            scopeMode = "targets"
            runTargets = [{ cluster: found.cluster, instance: found.instance }]
          }
        }
        const res = await triggerMonitorRun(session, {
          scope_mode: scopeMode,
          targets: runTargets,
          skills: [skillSlug],
          inputs: Object.keys(check.payload).length > 0 ? check.payload : undefined,
        })
        setLatestRun(res.run)
        const matchingItems = (res.items || []).filter(
          (item) => item.asset_id === skillSlug || item.strategy === `skill:${skillSlug}`,
        )
        setRunItems(matchingItems)
        if (matchingItems[0]) {
          setSelectedItemKey(`${matchingItems[0].cluster}/${matchingItems[0].instance}`)
        }
        if (!silent) {
          setNotice(`${label || plugin.name} 执行完成（#${res.run.id}，耗时 ${res.run.duration_ms} ms）`)
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "插件执行失败")
      } finally {
        setRunning(false)
      }
    },
    [running, inputValues, validateAndBuildInputs, onlineTargets, scopedTarget, targetScope, session, skillSlug, plugin.name],
  )

  function handlePresetClick(preset: SkillPluginPreset) {
    if (preset.mode === "agent") {
      if (!onOpenAskWithPrompt) {
        setError("当前页面暂不支持跳转到 AI 助手，请先选择在线 Agent 后从 AI 助手发起")
        return
      }
      onOpenAskWithPrompt(preset.prompt || `请运行 ${skillSlug} Skill 进行分析`)
      return
    }
    void executePluginRun(preset.inputs, preset.title)
  }

  async function handleCreateRecommendedSchedule() {
    if (!manifest?.schedule_template || !isAdmin || plugin.scope !== "system" || scheduleBusy) return
    setScheduleBusy(true)
    setError("")
    setNotice("")
    try {
      const tpl = manifest.schedule_template
      const res = await mutateMonitorSchedule(session, {
        action: "create",
        name: tpl.name,
        description: tpl.description || plugin.description,
        enabled: true,
        scope_mode: tpl.scope_mode || "all_online",
        skills: [skillSlug],
        interval_seconds: tpl.interval_seconds,
        inputs: tpl.inputs,
      })
      setNotice(`已创建定时巡检计划「${res.schedule?.name || tpl.name}」（${formatIntervalLabel(tpl.interval_seconds)}）`)
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建推荐定时计划失败")
    } finally {
      setScheduleBusy(false)
    }
  }

  function handleFormSubmit(event: FormEvent) {
    event.preventDefault()
    void executePluginRun()
  }

  if (!manifest) {
    return (
      <section className="min-h-0 flex-1 overflow-auto p-6">
        <StatusBanner tone="warning">
          插件「{plugin.name}」的声明式配置（plugin.yaml）不可用{plugin.manifest_error ? `：${plugin.manifest_error}` : ""}。
        </StatusBanner>
      </section>
    )
  }

  function handleExportFormalReport(printAfterOpen: boolean) {
    if (!activeItem || !manifest) return
    const html = renderFormalInspectionReportHtml({
      title: manifest.plugin.name,
      subtitle: manifest.plugin.description,
      skillSlug,
      targetLabel: `${activeItem.cluster}/${activeItem.instance}`,
      runId: latestRun?.id,
      collectedAt: formatRunStamp(latestRun?.finished_at_ms || latestRun?.started_at_ms),
      status: activeItem.status,
      summary: latestRun?.summary,
      checks: activeItem.checks,
      artifactJson: activeItem.artifact_json,
    })
    const blob = new Blob([html], { type: "text/html;charset=utf-8" })
    const url = URL.createObjectURL(blob)
    if (printAfterOpen) {
      const win = window.open(url, "_blank", "noopener,noreferrer")
      if (win) {
        win.addEventListener("load", () => {
          try { win.print() } catch { /* ignore */ }
        })
      }
      setTimeout(() => URL.revokeObjectURL(url), 60_000)
      return
    }
    const a = document.createElement("a")
    a.href = url
    a.download = `${skillSlug}-${activeItem.cluster}-${activeItem.instance}-report.html`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 2000)
  }

  function handleSelectResourceForAction(contextText: string, actionId?: string) {
    setSelectedResourceContext(contextText)
    if (actionId) setSelectedActionId(actionId)
    setActiveTab("actions")
  }

  // Automatically fetch live state ONCE when opening a fast, zero-token read-only
  // domain workbench with no cached run yet (just like K8s / Database workbenches).
  const autoProbeKey = `${plugin.id}:${scopedTarget?.key ?? targetScope}`
  useEffect(() => {
    if (loadingLatest || running) return
    const shouldProbe = shouldAutoLiveProbe({
      plugin,
      hasCachedRun: Boolean(latestRun && runItems.length > 0),
      hasOnlineTarget: onlineTargets.length > 0 || Boolean(scopedTarget),
      alreadyProbed: autoProbedKeyRef.current === autoProbeKey,
    })
    if (!shouldProbe) return
    autoProbedKeyRef.current = autoProbeKey
    void executePluginRun(undefined, undefined, true)
  }, [loadingLatest, running, plugin, latestRun, runItems.length, onlineTargets.length, scopedTarget, autoProbeKey, executePluginRun])

  const Icon = CONSOLE_NAV_ICONS[manifest.plugin.icon] || Layers
  const inputs = manifest.ui.inputs ?? []
  const presets = manifest.ui.presets ?? []
  const scriptPresets = presets.filter((p) => p.mode === "script")
  const agentPresets = presets.filter((p) => p.mode === "agent")
  const askPresets = manifest.ui.slots.ask_presets ?? []
  const actions = manifest.ui.actions ?? []
  const catalogInputs = manifest.capabilities?.catalog_inputs ?? []
  const catalogSchemas = manifest.catalog_schemas ?? []
  const views: SkillPluginView[] = manifest.ui.views?.length
    ? manifest.ui.views
    : [{ type: "checks_table", title: "检查项明细" }, { type: "artifact_viewer", title: "完整原始数据" }]
  const { liveViews, reportViews } = splitPluginViewsForTabs(views)
  const hasOperationsTab = actions.length > 0 || agentPresets.length > 0 || askPresets.length > 0
  const primaryAiPrompt = agentPresets[0]?.prompt || askPresets[0]?.prompt
  const isGpuWorkbench = skillSlug === "gpu-governance"
  const activeTargetLabel = activeItem
    ? `${activeItem.cluster}/${activeItem.instance}`
    : scopedTarget?.key || (targetScope && targetScope !== "all_online" ? targetScope : `全部在线节点 (${onlineTargets.length})`)
  const latestStatusMeta = latestRun ? describeMonitorStatus(latestRun.status) : null

  const tabItems = [
    { id: "workbench" as const, label: "实时工作台" },
    ...(hasOperationsTab ? [{ id: "actions" as const, label: "运维操作" }] : []),
    { id: "strategy" as const, label: "策略与报告" },
  ]
  const effectiveTab = activeTab === "actions" && !hasOperationsTab ? "workbench" : activeTab

  return (
    <main aria-label={`${plugin.name} 工作台`} className="flex min-h-0 flex-1 flex-col overflow-hidden">
      {/* Clean Domain Workbench Header (zero developer/L2 plumbing jargon) */}
      <header className="shrink-0 border-b border-border/60 bg-card/50 px-4 py-3 sm:px-6">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
              <Icon width={18} height={18} aria-hidden="true" />
            </span>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="text-base font-semibold text-foreground">{manifest.plugin.name}</h1>
                {latestStatusMeta && (
                  <Badge variant={latestStatusMeta.variant}>{latestStatusMeta.label}</Badge>
                )}
                {latestRun && (
                  <span className="hidden font-mono text-2xs text-muted-foreground sm:inline">
                    更新于 {formatRunStamp(latestRun.finished_at_ms || latestRun.started_at_ms)}
                  </span>
                )}
              </div>
              {manifest.plugin.description && (
                <p className="truncate text-xs text-muted-foreground">{manifest.plugin.description}</p>
              )}
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {!scopedTarget && onlineTargets.length > 0 && (
              <Select
                scale="sm"
                aria-label="选择目标节点"
                className="w-48 font-mono"
                value={targetScope}
                disabled={running}
                onChange={(e) => {
                  const next = e.target.value
                  setTargetScope(next)
                  if (next !== "all_online" && runItems.some((it) => `${it.cluster}/${it.instance}` === next)) {
                    setSelectedItemKey(next)
                  }
                }}
              >
                <option value="all_online">全部在线节点 ({onlineTargets.length})</option>
                {onlineTargets.map((t) => (
                  <option key={t.key} value={t.key}>
                    {t.key}
                  </option>
                ))}
              </Select>
            )}
            <Button
              size="sm"
              variant="primary"
              disabled={running || (onlineTargets.length === 0 && !scopedTarget)}
              onClick={() => void executePluginRun(undefined, manifest.plugin.name)}
            >
              <RefreshCw className={`size-3.5 ${running ? "animate-spin" : ""}`} aria-hidden="true" />
              {running ? "采集中…" : activeItem ? "刷新实时状态" : "采集实时状态"}
            </Button>
            {primaryAiPrompt && onOpenAskWithPrompt && (
              <Button
                size="sm"
                variant="default"
                onClick={() => onOpenAskWithPrompt(primaryAiPrompt)}
              >
                <Sparkles className="size-3.5" aria-hidden="true" />
                AI 分析
              </Button>
            )}
            {activeItem && (
              <Button size="sm" variant="ghost" onClick={() => handleExportFormalReport(false)}>
                <Download className="size-3.5" aria-hidden="true" />
                导出报告
              </Button>
            )}
          </div>
        </div>
      </header>

      <PageTabs
        ariaLabel={`${manifest.plugin.name}功能切换`}
        value={effectiveTab}
        onChange={(next) => setActiveTab(next)}
        items={tabItems}
      />

      <div className="min-h-0 flex-1 overflow-auto p-4 sm:p-6">
        <div className="mx-auto max-w-6xl space-y-4">
          {error && <StatusBanner tone="error">{error}</StatusBanner>}
          {notice && (
            <StatusBanner tone="success" compact>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span>{notice}</span>
                <div className="flex items-center gap-1">
                  {latestRun && onOpenInspectionRun && (
                    <Button size="sm" variant="ghost" onClick={() => onOpenInspectionRun(latestRun.id)}>
                      在自动化查看记录
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" onClick={() => setNotice("")}>
                    关闭
                  </Button>
                </div>
              </div>
            </StatusBanner>
          )}

          {effectiveTab === "workbench" && (
            <div className="space-y-4">
              {/* Managed Domain Asset Directory FIRST (e.g. DNS Zones / Mail Domains) */}
              {catalogSchemas.length > 0 && (
                <InlineCatalogManager
                  skillSlug={skillSlug}
                  schemas={catalogSchemas}
                  targets={targets}
                  scopedTarget={scopedTarget}
                  defaultTarget={resolvedCredentialTarget}
                />
              )}

              {/* Multi-target Result Switcher */}
              {runItems.length > 1 && (
                <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-card px-3.5 py-2 text-xs">
                  <span className="text-muted-foreground">切换已采集节点视图：</span>
                  <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="切换目标结果">
                    {runItems.map((it) => {
                      const key = `${it.cluster}/${it.instance}`
                      const st = describeMonitorStatus(it.status)
                      return (
                        <Button
                          key={key}
                          size="sm"
                          variant={selectedItemKey === key ? "primary" : "default"}
                          onClick={() => setSelectedItemKey(key)}
                        >
                          <span className="font-mono">{key}</span>
                          <Badge variant={st.variant} className="ml-1">{st.label}</Badge>
                        </Button>
                      )
                    })}
                  </div>
                </div>
              )}

              {/* Primary Live Domain Canvas */}
              {(loadingLatest || running) && !activeItem ? (
                <Card>
                  <CardContent className="flex flex-col items-center justify-center gap-2 py-12 text-center" role="status">
                    <RefreshCw className="size-5 animate-spin text-primary" aria-hidden="true" />
                    <p className="text-sm font-medium text-foreground">
                      {running ? `正在实时采集 ${activeTargetLabel} 的${manifest.plugin.name}状态…` : `正在加载${manifest.plugin.name}实时数据…`}
                    </p>
                  </CardContent>
                </Card>
              ) : !activeItem ? (
                <Card>
                  <CardContent className="flex flex-col items-center justify-center gap-3 py-10 text-center">
                    <span className="flex size-11 items-center justify-center rounded-xl bg-primary/12 text-primary">
                      <Icon width={20} height={20} aria-hidden="true" />
                    </span>
                    <div className="space-y-1">
                      <p className="text-sm font-medium text-foreground">
                        {onlineTargets.length === 0 && !scopedTarget
                          ? "当前暂无在线 Agent 节点"
                          : `尚未获取 ${activeTargetLabel} 的实时数据`}
                      </p>
                      <p className="max-w-md text-xs text-muted-foreground">
                        {onlineTargets.length === 0 && !scopedTarget
                          ? "接入并上线目标节点后，即可实时查看本领域状态并执行受控运维。"
                          : manifest.plugin.description || "点击下方按钮立即采集当前节点的实时状态与资产明细。"}
                      </p>
                    </div>
                    <div className="flex flex-wrap items-center justify-center gap-2">
                      <Button
                        size="sm"
                        variant="primary"
                        disabled={running || (onlineTargets.length === 0 && !scopedTarget)}
                        onClick={() => void executePluginRun(undefined, manifest.plugin.name)}
                      >
                        <Play className="size-3.5" aria-hidden="true" />
                        立即采集实时状态
                      </Button>
                      {hasOperationsTab && (
                        <Button size="sm" variant="default" onClick={() => setActiveTab("actions")}>
                          前往运维操作
                        </Button>
                      )}
                    </div>
                  </CardContent>
                </Card>
              ) : (
                <div className="space-y-4">
                  <MachineTagSyncCard
                    session={session}
                    cluster={activeItem.cluster}
                    instance={activeItem.instance}
                    artifactJson={activeItem.artifact_json}
                  />
                  {isGpuWorkbench ? (
                    <GpuGovernanceWorkbench
                      artifactJson={activeItem.artifact_json}
                      targetLabel={`${activeItem.cluster}/${activeItem.instance}`}
                      hasActions={actions.length > 0}
                      onSelectResource={handleSelectResourceForAction}
                    />
                  ) : (
                    liveViews.map((view, idx) => (
                      <SkillPluginViewBlock
                        key={`${view.type}-${idx}`}
                        session={session}
                        skillSlug={skillSlug}
                        view={view}
                        item={activeItem}
                        metricsMap={metricsMap}
                        charts={parsedCharts}
                        hasActions={actions.length > 0}
                        onSelectResource={handleSelectResourceForAction}
                      />
                    ))
                  )}
                </div>
              )}
            </div>
          )}

          {effectiveTab === "actions" && hasOperationsTab && (
            <div className="space-y-4">
              {(askPresets.length > 0 || agentPresets.length > 0) && onOpenAskWithPrompt && (
                <Card>
                  <CardHeader className="pb-2">
                    <CardTitle className="flex items-center gap-1.5 text-sm">
                      <Sparkles className="size-4 text-primary" aria-hidden="true" />
                      AI 协同排查快捷入口
                    </CardTitle>
                  </CardHeader>
                  <CardContent className="grid gap-2.5 md:grid-cols-2">
                    {[
                      ...askPresets.map((p, i) => ({
                        id: `ask-${i}`,
                        title: p.title,
                        description: p.description,
                        prompt: p.prompt,
                      })),
                      ...agentPresets.map((p) => ({
                        id: p.id,
                        title: p.title,
                        description: p.description,
                        prompt: p.prompt || `请分析当前节点的${manifest.plugin.name}状态`,
                      })),
                    ].map((item) => (
                      <div
                        key={item.id}
                        className="flex items-center justify-between gap-3 rounded-lg border bg-background/60 p-3"
                      >
                        <div className="min-w-0">
                          <div className="truncate text-xs font-semibold text-foreground">{item.title}</div>
                          {item.description && (
                            <p className="mt-0.5 line-clamp-2 text-2xs text-muted-foreground">{item.description}</p>
                          )}
                        </div>
                        <Button
                          size="sm"
                          variant="default"
                          className="shrink-0"
                          onClick={() => onOpenAskWithPrompt(item.prompt)}
                        >
                          发给 AI
                        </Button>
                      </div>
                    ))}
                  </CardContent>
                </Card>
              )}

              {actions.length > 0 && (
                <SkillPluginActionWorkbench
                  session={session}
                  sessionId={sessionId}
                  pluginName={manifest.plugin.name}
                  skillSlug={skillSlug}
                  actions={actions}
                  target={resolvedCredentialTarget}
                  credentials={targetCredentials}
                  selectedActionId={selectedActionId}
                  contextText={selectedResourceContext}
                  onClearContext={() => setSelectedResourceContext("")}
                  onSelectActionId={setSelectedActionId}
                  onFillTerminal={onFillTerminal}
                  onOpenTerminalSplit={onOpenTerminalSplit}
                />
              )}
            </div>
          )}

          {effectiveTab === "strategy" && (
            <div className="space-y-4">
              {/* Threshold & Parameters + Presets */}
              <Card>
                <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 pb-2">
                  <CardTitle className="text-sm">采集阈值与参数配置</CardTitle>
                  {scriptPresets.length > 0 && (
                    <div className="flex flex-wrap items-center gap-1.5">
                      {scriptPresets.map((preset) => (
                        <Button
                          key={preset.id}
                          type="button"
                          size="sm"
                          variant="ghost"
                          disabled={running || (onlineTargets.length === 0 && !scopedTarget)}
                          title={preset.description}
                          onClick={() => handlePresetClick(preset)}
                        >
                          <Play className="size-3" aria-hidden="true" />
                          {preset.title}
                        </Button>
                      ))}
                    </div>
                  )}
                </CardHeader>
                <CardContent>
                  <form onSubmit={handleFormSubmit} className="space-y-4">
                    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                      {inputs.map((inp) => (
                        <SkillPluginInputField
                          key={inp.key}
                          input={inp}
                          value={inputValues[inp.key]}
                          disabled={running}
                          credentials={targetCredentials}
                          onChange={(next) => setInputValues((prev) => ({ ...prev, [inp.key]: next }))}
                        />
                      ))}
                      {inputs.length === 0 && (
                        <p className="col-span-full text-xs text-muted-foreground">
                          该能力使用内置默认采集策略，无需额外阈值参数。
                        </p>
                      )}
                    </div>

                    <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
                      <div className="text-xs text-muted-foreground">
                        {catalogInputs.length > 0 || catalogSchemas.length > 0 ? (
                          <span>
                            自动联动资产目录：
                            {[
                              ...catalogInputs.map((ci) => CATALOG_INPUT_LABELS[ci] ?? ci),
                              ...catalogSchemas.map((cs) => cs.label),
                            ].join(" · ")}
                          </span>
                        ) : (
                          <span>阈值仅用于告警分级，不修改节点业务配置。</span>
                        )}
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        {isAdmin && plugin.scope === "system" && manifest.schedule_template && (
                          <Button
                            type="button"
                            size="sm"
                            variant="default"
                            disabled={scheduleBusy}
                            onClick={() => void handleCreateRecommendedSchedule()}
                          >
                            <CalendarPlus className="size-3.5" aria-hidden="true" />
                            {scheduleBusy
                              ? "创建中…"
                              : `开启定时巡检（${formatIntervalLabel(manifest.schedule_template.interval_seconds)}）`}
                          </Button>
                        )}
                        <Button
                          type="submit"
                          size="sm"
                          variant="primary"
                          disabled={running || (onlineTargets.length === 0 && !scopedTarget)}
                        >
                          <Play className="size-3.5" aria-hidden="true" />
                          {running ? "采集中…" : "按当前参数采集"}
                        </Button>
                      </div>
                    </div>
                  </form>
                </CardContent>
              </Card>

              {/* Formal Report Actions & Historical Views */}
              {activeItem && (
                <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-card px-4 py-2.5 text-xs">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium text-foreground">正式评估报告</span>
                    <span className="font-mono text-muted-foreground">
                      {activeItem.cluster}/{activeItem.instance}
                    </span>
                    {latestRun && <span className="font-mono text-muted-foreground">#{latestRun.id}</span>}
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Button size="sm" variant="default" onClick={() => handleExportFormalReport(false)}>
                      <Download className="size-3.5" aria-hidden="true" />
                      导出 HTML 报告
                    </Button>
                    <Button size="sm" variant="default" onClick={() => handleExportFormalReport(true)}>
                      <Printer className="size-3.5" aria-hidden="true" />
                      打印 / PDF
                    </Button>
                    {onOpenPluginCenter && (
                      <Button size="sm" variant="ghost" onClick={onOpenPluginCenter}>
                        插件管理
                      </Button>
                    )}
                  </div>
                </div>
              )}

              {activeItem ? (
                reportViews.map((view, idx) => (
                  <SkillPluginViewBlock
                    key={`${view.type}-${idx}`}
                    session={session}
                    skillSlug={skillSlug}
                    view={view}
                    item={activeItem}
                    metricsMap={metricsMap}
                    charts={parsedCharts}
                    hasActions={actions.length > 0}
                    onSelectResource={handleSelectResourceForAction}
                  />
                ))
              ) : (
                <Card>
                  <CardContent className="py-8 text-center text-xs text-muted-foreground">
                    完成一次实时采集后，此处自动呈现历史趋势曲线、基线核查明细与完整报告导出。
                  </CardContent>
                </Card>
              )}
            </div>
          )}
        </div>
      </div>
    </main>
  )
}

function SkillPluginInputField({
  input,
  value,
  disabled,
  credentials,
  onChange,
}: {
  input: SkillPluginInput
  value: string | number | boolean | undefined
  disabled: boolean
  credentials: CredentialResource[]
  onChange: (next: string | number | boolean) => void
}) {
  if (input.type === "boolean") {
    const checked = Boolean(value)
    return (
      <div className="flex flex-col justify-between rounded-md border bg-muted/20 px-3 py-2 text-xs">
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium text-foreground">{input.label}</span>
          <Switch
            checked={checked}
            aria-label={input.label}
            disabled={disabled}
            onCheckedChange={(next) => onChange(next)}
          />
        </div>
        {input.description && <p className="mt-1 text-2xs text-muted-foreground">{input.description}</p>}
      </div>
    )
  }

  if (input.type === "select") {
    return (
      <label className="block space-y-1 text-xs text-muted-foreground">
        <span className="block font-medium text-foreground">{input.label}</span>
        <Select
          scale="sm"
          className="w-full"
          value={String(value ?? "")}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        >
          {(input.options ?? []).map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </Select>
        {input.description && <span className="block text-2xs text-muted-foreground">{input.description}</span>}
      </label>
    )
  }

  if (input.type === "credential_ref") {
    return (
      <label className="block space-y-1 text-xs text-muted-foreground">
        <span className="block font-medium text-foreground">
          {input.label}
          {input.required ? " *" : ""}
        </span>
        <Select
          scale="sm"
          className="w-full"
          value={String(value ?? "")}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        >
          <option value="">{credentials.length === 0 ? "当前目标无已绑定激活凭据" : "请选择已授权凭据…"}</option>
          {credentials.map((cred) => (
            <option key={cred.id} value={cred.name}>
              {cred.name} ({cred.type})
            </option>
          ))}
        </Select>
        {input.description && <span className="block text-2xs text-muted-foreground">{input.description}</span>}
      </label>
    )
  }

  return (
    <label className="block space-y-1 text-xs text-muted-foreground">
      <span className="block font-medium text-foreground">
        {input.label}
        {input.unit ? ` (${input.unit})` : ""}
      </span>
      <Input
        scale="sm"
        className="w-full"
        type="number"
        min={input.min}
        max={input.max}
        step={input.step ?? 1}
        value={value === undefined ? "" : String(value)}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value === "" ? "" : Number(e.target.value))}
      />
      {input.description && <span className="block text-2xs text-muted-foreground">{input.description}</span>}
    </label>
  )
}

function SkillPluginViewBlock({
  session,
  skillSlug,
  view,
  item,
  metricsMap,
  charts,
  hasActions,
  onSelectResource,
}: {
  session: Session
  skillSlug: string
  view: SkillPluginView
  item: MonitorRunItemRecord
  metricsMap: Map<string, ExtractedMetric>
  charts: InspectionChart[]
  hasActions?: boolean
  onSelectResource?: (contextText: string, actionId?: string) => void
}) {
  if (view.type === "findings_list") {
    return (
      <SkillPluginFindingsSection
        title={view.title || "风险发现与覆盖缺口"}
        artifactJson={item.artifact_json}
      />
    )
  }

  if (view.type === "resource_table") {
    return (
      <SkillPluginResourceTableSection
        title={view.title || "领域资产明细"}
        view={view}
        artifactJson={item.artifact_json}
        canSelectForAction={Boolean(hasActions && onSelectResource)}
        onSelectResource={onSelectResource}
      />
    )
  }

  if (view.type === "kpi_grid") {
    const kpiItems = view.items ?? []
    if (kpiItems.length === 0) return null
    return (
      <Card>
        {view.title && (
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">{view.title}</CardTitle>
          </CardHeader>
        )}
        <CardContent className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
          {kpiItems.map((kpi) => {
            const metric = metricsMap.get(kpi.key)
            if (!metric) {
              // §7.4: explicit honest missing metric notice — never fabricate 0.
              return (
                <div key={kpi.key} className="rounded-lg border border-dashed bg-muted/20 p-3">
                  <div className="truncate text-xs text-muted-foreground">{kpi.label}</div>
                  <div className="mt-1.5 text-xs text-muted-foreground">本次运行未输出 <code>{kpi.key}</code></div>
                </div>
              )
            }
            const tone = kpiToneBadge(metric.value, metric.status, kpi.warn_threshold, kpi.crit_threshold)
            return (
              <div key={kpi.key} className="rounded-lg border bg-card p-3">
                <div className="flex items-center justify-between gap-1">
                  <span className="truncate text-xs text-muted-foreground">{kpi.label}</span>
                  <Badge variant={tone} className="px-1.5 py-0 text-2xs">
                    {tone === "danger" ? "严重" : tone === "warning" ? "告警" : "正常"}
                  </Badge>
                </div>
                <div className="mt-1.5 font-mono text-lg font-semibold tabular-nums text-foreground">
                  {formatMetricValue(metric.value, kpi.unit || metric.unit)}
                </div>
              </div>
            )
          })}
        </CardContent>
      </Card>
    )
  }

  if (view.type === "checks_table") {
    const checks = item.checks ?? []
    return (
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">{view.title || "巡检检查明细"}</CardTitle>
        </CardHeader>
        <CardContent>
          {checks.length === 0 ? (
            <p className="text-xs text-muted-foreground">本次运行未输出结构化检查项。</p>
          ) : (
            <Table wrapperClassName="rounded-md border">
              <TableHeader className="bg-muted/30">
                <TableRow>
                  <TableHead className="w-24">状态</TableHead>
                  <TableHead>检查项</TableHead>
                  <TableHead className="w-28">实测值</TableHead>
                  <TableHead className="w-36">阈值</TableHead>
                  <TableHead>详情</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {checks.map((c) => {
                  const st = describeMonitorStatus(c.status)
                  return (
                    <TableRow key={c.id + c.name}>
                      <TableCell>
                        <Badge variant={st.variant}>{st.label}</Badge>
                      </TableCell>
                      <TableCell className="text-xs font-medium text-foreground">{c.name}</TableCell>
                      <TableCell className="font-mono text-xs tabular-nums">{c.value || "—"}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{c.threshold || "—"}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">{c.detail || "—"}</TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    )
  }

  if (view.type === "charts") {
    if (charts.length === 0) return null
    const toneBarClass = (tone: string) =>
      tone === "danger"
        ? "bg-destructive"
        : tone === "warning"
          ? "bg-warning"
          : tone === "success"
            ? "bg-success"
            : "bg-primary"
    return (
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">{view.title || "可视化图表"}</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-2">
          {charts.map((chart, idx) => (
            <div key={`${chart.title}-${idx}`} className="space-y-2 rounded-lg border bg-muted/15 p-3">
              <div className="text-xs font-semibold text-foreground">{chart.title}</div>
              {chart.kind === "bar" && (
                <div className="space-y-2">
                  {chart.series.map((bar) => {
                    const pct = Math.max(2, Math.min(100, bar.ratio * 100))
                    return (
                      <div key={bar.label} className="space-y-0.5">
                        <div className="flex items-center justify-between text-2xs">
                          <span className="truncate text-muted-foreground">{bar.label}</span>
                          <span className="font-mono font-medium tabular-nums text-foreground">
                            {formatChartValue(bar.value, chart.unit)}
                          </span>
                        </div>
                        <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                          <div className={`h-full rounded-full transition-all ${toneBarClass(bar.tone)}`} style={{ width: `${pct}%` }} />
                        </div>
                      </div>
                    )
                  })}
                </div>
              )}
              {chart.kind === "gauge" && chart.arcs.length > 0 && (
                <div className="space-y-1.5">
                  {chart.arcs.map((arc) => (
                    <div key={arc.label} className="flex items-baseline justify-between text-xs">
                      <span className="text-muted-foreground">{arc.label}</span>
                      <span className="font-mono font-semibold tabular-nums text-foreground">
                        {formatChartValue(arc.value, chart.unit)} / {formatChartValue(chart.max, chart.unit)}
                      </span>
                    </div>
                  ))}
                </div>
              )}
              {chart.kind === "line" && chart.points.length > 0 && (
                <svg viewBox="0 0 100 40" className="h-16 w-full text-primary" role="img" aria-label={chart.title}>
                  <polyline
                    fill="none"
                    stroke="currentColor"
                    strokeWidth={1.8}
                    points={chart.points.map((p) => `${p.x},${(p.y * 0.4).toFixed(1)}`).join(" ")}
                  />
                </svg>
              )}
            </div>
          ))}
        </CardContent>
      </Card>
    )
  }

  if (view.type === "trend_series") {
    return (
      <SkillPluginTrendSection
        session={session}
        skillSlug={skillSlug}
        cluster={item.cluster}
        instance={item.instance}
        title={view.title || "历史趋势"}
        metricKeys={view.metric_keys}
        initialRange={view.range || "24h"}
      />
    )
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">{view.title || "完整巡检产物"}</CardTitle>
      </CardHeader>
      <CardContent>
        <InspectionArtifactViewer
          format={item.artifact_format}
          markdown={item.artifact_markdown}
          jsonData={item.artifact_json}
          rawOutput={item.raw_output}
          filePrefix={`${skillSlug}-${item.cluster}-${item.instance}`}
          session={session}
          trendSkill={skillSlug}
          trendCluster={item.cluster}
          trendInstance={item.instance}
        />
      </CardContent>
    </Card>
  )
}

function SkillPluginTrendSection({
  session,
  skillSlug,
  cluster,
  instance,
  title,
  metricKeys,
  initialRange,
}: {
  session: Session
  skillSlug: string
  cluster: string
  instance: string
  title: string
  metricKeys?: string[]
  initialRange: MonitorMetricRange
}) {
  const [range, setRange] = useState<MonitorMetricRange>(initialRange)
  const [series, setSeries] = useState<MonitorMetricSeries[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    fetchMonitorMetricSeries(session, { skill: skillSlug, cluster, instance, range }, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        const all = res.series || []
        const filtered = metricKeys?.length
          ? all.filter((s) => metricKeys.includes(s.key))
          : all
        setSeries(filtered)
        setLoading(false)
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError(err instanceof Error ? err.message : "加载趋势数据失败")
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, skillSlug, cluster, instance, range, metricKeys?.join(",")])

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 pb-2">
        <CardTitle className="flex items-center gap-1.5 text-sm">
          <TrendingUp className="size-4 text-primary" aria-hidden="true" />
          {title}
          <span className="font-mono text-xs font-normal text-muted-foreground">({cluster}/{instance})</span>
        </CardTitle>
        <div className="flex flex-wrap gap-1" role="group" aria-label="趋势时间范围">
          {MONITOR_METRIC_RANGES.map((r) => (
            <Button
              key={r.value}
              size="sm"
              variant={range === r.value ? "primary" : "ghost"}
              onClick={() => setRange(r.value)}
            >
              {r.label}
            </Button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        {loading ? (
          <p role="status" className="text-xs text-muted-foreground">正在加载历史趋势…</p>
        ) : error ? (
          <p className="text-xs text-destructive">{error}</p>
        ) : series.length === 0 ? (
          <p className="text-xs text-muted-foreground">当前时间范围内暂无历史时序采样点，运行多次巡检后自动绘制曲线。</p>
        ) : (
          <div className="grid gap-3 md:grid-cols-3">
            {series.map((s) => {
              const geom = buildTrendGeometry(s.points, 220, 56, 4)
              const latest = s.points[s.points.length - 1]
              return (
                <div key={s.key} className="space-y-1.5 rounded-lg border bg-muted/15 p-3">
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="truncate text-xs font-medium text-foreground">{s.label || s.key}</span>
                    <span className="font-mono text-xs font-semibold tabular-nums text-primary">
                      {formatMetricValue(latest?.value, s.unit)}
                    </span>
                  </div>
                  <svg viewBox="0 0 220 56" className="h-14 w-full text-primary" role="img" aria-label={`${s.label || s.key} 趋势图`}>
                    {geom.areaPath && <path d={geom.areaPath} fill="currentColor" fillOpacity={0.12} />}
                    {geom.path && <path d={geom.path} fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" />}
                  </svg>
                  <div className="flex justify-between font-mono text-2xs text-muted-foreground">
                    <span>最低 {formatMetricValue(geom.min, s.unit)}</span>
                    <span>{s.points.length} 点</span>
                    <span>最高 {formatMetricValue(geom.max, s.unit)}</span>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

const FINDING_SEV_META: Record<FindingSeverity, { label: string; variant: "danger" | "warning" | "outline" | "primary" }> = {
  critical: { label: "严重", variant: "danger" },
  high: { label: "高危", variant: "danger" },
  medium: { label: "中危", variant: "warning" },
  low: { label: "低危", variant: "outline" },
  info: { label: "信息", variant: "primary" },
}

function SkillPluginFindingsSection({
  title,
  artifactJson,
}: {
  title: string
  artifactJson: unknown
}) {
  const [sevFilter, setSevFilter] = useState<"all" | FindingSeverity>("all")
  const findings = useMemo(() => normalizeInspectionFindings(artifactJson), [artifactJson])
  const gaps = useMemo(() => normalizeCoverageGaps(artifactJson), [artifactJson])
  const scoreSummary = useMemo(() => computeInspectionScore(artifactJson), [artifactJson])

  const filtered = useMemo(
    () => (sevFilter === "all" ? findings : findings.filter((f) => f.severity === sevFilter)),
    [findings, sevFilter],
  )

  const scoreBadgeVariant =
    scoreSummary.grade === "A" || scoreSummary.grade === "B"
      ? "success"
      : scoreSummary.grade === "C"
        ? "warning"
        : "danger"

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 pb-2">
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-sm">{title}</CardTitle>
          <Badge variant={scoreBadgeVariant}>
            健康分 {scoreSummary.score} · {scoreSummary.grade} 级（根因封顶）
          </Badge>
          {scoreSummary.quality_audit.status === "warning" && (
            <Badge variant="warning">质量审计提示</Badge>
          )}
        </div>
        <div className="flex flex-wrap gap-1" role="group" aria-label="按风险级别筛选">
          <Button size="sm" variant={sevFilter === "all" ? "primary" : "ghost"} onClick={() => setSevFilter("all")}>
            全部 ({findings.length})
          </Button>
          {(["critical", "high", "medium", "low", "info"] as const).map((sev) => {
            const count = scoreSummary.severity_counts[sev]
            if (count === 0) return null
            return (
              <Button key={sev} size="sm" variant={sevFilter === sev ? "primary" : "ghost"} onClick={() => setSevFilter(sev)}>
                {FINDING_SEV_META[sev].label} ({count})
              </Button>
            )
          })}
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {scoreSummary.deductions.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 rounded-md border bg-muted/20 px-3 py-2 text-xs">
            <span className="font-medium text-muted-foreground">根因归并扣分：</span>
            {scoreSummary.deductions.slice(0, 6).map((d) => (
              <Badge key={d.root_cause} variant={d.severity === "critical" || d.severity === "high" ? "danger" : "warning"}>
                {d.label} ×{d.count} (-{d.penalty}{d.capped ? " 封顶" : ""})
              </Badge>
            ))}
          </div>
        )}

        {filtered.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {findings.length === 0 ? "本次巡检在已覆盖模块中未检出结构化风险发现。" : "当前筛选条件下无匹配风险项。"}
          </p>
        ) : (
          <div className="space-y-2">
            {filtered.slice(0, 40).map((f) => {
              const meta = FINDING_SEV_META[f.severity]
              return (
                <div key={`${f.id}-${f.asset}`} className="rounded-lg border bg-card p-3 text-xs">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <Badge variant={meta.variant}>{meta.label}</Badge>
                      <Badge variant="outline">{f.priority}</Badge>
                      <span className="font-semibold text-foreground">{f.title}</span>
                    </div>
                    <span className="font-mono text-2xs text-muted-foreground">{f.id} · {f.root_cause}</span>
                  </div>
                  <div className="mt-1.5 space-y-1 text-muted-foreground">
                    <div>资产：<code className="text-foreground">{f.asset}</code></div>
                    <div>证据：<span className="font-mono text-2xs">{f.evidence}</span></div>
                    <div>建议：<span className="text-foreground">{f.recommendation}</span></div>
                  </div>
                </div>
              )
            })}
          </div>
        )}

        {gaps.length > 0 && (
          <div className="rounded-lg border border-warning/40 bg-warning/10 p-3 text-xs">
            <div className="mb-1.5 flex items-center gap-1.5 font-semibold text-foreground">
              <AlertTriangle className="size-3.5 text-warning" aria-hidden="true" />
              覆盖缺口与证据边界（{gaps.length} 项 · 未覆盖绝不视作健康）
            </div>
            <ul className="space-y-1 text-muted-foreground">
              {gaps.map((g, idx) => (
                <li key={`${g.module}-${idx}`}>
                  <strong className="text-foreground">{g.module}</strong>：{g.reason}（{g.impact}）
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function SkillPluginResourceTableSection({
  title,
  view,
  artifactJson,
  canSelectForAction,
  onSelectResource,
}: {
  title: string
  view: SkillPluginView
  artifactJson: unknown
  canSelectForAction: boolean
  onSelectResource?: (contextText: string, actionId?: string) => void
}) {
  const [query, setQuery] = useState("")
  const sourceKey = view.source_key || "resources"
  const columns = view.columns ?? []

  const rows = useMemo<Record<string, unknown>[]>(() => {
    if (!artifactJson || typeof artifactJson !== "object" || Array.isArray(artifactJson)) return []
    const raw = (artifactJson as Record<string, unknown>)[sourceKey]
    if (!Array.isArray(raw)) return []
    return raw.filter((r): r is Record<string, unknown> => Boolean(r && typeof r === "object" && !Array.isArray(r)))
  }, [artifactJson, sourceKey])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return rows
    return rows.filter((row) =>
      columns.some((col) => String(row[col.key] ?? "").toLowerCase().includes(q)),
    )
  }, [rows, columns, query])

  function badgeVariantForCell(val: string): "success" | "warning" | "danger" | "outline" {
    const s = val.trim().toLowerCase()
    if (["pass", "ok", "healthy", "active", "running", "bound"].includes(s)) return "success"
    if (["warning", "warn", "degraded", "partial"].includes(s)) return "warning"
    if (["critical", "error", "fail", "failed", "down"].includes(s)) return "danger"
    return "outline"
  }

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 pb-2">
        <div className="flex items-center gap-2">
          <CardTitle className="text-sm">{title}</CardTitle>
          <Badge variant="outline">{filtered.length} / {rows.length}</Badge>
        </div>
        {rows.length > 0 && (
          <Input
            scale="sm"
            aria-label={`筛选${title}`}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索资产或状态…"
            className="w-52"
          />
        )}
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-xs text-muted-foreground">本次运行未产出 <code>{sourceKey}</code> 资产明细。</p>
        ) : (
          <Table wrapperClassName="rounded-md border">
            <TableHeader className="bg-muted/30">
              <TableRow>
                {columns.map((col) => (
                  <TableHead key={col.key}>{col.label}</TableHead>
                ))}
                {canSelectForAction && <TableHead className="w-28 text-right">协同</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.slice(0, 80).map((row, idx) => {
                const primaryVal = String(row[columns[0]?.key ?? ""] ?? `row-${idx + 1}`)
                return (
                  <TableRow key={`${primaryVal}-${idx}`}>
                    {columns.map((col) => {
                      const val = row[col.key]
                      const text = val === null || val === undefined || val === "" ? "—" : String(val)
                      if (col.kind === "badge") {
                        return (
                          <TableCell key={col.key}>
                            <Badge variant={badgeVariantForCell(text)}>{text}</Badge>
                          </TableCell>
                        )
                      }
                      if (col.kind === "code") {
                        return <TableCell key={col.key} className="font-mono text-xs text-foreground">{text}</TableCell>
                      }
                      if (col.kind === "number") {
                        return <TableCell key={col.key} className="font-mono text-xs tabular-nums text-foreground">{text}</TableCell>
                      }
                      return <TableCell key={col.key} className="text-xs text-muted-foreground">{text}</TableCell>
                    })}
                    {canSelectForAction && onSelectResource && (
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => onSelectResource(`${columns[0]?.label || "对象"}: ${primaryVal}`, view.row_action_id)}
                        >
                          带入操作台
                        </Button>
                      </TableCell>
                    )}
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

function SkillPluginActionWorkbench({
  session,
  sessionId,
  pluginName,
  skillSlug,
  actions,
  target,
  credentials,
  selectedActionId,
  contextText,
  onClearContext,
  onSelectActionId,
  onFillTerminal,
  onOpenTerminalSplit,
}: {
  session: Session
  sessionId?: string
  pluginName: string
  skillSlug: string
  actions: SkillPluginAction[]
  target: Target | null
  credentials: CredentialResource[]
  selectedActionId: string
  contextText: string
  onClearContext: () => void
  onSelectActionId: (id: string) => void
  onFillTerminal?: (command: string) => void
  onOpenTerminalSplit?: (prompt?: string) => void
}) {
  const activeAction = actions.find((a) => a.id === selectedActionId) ?? actions[0]
  const [request, setRequest] = useState("")
  const [credRef, setCredRef] = useState("")
  const [running, setRunning] = useState(false)
  const [stopPending, setStopPending] = useState(false)
  const [error, setError] = useState("")
  const [agentRun, setAgentRun] = useState<AgentRun | null>(null)
  const cancelRef = useRef<ToolCancel | null>(null)
  const fallbackSessionRef = useRef<string>(randomSession(`plg-${skillSlug}`))

  const modeLabel = (mode: string) => (mode === "apply" ? "受控变更" : mode === "plan" ? "变更规划" : "只读诊断")

  async function runAction() {
    if (!activeAction || !target || running || stopPending) return
    const extra = request.trim()
    const cleanCred = credRef.trim()
    if (cleanCred && !/^[a-z0-9][a-z0-9._-]{0,127}$/.test(cleanCred)) {
      setError("受管凭据只能填写合法的小写资源名")
      return
    }
    setError("")
    setRunning(true)
    setStopPending(false)
    cancelRef.current = null

    const instruction = [
      activeAction.prompt_template,
      `目标节点：${target.cluster}/${target.instance}`,
      contextText ? `已选资产上下文：${contextText}` : "",
      extra ? `用户补充需求：${extra}` : "",
    ].filter(Boolean).join("\n")

    setAgentRun(startAgentRun(`${pluginName} · ${activeAction.label} (${target.cluster}/${target.instance})`))
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.prompt,
          arguments: {
            session_id: sessionId || fallbackSessionRef.current,
            operation: activeAction.mode === "apply" ? "apply" : "ask",
            skill: activeAction.skill || skillSlug,
            instruction,
            ...(cleanCred ? { credential_refs: [cleanCred] } : {}),
          },
          onCancelReady: (cancel) => { cancelRef.current = cancel },
        },
        (event) => {
          if (event.type === "output") setAgentRun((cur) => cur && addAgentRunOutput(cur, event.kind, event.data))
          if (event.type === "result") setAgentRun((cur) => cur && addAgentRunResult(cur, extractText(event.result)))
        },
      )
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        const msg = err instanceof Error ? err.message : "操作执行失败"
        setError(msg)
        setAgentRun((cur) => cur && addAgentRunNote(cur, { kind: "err", text: msg }))
      }
    } finally {
      setRunning(false)
      setStopPending(false)
      cancelRef.current = null
    }
  }

  function stopAction() {
    if (!cancelRef.current || stopPending) return
    setStopPending(true)
    setAgentRun((cur) => cur && addAgentRunNote(cur, { kind: "info", text: "已请求停止，等待远端终态" }))
    void cancelRef.current().catch((err) => {
      setStopPending(false)
      setAgentRun((cur) => cur && addAgentRunNote(cur, { kind: "err", text: `停止失败：${(err as Error).message}` }))
    })
  }

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 pb-2">
        <CardTitle className="text-sm">{pluginName} · 受控运维操作台</CardTitle>
        <span className="text-xs text-muted-foreground">
          支持只读排查、变更规划与受控执行，全程留痕审计
        </span>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap gap-2">
          {actions.map((act) => (
            <Button
              key={act.id}
              type="button"
              size="sm"
              disabled={running}
              variant={activeAction?.id === act.id ? "primary" : "default"}
              onClick={() => {
                onSelectActionId(act.id)
                setError("")
              }}
            >
              {act.label}
              <Badge variant="outline" className="ml-1 px-1 py-0 text-2xs">{modeLabel(act.mode)}</Badge>
            </Button>
          ))}
        </div>

        {activeAction?.description && (
          <p className="text-xs text-muted-foreground">{activeAction.description}</p>
        )}

        {contextText && (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border bg-muted/25 px-3 py-1.5 text-xs">
            <span>已带入上下文：<code className="text-foreground">{contextText}</code></span>
            <Button type="button" size="sm" variant="ghost" onClick={onClearContext}>清除</Button>
          </div>
        )}

        <label className="block space-y-1.5 text-xs">
          <span className="block font-medium text-foreground">补充操作需求（可选，留空按所选动作默认模板执行）</span>
          <Textarea
            value={request}
            disabled={running}
            onChange={(e) => setRequest(e.target.value)}
            placeholder="例如：限定排查的节点、对象名称或重点指标；留空则按所选动作默认模板执行…"
            className="block min-h-20 w-full text-sm"
          />
        </label>

        {(activeAction?.mode === "apply" || activeAction?.credential_use) && (
          <label className="block space-y-1.5 text-xs">
            <span className="block font-medium text-foreground">受管凭据引用（可选，仅传资源名）</span>
            <Select scale="sm" className="w-full" value={credRef} disabled={running} onChange={(e) => setCredRef(e.target.value)}>
              <option value="">不附带凭据引用</option>
              {credentials.map((c) => (
                <option key={c.id} value={c.name}>{c.name} ({c.type})</option>
              ))}
            </Select>
          </label>
        )}

        {error && <StatusBanner tone="error">{error}</StatusBanner>}

        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="font-mono text-2xs text-muted-foreground">
            目标：{target ? `${target.cluster}/${target.instance}` : "暂无在线目标"} · Skill：{activeAction?.skill || skillSlug}
          </span>
          <div className="flex flex-wrap items-center gap-2">
            {onOpenTerminalSplit && (
              <Button
                type="button"
                size="sm"
                variant="default"
                disabled={!target}
                onClick={() => {
                  const splitPrompt = [
                    activeAction?.prompt_template || "",
                    target ? `目标节点：${target.cluster}/${target.instance}` : "",
                    contextText ? `已选资产上下文：${contextText}` : "",
                    request.trim() ? `补充需求：${request.trim()}` : "",
                  ].filter(Boolean).join("\n")
                  onOpenTerminalSplit(splitPrompt)
                }}
              >
                <Terminal className="size-3.5" aria-hidden="true" />
                终端分栏协同
              </Button>
            )}
            {running || stopPending ? (
              <Button type="button" size="sm" variant="danger-outline" disabled={stopPending} onClick={stopAction}>
                <Square className="size-3.5" aria-hidden="true" />
                {stopPending ? "停止中…" : "停止"}
              </Button>
            ) : (
              <Button type="button" size="sm" variant="primary" disabled={!target} onClick={() => void runAction()}>
                <Send className="size-3.5" aria-hidden="true" />
                执行「{activeAction?.label}」
              </Button>
            )}
          </div>
        </div>

        <section className="min-h-32 rounded-md border bg-background/60 p-3" aria-live="polite">
          <AgentRunOutput
            run={agentRun}
            running={running}
            onFillTerminal={onFillTerminal}
            empty="操作证据、计划摘要与校验结果将实时流式回传；输出的 Shell 命令可一键填入交互终端联动执行。"
          />
        </section>
      </CardContent>
    </Card>
  )
}
