import type { MonitorRunRecord, MonitorScheduleRecord } from "./monitor-engine.ts"

export type InspectionRunViewMode = "grouped" | "flat"

export type RunSourceFilter = "all" | "scheduled" | "adhoc" | "retest"

export type InspectionTaskGroupKind = "schedule" | "skill" | "command" | "prompt" | "adhoc"

export const TASK_GROUP_KIND_LABELS: Record<InspectionTaskGroupKind, string> = {
  schedule: "定时任务",
  skill: "即时巡检",
  command: "命令执行",
  prompt: "提示词执行",
  adhoc: "即时执行",
}

export function describeRunSourceLabel(
  run: Pick<MonitorRunRecord, "schedule_id" | "trigger_kind">,
): string {
  if (run.trigger_kind === "retest") return "异常复检"
  if ((run.schedule_id || "").trim()) {
    return run.trigger_kind === "scheduled" ? "定时调度" : "定时任务（手动）"
  }
  return "即时执行"
}

export function filterInspectionRunsBySource(
  runs: readonly MonitorRunRecord[],
  source: RunSourceFilter,
): MonitorRunRecord[] {
  if (source === "all") return [...runs]
  return runs.filter((run) => {
    const isRetest = run.trigger_kind === "retest"
    const hasSchedule = Boolean((run.schedule_id || "").trim())
    if (source === "retest") return isRetest
    if (source === "scheduled") return hasSchedule && !isRetest
    return !hasSchedule && !isRetest
  })
}

export interface InspectionTaskStatusCounts {
  total: number
  running: number
  success: number
  warning: number
  partial: number
  critical: number
  failed: number
  other: number
}

export interface InspectionTaskTriggerCounts {
  scheduled: number
  manual: number
  retest: number
  other: number
}

export interface InspectionTaskIdentity {
  key: string
  kind: InspectionTaskGroupKind
  title: string
  scheduleId?: string
  skills: string[]
  promptPreview?: string
  commandPreview?: string
}

export interface InspectionTaskGroup {
  key: string
  kind: InspectionTaskGroupKind
  title: string
  scheduleId?: string
  schedule?: MonitorScheduleRecord
  scheduleDeleted?: boolean
  skills: string[]
  promptPreview?: string
  commandPreview?: string
  runs: MonitorRunRecord[]
  runCount: number
  latestRun?: MonitorRunRecord
  latestRunId?: string
  latestStatus: string
  latestStartedAtMs: number
  avgDurationMs: number | null
  statusCounts: InspectionTaskStatusCounts
  triggerCounts: InspectionTaskTriggerCounts
  abnormalCount: number
  successRatePct: number | null
  recentTimeline: MonitorRunRecord[]
  hasUnloadedHistory: boolean
}

export interface GroupInspectionRunsOptions {
  schedules?: readonly MonitorScheduleRecord[]
  skillNameBySlug?: ReadonlyMap<string, string>
  statusFilter?: string
  /** Include configured schedules that have not yet run (no last_run_id) as first-class task groups. */
  includeUnrunSchedules?: boolean
  /** Restrict groups strictly to currently configured schedules (excluding ad-hoc/deleted-schedule groups). */
  onlyConfiguredSchedules?: boolean
}

function firstNonEmptyLine(raw?: string, maxLen = 64): string {
  if (!raw) return ""
  const line =
    raw
      .split(/\r?\n/)
      .map((s) => s.trim())
      .find((s) => s.length > 0) || ""
  if (!line) return ""
  if (line.length <= maxLen) return line
  return `${line.slice(0, maxLen)}…`
}

function normalizeFingerprint(raw: string, maxLen = 160): string {
  return raw.trim().replace(/\s+/g, " ").toLowerCase().slice(0, maxLen)
}

export function normalizeTaskSkills(skills?: readonly string[]): string[] {
  if (!skills || skills.length === 0) return []
  const unique = new Set<string>()
  for (const slug of skills) {
    const clean = (slug || "").trim().toLowerCase()
    if (clean) unique.add(clean)
  }
  return Array.from(unique).sort()
}

function formatSkillGroupTitle(skills: readonly string[], skillNameBySlug?: ReadonlyMap<string, string>): string {
  if (skills.length === 0) return "即时巡检"
  const first = skillNameBySlug?.get(skills[0]) || skills[0]
  return skills.length === 1 ? first : `${first} 等 ${skills.length} 个 Skill`
}

