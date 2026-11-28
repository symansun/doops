import { parseAutomationCommand } from "./automation-command.ts"
import type { MonitorRunRecord, MonitorScheduleRecord } from "./monitor-engine.ts"

export type AutomationBadgeVariant = "primary" | "success" | "warning" | "danger" | "default" | "outline"

export interface BackgroundTaskStatusMeta {
  label: string
  variant: AutomationBadgeVariant
  terminal: boolean
}

export type TaskStatusFilter = "all" | "running" | "success" | "failed" | "canceled" | "unknown"

export interface TaskStatusSummary {
  total: number
  running: number
  success: number
  failed: number
  canceled: number
  unknown: number
}

// Gateway detached_tasks persists "error" (from client_tool_call.go) alongside
// "failed", "timeout", "canceled", "success", "running", and "outcome_unknown".
// Status colors follow DESIGN.md & DOOPS_WEBUI.md: running=blue, unknown=amber, failed/timeout=red.
export function describeBackgroundTaskStatus(status: string): BackgroundTaskStatusMeta {
  switch (status) {
    case "running":
      return { label: "运行中", variant: "primary", terminal: false }
    case "success":
      return { label: "成功", variant: "success", terminal: true }
    case "error":
    case "failed":
      return { label: "失败", variant: "danger", terminal: true }
    case "timeout":
      return { label: "超时", variant: "danger", terminal: true }
    case "canceled":
      return { label: "已停止", variant: "default", terminal: true }
    case "outcome_unknown":
      return { label: "结果未知", variant: "warning", terminal: true }
    default:
      return { label: status || "未知", variant: "outline", terminal: true }
  }
}

export function isBackgroundTaskTerminal(status: string): boolean {
  return describeBackgroundTaskStatus(status).terminal
}

export function matchesTaskStatusFilter(status: string, filter: TaskStatusFilter): boolean {
  switch (filter) {
    case "all":
      return true
    case "running":
      return status === "running"
    case "success":
      return status === "success"
    case "failed":
      return status === "error" || status === "failed" || status === "timeout"
    case "canceled":
      return status === "canceled"
    case "unknown":
      return status === "outcome_unknown"
  }
}

export function summarizeBackgroundTasks(tasks: readonly { status: string }[]): TaskStatusSummary {
  const summary: TaskStatusSummary = {
    total: tasks.length,
    running: 0,
    success: 0,
    failed: 0,
    canceled: 0,
    unknown: 0,
  }
  for (const task of tasks) {
    if (task.status === "running") summary.running += 1
    else if (task.status === "success") summary.success += 1
    else if (task.status === "error" || task.status === "failed" || task.status === "timeout") summary.failed += 1
    else if (task.status === "canceled") summary.canceled += 1
    else if (task.status === "outcome_unknown") summary.unknown += 1
  }
  return summary
}

// Tab badge for 「一次性后台执行」: only surfaces an active badge when commands
// are genuinely running, avoiding a misleading "(1)" count for already-finished
// historical exec --bg records.
export function backgroundTaskTabBadge(
  summary: Pick<TaskStatusSummary, "running"> | null,
): { label: string; variant: "primary" } | null {
  if (!summary || summary.running <= 0) return null
  return { label: `运行中 ${summary.running}`, variant: "primary" }
}

export function filterBackgroundTasks<
  T extends {
    audit_id: number
    command_summary: string
    session: string
    status: string
    task_id?: string
  },
>(tasks: readonly T[], opts: { statusFilter: TaskStatusFilter; query: string }): T[] {
  const needle = opts.query.trim().toLowerCase()
  const numericNeedle = needle.startsWith("#") ? needle.slice(1) : needle
  return tasks.filter((task) => {
    if (!matchesTaskStatusFilter(task.status, opts.statusFilter)) return false
    if (!needle) return true
    if (String(task.audit_id).includes(numericNeedle)) return true
    if (task.command_summary.toLowerCase().includes(needle)) return true
    if (task.session.toLowerCase().includes(needle)) return true
    if (task.task_id && task.task_id.toLowerCase().includes(needle)) return true
    const parsed = parseAutomationCommand(task.command_summary)
    if (parsed.title.toLowerCase().includes(needle)) return true
    if (parsed.category.toLowerCase().includes(needle)) return true
    if (parsed.workdir && parsed.workdir.toLowerCase().includes(needle)) return true
    return false
  })
}

