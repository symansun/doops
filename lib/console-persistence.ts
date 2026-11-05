import type { Target } from "./gateway"

// A page reload must not silently move the user to another target or session.
// The selected target key and the current session id are kept per browser tab
// (sessionStorage) and per Gateway + user. Command output, credentials and
// tokens are never stored.

export type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">

export interface StorageScope {
  gateway: string
  username?: string
}

export interface ConsoleSelection {
  targetKey: string
  sessionId: string
}

const PREFIX = "doops.console.v1"

export function consoleScopeKey(scope: StorageScope): string {
  return `${encodeURIComponent(scope.gateway)}|${encodeURIComponent(scope.username ?? "")}`
}

function selectionKey(scope: StorageScope): string {
  return `${PREFIX}.selection:${consoleScopeKey(scope)}`
}

// sessionStorage can be missing or throw (private mode, disabled storage); the
// console then behaves as before and keeps nothing.
export function browserSessionStorage(): StorageLike | null {
  try {
    return typeof window === "undefined" ? null : window.sessionStorage
  } catch {
    return null
  }
}

export function readStored(storage: StorageLike | null, key: string): unknown {
  try {
    const raw = storage?.getItem(key)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

export function writeStored(storage: StorageLike | null, key: string, value: unknown): void {
  try {
    if (value === null) storage?.removeItem(key)
    else storage?.setItem(key, JSON.stringify(value))
  } catch {
    // Storage is best effort; a failed write keeps the in-memory state.
  }
}

const CONTROL = /[\u0000-\u001f\u007f]/

// Same rule as the Gateway's validateSession: no path separators or "..".
export function validSessionId(value: unknown): value is string {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    value.length <= 128 &&
    value.trim() === value &&
    value !== "." &&
    !value.includes("..") &&
    !/[/\\]/.test(value) &&
    !CONTROL.test(value)
  )
}

export function validTargetKey(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 256 && !CONTROL.test(value)
}

export function saveSelection(storage: StorageLike | null, scope: StorageScope, selection: ConsoleSelection): void {
  if (!validTargetKey(selection.targetKey) || !validSessionId(selection.sessionId)) return
  writeStored(storage, selectionKey(scope), { targetKey: selection.targetKey, sessionId: selection.sessionId })
}

export function loadSelection(storage: StorageLike | null, scope: StorageScope): ConsoleSelection | null {
  const value = readStored(storage, selectionKey(scope))
  if (!value || typeof value !== "object" || Array.isArray(value)) return null
  const { targetKey, sessionId } = value as Record<string, unknown>
  if (!validTargetKey(targetKey) || !validSessionId(sessionId)) return null
  return { targetKey, sessionId }
}

export interface RestoredSelection {
  selected: Target | null
  // Set only when the saved session belongs to the restored target.
  sessionId: string | null
  // Shown once when the saved target is no longer listed.
  notice: string
}

export function restoreSelection(saved: ConsoleSelection | null, targets: readonly Target[]): RestoredSelection {
  const first = targets[0] ?? null
  if (!saved) return { selected: first, sessionId: null, notice: "" }
  const match = targets.find((target) => target.key === saved.targetKey)
  if (match) return { selected: match, sessionId: saved.sessionId, notice: "" }
  const reason = `上次选中的目标 ${saved.targetKey} 当前不在可用目标列表中（可能已离线或已取消授权）`
  return {
    selected: first,
    sessionId: null,
    notice: first ? `${reason}，已切换到 ${first.key} 并开启新会话` : `${reason}，当前没有可用目标`,
  }
}