export function describeInspectionRunTitle(
  run: MonitorRunRecord,
  skillNameBySlug?: ReadonlyMap<string, string>,
  scheduleById?: ReadonlyMap<string, MonitorScheduleRecord>,
): string {
  const sid = (run.schedule_id || "").trim()
  if (sid) {
    const sched = scheduleById?.get(sid)
    if (sched?.name?.trim()) return sched.name.trim()
    if (run.schedule_name?.trim()) return run.schedule_name.trim()
    return sid
  }
  if (run.schedule_name?.trim()) return run.schedule_name.trim()
  const promptLine = firstNonEmptyLine(run.prompt, 64)
  if (promptLine) return promptLine
  const skills = normalizeTaskSkills(run.skills)
  if (skills.length > 0) return formatSkillGroupTitle(skills, skillNameBySlug)
  const cmdLine = firstNonEmptyLine(run.command, 48)
  if (cmdLine) return `命令：${cmdLine}`
  return "即时巡检"
}

export function resolveInspectionRunTaskIdentity(
  run: MonitorRunRecord,
  opts: {
    scheduleById?: ReadonlyMap<string, MonitorScheduleRecord>
    skillNameBySlug?: ReadonlyMap<string, string>
  } = {},
): InspectionTaskIdentity {
  const sid = (run.schedule_id || "").trim()
  const skills = normalizeTaskSkills(run.skills)
  const promptPreview = firstNonEmptyLine(run.prompt, 96) || undefined
  const commandPreview = firstNonEmptyLine(run.command, 96) || undefined

  if (sid) {
    const sched = opts.scheduleById?.get(sid)
    const schedSkills = sched?.skills?.length ? normalizeTaskSkills(sched.skills) : skills
    return {
      key: `schedule:${sid}`,
      kind: "schedule",
      title: sched?.name?.trim() || run.schedule_name?.trim() || sid,
      scheduleId: sid,
      skills: schedSkills,
      promptPreview: firstNonEmptyLine(sched?.prompt, 96) || promptPreview,
      commandPreview: firstNonEmptyLine(sched?.command, 96) || commandPreview,
    }
  }

  const trimmedCommand = (run.command || "").trim()
  if (skills.length > 0 && !trimmedCommand) {
    return {
      key: `skill:${skills.join("+")}`,
      kind: "skill",
      title: formatSkillGroupTitle(skills, opts.skillNameBySlug),
      skills,
      promptPreview,
    }
  }

  if (trimmedCommand) {
    const skillsPrefix = skills.length > 0 ? `${skills.join("+")}::` : ""
    return {
      key: `command:${skillsPrefix}${normalizeFingerprint(trimmedCommand)}`,
      kind: "command",
      title: firstNonEmptyLine(run.prompt, 64) || `命令：${firstNonEmptyLine(trimmedCommand, 48)}`,
      skills,
      promptPreview,
      commandPreview,
    }
  }

  const promptLine = firstNonEmptyLine(run.prompt, 120)
  if (promptLine) {
    return {
      key: `prompt:${normalizeFingerprint(promptLine)}`,
      kind: "prompt",
      title: firstNonEmptyLine(run.prompt, 64),
      skills,
      promptPreview,
    }
  }

  return {
    key: "adhoc:default",
    kind: "adhoc",
    title: "即时巡检",
    skills,
  }
}

function compareRunsDesc(a: MonitorRunRecord, b: MonitorRunRecord): number {
  const diff = (b.started_at_ms || 0) - (a.started_at_ms || 0)
  if (diff !== 0) return diff
  return b.id.localeCompare(a.id)
}

export function mergeInspectionRuns(
  primary: readonly MonitorRunRecord[],
  extraLists: readonly (readonly MonitorRunRecord[])[] = [],
  statusFilter = "",
): MonitorRunRecord[] {
  const byId = new Map<string, MonitorRunRecord>()
  const ingest = (list: readonly MonitorRunRecord[]) => {
    for (const run of list) {
      if (!run?.id) continue
      if (statusFilter && run.status !== statusFilter) continue
      const existing = byId.get(run.id)
      if (!existing || (run.finished_at_ms || 0) >= (existing.finished_at_ms || 0)) {
        byId.set(run.id, run)
      }
    }
  }
  ingest(primary)
  for (const extra of extraLists) ingest(extra)
  return Array.from(byId.values()).sort(compareRunsDesc)
}

function emptyStatusCounts(): InspectionTaskStatusCounts {
  return {
    total: 0,
    running: 0,
    success: 0,
    warning: 0,
    partial: 0,
    critical: 0,
    failed: 0,
    other: 0,
  }
}

function emptyTriggerCounts(): InspectionTaskTriggerCounts {
  return {
    scheduled: 0,
    manual: 0,
    retest: 0,
    other: 0,
  }
}

function countStatuses(runs: readonly MonitorRunRecord[]): InspectionTaskStatusCounts {
  const counts = emptyStatusCounts()
  counts.total = runs.length
  for (const r of runs) {
    switch (r.status) {
      case "running":
        counts.running++
        break
      case "success":
      case "pass":
      case "ok":
        counts.success++
        break
      case "warning":
        counts.warning++
        break
      case "partial":
        counts.partial++
        break
      case "critical":
        counts.critical++
        break
      case "failed":
      case "error":
        counts.failed++
        break
      default:
        counts.other++
        break
    }
  }
  return counts
}

