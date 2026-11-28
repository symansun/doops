"use client"

import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react"
import { CalendarClock, ChevronDown, ChevronRight, Pencil, Play, Plus, Trash2 } from "lucide-react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import {
  describeScheduleScopeForTarget,
  filterRunsForTargetFallback,
  scheduleAppliesToTarget,
  scheduleIsInspectionPolicy,
} from "@/lib/automation"
import {
  describeInspectionRunTitle,
  describeRunSourceLabel,
  filterInspectionRuns,
  filterInspectionRunsBySource,
  filterInspectionTaskGroups,
  groupInspectionRuns,
  mergeInspectionRuns,
  statusDotToneClass,
  type InspectionTaskGroup,
  type RunSourceFilter,
} from "@/lib/inspection-run-groups"
import {
  TRIGGER_LABELS,
  describeMonitorStatus,
  fetchMonitorRuns,
  fetchMonitorSchedules,
  formatIntervalLabel,
  mutateMonitorSchedule,
  triggerMonitorRun,
  type MonitorRunRecord,
  type MonitorScheduleRecord,
} from "@/lib/monitor-engine"
import { fetchPlatformSkills, type PlatformSkillRecord } from "@/lib/skills-registry"
import { HistoryIcon, RefreshIcon, RocketIcon } from "./icons"
import { MonitorRunDetailView } from "./monitor-run-detail"
import { MonitorScheduleEditor } from "./monitor-schedule-editor"
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

export type MonitorWorkspaceTab = "schedules" | "runs"

