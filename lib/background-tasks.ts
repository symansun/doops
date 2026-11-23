import { consoleFetch } from "./network-error.ts"
import type { Session, Target } from "./client"

// Gateway-hosted detached background tasks (doops_bg detach). The Gateway owns
// the registry: every response here is already scoped to the current user and
// target; there is no admin override.

export interface BackgroundTaskView {
  audit_id: number
  operation_id: string
  cluster: string
  instance: string
  session: string
  command_summary: string
  status: string
  started_at: string
  finished_at?: string
  task_id?: string
  cancel_requested?: boolean
  output?: string
}

function queryTarget(target: Target): string {
  const qs = new URLSearchParams({ cluster: target.cluster, instance: target.instance })
  return qs.toString()
}

export async function listBackgroundTasks(session: Session, target: Target): Promise<BackgroundTaskView[]> {
  if (session.demo) {
    const { demoBackgroundTasks } = await import("./demo")
    return demoBackgroundTasks(target)
  }
  const res = await consoleFetch(`/api/background-tasks?${queryTarget(target)}`, { cache: "no-store" })
  const data = (await res.json().catch(() => ({}))) as { tasks?: BackgroundTaskView[]; error?: string }
  if (!res.ok) throw new BackgroundTaskError(data.error || `后台任务读取失败 (${res.status})`, res.status)
  return Array.isArray(data.tasks) ? data.tasks : []
}

export async function getBackgroundTask(session: Session, id: number): Promise<BackgroundTaskView> {
  if (session.demo) {
    const { demoBackgroundTask } = await import("./demo")
    return demoBackgroundTask(id)
  }
  const res = await consoleFetch(`/api/background-tasks/${id}`, { cache: "no-store" })
  const data = (await res.json().catch(() => ({}))) as BackgroundTaskView & { error?: string }
  if (!res.ok) throw new BackgroundTaskError(data.error || `后台任务读取失败 (${res.status})`, res.status)
  return data
}

export async function stopBackgroundTask(session: Session, id: number): Promise<"requested" | "already_finished"> {
  if (session.demo) {
    const { demoStopBackgroundTask } = await import("./demo")
    return demoStopBackgroundTask(id)
  }
  const res = await consoleFetch(`/api/background-tasks/${id}/cancel`, { method: "POST" })
  if (res.status === 202) return "requested"
  // A task that is already gone or already terminal is not a failed stop: the user
  // asked to end something that had ended. Report it as such instead of a red error.
  if (res.status === 404 || res.status === 409) return "already_finished"
  const data = (await res.json().catch(() => ({}))) as { error?: string }
  throw new BackgroundTaskError(data.error || `停止请求失败 (${res.status})`, res.status)
}

export class BackgroundTaskError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = "BackgroundTaskError"
    this.status = status
  }
}