function countTriggers(runs: readonly MonitorRunRecord[]): InspectionTaskTriggerCounts {
  const counts = emptyTriggerCounts()
  for (const r of runs) {
    switch (r.trigger_kind) {
      case "scheduled":
        counts.scheduled++
        break
      case "manual":
        counts.manual++
        break
      case "retest":
        counts.retest++
        break
      default:
        counts.other++
        break
    }
  }
  return counts
}

export function groupInspectionRuns(
  runs: readonly MonitorRunRecord[],
  options: GroupInspectionRunsOptions = {},
): InspectionTaskGroup[] {
  const schedules = options.schedules || []
  const skillNameBySlug = options.skillNameBySlug
  const statusFilter = (options.statusFilter || "").trim()

  const scheduleById = new Map<string, MonitorScheduleRecord>()
  for (const s of schedules) {
    if (s?.id) scheduleById.set(s.id, s)
  }

  const dedupedRuns = mergeInspectionRuns(runs, [], statusFilter)
  const buckets = new Map<string, { identity: InspectionTaskIdentity; runs: MonitorRunRecord[] }>()

  for (const run of dedupedRuns) {
    const identity = resolveInspectionRunTaskIdentity(run, { scheduleById, skillNameBySlug })
    if (options.onlyConfiguredSchedules) {
      if (identity.kind !== "schedule" || !identity.scheduleId || !scheduleById.has(identity.scheduleId)) {
        continue
      }
    }
    const existing = buckets.get(identity.key)
    if (existing) {
      existing.runs.push(run)
      if (!existing.identity.promptPreview && identity.promptPreview) {
        existing.identity.promptPreview = identity.promptPreview
      }
      if (!existing.identity.commandPreview && identity.commandPreview) {
        existing.identity.commandPreview = identity.commandPreview
      }
    } else {
      buckets.set(identity.key, { identity, runs: [run] })
    }
  }

  const groups: InspectionTaskGroup[] = []

  for (const { identity, runs: groupRuns } of buckets.values()) {
    groupRuns.sort(compareRunsDesc)
    const latestRun = groupRuns[0]
    const schedule = identity.scheduleId ? scheduleById.get(identity.scheduleId) : undefined
    const statusCounts = countStatuses(groupRuns)
    const triggerCounts = countTriggers(groupRuns)
    const abnormalCount = statusCounts.warning + statusCounts.partial + statusCounts.critical + statusCounts.failed
    const finishedCount = statusCounts.total - statusCounts.running
    const successRatePct = finishedCount > 0 ? Math.round((statusCounts.success / finishedCount) * 100) : null

    let durationSum = 0
    let durationCount = 0
    for (const r of groupRuns) {
      if (Number.isFinite(r.duration_ms) && r.duration_ms > 0) {
        durationSum += r.duration_ms
        durationCount++
      }
    }
    const avgDurationMs = durationCount > 0 ? Math.round(durationSum / durationCount) : null

    groups.push({
      key: identity.key,
      kind: identity.kind,
      title: identity.title,
      scheduleId: identity.scheduleId,
      schedule,
      scheduleDeleted: identity.kind === "schedule" ? !schedule : false,
      skills: identity.skills,
      promptPreview: identity.promptPreview,
      commandPreview: identity.commandPreview,
      runs: groupRuns,
      runCount: groupRuns.length,
      latestRun,
      latestRunId: latestRun?.id || schedule?.last_run_id || undefined,
      latestStatus: latestRun?.status || schedule?.last_status || "unknown",
      latestStartedAtMs: latestRun?.started_at_ms || schedule?.last_run_at_ms || 0,
      avgDurationMs,
      statusCounts,
      triggerCounts,
      abnormalCount,
      successRatePct,
      recentTimeline: groupRuns.slice(0, 10).reverse(),
      hasUnloadedHistory: false,
    })
  }

  // Keep scheduled tasks that have executed previously even when high-frequency
  // runs push their history out of the global top-N window. When unified task
  // views opt in via includeUnrunSchedules, also include configured schedules
  // that have not yet executed so config and execution history stay in one list.
  for (const s of schedules) {
    const key = `schedule:${s.id}`
    if (buckets.has(key)) continue
    const lastRunId = (s.last_run_id || "").trim()
    if (!lastRunId) {
      if (!options.includeUnrunSchedules || statusFilter) continue
      groups.push({
        key,
        kind: "schedule",
        title: s.name?.trim() || s.id,
        scheduleId: s.id,
        schedule: s,
        scheduleDeleted: false,
        skills: normalizeTaskSkills(s.skills),
        promptPreview: firstNonEmptyLine(s.prompt, 96) || undefined,
        commandPreview: firstNonEmptyLine(s.command, 96) || undefined,
        runs: [],
        runCount: 0,
        latestRun: undefined,
        latestRunId: undefined,
        latestStatus: "",
        latestStartedAtMs: 0,
        avgDurationMs: null,
        statusCounts: emptyStatusCounts(),
        triggerCounts: emptyTriggerCounts(),
        abnormalCount: 0,
        successRatePct: null,
        recentTimeline: [],
        hasUnloadedHistory: false,
      })
      continue
    }
    if (statusFilter && s.last_status !== statusFilter) continue

    groups.push({
      key,
      kind: "schedule",
      title: s.name?.trim() || s.id,
      scheduleId: s.id,
      schedule: s,
      scheduleDeleted: false,
      skills: normalizeTaskSkills(s.skills),
      promptPreview: firstNonEmptyLine(s.prompt, 96) || undefined,
      commandPreview: firstNonEmptyLine(s.command, 96) || undefined,
      runs: [],
      runCount: 0,
      latestRun: undefined,
      latestRunId: lastRunId,
      latestStatus: s.last_status || "unknown",
      latestStartedAtMs: s.last_run_at_ms || 0,
      avgDurationMs: null,
      statusCounts: emptyStatusCounts(),
      triggerCounts: emptyTriggerCounts(),
      abnormalCount: 0,
      successRatePct: null,
      recentTimeline: [],
      hasUnloadedHistory: true,
    })
  }

  groups.sort((a, b) => {
    const timeDiff = (b.latestStartedAtMs || 0) - (a.latestStartedAtMs || 0)
    if (timeDiff !== 0) return timeDiff
    const enabledDiff = Number(Boolean(b.schedule?.enabled)) - Number(Boolean(a.schedule?.enabled))
    if (enabledDiff !== 0) return enabledDiff
    const titleDiff = a.title.localeCompare(b.title, "zh-CN")
    if (titleDiff !== 0) return titleDiff
    return a.key.localeCompare(b.key)
  })

  return groups
}