function fmtStamp(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function fmtDuration(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

function toggleItem(list: string[], id: string): string[] {
  return list.includes(id) ? list.filter((x) => x !== id) : [...list, id]
}

function describeMonitorError(err: unknown): string {
  const message = (err as Error)?.message || "请求执行器失败"
  const code = (err as { code?: string })?.code || ""
  if (code === "strategies_removed" || message.includes("strategies_removed") || message.includes("strategies 已移除")) {
    return "巡检能力已改为由 Skill 包提供，请刷新后重新发起"
  }
  if (code === "ssh_hosts_removed" || message.includes("ssh_hosts_removed") || message.includes("ssh_hosts 已移除")) {
    return "SSH 主机巡检已改为由 inspect-ssh-host Skill 包提供，请刷新后重新发起"
  }
  return message
}

export function MonitorTaskWorkspace({
  session,
  targets,
  selectedTarget = null,
  activeTab,
  initialRunId,
  focusedScheduleId,
  onSelectRun,
  onClearInitialRun,
  onScheduleCountChange,
  onOpenSkills,
}: {
  session: Session
  targets: Target[]
  selectedTarget?: Target | null
  activeTab: MonitorWorkspaceTab
  initialRunId?: string
  focusedScheduleId?: string
  onSelectRun?: (runId: string) => void
  onClearInitialRun?: () => void
  onScheduleCountChange?: (counts: { targetCount: number; totalCount: number }) => void
  onOpenSkills?: () => void
}) {
  const isAdmin = sessionIsAdmin(session)

  const [schedules, setSchedules] = useState<MonitorScheduleRecord[]>([])
  const [platformSkills, setPlatformSkills] = useState<PlatformSkillRecord[]>([])
  const [runs, setRuns] = useState<MonitorRunRecord[]>([])
  const [targetFilteredByServer, setTargetFilteredByServer] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [revision, setRevision] = useState(0)

  // Scope & filter state shared across 「定时任务」 and 「执行记录」
  const [showAllScope, setShowAllScope] = useState(false)
  const [runStatusFilter, setRunStatusFilter] = useState("")
  const [runSourceFilter, setRunSourceFilter] = useState<RunSourceFilter>("all")
  const [searchQuery, setSearchQuery] = useState("")
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({})
  const [scheduleRunsById, setScheduleRunsById] = useState<Record<string, MonitorRunRecord[]>>({})
  const [loadingScheduleIds, setLoadingScheduleIds] = useState<Record<string, boolean>>({})
  const [loadedScheduleIds, setLoadedScheduleIds] = useState<Record<string, boolean>>({})
  const scheduleFetchEpochRef = useRef(0)
  const focusedAppliedRef = useRef("")

  // In-place run detail
  const [selectedRunId, setSelectedRunId] = useState(initialRunId || "")

  // Unified schedule editor
  const [editorOpen, setEditorOpen] = useState(false)
  const [editingSchedule, setEditingSchedule] = useState<MonitorScheduleRecord | null>(null)

  // Collapsible one-shot execution / inspection drawer
  const [quickOpen, setQuickOpen] = useState(false)
  const [quickTarget, setQuickTarget] = useState("")
  const [quickTargetTouched, setQuickTargetTouched] = useState(false)
  const [quickSkills, setQuickSkills] = useState<string[]>([])
  const [quickSkillsInitialized, setQuickSkillsInitialized] = useState(false)
  const [quickPrompt, setQuickPrompt] = useState("")
  const [quickCommand, setQuickCommand] = useState("")
  const [skillPickerOpen, setSkillPickerOpen] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)

  useEffect(() => {
    setSelectedRunId(initialRunId || "")
  }, [initialRunId])

  useEffect(() => {
    if (!quickTargetTouched) {
      setQuickTarget(selectedTarget && selectedTarget.status !== "offline" ? selectedTarget.key : "")
    }
  }, [selectedTarget?.key, selectedTarget?.status, quickTargetTouched])

  const openRun = useCallback(
    (runId: string) => {
      setSelectedRunId(runId)
      onSelectRun?.(runId)
    },
    [onSelectRun],
  )

  const closeRun = useCallback(() => {
    setSelectedRunId("")
    onClearInitialRun?.()
  }, [onClearInitialRun])

  const reload = useCallback(() => setRevision((v) => v + 1), [])

  const applyTargetFilter = Boolean(selectedTarget && !showAllScope)
  const filterCluster = applyTargetFilter ? selectedTarget?.cluster : undefined
  const filterInstance = applyTargetFilter ? selectedTarget?.instance : undefined

  useEffect(() => {
    const ctrl = new AbortController()
    scheduleFetchEpochRef.current += 1
    setScheduleRunsById({})
    setLoadingScheduleIds({})
    setLoadedScheduleIds({})
    setLoading(true)
    setError("")
    Promise.all([
      fetchMonitorSchedules(session, ctrl.signal),
      fetchMonitorRuns(
        session,
        {
          status: runStatusFilter || undefined,
          limit: 200,
          cluster: filterCluster,
          instance: filterInstance,
        },
        ctrl.signal,
      ),
      fetchPlatformSkills(session, {}, ctrl.signal).catch(() => ({ skills: [] as PlatformSkillRecord[] })),
    ])
      .then(([schedRes, runsRes, skillsRes]) => {
        if (ctrl.signal.aborted) return
        const nextSchedules = schedRes.schedules || []
        setSchedules(nextSchedules)
        setRuns(runsRes.runs || [])
        setTargetFilteredByServer(Boolean(runsRes.target_filtered))
        const fetchedSkills = skillsRes.skills || []
        setPlatformSkills(fetchedSkills)
        if (!quickSkillsInitialized && fetchedSkills.length > 0) {
          const defaultSkill =
            fetchedSkills.find((s) => s.enabled && s.slug === "inspect-vm-host") ||
            fetchedSkills.find((s) => s.enabled)
          setQuickSkills(defaultSkill ? [defaultSkill.slug] : [])
          setQuickSkillsInitialized(true)
        }
        setLoading(false)
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError(describeMonitorError(err))
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, runStatusFilter, filterCluster, filterInstance, revision, quickSkillsInitialized])

  const targetSchedules = useMemo(
    () => schedules.filter((s) => scheduleAppliesToTarget(s, selectedTarget)),
    [schedules, selectedTarget],
  )

  useEffect(() => {
    onScheduleCountChange?.({ targetCount: targetSchedules.length, totalCount: schedules.length })
  }, [targetSchedules.length, schedules.length, onScheduleCountChange])

  const loadScheduleHistory = useCallback(
    async (scheduleId: string) => {
      const sid = scheduleId.trim()
      if (!sid) return
      const epoch = scheduleFetchEpochRef.current
      setLoadingScheduleIds((prev) => ({ ...prev, [sid]: true }))
      try {
        const res = await fetchMonitorRuns(session, {
          scheduleId: sid,
          status: runStatusFilter || undefined,
          limit: 200,
          cluster: filterCluster,
          instance: filterInstance,
        })
        if (scheduleFetchEpochRef.current !== epoch) return
        setScheduleRunsById((prev) => ({ ...prev, [sid]: res.runs || [] }))
        setLoadedScheduleIds((prev) => ({ ...prev, [sid]: true }))
      } catch (err) {
        if (scheduleFetchEpochRef.current !== epoch) return
        setError(describeMonitorError(err))
      } finally {
        if (scheduleFetchEpochRef.current === epoch) {
          setLoadingScheduleIds((prev) => ({ ...prev, [sid]: false }))
        }
      }
    },
    [session, runStatusFilter, filterCluster, filterInstance],
  )

  useEffect(() => {
    if (!focusedScheduleId) {
      focusedAppliedRef.current = ""
      return
    }
    if (selectedTarget && !showAllScope) {
      const found = schedules.find((s) => s.id === focusedScheduleId)
      if (found && !scheduleAppliesToTarget(found, selectedTarget)) {
        setShowAllScope(true)
        return
      }
    }
    const token = `${focusedScheduleId}:${applyTargetFilter ? `${selectedTarget?.key || ""}` : "all"}`
    if (focusedAppliedRef.current === token) return
    focusedAppliedRef.current = token
    const key = `schedule:${focusedScheduleId}`
    setExpandedGroups((prev) => (prev[key] ? prev : { ...prev, [key]: true }))
    void loadScheduleHistory(focusedScheduleId)
  }, [focusedScheduleId, selectedTarget, showAllScope, applyTargetFilter, schedules, loadScheduleHistory])

  const systemSkills = useMemo(
    () => platformSkills.filter((s) => s.scope === "system" && s.enabled),
    [platformSkills],
  )
  const enabledSkills = useMemo(() => platformSkills.filter((s) => s.enabled), [platformSkills])

  const skillBySlug = useMemo(() => {
    const map = new Map<string, PlatformSkillRecord>()
    for (const sk of platformSkills) map.set(sk.slug, sk)
    return map
  }, [platformSkills])

  const skillNameBySlug = useMemo(() => {
    const map = new Map<string, string>()
    for (const sk of platformSkills) map.set(sk.slug, sk.name)
    return map
  }, [platformSkills])

  const scheduleById = useMemo(() => {
    const map = new Map<string, MonitorScheduleRecord>()
    for (const s of schedules) map.set(s.id, s)
    return map
  }, [schedules])

  const scopedSchedules = applyTargetFilter ? targetSchedules : schedules

  const scopedPrimaryRuns = useMemo(
    () =>
      applyTargetFilter
        ? filterRunsForTargetFallback(runs, schedules, selectedTarget, targetFilteredByServer)
        : runs,
    [applyTargetFilter, runs, schedules, selectedTarget, targetFilteredByServer],
  )

  const mergedRuns = useMemo(
    () => mergeInspectionRuns(scopedPrimaryRuns, Object.values(scheduleRunsById), runStatusFilter),
    [scopedPrimaryRuns, scheduleRunsById, runStatusFilter],
  )

  // Tab 1 (「定时任务」): strictly configured schedules — zero mixed-in ad-hoc duplicate cards.
  const scheduleGroups = useMemo(
    () =>
      groupInspectionRuns(mergedRuns, {
        schedules: scopedSchedules,
        skillNameBySlug,
        statusFilter: runStatusFilter,
        includeUnrunSchedules: true,
        onlyConfiguredSchedules: true,
      }),
    [mergedRuns, scopedSchedules, skillNameBySlug, runStatusFilter],
  )

  const visibleScheduleGroups = useMemo(
    () => filterInspectionTaskGroups(scheduleGroups, searchQuery, skillNameBySlug),
    [scheduleGroups, searchQuery, skillNameBySlug],
  )

  // Tab 2 (「执行记录」): chronological history of all runs (scheduled + ad-hoc + retest).
  const visibleRuns = useMemo(() => {
    const bySource = filterInspectionRunsBySource(mergedRuns, runSourceFilter)
    return filterInspectionRuns(bySource, searchQuery, { scheduleById, skillNameBySlug })
  }, [mergedRuns, runSourceFilter, searchQuery, scheduleById, skillNameBySlug])

  const toggleGroupExpanded = useCallback(
    (group: InspectionTaskGroup) => {
      const nextExpanded = !expandedGroups[group.key]
      setExpandedGroups((prev) => ({ ...prev, [group.key]: nextExpanded }))
      if (
        nextExpanded &&
        group.kind === "schedule" &&
        group.scheduleId &&
        group.hasUnloadedHistory &&
        !loadedScheduleIds[group.scheduleId] &&
        !loadingScheduleIds[group.scheduleId]
      ) {
        void loadScheduleHistory(group.scheduleId)
      }
    },
    [expandedGroups, loadedScheduleIds, loadingScheduleIds, loadScheduleHistory],
  )

  function openCreateEditor() {
    setEditingSchedule(null)
    setEditorOpen(true)
    setQuickOpen(false)
    setError("")
    setNotice("")
  }

  function openEditEditor(s: MonitorScheduleRecord) {
    setEditingSchedule(s)
    setEditorOpen(true)
    setQuickOpen(false)
    setError("")
    setNotice("")
  }

  async function handleToggle(s: MonitorScheduleRecord) {
    if (busy || !isAdmin) return
    const nextEnabled = !s.enabled
    setBusy(true)
    setError("")
    setNotice("")
    try {
      await mutateMonitorSchedule(session, { action: "toggle", id: s.id, enabled: nextEnabled })
      setNotice(`已${nextEnabled ? "启用" : "暂停"}定时任务「${s.name}」`)
      reload()
    } catch (err) {
      setError(describeMonitorError(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(s: MonitorScheduleRecord) {
    if (busy || !isAdmin) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      await mutateMonitorSchedule(session, { action: "delete", id: s.id })
      setNotice(`已删除定时任务「${s.name}」`)
      reload()
    } catch (err) {
      setError(describeMonitorError(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleRunScheduleNow(s: MonitorScheduleRecord) {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, { schedule_id: s.id })
      setNotice(`「${s.name}」执行完成（#${res.run.id}）`)
      reload()
      openRun(res.run.id)
    } catch (err) {
      setError(describeMonitorError(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleRetestRun(runId: string) {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, { retest_run_id: runId })
      setNotice(`已完成异常项复检（#${res.run.id}）`)
      reload()
      openRun(res.run.id)
    } catch (err) {
      setError(describeMonitorError(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleQuickDispatch(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const promptText = quickPrompt.trim()
      const commandText = commandOpen ? quickCommand.trim() : ""
      const payload: Parameters<typeof triggerMonitorRun>[1] = {}
      if (quickSkills.length > 0) payload.skills = quickSkills
      if (promptText) payload.prompt = promptText
      if (commandText) payload.command = commandText
      if (quickTarget) {
        const idx = quickTarget.indexOf("/")
        if (idx > 0) {
          payload.scope_mode = "targets"
          payload.targets = [{ cluster: quickTarget.slice(0, idx), instance: quickTarget.slice(idx + 1) }]
        }
      } else {
        payload.scope_mode = "all_online"
      }
      const res = await triggerMonitorRun(session, payload)
      setNotice(`即时执行已完成（#${res.run.id}），共生成 ${res.items.length} 份目标产物`)
      setQuickOpen(false)
      reload()
      openRun(res.run.id)
    } catch (err) {
      setError(describeMonitorError(err))
    } finally {
      setBusy(false)
    }
  }

  if (selectedRunId) {
    return (
      <MonitorRunDetailView
        session={session}
        runId={selectedRunId}
        skillBySlug={skillBySlug}
        scheduleById={scheduleById}
        focusTarget={applyTargetFilter ? selectedTarget : null}
        headingLabel="执行报告与产物"
        backLabel={activeTab === "runs" ? "返回执行记录" : "返回定时任务"}
        onBack={closeRun}
        onSelectRun={openRun}
        onRunCreated={reload}
      />
    )
  }

  const canRunQuick =
    quickSkills.length > 0 ||
    Boolean(quickPrompt.trim()) ||
    Boolean(commandOpen && quickCommand.trim())

  const renderScheduleHistoryTable = (list: MonitorRunRecord[]) => (
    <Table>
      <TableHeader className="bg-muted/40">
        <TableRow>
          <TableHead>执行时间</TableHead>
          <TableHead className="whitespace-nowrap">触发方式</TableHead>
          <TableHead className="whitespace-nowrap">耗时</TableHead>
          <TableHead className="whitespace-nowrap">通过 / 告警 / 严重</TableHead>
          <TableHead className="whitespace-nowrap">状态</TableHead>
          <TableHead className="whitespace-nowrap text-right">操作</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {list.map((r) => {
          const st = describeMonitorStatus(r.status)
          const detailSnippet = r.summary || ""
          return (
            <TableRow key={r.id}>
              <TableCell className="max-w-[300px]">
                <button
                  type="button"
                  onClick={() => openRun(r.id)}
                  className="block max-w-full truncate text-left font-mono text-xs font-medium text-primary hover:underline"
                >
                  {fmtStamp(r.started_at_ms)}
                </button>
                <div className="mt-0.5 flex flex-wrap items-center gap-1 text-2xs text-muted-foreground">
                  <span className="font-mono">#{r.id}</span>
                  <span>· {r.total_targets} 目标 / {r.total_assets} 产物</span>
                  {detailSnippet && (
                    <span className="max-w-[200px] truncate" title={detailSnippet}>
                      · {detailSnippet}
                    </span>
                  )}
                </div>
              </TableCell>
              <TableCell className="whitespace-nowrap text-xs">
                <Badge variant="outline">{TRIGGER_LABELS[r.trigger_kind] || r.trigger_kind}</Badge>
              </TableCell>
              <TableCell className="whitespace-nowrap font-mono text-xs text-muted-foreground">
                {fmtDuration(r.duration_ms)}
              </TableCell>
              <TableCell className="whitespace-nowrap font-mono text-xs">
                <span className="text-success">{r.passed_checks}</span>
                {" / "}
                <span className="text-warning">{r.warning_checks}</span>
                {" / "}
                <span className="text-destructive">{r.failed_checks}</span>
                {r.offline_targets > 0 && <span className="text-muted-foreground"> · 离线 {r.offline_targets}</span>}
              </TableCell>
              <TableCell className="whitespace-nowrap">
                <Badge variant={st.variant}>{st.label}</Badge>
              </TableCell>
              <TableCell className="whitespace-nowrap text-right">
                <Inline gap={2} className="justify-end">
                  <Button size="sm" onClick={() => openRun(r.id)}>
                    查看详情
                  </Button>
                  {(r.warning_checks > 0 || r.failed_checks > 0 || r.offline_targets > 0) && (
                    <Button size="sm" variant="ghost" disabled={busy} onClick={() => void handleRetestRun(r.id)}>
                      复检
                    </Button>
                  )}
                </Inline>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )

  const renderChronologicalRunsTable = (list: MonitorRunRecord[]) => (
    <Table wrapperClassName="rounded-lg border">
      <TableHeader className="bg-muted/40">
        <TableRow>
          <TableHead>任务 / 来源</TableHead>
          <TableHead className="whitespace-nowrap">执行时间 / 耗时</TableHead>
          <TableHead className="whitespace-nowrap">通过 / 告警 / 严重</TableHead>
          <TableHead className="whitespace-nowrap">状态</TableHead>
          <TableHead className="whitespace-nowrap text-right">操作</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {list.map((r) => {
          const st = describeMonitorStatus(r.status)
          const runTitle = describeInspectionRunTitle(r, skillNameBySlug, scheduleById)
          const sourceLabel = describeRunSourceLabel(r)
          const hasSchedule = Boolean((r.schedule_id || "").trim())
          return (
            <TableRow key={r.id}>
              <TableCell className="max-w-[340px]">
                <button
                  type="button"
                  onClick={() => openRun(r.id)}
                  title={runTitle}
                  className="block max-w-full truncate text-left font-medium text-primary hover:underline"
                >
                  {runTitle}
                </button>
                <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-2xs text-muted-foreground">
                  <Badge variant={hasSchedule ? "primary" : "outline"}>{sourceLabel}</Badge>
                  <span className="font-mono">{r.total_targets} 目标 / {r.total_assets} 产物</span>
                  {r.command && <Badge variant="outline">命令</Badge>}
                  {r.skills &&
                    r.skills.slice(0, 1).map((slug) => (
                      <Badge key={slug} variant="outline">{skillNameBySlug.get(slug) || slug}</Badge>
                    ))}
                  <span className="font-mono opacity-75">#{r.id}</span>
                </div>
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
              <TableCell className="whitespace-nowrap">
                <Badge variant={st.variant}>{st.label}</Badge>
              </TableCell>
              <TableCell className="whitespace-nowrap text-right">
                <Inline gap={2} className="justify-end">
                  <Button size="sm" onClick={() => openRun(r.id)}>
                    查看详情
                  </Button>
                  {(r.warning_checks > 0 || r.failed_checks > 0 || r.offline_targets > 0) && (
                    <Button size="sm" variant="ghost" disabled={busy} onClick={() => void handleRetestRun(r.id)}>
                      复检
                    </Button>
                  )}
                </Inline>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )

  const totalScheduledRuns = visibleScheduleGroups.reduce((sum, g) => sum + g.runCount, 0)

  return (
    <Stack gap={3}>
      {/* Top unified scope & action bar */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          {selectedTarget ? (
            <div className="flex items-center gap-1.5" role="group" aria-label="目标范围筛选">
              <button
                type="button"
                aria-pressed={!showAllScope}
                onClick={() => setShowAllScope(false)}
                className={`rounded-md border px-2.5 py-1 text-xs transition-colors ${
                  !showAllScope
                    ? "border-primary/60 bg-primary/15 font-medium text-primary"
                    : "border-border bg-card text-muted-foreground hover:bg-muted/50 hover:text-foreground"
                }`}
              >
                {activeTab === "runs" ? "当前目标相关" : "当前目标"}
              </button>
              <button
                type="button"
                aria-pressed={showAllScope}
                onClick={() => setShowAllScope(true)}
                className={`rounded-md border px-2.5 py-1 text-xs transition-colors ${
                  showAllScope
                    ? "border-primary/60 bg-primary/15 font-medium text-primary"
                    : "border-border bg-card text-muted-foreground hover:bg-muted/50 hover:text-foreground"
                }`}
              >
                {activeTab === "runs" ? "全部记录" : "全部目标"}
              </button>
            </div>
          ) : (
            <Inline gap={2}>
              <HistoryIcon width={15} height={15} className="text-muted-foreground" />
              <span className="text-sm font-semibold text-foreground">
                {activeTab === "schedules" ? "全部定时任务" : "全部执行记录"}
              </span>
            </Inline>
          )}
          <span className="font-mono text-2xs text-muted-foreground">
            {activeTab === "schedules"
              ? `${visibleScheduleGroups.length} 个定时任务 · ${totalScheduledRuns} 次执行`
              : `共 ${visibleRuns.length} 条记录`}
          </span>
          {!isAdmin && activeTab === "schedules" && <Badge variant="outline">只读（配置需平台管理员）</Badge>}
        </div>

        <Inline gap={2} wrap>
          {activeTab === "schedules" && isAdmin && (
            <Button
              size="sm"
              variant="primary"
              disabled={busy}
              onClick={() => (editorOpen && !editingSchedule ? setEditorOpen(false) : openCreateEditor())}
            >
              <Plus width={14} height={14} aria-hidden="true" />
              新建定时任务
            </Button>
          )}
          <Button
            size="sm"
            variant={quickOpen ? "primary" : "default"}
            disabled={busy}
            onClick={() => {
              setQuickOpen((v) => !v)
              if (!quickOpen) {
                setEditorOpen(false)
                setEditingSchedule(null)
              }
            }}
          >
            <RocketIcon width={14} height={14} />
            {quickOpen ? "收起即时执行" : "即时执行 / 巡检"}
          </Button>
          <Button size="sm" variant="default" disabled={loading} onClick={reload}>
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
          </Button>
        </Inline>
      </div>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="success">{notice}</StatusBanner>}

      {/* Single unified schedule create/edit form */}
      {activeTab === "schedules" && editorOpen && isAdmin && (
        <MonitorScheduleEditor
          session={session}
          targets={targets}
          selectedTarget={selectedTarget}
          systemSkills={systemSkills}
          editingSchedule={editingSchedule}
          onCancel={() => {
            setEditorOpen(false)
            setEditingSchedule(null)
          }}
          onSaved={(msg) => {
            setEditorOpen(false)
            setEditingSchedule(null)
            setNotice(msg)
            reload()
          }}
          onOpenSkills={onOpenSkills}
        />
      )}

      {/* Collapsible one-shot inspection / execution form */}
      {quickOpen && (
        <Card>
          <CardContent className="p-3.5">
            <form onSubmit={(e) => void handleQuickDispatch(e)} className="space-y-2.5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <Select
                    scale="sm"
                    aria-label="选择执行目标"
                    className="w-52"
                    value={quickTarget}
                    onChange={(e) => {
                      setQuickTargetTouched(true)
                      setQuickTarget(e.target.value)
                    }}
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
                <Button type="submit" size="sm" variant="primary" disabled={busy || !canRunQuick}>
                  <RocketIcon width={14} height={14} />
                  {busy ? "执行中…" : "立即执行"}
                </Button>
              </div>

              <Textarea
                rows={2}
                aria-label="执行提示词"
                className="w-full text-xs"
                placeholder="输入即时执行或巡检提示词（可选，如：检查磁盘、端口与进程并输出 Markdown + JSON 报告）"
                value={quickPrompt}
                onChange={(e) => setQuickPrompt(e.target.value)}
              />

              {skillPickerOpen && (
                <div className="space-y-2 border-t pt-2.5">
                  <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                    <span>选择关联 Skill（即时执行支持系统与本人自定义 Skill）：</span>
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
                          管理 Skill 能力包 →
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
                    在目标 Agent 上直接执行任意命令（stdout 为 JSON 或 Markdown+JSON 时自动可视化）：
                  </span>
                  <Textarea
                    rows={2}
                    aria-label="直接执行命令"
                    className="w-full font-mono text-xs"
                    placeholder="例如：df -h 或输出 JSON / Markdown+JSON 的检查脚本"
                    value={quickCommand}
                    onChange={(e) => setQuickCommand(e.target.value)}
                  />
                </div>
              )}
            </form>
          </CardContent>
        </Card>
      )}

      {/* Search & filter row */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            scale="sm"
            aria-label={activeTab === "schedules" ? "搜索定时任务" : "搜索执行记录"}
            className="w-56"
            placeholder={activeTab === "schedules" ? "搜索任务名、Skill 或命令…" : "搜索任务名、Skill、命令或 ID…"}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
          {activeTab === "runs" && (
            <Select
              scale="sm"
              aria-label="按来源筛选"
              className="w-32"
              value={runSourceFilter}
              onChange={(e) => setRunSourceFilter(e.target.value as RunSourceFilter)}
            >
              <option value="all">全部来源</option>
              <option value="scheduled">定时任务</option>
              <option value="adhoc">即时执行</option>
              <option value="retest">异常复检</option>
            </Select>
          )}
          <Select
            scale="sm"
            aria-label="按执行状态筛选"
            className="w-32"
            value={runStatusFilter}
            onChange={(e) => setRunStatusFilter(e.target.value)}
          >
            <option value="">全部状态</option>
            <option value="success">正常</option>
            <option value="warning">告警</option>
            <option value="critical">严重</option>
            <option value="partial">部分完成</option>
            <option value="failed">失败</option>
          </Select>
        </div>

        {activeTab === "schedules" && visibleScheduleGroups.length > 0 && (() => {
          const allExpanded = visibleScheduleGroups.every((g) => Boolean(expandedGroups[g.key]))
          return (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => {
                if (allExpanded) {
                  setExpandedGroups({})
                  return
                }
                const next: Record<string, boolean> = {}
                for (const g of visibleScheduleGroups) {
                  next[g.key] = true
                  if (
                    g.kind === "schedule" &&
                    g.scheduleId &&
                    g.hasUnloadedHistory &&
                    !loadedScheduleIds[g.scheduleId] &&
                    !loadingScheduleIds[g.scheduleId]
                  ) {
                    void loadScheduleHistory(g.scheduleId)
                  }
                }
                setExpandedGroups(next)
              }}
            >
              {allExpanded ? "全部收起" : "全部展开"}
            </Button>
          )
        })()}
      </div>

      {/* Main content */}
      {loading && schedules.length === 0 && runs.length === 0 ? (
        <div className="card-surface p-6 text-center text-sm text-muted-foreground">
          正在加载{activeTab === "schedules" ? "定时任务" : "执行记录"}…
        </div>
      ) : activeTab === "runs" ? (
        visibleRuns.length === 0 ? (
          <div className="card-surface flex flex-col items-center gap-2 p-8 text-center text-muted-foreground">
            <CalendarClock width={24} height={24} aria-hidden="true" />
            <p className="text-sm text-foreground">
              {mergedRuns.length === 0 ? "暂无执行记录" : "当前筛选条件下没有匹配的执行记录"}
            </p>
            <p className="text-xs text-muted-foreground">
              {applyTargetFilter && schedules.length > 0
                ? "可切换到「全部记录」查看其他目标的历史记录，或点击「即时执行 / 巡检」立即运行一次。"
                : "定时任务按周期运行或点击「即时执行 / 巡检」后，所有执行记录都会在此按时间倒序沉淀。"}
            </p>
          </div>
        ) : (
          renderChronologicalRunsTable(visibleRuns)
        )
      ) : visibleScheduleGroups.length === 0 ? (
        <div className="card-surface flex flex-col items-center gap-2.5 p-8 text-center text-muted-foreground">
          <CalendarClock width={24} height={24} aria-hidden="true" />
          <p className="text-sm text-foreground">
            {scheduleGroups.length === 0
              ? applyTargetFilter
                ? "当前目标暂无命中的定时任务"
                : "暂无定时任务"
              : "当前筛选条件下没有匹配的定时任务"}
          </p>
          <p className="text-xs text-muted-foreground">
            新建定时任务后可按周期自动执行系统 Skill 巡检、Shell 命令或 AI 提示词，展开任意任务即可查看历史执行记录。
          </p>
          <Inline gap={2} wrap>
            {isAdmin && (
              <Button size="sm" variant="primary" onClick={openCreateEditor}>
                <Plus width={14} height={14} aria-hidden="true" />
                新建定时任务
              </Button>
            )}
            {applyTargetFilter && schedules.length > 0 && (
              <Button size="sm" variant="default" onClick={() => setShowAllScope(true)}>
                查看全部目标 ({schedules.length})
              </Button>
            )}
          </Inline>
        </div>
      ) : (
        <div className="space-y-2.5">
          {visibleScheduleGroups.map((group) => {
            const schedule = group.schedule
            if (!schedule) return null
            const isExpanded = Boolean(expandedGroups[group.key])
            const hasStatus = Boolean(group.latestStatus && group.latestStatus !== "unknown")
            const latestSt = hasStatus ? describeMonitorStatus(group.latestStatus) : null
            const isLoadingSched = Boolean(group.scheduleId && loadingScheduleIds[group.scheduleId])
            const isLoadedSched = Boolean(group.scheduleId && loadedScheduleIds[group.scheduleId])
            const scopeLabel = describeScheduleScopeForTarget(schedule, selectedTarget)
            const isPolicySchedule = scheduleIsInspectionPolicy(schedule)

            return (
              <div key={group.key} className="overflow-hidden rounded-lg border bg-card/60">
                <div className="flex flex-wrap items-start justify-between gap-3 p-3.5 transition-colors hover:bg-muted/20">
                  <button
                    type="button"
                    aria-expanded={isExpanded}
                    onClick={() => toggleGroupExpanded(group)}
                    className="flex min-w-0 flex-1 items-start gap-2.5 text-left"
                  >
                    <span className="mt-0.5 shrink-0 text-muted-foreground" aria-hidden="true">
                      {isExpanded ? <ChevronDown width={16} height={16} /> : <ChevronRight width={16} height={16} />}
                    </span>
                    <div className="min-w-0 flex-1 space-y-1.5">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="truncate text-sm font-semibold text-foreground">{group.title}</span>
                        <Badge variant="primary">
                          {isPolicySchedule ? "巡检策略" : "定时命令"}
                        </Badge>
                        <Badge variant={schedule.enabled ? "success" : "default"}>
                          {schedule.enabled ? "启用中" : "已暂停"}
                        </Badge>
                        <Badge variant="outline">{formatIntervalLabel(schedule.interval_seconds)}</Badge>
                        <Badge variant={scopeLabel === "仅当前目标" ? "primary" : "outline"}>
                          {scopeLabel}
                        </Badge>
                        {latestSt ? (
                          <Badge variant={latestSt.variant}>最新：{latestSt.label}</Badge>
                        ) : (
                          <Badge variant="outline">尚未运行</Badge>
                        )}
                        <Badge variant="outline" className="font-mono">
                          {group.hasUnloadedHistory
                            ? "历史待加载"
                            : group.runCount > 0
                              ? `已执行 ${group.runCount} 次`
                              : "0 次执行"}
                        </Badge>
                      </div>

                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-2xs text-muted-foreground">
                        {group.latestStartedAtMs > 0 && (
                          <span className="font-mono">最近：{fmtStamp(group.latestStartedAtMs)}</span>
                        )}
                        {schedule.enabled && schedule.next_run_at_ms > 0 && (
                          <span className="font-mono">下次：{fmtStamp(schedule.next_run_at_ms)}</span>
                        )}
                        {group.latestRun && (
                          <span className="font-mono">
                            最新检查：
                            <span className="text-success">{group.latestRun.passed_checks}</span>
                            {" / "}
                            <span className="text-warning">{group.latestRun.warning_checks}</span>
                            {" / "}
                            <span className="text-destructive">{group.latestRun.failed_checks}</span>
                            {group.latestRun.offline_targets > 0 && ` · 离线 ${group.latestRun.offline_targets}`}
                          </span>
                        )}
                        {group.avgDurationMs != null && (
                          <span className="font-mono">均耗时 {fmtDuration(group.avgDurationMs)}</span>
                        )}
                        {group.successRatePct != null && (
                          <span className="font-mono">
                            正常率 <strong className="text-foreground">{group.successRatePct}%</strong>
                            {group.abnormalCount > 0 && (
                              <span className="text-warning">（异常 {group.abnormalCount} 次）</span>
                            )}
                          </span>
                        )}
                      </div>

                      {(group.skills.length > 0 || group.promptPreview || group.commandPreview) && (
                        <div className="flex flex-wrap items-center gap-1.5 text-2xs text-muted-foreground">
                          {group.skills.slice(0, 3).map((slug) => (
                            <Badge key={slug} variant="outline" className="font-mono">
                              {skillNameBySlug.get(slug) || slug}
                            </Badge>
                          ))}
                          {group.skills.length > 3 && (
                            <Badge variant="outline">+{group.skills.length - 3}</Badge>
                          )}
                          {group.commandPreview && (
                            <span className="max-w-md truncate font-mono" title={group.commandPreview}>
                              $ {group.commandPreview}
                            </span>
                          )}
                          {group.promptPreview && (
                            <span className="max-w-md truncate" title={group.promptPreview}>
                              提示词：{group.promptPreview}
                            </span>
                          )}
                        </div>
                      )}
                    </div>
                  </button>

                  <div className="flex flex-wrap items-center gap-2">
                    {group.recentTimeline.length > 0 && (
                      <div
                        className="flex items-center gap-1"
                        aria-label={`近期 ${group.recentTimeline.length} 次执行状态轨迹`}
                      >
                        {group.recentTimeline.map((r) => {
                          const rSt = describeMonitorStatus(r.status)
                          return (
                            <button
                              key={r.id}
                              type="button"
                              title={`${fmtStamp(r.started_at_ms)} · ${rSt.label} (#${r.id})`}
                              aria-label={`查看执行记录 ${r.id}（${rSt.label}）`}
                              onClick={() => openRun(r.id)}
                              className={`h-4 w-2 rounded-xs transition-opacity hover:opacity-80 ${statusDotToneClass(r.status)}`}
                            />
                          )
                        })}
                      </div>
                    )}
                    <Inline gap={1} wrap>
                      {group.latestRunId && (
                        <Button size="sm" onClick={() => openRun(group.latestRunId!)}>
                          最新报告
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="default"
                        disabled={busy}
                        onClick={() => void handleRunScheduleNow(schedule)}
                        title="立即触发一次执行"
                      >
                        <Play width={12} height={12} aria-hidden="true" />
                        立即执行
                      </Button>
                      {isAdmin && (
                        <>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={busy}
                            onClick={() => void handleToggle(schedule)}
                          >
                            {schedule.enabled ? "暂停" : "启用"}
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={busy}
                            aria-label={`编辑 ${schedule.name}`}
                            onClick={() => openEditEditor(schedule)}
                          >
                            <Pencil width={12} height={12} aria-hidden="true" />
                          </Button>
                          <Button
                            size="sm"
                            variant="danger-outline"
                            disabled={busy}
                            aria-label={`删除 ${schedule.name}`}
                            onClick={() => void handleDelete(schedule)}
                          >
                            <Trash2 width={12} height={12} aria-hidden="true" />
                          </Button>
                        </>
                      )}
                    </Inline>
                  </div>
                </div>

                {isExpanded && (
                  <div className="border-t bg-background/40">
                    {isLoadingSched && group.runs.length === 0 ? (
                      <p role="status" className="px-4 py-4 text-xs text-muted-foreground">
                        正在加载该任务的历史执行记录…
                      </p>
                    ) : group.runs.length === 0 ? (
                      <p className="px-4 py-4 text-xs text-muted-foreground">
                        {group.hasUnloadedHistory
                          ? "当前筛选条件下暂无已加载记录。"
                          : "该任务尚未产生执行记录；可点击「立即执行」测试运行，或等待下次周期调度。"}
                      </p>
                    ) : (
                      renderScheduleHistoryTable(group.runs)
                    )}
                    {group.scheduleId && (
                      <div className="flex flex-wrap items-center justify-between gap-2 border-t bg-muted/20 px-3.5 py-2 text-2xs text-muted-foreground">
                        <span>
                          {isLoadedSched
                            ? `已加载该定时任务完整历史（${group.runs.length} 条${applyTargetFilter ? "，已按当前目标筛选" : ""}）`
                            : `当前展示 ${group.runs.length} 条记录`}
                        </span>
                        {!isLoadedSched && (
                          <Button
                            type="button"
                            size="sm"
                            variant="ghost"
                            disabled={isLoadingSched}
                            onClick={() => void loadScheduleHistory(group.scheduleId!)}
                          >
                            {isLoadingSched ? "加载中…" : "加载完整历史"}
                          </Button>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </Stack>
  )
}
