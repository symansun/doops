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
  formatIntervalLabel,
  mutateMonitorSchedule,
  triggerMonitorRun,
  type MonitorRunDetailData,
  type MonitorRunItemRecord,
  type MonitorRunRecord,
  type MonitorScheduleRecord,
} from "@/lib/monitor-engine"
import { fetchPlatformSkills, type PlatformSkillRecord } from "@/lib/skills-registry"
import { HistoryIcon, RefreshIcon, RocketIcon } from "./icons"
import { InspectionArtifactViewer } from "./inspection-artifact-viewer"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Checkbox,
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
  Textarea,
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

function buildLegacyFallbackArtifact(item: MonitorRunItemRecord): Record<string, unknown> {
  const out: Record<string, unknown> = {
    asset_id: item.asset_id,
    asset_name: item.asset_name,
    asset_kind: item.asset_kind,
    strategy: item.strategy,
    status: item.status,
  }
  const m = item.metrics || {}
  if (m.cpu_percent != null) out.cpu_percent = m.cpu_percent
  if (m.memory_percent != null) out.memory_percent = m.memory_percent
  if (m.disk_percent != null) out.disk_percent = m.disk_percent
  if (m.disk_iops != null) out.disk_iops = m.disk_iops
  if (m.response_time_ms != null) out.response_time_ms = m.response_time_ms
  if (m.os_info) out.os_info = m.os_info
  if (m.kernel_info) out.kernel_info = m.kernel_info
  if (item.checks?.length) {
    out.checks = item.checks.map((c) => ({
      name: c.name,
      status: c.status,
      value: c.value || "—",
      threshold: c.threshold || "—",
      detail: c.detail || "—",
    }))
  }
  if (item.evidence && Object.keys(item.evidence).length > 0) {
    out.evidence = item.evidence
  }
  return out
}

