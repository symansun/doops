"use client"

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import {
  ASSET_KIND_LABELS,
  COLLECTOR_METHOD_LABELS,
  INTERVAL_PRESETS,
  STRATEGY_LABELS,
  TRIGGER_LABELS,
  buildRunReportCSV,
  describeMonitorStatus,
  fetchMonitorRunDetail,
  fetchMonitorRuns,
  fetchMonitorSchedules,
  formatBytesCompact,
  formatIntervalLabel,
  formatPercent,
  mutateMonitorSchedule,
  triggerMonitorRun,
  type MonitorRunDetailData,
  type MonitorRunRecord,
  type MonitorScheduleRecord,
  type MonitorStrategyMeta,
  type MonitorThresholds,
} from "@/lib/monitor-engine"
import {
  analyzeInspectionWithAi,
  buildCustomReportHtml,
  buildCustomReportMarkdown,
} from "@/lib/inspection-report"
import { fetchPlatformSkills, SKILL_DRIVER_LABELS, type PlatformSkillRecord } from "@/lib/skills-registry"
import { HistoryIcon, RefreshIcon, RocketIcon, SparkIcon } from "./icons"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Checkbox,
  Grid,
  Inline,
  Input,
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

function fmtStamp(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function fmtDuration(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

function downloadText(filename: string, content: string, mime: string) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function InspectionCenterPanel({
  session,
  targets,
  initialRunId,
  onClearInitialRun,
  onOpenSkills,
}: {
  session: Session
  targets: Target[]
  initialRunId?: string
  onClearInitialRun?: () => void
  onOpenSkills?: () => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const [schedules, setSchedules] = useState<MonitorScheduleRecord[]>([])
  const [strategies, setStrategies] = useState<MonitorStrategyMeta[]>([])
  const [platformSkills, setPlatformSkills] = useState<PlatformSkillRecord[]>([])
  const [runs, setRuns] = useState<MonitorRunRecord[]>([])
  const [selectedRunId, setSelectedRunId] = useState<string>(initialRunId || "")
  const [runDetail, setRunDetail] = useState<MonitorRunDetailData | null>(null)
  const [loading, setLoading] = useState(true)
  const [detailLoading, setDetailLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [revision, setRevision] = useState(0)

  // Filters
  const [runStatusFilter, setRunStatusFilter] = useState("")
  const [itemStatusFilter, setItemStatusFilter] = useState("")
  const [itemStrategyFilter, setItemStrategyFilter] = useState("")

  // Schedule editor state
  const [formOpen, setFormOpen] = useState(false)
  const [editingId, setEditingId] = useState("")
  const [schedName, setSchedName] = useState("")
  const [schedDesc, setSchedDesc] = useState("")
  const [scopeMode, setScopeMode] = useState<"all_online" | "targets" | "tags">("all_online")
  const [selectedTargetKeys, setSelectedTargetKeys] = useState<string[]>([])
  const [tagsInput, setTagsInput] = useState("")
  const [selectedStrategies, setSelectedStrategies] = useState<string[]>([
    "vm_host",
    "ssh_host",
    "k8s_cluster",
    "container_runtime",
    "app_service",
    "database_service",
  ])
  const [selectedSkills, setSelectedSkills] = useState<string[]>([])
  const [intervalSeconds, setIntervalSeconds] = useState(300)
  const [thresholds, setThresholds] = useState<MonitorThresholds>({
    cpu_warn_percent: 80,
    cpu_crit_percent: 90,
    mem_warn_percent: 80,
    mem_crit_percent: 90,
    disk_warn_percent: 85,
    disk_crit_percent: 95,
    cert_warn_days: 14,
    latency_warn_ms: 1000,
  })

  // Quick ad-hoc dispatch bar state
  const [quickTarget, setQuickTarget] = useState("")
  const [quickStrategies, setQuickStrategies] = useState<string[]>([
    "vm_host",
    "k8s_cluster",
    "container_runtime",
    "app_service",
    "database_service",
  ])
  const [quickSkills, setQuickSkills] = useState<string[]>([])
  const [quickSkillsInitialized, setQuickSkillsInitialized] = useState(false)
  const [sshOpen, setSshOpen] = useState(false)
  const [sshAssetId, setSshAssetId] = useState("")
  const [sshAddr, setSshAddr] = useState("")
  const [sshUser, setSshUser] = useState("")
  const [sshPort, setSshPort] = useState("22")

  useEffect(() => {
    if (initialRunId) setSelectedRunId(initialRunId)
  }, [initialRunId])

  const reload = useCallback(() => setRevision((v) => v + 1), [])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    Promise.all([
      fetchMonitorSchedules(session, ctrl.signal),
      fetchMonitorRuns(session, { status: runStatusFilter || undefined, limit: 50 }, ctrl.signal),
      fetchPlatformSkills(session, {}, ctrl.signal).catch(() => ({ skills: [] as PlatformSkillRecord[] })),
    ])
      .then(([schedRes, runsRes, skillsRes]) => {
        if (ctrl.signal.aborted) return
        setSchedules(schedRes.schedules || [])
        setStrategies(schedRes.strategies || runsRes.strategies || [])
        setRuns(runsRes.runs || [])
        const fetchedSkills = skillsRes.skills || []
        setPlatformSkills(fetchedSkills)
        if (!quickSkillsInitialized && fetchedSkills.length > 0) {
          setQuickSkills(
            fetchedSkills
              .filter((s) => s.enabled && (s.category === "inspection" || s.category === "security"))
              .map((s) => s.slug),
          )
          setQuickSkillsInitialized(true)
        }
        setLoading(false)
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error).message)
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, runStatusFilter, revision, quickSkillsInitialized])

  useEffect(() => {
    if (!selectedRunId) {
      setRunDetail(null)
      return
    }
    const ctrl = new AbortController()
    setDetailLoading(true)
    setError("")
    fetchMonitorRunDetail(session, selectedRunId, ctrl.signal)
      .then((res) => {
        if (!ctrl.signal.aborted) {
          setRunDetail(res)
          setDetailLoading(false)
        }
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error).message)
          setDetailLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, selectedRunId, revision])

  const toggleItem = (list: string[], id: string) =>
    list.includes(id) ? list.filter((x) => x !== id) : [...list, id]

  const skillBySlug = useMemo(() => {
    const map = new Map<string, PlatformSkillRecord>()
    for (const sk of platformSkills) map.set(sk.slug, sk)
    return map
  }, [platformSkills])

  const enabledSkills = useMemo(() => platformSkills.filter((s) => s.enabled), [platformSkills])

  function strategiesFromSkills(slugs: string[]): string[] {
    const set = new Set<string>()
    for (const slug of slugs) {
      const sk = skillBySlug.get(slug)
      if (sk?.driver_mode === "builtin_strategy" && sk.strategy_id) {
        set.add(sk.strategy_id)
      }
    }
    return Array.from(set)
  }

  function openNewSchedule() {
    setEditingId("")
    setSchedName("")
    setSchedDesc("")
    setScopeMode("all_online")
    setSelectedTargetKeys([])
    setTagsInput("")
    const defaultSlugs = enabledSkills
      .filter((s) => s.category === "inspection" || s.category === "security")
      .map((s) => s.slug)
    setSelectedSkills(defaultSlugs)
    setSelectedStrategies(
      defaultSlugs.length > 0
        ? strategiesFromSkills(defaultSlugs)
        : ["vm_host", "ssh_host", "k8s_cluster", "container_runtime", "app_service", "database_service"],
    )
    setIntervalSeconds(300)
    setFormOpen(true)
  }

  function openEditSchedule(s: MonitorScheduleRecord) {
    setEditingId(s.id)
    setSchedName(s.name)
    setSchedDesc(s.description || "")
    setScopeMode(s.scope_mode)
    setSelectedTargetKeys((s.targets || []).map((t) => `${t.cluster}/${t.instance}`))
    setTagsInput((s.tags || []).join(", "))
    setSelectedStrategies(s.strategies || [])
    if (s.skills && s.skills.length > 0) {
      setSelectedSkills(s.skills)
    } else {
      const inferred = platformSkills
        .filter((sk) => sk.strategy_id && (s.strategies || []).includes(sk.strategy_id))
        .map((sk) => sk.slug)
      setSelectedSkills(inferred)
    }
    setIntervalSeconds(s.interval_seconds || 300)
    setThresholds({ ...thresholds, ...(s.thresholds || {}) })
    setFormOpen(true)
  }

  async function saveSchedule(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const targetRefs = selectedTargetKeys.map((k) => {
        const [cluster, instance] = k.split("/")
        return { cluster, instance }
      })
      const tags = tagsInput
        .split(/[,，\s]+/)
        .map((x) => x.trim())
        .filter(Boolean)
      const effectiveStrategies =
        selectedSkills.length > 0 ? strategiesFromSkills(selectedSkills) : selectedStrategies
      await mutateMonitorSchedule(session, {
        action: editingId ? "update" : "create",
        ...(editingId ? { id: editingId } : {}),
        name: schedName.trim(),
        description: schedDesc.trim(),
        enabled: true,
        scope_mode: scopeMode,
        targets: scopeMode === "targets" ? targetRefs : [],
        tags: scopeMode === "tags" ? tags : [],
        strategies: effectiveStrategies,
        skills: selectedSkills,
        thresholds,
        interval_seconds: intervalSeconds,
      })
      setFormOpen(false)
      setNotice(editingId ? "定时巡检计划已更新" : "定时巡检计划已创建，Gateway 将按周期自动驱动所选 Skill 下发执行")
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleToggle(s: MonitorScheduleRecord) {
    if (busy) return
    setBusy(true)
    setError("")
    try {
      await mutateMonitorSchedule(session, { action: "toggle", id: s.id, enabled: !s.enabled })
      setNotice(`已${s.enabled ? "暂停" : "启用"}定时任务「${s.name}」`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(s: MonitorScheduleRecord) {
    if (busy) return
    setBusy(true)
    setError("")
    try {
      await mutateMonitorSchedule(session, { action: "delete", id: s.id })
      setNotice(`已删除定时任务「${s.name}」`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function runScheduleNow(s: MonitorScheduleRecord) {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, { schedule_id: s.id })
      setNotice(`计划「${s.name}」执行完成（#${res.run.id}）：${res.run.summary}`)
      setSelectedRunId(res.run.id)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function runQuickDispatch(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const useSkills = platformSkills.length > 0 && quickSkills.length > 0
      const payload: Parameters<typeof triggerMonitorRun>[1] = useSkills
        ? {
            skills: quickSkills,
            strategies: strategiesFromSkills(quickSkills),
          }
        : {
            strategies: quickStrategies,
          }
      if (quickTarget) {
        const [cluster, instance] = quickTarget.split("/")
        payload.scope_mode = "targets"
        payload.targets = [{ cluster, instance }]
      } else {
        payload.scope_mode = "all_online"
      }
      if (sshOpen && sshAddr.trim() && sshAssetId.trim()) {
        payload.strategies = Array.from(new Set([...(payload.strategies || []), "ssh_host"]))
        if (useSkills) {
          payload.skills = Array.from(new Set([...(payload.skills || []), "inspect-ssh-host"]))
        }
        payload.ssh_hosts = [
          {
            asset_id: sshAssetId.trim(),
            asset_name: sshAssetId.trim(),
            asset_kind: "vm",
            address: sshAddr.trim(),
            ssh_user: sshUser.trim() || undefined,
            port: Number(sshPort) || 22,
          },
        ]
      }
      const res = await triggerMonitorRun(session, payload)
      setNotice(`已向 ${res.run.total_targets} 个目标下发 Skill 巡检并完成收集（#${res.run.id}）`)
      setSelectedRunId(res.run.id)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function retestRun(runId: string) {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, { retest_run_id: runId })
      setNotice(`已完成异常/离线项复检（#${res.run.id}）：${res.run.summary}`)
      setSelectedRunId(res.run.id)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const filteredItems = useMemo(() => {
    const items = runDetail?.items ?? []
    return items.filter((it) => {
      if (itemStatusFilter && it.status !== itemStatusFilter) return false
      if (itemStrategyFilter && it.strategy !== itemStrategyFilter) return false
      return true
    })
  }, [runDetail, itemStatusFilter, itemStrategyFilter])

  // ---------------- Run Detail Report View ----------------
  if (selectedRunId) {
    const run = runDetail?.run
    const st = describeMonitorStatus(run?.status || "running")
    const hasRetestable = (runDetail?.items ?? []).some((i) =>
      ["warning", "critical", "error", "offline"].includes(i.status),
    )

    const aiReport = runDetail ? analyzeInspectionWithAi(runDetail) : null

    return (
      <Stack gap={4}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              onClick={() => {
                setSelectedRunId("")
                onClearInitialRun?.()
              }}
            >
              返回任务与记录
            </Button>
            <h2 className="text-base font-semibold text-foreground">
              巡检执行详情 <span className="font-mono text-sm text-muted-foreground">#{selectedRunId}</span>
            </h2>
            {run && <Badge variant={st.variant}>{st.label}</Badge>}
          </div>
          <Inline gap={2} className="flex-wrap">
            {hasRetestable && (
              <Button size="sm" variant="primary" disabled={busy} onClick={() => void retestRun(selectedRunId)}>
                {busy ? "复检中…" : "仅复检异常/离线项"}
              </Button>
            )}
            {runDetail && aiReport && (
              <>
                <Button
                  size="sm"
                  onClick={() => {
                    const md = buildCustomReportMarkdown(
                      {
                        title: `智能巡检报告 #${selectedRunId}`,
                        period: "7d",
                        sections: ["inspection_vuln", "ai_remediation", "security_events"],
                      },
                      null,
                      runDetail,
                      aiReport,
                    )
                    downloadText(`inspection-${selectedRunId}.html`, buildCustomReportHtml(`智能巡检报告 #${selectedRunId}`, md), "text/html;charset=utf-8")
                  }}
                >
                  导出 HTML 报告
                </Button>
                <Button
                  size="sm"
                  onClick={() => downloadText(`inspection-${selectedRunId}.csv`, buildRunReportCSV(runDetail), "text/csv;charset=utf-8")}
                >
                  导出 CSV
                </Button>
                <Button
                  size="sm"
                  onClick={() => downloadText(`inspection-${selectedRunId}.json`, JSON.stringify(runDetail, null, 2), "application/json")}
                >
                  导出 JSON
                </Button>
              </>
            )}
            <Button size="sm" disabled={detailLoading} onClick={reload}>
              <RefreshIcon width={14} height={14} className={detailLoading ? "animate-spin" : ""} />
              刷新
            </Button>
          </Inline>
        </div>

        {error && <StatusBanner tone="error">{error}</StatusBanner>}
        {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

        {detailLoading && !runDetail ? (
          <p role="status" className="py-8 text-sm text-muted-foreground">正在加载巡检明细与证据…</p>
        ) : runDetail && run ? (
          <>
            {aiReport && (
              <Card>
                <CardContent className="space-y-3 p-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <SparkIcon width={15} height={15} className="text-primary" />
                      <h3 className="text-sm font-semibold text-foreground">浙音大模型 · AI 巡检结果智能分析</h3>
                      <Badge variant="primary">{aiReport.model}</Badge>
                      <Badge variant={aiReport.score >= 85 ? "success" : aiReport.score >= 70 ? "warning" : "danger"}>
                        态势评分 {aiReport.score} / 100
                      </Badge>
                    </div>
                  </div>
                  <p className="text-xs font-medium text-foreground">{aiReport.headline}</p>
                  <p className="text-xs text-muted-foreground">{aiReport.correlationSummary}</p>
                  {aiReport.findings.length > 0 && (
                    <div className="divide-y rounded-md border bg-background/60 text-xs">
                      {aiReport.findings.map((f, idx) => (
                        <div key={`${f.priority}-${idx}`} className="space-y-1 p-2.5">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge variant={f.priority === "P0" ? "danger" : f.priority === "P1" ? "warning" : f.priority === "P2" ? "primary" : "outline"}>
                              {f.priority} · {f.riskLevel}
                            </Badge>
                            <strong className="text-foreground">{f.title}</strong>
                            <span className="text-muted-foreground">影响：{f.businessImpact}</span>
                          </div>
                          <div className="text-muted-foreground">根因分析：{f.rootCause}</div>
                          <div className="font-mono text-2xs text-foreground">修复建议：{f.remediation}</div>
                        </div>
                      ))}
                    </div>
                  )}
                </CardContent>
              </Card>
            )}
            <Card>
              <CardContent className="space-y-3 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>
                    计划：<strong className="text-foreground">{run.schedule_name || "即时手动巡检"}</strong> · 触发：
                    {TRIGGER_LABELS[run.trigger_kind] || run.trigger_kind} · 执行人：{run.triggered_by || "system"}
                  </span>
                  <span className="font-mono">
                    {fmtStamp(run.started_at_ms)} · 耗时 {fmtDuration(run.duration_ms)} · 写入 {run.metrics_written} 条指标
                  </span>
                </div>
                {run.summary && <p className="text-sm font-medium text-foreground">{run.summary}</p>}
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">目标 / 资产项</div>
                    <div className="font-mono text-base font-semibold">{run.total_targets} / {run.total_assets}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">检查通过</div>
                    <div className="font-mono text-base font-semibold text-success">{run.passed_checks}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">告警项</div>
                    <div className="font-mono text-base font-semibold text-warning">{run.warning_checks}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">严重 / 失败</div>
                    <div className="font-mono text-base font-semibold text-destructive">{run.failed_checks}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">离线目标 / 跳过项</div>
                    <div className="font-mono text-base font-semibold">{run.offline_targets} / {run.skipped_checks}</div>
                  </div>
                </div>
              </CardContent>
            </Card>

            <div className="flex flex-wrap items-center gap-2">
              <Select scale="sm" className="w-36" value={itemStatusFilter} onChange={(e) => setItemStatusFilter(e.target.value)}>
                <option value="">全部状态</option>
                <option value="critical">严重</option>
                <option value="warning">告警</option>
                <option value="offline">离线</option>
                <option value="error">错误</option>
                <option value="pass">通过</option>
                <option value="skipped">跳过</option>
              </Select>
              <Select scale="sm" className="w-52" value={itemStrategyFilter} onChange={(e) => setItemStrategyFilter(e.target.value)}>
                <option value="">全部策略 / Skill</option>
                {Array.from(
                  new Set([
                    ...Object.keys(STRATEGY_LABELS),
                    ...(runDetail.items || []).map((i) => i.strategy),
                  ]),
                ).map((k) => (
                  <option key={k} value={k}>
                    {k.startsWith("skill:") ? `Skill: ${k.slice(6)}` : STRATEGY_LABELS[k] || k}
                  </option>
                ))}
              </Select>
              <span className="text-xs text-muted-foreground">显示 {filteredItems.length} / {runDetail.items.length} 条资产策略结果</span>
            </div>

            <Stack gap={3}>
              {filteredItems.map((it) => {
                const itSt = describeMonitorStatus(it.status)
                const m = it.metrics || {}
                const drivenSkillSlug = it.strategy.startsWith("skill:")
                  ? it.strategy.slice(6)
                  : typeof it.evidence?.skill_slug === "string"
                    ? it.evidence.skill_slug
                    : ""
                return (
                  <Card key={it.id}>
                    <CardContent className="space-y-3 p-4">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <Badge variant={itSt.variant}>{itSt.label}</Badge>
                          <strong className="text-sm text-foreground">{it.asset_name || it.asset_id}</strong>
                          <span className="font-mono text-xs text-muted-foreground">{it.cluster}/{it.instance}</span>
                          {drivenSkillSlug && <Badge variant="primary">Skill: {drivenSkillSlug}</Badge>}
                          <Badge variant="outline">{ASSET_KIND_LABELS[it.asset_kind] || it.asset_kind}</Badge>
                          <Badge variant="outline">
                            {it.strategy.startsWith("skill:") ? "Agent Skill 驱动" : STRATEGY_LABELS[it.strategy] || it.strategy}
                          </Badge>
                          <Badge variant="outline">{COLLECTOR_METHOD_LABELS[it.collector_method] || it.collector_method}</Badge>
                        </div>
                        <span className="font-mono text-2xs text-muted-foreground">
                          耗时 {fmtDuration(it.duration_ms)}{it.reason ? ` · ${it.reason}` : ""}
                        </span>
                      </div>

                      {(m.cpu_percent != null || m.memory_percent != null || m.disk_percent != null || m.response_time_ms != null || m.os_info) && (
                        <div className="flex flex-wrap gap-3 rounded-md bg-muted/40 px-3 py-2 font-mono text-xs">
                          {m.cpu_percent != null && <span>CPU {formatPercent(m.cpu_percent)}</span>}
                          {m.memory_percent != null && (
                            <span>
                              内存 {formatPercent(m.memory_percent)} ({formatBytesCompact(m.memory_used_bytes)})
                            </span>
                          )}
                          {m.disk_percent != null && <span>磁盘峰值 {formatPercent(m.disk_percent)}</span>}
                          {m.disk_iops != null && <span>IOPS {m.disk_iops.toFixed(1)}</span>}
                          {m.response_time_ms != null && <span>耗时 {m.response_time_ms.toFixed(1)} ms</span>}
                          {m.os_info && <span className="text-muted-foreground">{m.os_info}</span>}
                        </div>
                      )}

                      {it.checks?.length > 0 && (
                        <Table wrapperClassName="rounded-md border">
                          <TableHeader className="bg-muted/30">
                            <TableRow>
                              <TableHead>检查项</TableHead>
                              <TableHead>结论</TableHead>
                              <TableHead>实测值</TableHead>
                              <TableHead>基线 / 阈值</TableHead>
                              <TableHead>详情</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {it.checks.map((c) => {
                              const cst = describeMonitorStatus(c.status)
                              return (
                                <TableRow key={c.id}>
                                  <TableCell className="text-xs font-medium">{c.name}</TableCell>
                                  <TableCell><Badge variant={cst.variant}>{cst.label}</Badge></TableCell>
                                  <TableCell className="font-mono text-xs">{c.value || "—"}</TableCell>
                                  <TableCell className="font-mono text-2xs text-muted-foreground">{c.threshold || "—"}</TableCell>
                                  <TableCell className="text-xs text-muted-foreground">{c.detail || "—"}</TableCell>
                                </TableRow>
                              )
                            })}
                          </TableBody>
                        </Table>
                      )}

                      {it.evidence && Object.keys(it.evidence).length > 0 && (
                        <details className="text-xs">
                          <summary className="cursor-pointer text-muted-foreground hover:text-foreground">查看脱敏取证快照</summary>
                          <pre className="mt-2 max-h-48 overflow-auto rounded-md border bg-background p-2 font-mono text-2xs">
                            {JSON.stringify(it.evidence, null, 2)}
                          </pre>
                        </details>
                      )}
                    </CardContent>
                  </Card>
                )
              })}
            </Stack>
          </>
        ) : null}
      </Stack>
    )
  }

  // ---------------- Schedules + Quick Dispatch + Run History View ----------------
  return (
    <Stack gap={4}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="space-y-0.5">
          <h2 className="text-base font-semibold text-foreground">Skill 驱动巡检任务、定时调度与执行记录</h2>
          <p className="text-xs text-muted-foreground">
            巡检由系统驱动所选 Skill（内置系统 Skill + 自定义上传 Skill）执行：内置策略类走 Agent 原生高速只读探测，自定义/安全类自动同步至 Agent 并驱动执行。
          </p>
        </div>
        <Inline gap={2}>
          {onOpenSkills && (
            <Button size="sm" onClick={onOpenSkills}>
              + 上传 / 管理 Skill
            </Button>
          )}
          {isAdmin && (
            <Button size="sm" variant="primary" onClick={() => (formOpen ? setFormOpen(false) : openNewSchedule())}>
              {formOpen ? "收起计划表单" : "新建定时任务"}
            </Button>
          )}
          <Button size="sm" disabled={loading} onClick={reload}>
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
        </Inline>
      </div>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

      {/* Quick Ad-hoc Dispatch Bar */}
      <Card>
        <CardContent className="space-y-3 p-4">
          <form onSubmit={(e) => void runQuickDispatch(e)} className="space-y-3">
            <div className="flex flex-wrap items-end justify-between gap-3">
              <div className="flex flex-wrap items-end gap-3">
                <label className="text-xs text-muted-foreground">
                  下发目标 Agent
                  <Select scale="sm" className="mt-1 w-56" value={quickTarget} onChange={(e) => setQuickTarget(e.target.value)}>
                    <option value="">全部在线目标 ({targets.filter((t) => t.status !== "offline").length})</option>
                    {targets.map((t) => (
                      <option key={t.key} value={t.key}>
                        {t.cluster}/{t.instance}{t.status === "offline" ? "（离线）" : ""}
                      </option>
                    ))}
                  </Select>
                </label>
                <Button type="button" size="sm" variant="ghost" onClick={() => setSshOpen(!sshOpen)}>
                  {sshOpen ? "隐藏临时 SSH 主机" : "+ 附加 SSH 远程主机"}
                </Button>
              </div>
              <Button
                type="submit"
                size="sm"
                variant="primary"
                disabled={busy || (platformSkills.length > 0 ? quickSkills.length === 0 : quickStrategies.length === 0)}
              >
                <RocketIcon width={14} height={14} />
                {busy
                  ? "下发执行中…"
                  : platformSkills.length > 0
                    ? `驱动所选 ${quickSkills.length} 个 Skill 立即巡检`
                    : "立即下发 Agent 巡检"}
              </Button>
            </div>

            {platformSkills.length > 0 ? (
              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>选择驱动本次巡检的 Skill（系统内置 + 自定义上传）：</span>
                  <Inline gap={2}>
                    <button
                      type="button"
                      className="text-primary hover:underline"
                      onClick={() => setQuickSkills(enabledSkills.map((s) => s.slug))}
                    >
                      全选已启用 ({enabledSkills.length})
                    </button>
                    <button
                      type="button"
                      className="text-muted-foreground hover:underline"
                      onClick={() => setQuickSkills([])}
                    >
                      清空
                    </button>
                  </Inline>
                </div>
                <div className="flex flex-wrap gap-2">
                  {platformSkills.map((sk) => (
                    <label
                      key={sk.slug}
                      className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs ${
                        sk.enabled ? "bg-background" : "opacity-50"
                      }`}
                    >
                      <Checkbox
                        disabled={!sk.enabled}
                        checked={quickSkills.includes(sk.slug)}
                        onChange={() => setQuickSkills(toggleItem(quickSkills, sk.slug))}
                      />
                      <span className="font-medium text-foreground">{sk.name}</span>
                      <Badge variant={sk.scope === "system" ? "outline" : "success"}>
                        {sk.scope === "system" ? "系统" : "自定义"}
                      </Badge>
                    </label>
                  ))}
                </div>
              </div>
            ) : (
              <div className="flex flex-wrap gap-3">
                {(strategies.length > 0 ? strategies : Object.entries(STRATEGY_LABELS).map(([id, name]) => ({ id, name }))).map((s) => (
                  <label key={s.id} className="inline-flex items-center gap-1.5 text-xs">
                    <Checkbox
                      checked={quickStrategies.includes(s.id)}
                      onChange={() => setQuickStrategies(toggleItem(quickStrategies, s.id))}
                    />
                    <span>{s.name}</span>
                  </label>
                ))}
              </div>
            )}

            {sshOpen && (
              <div className="grid gap-2 rounded-md border bg-muted/30 p-3 sm:grid-cols-4">
                <label className="text-xs text-muted-foreground">
                  远程资产编号
                  <Input scale="sm" className="mt-1" placeholder="如 vm-db-02" value={sshAssetId} onChange={(e) => setSshAssetId(e.target.value)} />
                </label>
                <label className="text-xs text-muted-foreground">
                  主机 IP / 域名
                  <Input scale="sm" className="mt-1" placeholder="如 10.0.1.25" value={sshAddr} onChange={(e) => setSshAddr(e.target.value)} />
                </label>
                <label className="text-xs text-muted-foreground">
                  SSH 用户名（可选）
                  <Input scale="sm" className="mt-1" placeholder="如 ops" value={sshUser} onChange={(e) => setSshUser(e.target.value)} />
                </label>
                <label className="text-xs text-muted-foreground">
                  端口
                  <Input scale="sm" type="number" min={1} max={65535} className="mt-1" value={sshPort} onChange={(e) => setSshPort(e.target.value)} />
                </label>
              </div>
            )}
          </form>
        </CardContent>
      </Card>

      {/* Create / Edit Periodic Schedule Form (Admin) */}
      {formOpen && isAdmin && (
        <Card>
          <CardContent className="p-4">
            <form onSubmit={(e) => void saveSchedule(e)} className="space-y-4">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold">{editingId ? "编辑定时巡检计划" : "新建定时采集与巡检计划"}</h3>
              </div>
              <div className="grid gap-3 sm:grid-cols-3">
                <label className="text-xs text-muted-foreground">
                  计划名称
                  <Input scale="sm" required className="mt-1" placeholder="例如：核心机房 5 分钟监控巡检" value={schedName} onChange={(e) => setSchedName(e.target.value)} />
                </label>
                <label className="text-xs text-muted-foreground">
                  调度周期
                  <Select scale="sm" className="mt-1" value={intervalSeconds} onChange={(e) => setIntervalSeconds(Number(e.target.value))}>
                    {INTERVAL_PRESETS.map((p) => (
                      <option key={p.value} value={p.value}>{p.label}</option>
                    ))}
                  </Select>
                </label>
                <label className="text-xs text-muted-foreground">
                  作用范围
                  <Select scale="sm" className="mt-1" value={scopeMode} onChange={(e) => setScopeMode(e.target.value as "all_online" | "targets" | "tags")}>
                    <option value="all_online">全部活跃 / 在线 Agent</option>
                    <option value="targets">指定 Agent 目标</option>
                    <option value="tags">按机器标签筛选</option>
                  </Select>
                </label>
              </div>

              <label className="block text-xs text-muted-foreground">
                说明（可选）
                <Input scale="sm" className="mt-1" placeholder="记录巡检用途或值班分组" value={schedDesc} onChange={(e) => setSchedDesc(e.target.value)} />
              </label>

              {scopeMode === "targets" && (
                <div className="space-y-1.5">
                  <div className="text-xs font-medium text-muted-foreground">选择目标 Agent</div>
                  <div className="flex flex-wrap gap-3">
                    {targets.map((t) => (
                      <label key={t.key} className="inline-flex items-center gap-1.5 text-xs">
                        <Checkbox
                          checked={selectedTargetKeys.includes(t.key)}
                          onChange={() => setSelectedTargetKeys(toggleItem(selectedTargetKeys, t.key))}
                        />
                        <span className="font-mono">{t.cluster}/{t.instance}</span>
                      </label>
                    ))}
                  </div>
                </div>
              )}

              {scopeMode === "tags" && (
                <label className="block text-xs text-muted-foreground">
                  机器标签（逗号分隔，支持 key=value）
                  <Input scale="sm" className="mt-1" placeholder="例如 env=prod, 院系=计算机" value={tagsInput} onChange={(e) => setTagsInput(e.target.value)} />
                </label>
              )}

              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="text-xs font-medium text-muted-foreground">
                    绑定的巡检 Skill（支持系统内置 Skill 与用户自定义 Skill，按周期自动驱动执行）
                  </div>
                  {onOpenSkills && (
                    <button type="button" className="text-xs text-primary hover:underline" onClick={onOpenSkills}>
                      去上传新 Skill →
                    </button>
                  )}
                </div>
                {platformSkills.length > 0 ? (
                  <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {platformSkills.map((sk) => (
                      <label
                        key={sk.slug}
                        className={`flex items-start gap-2 rounded-md border p-2.5 text-xs ${sk.enabled ? "" : "opacity-55"}`}
                      >
                        <Checkbox
                          disabled={!sk.enabled}
                          checked={selectedSkills.includes(sk.slug)}
                          onChange={() => setSelectedSkills(toggleItem(selectedSkills, sk.slug))}
                        />
                        <span className="min-w-0 space-y-0.5">
                          <span className="flex flex-wrap items-center gap-1.5">
                            <strong className="text-foreground">{sk.name}</strong>
                            <Badge variant={sk.scope === "system" ? "outline" : "success"}>
                              {sk.scope === "system" ? "系统" : "自定义"}
                            </Badge>
                          </span>
                          <span className="block font-mono text-2xs text-muted-foreground">
                            {sk.slug} · {SKILL_DRIVER_LABELS[sk.driver_mode]}
                          </span>
                          <span className="block text-2xs text-muted-foreground">{sk.description}</span>
                        </span>
                      </label>
                    ))}
                  </div>
                ) : (
                  <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {strategies.map((s) => (
                      <label key={s.id} className="flex items-start gap-2 rounded-md border p-2.5 text-xs">
                        <Checkbox
                          checked={selectedStrategies.includes(s.id)}
                          onChange={() => setSelectedStrategies(toggleItem(selectedStrategies, s.id))}
                        />
                        <span className="min-w-0">
                          <strong className="block text-foreground">{s.name}</strong>
                          <span className="text-2xs text-muted-foreground">{s.description}</span>
                        </span>
                      </label>
                    ))}
                  </div>
                )}
              </div>

              <div className="grid gap-2 sm:grid-cols-4">
                <label className="text-xs text-muted-foreground">
                  CPU 告警 / 严重 (%)
                  <Inline gap={1} className="mt-1">
                    <Input scale="sm" type="number" min={1} max={100} value={thresholds.cpu_warn_percent ?? 80} onChange={(e) => setThresholds({ ...thresholds, cpu_warn_percent: Number(e.target.value) })} />
                    <Input scale="sm" type="number" min={1} max={100} value={thresholds.cpu_crit_percent ?? 90} onChange={(e) => setThresholds({ ...thresholds, cpu_crit_percent: Number(e.target.value) })} />
                  </Inline>
                </label>
                <label className="text-xs text-muted-foreground">
                  内存告警 / 严重 (%)
                  <Inline gap={1} className="mt-1">
                    <Input scale="sm" type="number" min={1} max={100} value={thresholds.mem_warn_percent ?? 80} onChange={(e) => setThresholds({ ...thresholds, mem_warn_percent: Number(e.target.value) })} />
                    <Input scale="sm" type="number" min={1} max={100} value={thresholds.mem_crit_percent ?? 90} onChange={(e) => setThresholds({ ...thresholds, mem_crit_percent: Number(e.target.value) })} />
                  </Inline>
                </label>
                <label className="text-xs text-muted-foreground">
                  磁盘告警 / 严重 (%)
                  <Inline gap={1} className="mt-1">
                    <Input scale="sm" type="number" min={1} max={100} value={thresholds.disk_warn_percent ?? 85} onChange={(e) => setThresholds({ ...thresholds, disk_warn_percent: Number(e.target.value) })} />
                    <Input scale="sm" type="number" min={1} max={100} value={thresholds.disk_crit_percent ?? 95} onChange={(e) => setThresholds({ ...thresholds, disk_crit_percent: Number(e.target.value) })} />
                  </Inline>
                </label>
                <label className="text-xs text-muted-foreground">
                  HTTPS 证书预警 (天)
                  <Input scale="sm" type="number" min={1} max={365} className="mt-1" value={thresholds.cert_warn_days ?? 14} onChange={(e) => setThresholds({ ...thresholds, cert_warn_days: Number(e.target.value) })} />
                </label>
              </div>

              <Inline gap={2}>
                <Button
                  type="submit"
                  size="sm"
                  variant="primary"
                  disabled={busy || !schedName.trim() || (platformSkills.length > 0 ? selectedSkills.length === 0 : selectedStrategies.length === 0)}
                >
                  {busy ? "保存中…" : editingId ? "保存修改" : "创建并启用定时任务"}
                </Button>
                <Button type="button" size="sm" onClick={() => setFormOpen(false)}>取消</Button>
              </Inline>
            </form>
          </CardContent>
        </Card>
      )}

      {/* Scheduled Jobs List */}
      <Card>
        <CardContent className="space-y-3 p-4">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold text-foreground">定时巡检计划 ({schedules.length})</h3>
          </div>
          {schedules.length === 0 ? (
            <p className="py-4 text-xs text-muted-foreground">
              尚未创建定时巡检任务。管理员点击右上角「新建定时任务」后，Gateway 后台调度器将按周期自动向在线 Agent 下发采集与巡检。
            </p>
          ) : (
            <div className="space-y-2.5">
              {schedules.map((s) => {
                const lastSt = describeMonitorStatus(s.last_status || "")
                return (
                  <div key={s.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3">
                    <div className="min-w-0 space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <strong className="text-sm text-foreground">{s.name}</strong>
                        <Badge variant={s.enabled ? "success" : "default"}>{s.enabled ? "运行中" : "已暂停"}</Badge>
                        <Badge variant="outline">{formatIntervalLabel(s.interval_seconds)}</Badge>
                        {s.last_status && <Badge variant={lastSt.variant}>上次：{lastSt.label}</Badge>}
                      </div>
                      {s.description && <p className="text-xs text-muted-foreground">{s.description}</p>}
                      <div className="flex flex-wrap gap-1">
                        {s.skills && s.skills.length > 0
                          ? s.skills.map((slug) => {
                              const sk = skillBySlug.get(slug)
                              return (
                                <Badge key={slug} variant={sk?.scope === "custom" ? "success" : "primary"}>
                                  Skill: {sk?.name || slug}
                                </Badge>
                              )
                            })
                          : s.strategies.map((stId) => (
                              <Badge key={stId} variant="outline">{STRATEGY_LABELS[stId] || stId}</Badge>
                            ))}
                      </div>
                      <p className="font-mono text-2xs text-muted-foreground">
                        范围：{s.scope_mode === "all_online" ? "全部在线目标" : s.scope_mode === "tags" ? `标签 ${s.tags.join(",")}` : `${s.targets.length} 个目标`}
                        {" · "}下次执行：{s.enabled ? fmtStamp(s.next_run_at_ms) : "已暂停"}
                        {s.last_run_at_ms > 0 && ` · 上次执行：${fmtStamp(s.last_run_at_ms)}`}
                      </p>
                    </div>
                    <Inline gap={2} className="flex-wrap">
                      <Button size="sm" variant="primary" disabled={busy} onClick={() => void runScheduleNow(s)}>
                        立即执行
                      </Button>
                      {s.last_run_id && (
                        <Button size="sm" onClick={() => setSelectedRunId(s.last_run_id!)}>
                          上次报告
                        </Button>
                      )}
                      {isAdmin && (
                        <>
                          <Button size="sm" disabled={busy} onClick={() => openEditSchedule(s)}>编辑</Button>
                          <Button size="sm" disabled={busy} onClick={() => void handleToggle(s)}>
                            {s.enabled ? "暂停" : "启用"}
                          </Button>
                          <Button size="sm" variant="danger-outline" disabled={busy} onClick={() => void handleDelete(s)}>
                            删除
                          </Button>
                        </>
                      )}
                    </Inline>
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Run History Table */}
      <Card>
        <CardContent className="space-y-3 p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              <HistoryIcon width={15} height={15} className="text-muted-foreground" />
              <h3 className="text-sm font-semibold text-foreground">巡检执行历史记录 ({runs.length})</h3>
            </div>
            <Select scale="sm" className="w-36" value={runStatusFilter} onChange={(e) => setRunStatusFilter(e.target.value)}>
              <option value="">全部状态</option>
              <option value="success">正常</option>
              <option value="warning">告警</option>
              <option value="critical">严重</option>
              <option value="partial">部分完成</option>
              <option value="failed">失败</option>
            </Select>
          </div>

          {runs.length === 0 ? (
            <p className="py-6 text-center text-xs text-muted-foreground">暂无巡检执行记录。使用上方「立即下发 Agent 巡检」即可生成首条记录。</p>
          ) : (
            <Table wrapperClassName="rounded-lg border">
              <TableHeader className="bg-muted/40">
                <TableRow>
                  <TableHead>执行编号 / 计划</TableHead>
                  <TableHead>触发方式</TableHead>
                  <TableHead>开始时间 / 耗时</TableHead>
                  <TableHead>目标 / 资产</TableHead>
                  <TableHead>通过 / 告警 / 严重 / 离线</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {runs.map((r) => {
                  const st = describeMonitorStatus(r.status)
                  return (
                    <TableRow key={r.id}>
                      <TableCell>
                        <button type="button" onClick={() => setSelectedRunId(r.id)} className="text-left font-medium text-primary hover:underline">
                          {r.schedule_name || "即时手动巡检"}
                        </button>
                        <div className="font-mono text-2xs text-muted-foreground">{r.id}</div>
                      </TableCell>
                      <TableCell className="text-xs">
                        <Badge variant="outline">{TRIGGER_LABELS[r.trigger_kind] || r.trigger_kind}</Badge>
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        <div>{fmtStamp(r.started_at_ms)}</div>
                        <div className="text-2xs text-muted-foreground">{fmtDuration(r.duration_ms)} · {r.metrics_written} 指标</div>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{r.total_targets} / {r.total_assets}</TableCell>
                      <TableCell className="font-mono text-xs">
                        <span className="text-success">{r.passed_checks}</span>
                        {" / "}
                        <span className="text-warning">{r.warning_checks}</span>
                        {" / "}
                        <span className="text-destructive">{r.failed_checks}</span>
                        {" / "}
                        <span className="text-muted-foreground">{r.offline_targets}</span>
                      </TableCell>
                      <TableCell><Badge variant={st.variant}>{st.label}</Badge></TableCell>
                      <TableCell className="text-right">
                        <Inline gap={2} className="justify-end">
                          <Button size="sm" onClick={() => setSelectedRunId(r.id)}>查看明细</Button>
                          {(r.warning_checks > 0 || r.failed_checks > 0 || r.offline_targets > 0) && (
                            <Button size="sm" disabled={busy} onClick={() => void retestRun(r.id)}>复检</Button>
                          )}
                        </Inline>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </Stack>
  )
}