export function filterInspectionTaskGroups(
  groups: readonly InspectionTaskGroup[],
  query: string,
  skillNameBySlug?: ReadonlyMap<string, string>,
): InspectionTaskGroup[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...groups]
  return groups.filter((group) => {
    if (group.title.toLowerCase().includes(q)) return true
    if (TASK_GROUP_KIND_LABELS[group.kind].toLowerCase().includes(q)) return true
    if (group.scheduleId?.toLowerCase().includes(q)) return true
    if (group.promptPreview?.toLowerCase().includes(q)) return true
    if (group.commandPreview?.toLowerCase().includes(q)) return true
    for (const slug of group.skills) {
      if (slug.toLowerCase().includes(q)) return true
      const name = skillNameBySlug?.get(slug)
      if (name && name.toLowerCase().includes(q)) return true
    }
    for (const r of group.runs) {
      if (r.id.toLowerCase().includes(q)) return true
      if (r.summary?.toLowerCase().includes(q)) return true
      if (r.schedule_name?.toLowerCase().includes(q)) return true
    }
    return false
  })
}

export function filterInspectionRuns(
  runs: readonly MonitorRunRecord[],
  query: string,
  opts: {
    scheduleById?: ReadonlyMap<string, MonitorScheduleRecord>
    skillNameBySlug?: ReadonlyMap<string, string>
  } = {},
): MonitorRunRecord[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...runs]
  return runs.filter((r) => {
    if (r.id.toLowerCase().includes(q)) return true
    if (describeInspectionRunTitle(r, opts.skillNameBySlug, opts.scheduleById).toLowerCase().includes(q)) return true
    if (r.schedule_id?.toLowerCase().includes(q)) return true
    if (r.schedule_name?.toLowerCase().includes(q)) return true
    if (r.prompt?.toLowerCase().includes(q)) return true
    if (r.command?.toLowerCase().includes(q)) return true
    if (r.summary?.toLowerCase().includes(q)) return true
    for (const slug of r.skills || []) {
      if (slug.toLowerCase().includes(q)) return true
      const name = opts.skillNameBySlug?.get(slug)
      if (name && name.toLowerCase().includes(q)) return true
    }
    return false
  })
}

export function statusDotToneClass(status: string): string {
  switch (status) {
    case "success":
    case "pass":
    case "ok":
      return "bg-success"
    case "warning":
    case "partial":
      return "bg-warning"
    case "critical":
    case "failed":
    case "error":
      return "bg-destructive"
    case "running":
      return "bg-primary"
    default:
      return "bg-muted-foreground/60"
  }
}
