import type { Target } from "./gateway"

// The console polls /v1/targets every 10 seconds. A poll that returns the same
// target content must keep the same object, and panels must depend on the
// target's identity rather than the object, so a poll never reloads a panel or
// clears what the user is looking at.

const TARGET_FIELDS = [
  "cluster",
  "instance",
  "key",
  "remote",
  "connected_at",
  "last_seen",
  "busy",
  "status",
  "active_ops",
  "queued_ops",
  "busy_reason",
] as const

function sameList(left: readonly string[] = [], right: readonly string[] = []): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

export function sameTarget(left: Target, right: Target): boolean {
  return (
    TARGET_FIELDS.every((field) => left[field] === right[field]) &&
    sameList(left.resources, right.resources) &&
    sameList(left.sessions, right.sessions)
  )
}

// reconcileTargets keeps every unchanged target object, and the previous list
// itself when nothing changed.
export function reconcileTargets(previous: readonly Target[], next: readonly Target[]): Target[] {
  const byKey = new Map(previous.map((target) => [target.key, target]))
  const merged = next.map((target) => {
    const old = byKey.get(target.key)
    return old && sameTarget(old, target) ? old : target
  })
  if (merged.length === previous.length && merged.every((target, index) => target === previous[index])) {
    return previous as Target[]
  }
  return merged
}

// reconcileSelected keeps the selected target while it is still listed, and the
// same object while its content is unchanged. An in-flight command keeps its
// target even if a poll temporarily omits it, so the command owner stays mounted.
export function reconcileSelected(current: Target | null, targets: readonly Target[], commandRunning = false): Target | null {
  if (!current) return targets[0] ?? null
  const still = targets.find((target) => target.key === current.key)
  if (!still) return commandRunning ? current : targets[0] ?? null
  return sameTarget(current, still) ? current : still
}

// targetIdentity is what panels reload on. Heartbeat fields such as last_seen,
// busy and sessions change between polls without changing the target.
export function targetIdentity(target: Pick<Target, "cluster" | "instance">): string {
  return `${target.cluster}/${target.instance}`
}

export type RefreshTrigger = "user" | "background"

// A background poll keeps an operation error on screen. Only a refresh the user
// asked for (or a target or view change) clears it.
export function errorAfterRefresh(error: string, trigger: RefreshTrigger): string {
  return trigger === "background" ? error : ""
}

// mapWithConcurrency runs fn over items with at most `limit` calls in flight,
// keeps result order, stops starting new calls after a failure, and rejects
// with the signal's reason once it is aborted.
export async function mapWithConcurrency<T, R>(
  items: readonly T[],
  limit: number,
  fn: (item: T, index: number) => Promise<R>,
  signal?: AbortSignal,
): Promise<R[]> {
  signal?.throwIfAborted()
  const results = new Array<R>(items.length)
  let next = 0
  let failed = false
  async function worker() {
    while (!failed && next < items.length) {
      signal?.throwIfAborted()
      const index = next++
      try {
        results[index] = await fn(items[index], index)
      } catch (err) {
        failed = true
        throw err
      }
    }
  }
  const workers = Math.min(Math.max(1, Math.floor(limit)), items.length)
  await Promise.all(Array.from({ length: workers }, worker))
  signal?.throwIfAborted()
  return results
}
