// What the console shows after an upgrade is remembered per browser only: the
// release version the notes were last shown for and the local day the user
// asked not to be popped up again. Nothing leaves the browser, so the dialog
// works for an air-gapped Gateway and tells it nothing about which notes were
// read. The dates are local days ("2026-11-11"), not instants, so "today"
// ends at the user's midnight rather than at UTC midnight.

import { compareVersions } from "./changelog.ts"
import type { ChangelogRelease } from "./changelog.ts"
import type { StorageLike } from "./console-persistence.ts"

export const LAST_VERSION_KEY = "doops.whats-new.v1.last-version"
export const QUIET_DAY_KEY = "doops.whats-new.v1.quiet-day"

export interface WhatsNewState {
  /** The version whose notes were last shown, or null before the first visit. */
  lastVersion: string | null
  /** The local day (YYYY-MM-DD) the user asked not to be popped up again. */
  quietDay: string | null
}

function readKey(storage: StorageLike | null, key: string): string | null {
  try {
    const value = storage?.getItem(key)
    return value && value.trim() ? value.trim() : null
  } catch {
    return null
  }
}

function writeKey(storage: StorageLike | null, key: string, value: string | null): void {
  try {
    if (value === null) storage?.removeItem(key)
    else storage?.setItem(key, value)
  } catch {
    // Storage is best effort: a private window keeps nothing, and the dialog
    // still opens manually for this page load.
  }
}

export function readWhatsNewState(storage: StorageLike | null): WhatsNewState {
  return {
    lastVersion: readKey(storage, LAST_VERSION_KEY),
    quietDay: readKey(storage, QUIET_DAY_KEY),
  }
}

export function recordLastVersion(storage: StorageLike | null, version: string): void {
  writeKey(storage, LAST_VERSION_KEY, version)
}

export function setQuietDay(storage: StorageLike | null, day: string | null): void {
  writeKey(storage, QUIET_DAY_KEY, day)
}

export function localDay(now: Date = new Date()): string {
  const pad = (value: number) => String(value).padStart(2, "0")
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

export type AutoOpenDecision = "open" | "record" | "skip"

// autoOpenDecision mirrors what magpie does on start: a first visit records
// the running version without a dialog (a new user gets no wall of history),
// a visit on a newer version opens it once, and "today" suppresses the popup
// while leaving the manual entry available.
export function autoOpenDecision(input: {
  state: WhatsNewState
  currentVersion: string
  today: string
}): AutoOpenDecision {
  if (!input.currentVersion) return "skip"
  if (input.state.lastVersion === null) return "record"
  if (compareVersions(input.state.lastVersion, input.currentVersion) >= 0) return "skip"
  if (input.state.quietDay === input.today) return "skip"
  return "open"
}

export function hasUnseenVersions(state: WhatsNewState, currentVersion: string): boolean {
  if (!currentVersion || state.lastVersion === null) return false
  return compareVersions(state.lastVersion, currentVersion) < 0
}

// rangeReleases is the dialog's default view: since the last visit when there
// is one, otherwise the latest count. An upgrade whose notes are all missing
// from the changelog falls back to the current release so the dialog is never
// empty.
export function rangeReleases(
  releases: ChangelogRelease[],
  options: { since?: string | null; count?: number },
): ChangelogRelease[] {
  if (releases.length === 0) return []
  if (options.since) {
    const newer = releases.filter((release) => compareVersions(release.version, options.since as string) > 0)
    const found = releases.some((release) => compareVersions(release.version, options.since as string) === 0)
    if (found && newer.length > 0) return newer
  }
  return releases.slice(0, options.count ?? 20)
}