export function formatTaskDuration(startedAt?: string, finishedAt?: string, nowMs = Date.now()): string {
  if (!startedAt) return "—"
  const start = Date.parse(startedAt)
  if (!Number.isFinite(start)) return "—"
  const end = finishedAt ? Date.parse(finishedAt) : nowMs
  if (!Number.isFinite(end)) return "—"
  const diffMs = Math.max(0, end - start)
  if (diffMs < 1000) return "<1s"
  const totalSec = Math.floor(diffMs / 1000)
  if (totalSec < 60) return `${totalSec}s`
  const mins = Math.floor(totalSec / 60)
  const secs = totalSec % 60
  if (mins < 60) return secs > 0 ? `${mins}m ${secs}s` : `${mins}m`
  const hours = Math.floor(mins / 60)
  const remMins = mins % 60
  if (hours < 24) return remMins > 0 ? `${hours}h ${String(remMins).padStart(2, "0")}m` : `${hours}h`
  const days = Math.floor(hours / 24)
  const remHours = hours % 24
  return remHours > 0 ? `${days}d ${remHours}h` : `${days}d`
}

export function scheduleAppliesToTarget(
  schedule: Pick<MonitorScheduleRecord, "scope_mode" | "targets">,
  target: { cluster: string; instance: string } | null,
): boolean {
  if (!target) return true
  if (schedule.scope_mode === "all_online") return true
  if (schedule.scope_mode === "targets") {
    return (schedule.targets || []).some(
      (ref) => ref.cluster === target.cluster && ref.instance === target.instance,
    )
  }
  return false
}

export function describeScheduleScopeForTarget(
  schedule: Pick<MonitorScheduleRecord, "scope_mode" | "targets" | "tags">,
  target: { cluster: string; instance: string } | null,
): string {
  if (schedule.scope_mode === "all_online") return "全部在线目标"
  if (schedule.scope_mode === "tags") {
    const count = schedule.tags?.length ?? 0
    return count > 0 ? `按标签 (${count})` : "按标签筛选"
  }
  const refs = schedule.targets || []
  if (
    target &&
    refs.length === 1 &&
    refs[0].cluster === target.cluster &&
    refs[0].instance === target.instance
  ) {
    return "仅当前目标"
  }
  return `指定目标 (${refs.length})`
}

export function scheduleExecutionKinds(
  schedule: Pick<MonitorScheduleRecord, "command" | "prompt" | "skills">,
): string[] {
  const kinds: string[] = []
  if (schedule.command?.trim()) kinds.push("Shell 命令")
  if (schedule.skills && schedule.skills.length > 0) {
    kinds.push(schedule.skills.length === 1 ? `Skill: ${schedule.skills[0]}` : `Skill (${schedule.skills.length})`)
  }
  if (schedule.prompt?.trim()) kinds.push("AI 提示词")
  return kinds
}

export function scheduleIsInspectionPolicy(
  schedule: Pick<MonitorScheduleRecord, "skills" | "prompt" | "command">,
): boolean {
  if (schedule.skills && schedule.skills.length > 0) return true
  if (schedule.prompt?.trim() && !schedule.command?.trim()) return true
  return false
}

export function filterRunsForTargetFallback(
  runs: readonly MonitorRunRecord[],
  schedules: readonly Pick<MonitorScheduleRecord, "id" | "scope_mode" | "targets">[],
  target: { cluster: string; instance: string } | null,
  targetFiltered?: boolean,
): MonitorRunRecord[] {
  if (!target || targetFiltered) return [...runs]
  const scheduleById = new Map<string, Pick<MonitorScheduleRecord, "id" | "scope_mode" | "targets">>()
  for (const s of schedules) scheduleById.set(s.id, s)
  return runs.filter((run) => {
    const sid = (run.schedule_id || "").trim()
    if (!sid) return true
    const sched = scheduleById.get(sid)
    if (!sched) return true
    return scheduleAppliesToTarget(sched, target)
  })
}

export function validateAutomationScheduleInput(input: {
  name: string
  command: string
  prompt: string
  skills: readonly string[]
  scopeMode: "all_online" | "targets"
  hasTarget: boolean
  selectedTargetsCount?: number
}): string | null {
  const name = input.name.trim()
  if (!name) return "请填写定时计划名称"
  if (name.length > 128) return "计划名称不能超过 128 个字符"
  if (input.scopeMode === "targets") {
    const targetCount = input.selectedTargetsCount ?? (input.hasTarget ? 1 : 0)
    if (targetCount <= 0) {
      return "指定目标模式至少需要选择一个 Agent 目标"
    }
  }
  const hasAction =
    Boolean(input.command.trim()) ||
    Boolean(input.prompt.trim()) ||
    input.skills.length > 0
  if (!hasAction) {
    return "请至少填写 Shell 命令、提示词或勾选一个系统 Skill"
  }
  return null
}