export function InspectionCenterPanel({
  session,
  targets,
  mode = "runs",
  initialRunId,
  onSelectRun,
  onClearInitialRun,
  onScheduleCountChange,
  onOpenSkills,
}: {
  session: Session
  targets: Target[]
  mode?: "runs" | "schedules"
  initialRunId?: string
  onSelectRun?: (runId: string) => void
  onClearInitialRun?: () => void
  onScheduleCountChange?: (count: number) => void
  onOpenSkills?: () => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const [schedules, setSchedules] = useState<MonitorScheduleRecord[]>([])
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

  // Minimal schedule form state: Name + Interval + Target Scope + Skill + Prompt + optional Command
  const [formOpen, setFormOpen] = useState(false)
  const [editingId, setEditingId] = useState("")
  const [schedName, setSchedName] = useState("")
  const [schedPrompt, setSchedPrompt] = useState("")
  const [schedCommand, setSchedCommand] = useState("")
  const [scopeMode, setScopeMode] = useState<"all_online" | "targets">("all_online")
  const [selectedTargetKeys, setSelectedTargetKeys] = useState<string[]>([])
  const [selectedSkills, setSelectedSkills] = useState<string[]>([])
  const [intervalSeconds, setIntervalSeconds] = useState(300)

  // Minimal quick dispatch state: Target + Skill + Prompt + optional Command
  const [quickTarget, setQuickTarget] = useState("")
  const [quickSkills, setQuickSkills] = useState<string[]>([])
  const [quickSkillsInitialized, setQuickSkillsInitialized] = useState(false)
  const [quickPrompt, setQuickPrompt] = useState("")
  const [quickCommand, setQuickCommand] = useState("")
  const [skillPickerOpen, setSkillPickerOpen] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)

  useEffect(() => {
    setSelectedRunId(initialRunId || "")
  }, [initialRunId])

  const openRun = useCallback(
    (runId: string) => {
      setSelectedRunId(runId)
      onSelectRun?.(runId)
    },
    [onSelectRun],
  )

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
        const nextSchedules = schedRes.schedules || []
        setSchedules(nextSchedules)
        onScheduleCountChange?.(nextSchedules.length)
        setRuns(runsRes.runs || [])
        const fetchedSkills = skillsRes.skills || []
        setPlatformSkills(fetchedSkills)
        if (!quickSkillsInitialized && fetchedSkills.length > 0) {
          const defaultSkill = fetchedSkills.find((s) => s.enabled && s.slug === "inspect-vm-host") || fetchedSkills.find((s) => s.enabled)
          setQuickSkills(defaultSkill ? [defaultSkill.slug] : [])
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
  }, [session, runStatusFilter, revision, quickSkillsInitialized, onScheduleCountChange])

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
    setSchedPrompt("")
    setSchedCommand("")
    setScopeMode("all_online")
    setSelectedTargetKeys([])
    const firstSkill = enabledSkills.find((s) => s.slug === "inspect-vm-host") || enabledSkills[0]
    setSelectedSkills(firstSkill ? [firstSkill.slug] : [])
    setIntervalSeconds(300)
    setFormOpen(true)
  }

  function openEditSchedule(s: MonitorScheduleRecord) {
    setEditingId(s.id)
    setSchedName(s.name)
    setSchedPrompt(s.prompt || "")
    setSchedCommand(s.command || "")
    setScopeMode(s.scope_mode === "targets" ? "targets" : "all_online")
    setSelectedTargetKeys((s.targets || []).map((t) => `${t.cluster}/${t.instance}`))
    if (s.skills && s.skills.length > 0) {
      setSelectedSkills(s.skills)
    } else if (!s.prompt && !s.command) {
      const inferred = platformSkills
        .filter((sk) => sk.strategy_id && (s.strategies || []).includes(sk.strategy_id))
        .map((sk) => sk.slug)
      setSelectedSkills(inferred)
    } else {
      setSelectedSkills([])
    }
    setIntervalSeconds(s.interval_seconds || 300)
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
      await mutateMonitorSchedule(session, {
        action: editingId ? "update" : "create",
        ...(editingId ? { id: editingId } : {}),
        name: schedName.trim(),
        prompt: schedPrompt.trim(),
        command: schedCommand.trim(),
        enabled: true,
        scope_mode: scopeMode,
        targets: scopeMode === "targets" ? targetRefs : [],
        strategies: strategiesFromSkills(selectedSkills),
        skills: selectedSkills,
        interval_seconds: intervalSeconds,
      })
      setFormOpen(false)
      setNotice(editingId ? "定时巡检计划已更新" : "定时巡检计划已创建")
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
      setNotice(`计划「${s.name}」执行完成（#${res.run.id}）`)
      openRun(res.run.id)
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
      const promptText = quickPrompt.trim()
      const commandText = commandOpen ? quickCommand.trim() : ""
      const payload: Parameters<typeof triggerMonitorRun>[1] = {}
      if (quickSkills.length > 0) {
        payload.skills = quickSkills
        const derived = strategiesFromSkills(quickSkills)
        if (derived.length > 0) payload.strategies = derived
      }
      if (promptText) payload.prompt = promptText
      if (commandText) payload.command = commandText
      if (quickTarget) {
        const [cluster, instance] = quickTarget.split("/")
        payload.scope_mode = "targets"
        payload.targets = [{ cluster, instance }]
      } else {
        payload.scope_mode = "all_online"
      }
      const res = await triggerMonitorRun(session, payload)
      setNotice(`巡检已完成（#${res.run.id}），共生成 ${res.items.length} 份目标产物`)
      openRun(res.run.id)
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
      setNotice(`已完成异常项复检（#${res.run.id}）`)
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

  function describeRunTitle(r: MonitorRunRecord): string {
    if (r.schedule_name) return r.schedule_name
    if (r.prompt) return r.prompt.split(/\r?\n/)[0].slice(0, 64)
    if (r.skills && r.skills.length > 0) {
      const first = skillBySlug.get(r.skills[0])?.name || r.skills[0]
      return r.skills.length === 1 ? first : `${first} 等 ${r.skills.length} 个 Skill`
    }
    if (r.command) return "自定义命令巡检"
    return "即时巡检"
  }

  // ---------------- Run Detail & Artifact View ----------------
  if (selectedRunId) {
    const run = runDetail?.run
    const st = describeMonitorStatus(run?.status || "running")
    const hasRetestable = (runDetail?.items ?? []).some((i) =>
      ["warning", "critical", "error", "offline"].includes(i.status),
    )

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
              返回执行记录
            </Button>
            <h2 className="text-base font-semibold text-foreground">
              巡检产物与详情 <span className="font-mono text-sm text-muted-foreground">#{selectedRunId}</span>
            </h2>
            {run && <Badge variant={st.variant}>{st.label}</Badge>}
          </div>
          <Inline gap={2} className="flex-wrap">
            {hasRetestable && (
              <Button size="sm" variant="primary" disabled={busy} onClick={() => void retestRun(selectedRunId)}>
                {busy ? "复检中…" : "复检异常项"}
              </Button>
            )}
            {runDetail && (
              <>
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
                  导出完整 JSON
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
          <p role="status" className="py-8 text-sm text-muted-foreground">正在加载巡检产物…</p>
        ) : runDetail && run ? (
          <>
            <Card>
              <CardContent className="space-y-3 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>
                    任务：<strong className="text-foreground">{describeRunTitle(run)}</strong> · 触发：
                    {TRIGGER_LABELS[run.trigger_kind] || run.trigger_kind} · 执行人：{run.triggered_by || "system"}
                  </span>
                  <span className="font-mono">
                    {fmtStamp(run.started_at_ms)} · 耗时 {fmtDuration(run.duration_ms)}
                  </span>
                </div>

                {(run.skills?.length || run.prompt || run.command) && (
                  <div className="space-y-2 rounded-md border bg-muted/20 p-3 text-xs">
                    {run.skills && run.skills.length > 0 && (
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="text-muted-foreground">关联 Skill：</span>
                        {run.skills.map((slug) => (
                          <Badge key={slug} variant="primary">
                            {skillBySlug.get(slug)?.name || slug}
                          </Badge>
                        ))}
                      </div>
                    )}
                    {run.prompt && (
                      <div className="space-y-0.5">
                        <span className="text-muted-foreground">巡检提示词：</span>
                        <p className="whitespace-pre-wrap text-foreground">{run.prompt}</p>
                      </div>
                    )}
                    {run.command && (
                      <div className="space-y-0.5">
                        <span className="text-muted-foreground">执行命令：</span>
                        <pre className="max-h-28 overflow-auto rounded border bg-background px-2 py-1.5 font-mono text-2xs text-foreground">
                          {run.command}
                        </pre>
                      </div>
                    )}
                  </div>
                )}

                {run.summary && <p className="text-sm font-medium text-foreground">{run.summary}</p>}
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">目标 / 产物数</div>
                    <div className="font-mono text-base font-semibold">{run.total_targets} / {run.total_assets}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">通过</div>
                    <div className="font-mono text-base font-semibold text-success">{run.passed_checks}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">告警</div>
                    <div className="font-mono text-base font-semibold text-warning">{run.warning_checks}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">严重 / 失败</div>
                    <div className="font-mono text-base font-semibold text-destructive">{run.failed_checks}</div>
                  </div>
                  <div className="rounded-md border bg-background/60 p-2.5">
                    <div className="text-2xs text-muted-foreground">离线 / 跳过</div>
                    <div className="font-mono text-base font-semibold">{run.offline_targets} / {run.skipped_checks}</div>
                  </div>
                </div>
              </CardContent>
            </Card>

            {runDetail.items.length > 1 && (
              <div className="flex flex-wrap items-center gap-2">
                <Select scale="sm" className="w-36" value={itemStatusFilter} onChange={(e) => setItemStatusFilter(e.target.value)}>
                  <option value="">全部状态</option>
                  <option value="critical">严重</option>
                  <option value="warning">告警</option>
                  <option value="offline">离线</option>
                  <option value="error">错误</option>
                  <option value="pass">正常</option>
                  <option value="skipped">跳过</option>
                </Select>
                <Select scale="sm" className="w-52" value={itemStrategyFilter} onChange={(e) => setItemStrategyFilter(e.target.value)}>
                  <option value="">全部来源 / Skill</option>
                  {Array.from(new Set(runDetail.items.map((i) => i.strategy))).map((k) => (
                    <option key={k} value={k}>
                      {k.startsWith("skill:") ? `Skill: ${k.slice(6)}` : STRATEGY_LABELS[k] || k}
                    </option>
                  ))}
                </Select>
                <span className="text-xs text-muted-foreground">显示 {filteredItems.length} / {runDetail.items.length} 份目标产物</span>
              </div>
            )}

            <Stack gap={3}>
              {filteredItems.map((it) => {
                const itSt = describeMonitorStatus(it.status)
                const drivenSkillSlug = it.strategy.startsWith("skill:")
                  ? it.strategy.slice(6)
                  : typeof it.evidence?.skill_slug === "string"
                    ? it.evidence.skill_slug
                    : ""
                const hasNativeArtifact = Boolean(
                  it.artifact_json !== undefined ||
                    (it.artifact_markdown && it.artifact_markdown.trim()) ||
                    (it.raw_output && it.raw_output.trim()),
                )
                const effectiveJson = hasNativeArtifact ? it.artifact_json : buildLegacyFallbackArtifact(it)
                const effectiveFormat = hasNativeArtifact ? it.artifact_format : "json"

                return (
                  <Card key={it.id}>
                    <CardContent className="space-y-3 p-4">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <Badge variant={itSt.variant}>{itSt.label}</Badge>
                          <strong className="text-sm text-foreground">{it.asset_name || it.asset_id}</strong>
                          <span className="font-mono text-xs text-muted-foreground">{it.cluster}/{it.instance}</span>
                          {drivenSkillSlug && <Badge variant="primary">Skill: {drivenSkillSlug}</Badge>}
                          <Badge variant="outline">{COLLECTOR_METHOD_LABELS[it.collector_method] || it.collector_method}</Badge>
                          <Badge variant="outline">{ASSET_KIND_LABELS[it.asset_kind] || it.asset_kind}</Badge>
                        </div>
                        <span className="font-mono text-2xs text-muted-foreground">
                          耗时 {fmtDuration(it.duration_ms)}{it.reason ? ` · ${it.reason}` : ""}
                        </span>
                      </div>

                      <InspectionArtifactViewer
                        format={effectiveFormat}
                        markdown={it.artifact_markdown}
                        jsonData={effectiveJson}
                        rawOutput={it.raw_output}
                        filePrefix={`${selectedRunId}-${it.cluster}-${it.instance}-${it.asset_id}`}
                      />
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

  // ---------------- Schedules Tab View ----------------
  if (mode === "schedules") {
    const canSaveSchedule =
      Boolean(schedName.trim()) &&
      (selectedSkills.length > 0 || Boolean(schedPrompt.trim()) || Boolean(schedCommand.trim()))

    return (
      <Stack gap={4}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-sm font-medium text-muted-foreground">
            定时巡检计划 ({schedules.length})
          </span>
          <Inline gap={2}>
            {isAdmin && (
              <Button size="sm" variant="primary" onClick={() => (formOpen ? setFormOpen(false) : openNewSchedule())}>
                {formOpen ? "收起表单" : "+ 新建定时计划"}
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

        {formOpen && isAdmin && (
          <Card>
            <CardContent className="p-4">
              <form onSubmit={(e) => void saveSchedule(e)} className="space-y-4">
                <h3 className="text-sm font-semibold">{editingId ? "编辑定时计划" : "新建定时计划"}</h3>
                <div className="grid gap-3 sm:grid-cols-3">
                  <label className="text-xs text-muted-foreground">
                    计划名称
                    <Input scale="sm" required className="mt-1 w-full" placeholder="例如：主机每 5 分钟巡检" value={schedName} onChange={(e) => setSchedName(e.target.value)} />
                  </label>
                  <label className="text-xs text-muted-foreground">
                    调度周期
                    <Select scale="sm" className="mt-1 w-full" value={intervalSeconds} onChange={(e) => setIntervalSeconds(Number(e.target.value))}>
                      {INTERVAL_PRESETS.map((p) => (
                        <option key={p.value} value={p.value}>{p.label}</option>
                      ))}
                    </Select>
                  </label>
                  <label className="text-xs text-muted-foreground">
                    目标范围
                    <Select scale="sm" className="mt-1 w-full" value={scopeMode} onChange={(e) => setScopeMode(e.target.value as "all_online" | "targets")}>
                      <option value="all_online">全部在线目标</option>
                      <option value="targets">指定 Agent 目标</option>
                    </Select>
                  </label>
                </div>

                {scopeMode === "targets" && (
                  <div className="flex flex-wrap gap-3 rounded-md border bg-muted/20 p-2.5">
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
                )}

                <div className="space-y-1.5">
                  <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                    <span>关联 Skill（已选 {selectedSkills.length}，可不选）</span>
                    {onOpenSkills && (
                      <button type="button" className="text-primary hover:underline" onClick={onOpenSkills}>
                        管理 / 上传 Skill →
                      </button>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {platformSkills.map((sk) => (
                      <label
                        key={sk.slug}
                        className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs ${sk.enabled ? "bg-background" : "opacity-50"}`}
                      >
                        <Checkbox
                          disabled={!sk.enabled}
                          checked={selectedSkills.includes(sk.slug)}
                          onChange={() => setSelectedSkills(toggleItem(selectedSkills, sk.slug))}
                        />
                        <span className="font-medium text-foreground">{sk.name}</span>
                        {sk.scope === "custom" && <Badge variant="success">自定义</Badge>}
                      </label>
                    ))}
                  </div>
                </div>

                <label className="block text-xs text-muted-foreground">
                  巡检提示词（可选）
                  <Textarea
                    rows={2}
                    className="mt-1 w-full text-xs"
                    placeholder="描述检查内容与期望输出的 Markdown / JSON 结构"
                    value={schedPrompt}
                    onChange={(e) => setSchedPrompt(e.target.value)}
                  />
                </label>

                <label className="block text-xs text-muted-foreground">
                  直接执行命令（可选，任意 Shell 命令，输出 JSON 或 Markdown+JSON 将自动动态可视化）
                  <Textarea
                    rows={2}
                    className="mt-1 w-full font-mono text-xs"
                    placeholder="留空则由所选 Skill / 提示词驱动"
                    value={schedCommand}
                    onChange={(e) => setSchedCommand(e.target.value)}
                  />
                </label>

                <Inline gap={2}>
                  <Button type="submit" size="sm" variant="primary" disabled={busy || !canSaveSchedule}>
                    {busy ? "保存中…" : editingId ? "保存修改" : "创建计划"}
                  </Button>
                  <Button type="button" size="sm" variant="ghost" onClick={() => setFormOpen(false)}>取消</Button>
                </Inline>
              </form>
            </CardContent>
          </Card>
        )}

        {schedules.length === 0 ? (
          <Card>
            <CardContent className="py-8 text-center text-xs text-muted-foreground">
              暂无定时巡检计划。点击右上角「+ 新建定时计划」关联 Skill、提示词或命令。
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-2.5">
            {schedules.map((s) => {
              const lastSt = describeMonitorStatus(s.last_status || "")
              const tags =
                s.skills && s.skills.length > 0
                  ? s.skills.map((slug) => ({
                      key: slug,
                      label: skillBySlug.get(slug)?.name || slug,
                      custom: skillBySlug.get(slug)?.scope === "custom",
                    }))
                  : (s.strategies || []).map((stId) => ({
                      key: stId,
                      label: STRATEGY_LABELS[stId] || stId,
                      custom: false,
                    }))
              const visibleTags = tags.slice(0, 4)
              const extraTags = tags.length - visibleTags.length
              return (
                <Card key={s.id}>
                  <CardContent className="flex flex-wrap items-center justify-between gap-3 p-3.5">
                    <div className="min-w-0 flex-1 space-y-1.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <strong className="text-sm text-foreground">{s.name}</strong>
                        <Badge variant={s.enabled ? "success" : "default"}>{s.enabled ? "运行中" : "已暂停"}</Badge>
                        <Badge variant="outline">{formatIntervalLabel(s.interval_seconds)}</Badge>
                        {s.command && <Badge variant="primary">直接命令</Badge>}
                        {s.last_status && <Badge variant={lastSt.variant}>上次：{lastSt.label}</Badge>}
                      </div>
                      {visibleTags.length > 0 && (
                        <div className="flex flex-wrap items-center gap-1.5">
                          {visibleTags.map((t) => (
                            <Badge key={t.key} variant={t.custom ? "success" : "outline"}>{t.label}</Badge>
                          ))}
                          {extraTags > 0 && <Badge variant="outline">+{extraTags} 项</Badge>}
                        </div>
                      )}
                      {s.prompt && (
                        <p className="truncate text-xs text-muted-foreground" title={s.prompt}>
                          提示词：{s.prompt}
                        </p>
                      )}
                      <p className="font-mono text-2xs text-muted-foreground">
                        {s.scope_mode === "targets" ? `${s.targets.length} 个目标` : "全部在线目标"}
                        {" · "}下次：{s.enabled ? fmtStamp(s.next_run_at_ms) : "已暂停"}
                        {s.last_run_at_ms > 0 && ` · 上次：${fmtStamp(s.last_run_at_ms)}`}
                      </p>
                    </div>
                    <Inline gap={2} className="flex-wrap">
                      <Button size="sm" variant="primary" disabled={busy} onClick={() => void runScheduleNow(s)}>
                        立即执行
                      </Button>
                      {s.last_run_id && (
                        <Button size="sm" onClick={() => openRun(s.last_run_id!)}>
                          查看产物
                        </Button>
                      )}
                      {isAdmin && (
                        <>
                          <Button size="sm" variant="ghost" disabled={busy} onClick={() => openEditSchedule(s)}>编辑</Button>
                          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void handleToggle(s)}>
                            {s.enabled ? "暂停" : "启用"}
                          </Button>
                          <Button size="sm" variant="danger-outline" disabled={busy} onClick={() => void handleDelete(s)}>
                            删除
                          </Button>
                        </>
                      )}
                    </Inline>
                  </CardContent>
                </Card>
              )
            })}
          </div>
        )}
      </Stack>
    )
  }

  // ---------------- Runs Tab View (Minimal Skill + Prompt + Direct Command Dispatch + History) ----------------
  const canRunQuick =
    quickSkills.length > 0 ||
    Boolean(quickPrompt.trim()) ||
    Boolean(commandOpen && quickCommand.trim())

  return (
    <Stack gap={3}>
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

      <Card>
        <CardContent className="p-3.5">
          <form onSubmit={(e) => void runQuickDispatch(e)} className="space-y-2.5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2">
                <Select
                  scale="sm"
                  aria-label="选择巡检目标"
                  className="w-52"
                  value={quickTarget}
                  onChange={(e) => setQuickTarget(e.target.value)}
                >
                  <option value="">全部在线目标 ({targets.filter((t) => t.status !== "offline").length})</option>
                  {targets.map((t) => (
                    <option key={t.key} value={t.key}>
                      {t.cluster}/{t.instance}{t.status === "offline" ? "（离线）" : ""}
                    </option>
                  ))}
                </Select>
                <Button
                  type="button"
                  size="sm"
                  variant={skillPickerOpen ? "primary" : "default"}
                  onClick={() => setSkillPickerOpen((v) => !v)}
                >
                  关联 Skill ({quickSkills.length}/{enabledSkills.length}) {skillPickerOpen ? "▴" : "▾"}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant={commandOpen ? "primary" : "ghost"}
                  onClick={() => setCommandOpen((v) => !v)}
                >
                  {commandOpen ? "收起命令" : "+ 直接执行命令"}
                </Button>
              </div>
              <Inline gap={2}>
                <Button
                  type="submit"
                  size="sm"
                  variant="primary"
                  disabled={busy || !canRunQuick}
                >
                  <RocketIcon width={14} height={14} />
                  {busy ? "执行中…" : "立即巡检"}
                </Button>
                <Button type="button" size="sm" variant="ghost" disabled={loading} onClick={reload}>
                  <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} />
                </Button>
              </Inline>
            </div>

            <Textarea
              rows={2}
              aria-label="巡检提示词"
              className="w-full text-xs"
              placeholder="输入巡检提示词（可选，如：检查磁盘、端口与进程并输出 Markdown + JSON 产物）"
              value={quickPrompt}
              onChange={(e) => setQuickPrompt(e.target.value)}
            />

            {skillPickerOpen && (
              <div className="space-y-2 border-t pt-2.5">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>选择关联 Skill（可不选，仅凭提示词或命令执行）：</span>
                  <Inline gap={2}>
                    <button
                      type="button"
                      className="text-primary hover:underline"
                      onClick={() => setQuickSkills(enabledSkills.map((s) => s.slug))}
                    >
                      全选 ({enabledSkills.length})
                    </button>
                    <button
                      type="button"
                      className="text-muted-foreground hover:underline"
                      onClick={() => setQuickSkills([])}
                    >
                      清空
                    </button>
                    {onOpenSkills && (
                      <button type="button" className="text-primary hover:underline" onClick={onOpenSkills}>
                        管理 / 上传 Skill →
                      </button>
                    )}
                  </Inline>
                </div>
                <div className="flex flex-wrap gap-2">
                  {platformSkills.map((sk) => (
                    <label
                      key={sk.slug}
                      className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs ${
                        sk.enabled ? "bg-background" : "opacity-45"
                      }`}
                    >
                      <Checkbox
                        disabled={!sk.enabled}
                        checked={quickSkills.includes(sk.slug)}
                        onChange={() => setQuickSkills(toggleItem(quickSkills, sk.slug))}
                      />
                      <span className="text-foreground">{sk.name}</span>
                      {sk.scope === "custom" && <Badge variant="success">自定义</Badge>}
                    </label>
                  ))}
                </div>
              </div>
            )}

            {commandOpen && (
              <div className="space-y-1.5 border-t pt-2.5">
                <span className="block text-xs text-muted-foreground">
                  在目标 Agent 上直接执行任意命令（stdout 为 JSON 或 Markdown+JSON 时自动动态可视化）：
                </span>
                <Textarea
                  rows={3}
                  aria-label="直接执行命令"
                  className="w-full font-mono text-xs"
                  placeholder="输入任意 Shell 命令，例如：df -h 或输出 JSON / Markdown+JSON 的检查脚本"
                  value={quickCommand}
                  onChange={(e) => setQuickCommand(e.target.value)}
                />
              </div>
            )}
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="space-y-3 p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              <HistoryIcon width={15} height={15} className="text-muted-foreground" />
              <h2 className="text-sm font-semibold text-foreground">巡检执行与产物记录 ({runs.length})</h2>
            </div>
            <Select scale="sm" className="w-32" value={runStatusFilter} onChange={(e) => setRunStatusFilter(e.target.value)}>
              <option value="">全部状态</option>
              <option value="success">正常</option>
              <option value="warning">告警</option>
              <option value="critical">严重</option>
              <option value="partial">部分完成</option>
              <option value="failed">失败</option>
            </Select>
          </div>

          {runs.length === 0 ? (
            <p className="py-8 text-center text-xs text-muted-foreground">暂无巡检记录，选择 Skill 或输入提示词/命令后点击「立即巡检」。</p>
          ) : (
            <Table wrapperClassName="rounded-lg border">
              <TableHeader className="bg-muted/40">
                <TableRow>
                  <TableHead>巡检任务 / 提示词</TableHead>
                  <TableHead className="whitespace-nowrap">触发</TableHead>
                  <TableHead className="whitespace-nowrap">时间 / 耗时</TableHead>
                  <TableHead className="whitespace-nowrap">通过 / 告警 / 严重</TableHead>
                  <TableHead className="whitespace-nowrap">状态</TableHead>
                  <TableHead className="whitespace-nowrap text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {runs.map((r) => {
                  const st = describeMonitorStatus(r.status)
                  return (
                    <TableRow key={r.id}>
                      <TableCell className="max-w-[260px]">
                        <button
                          type="button"
                          onClick={() => openRun(r.id)}
                          title={describeRunTitle(r)}
                          className="block max-w-full truncate text-left font-medium text-primary hover:underline"
                        >
                          {describeRunTitle(r)}
                        </button>
                        <div className="mt-0.5 flex flex-wrap items-center gap-1 font-mono text-2xs text-muted-foreground">
                          <span>{r.id}</span>
                          <span>· {r.total_targets} 目标 / {r.total_assets} 产物</span>
                          {r.command && <Badge variant="outline">命令</Badge>}
                          {r.skills && r.skills.slice(0, 1).map((slug) => (
                            <Badge key={slug} variant="outline">{slug}</Badge>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs">
                        <Badge variant="outline">{TRIGGER_LABELS[r.trigger_kind] || r.trigger_kind}</Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap font-mono text-xs">
                        <div>{fmtStamp(r.started_at_ms)}</div>
                        <div className="text-2xs text-muted-foreground">{fmtDuration(r.duration_ms)}</div>
                      </TableCell>
                      <TableCell className="whitespace-nowrap font-mono text-xs">
                        <span className="text-success">{r.passed_checks}</span>
                        {" / "}
                        <span className="text-warning">{r.warning_checks}</span>
                        {" / "}
                        <span className="text-destructive">{r.failed_checks}</span>
                        {r.offline_targets > 0 && <span className="text-muted-foreground"> · 离线 {r.offline_targets}</span>}
                      </TableCell>
                      <TableCell className="whitespace-nowrap"><Badge variant={st.variant}>{st.label}</Badge></TableCell>
                      <TableCell className="whitespace-nowrap text-right">
                        <Inline gap={2} className="justify-end">
                          <Button size="sm" onClick={() => openRun(r.id)}>查看产物</Button>
                          {(r.warning_checks > 0 || r.failed_checks > 0 || r.offline_targets > 0) && (
                            <Button size="sm" variant="ghost" disabled={busy} onClick={() => void retestRun(r.id)}>复检</Button>
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
