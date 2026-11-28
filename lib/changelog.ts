// The console's "What's new" dialog reads the repository CHANGELOG.md, which
// follows Keep a Changelog: one "## [version] — YYYY-MM-DD" section per release
// and "### Added"/"### Changed"/"### Fixed"/"### Security" lists under it. The
// release section is the unit the console compares, so [Unreleased] is parsed
// but never shown: it belongs to a release that does not exist yet.
//
// Everything here is pure: no filesystem, no React, no storage. The server
// loader (lib/server/changelog.ts) reads the file and the dialog renders the
// values; that keeps parsing, merging and range selection testable in Node.

export type ChangeCategory =
  | "added"
  | "changed"
  | "performance"
  | "fixed"
  | "security"
  | "deprecated"
  | "removed"
  | "other"

// The order the dialog shows: what a user looks for after an upgrade first.
export const CATEGORY_ORDER: readonly ChangeCategory[] = [
  "added",
  "changed",
  "performance",
  "fixed",
  "security",
  "deprecated",
  "removed",
  "other",
]

const CATEGORY_LABELS: Record<ChangeCategory, string> = {
  added: "新功能",
  changed: "改进",
  performance: "性能",
  fixed: "修复",
  security: "安全",
  deprecated: "弃用",
  removed: "移除",
  other: "其他",
}

// Keep a Changelog headings are English; the aliases also accept the Chinese
// headings maintainers are likely to write by habit.
const CATEGORY_ALIASES: Record<string, ChangeCategory> = {
  added: "added",
  add: "added",
  new: "added",
  feature: "added",
  features: "added",
  "新功能": "added",
  "新增": "added",
  changed: "changed",
  change: "changed",
  changes: "changed",
  improvement: "changed",
  improvements: "changed",
  "改进": "changed",
  "变更": "changed",
  "优化": "changed",
  performance: "performance",
  perf: "performance",
  "性能": "performance",
  "性能优化": "performance",
  fixed: "fixed",
  fix: "fixed",
  fixes: "fixed",
  "bug fixes": "fixed",
  bugfix: "fixed",
  "修复": "fixed",
  "问题修复": "fixed",
  security: "security",
  "安全": "security",
  "安全修复": "security",
  deprecated: "deprecated",
  deprecations: "deprecated",
  "弃用": "deprecated",
  removed: "removed",
  remove: "removed",
  removals: "removed",
  "移除": "removed",
  "删除": "removed",
  other: "other",
  misc: "other",
  "其他": "other",
  "其它": "other",
}

export function changeCategoryLabel(category: ChangeCategory): string {
  return CATEGORY_LABELS[category] ?? CATEGORY_LABELS.other
}

function categoryOf(heading: string): ChangeCategory {
  const normalized = heading
    .trim()
    .toLowerCase()
    .replace(/[#:*_\s]+$/g, "")
    .replace(/^[#:*_]+/g, "")
    .replace(/\s+/g, " ")
  return CATEGORY_ALIASES[normalized] ?? "other"
}

export interface ChangelogSection {
  category: ChangeCategory
  label: string
  items: string[]
}

export interface ChangelogRelease {
  version: string
  date: string | null
  sections: ChangelogSection[]
}

export interface ParsedChangelog {
  /** Released versions only, newest first. */
  releases: ChangelogRelease[]
  /** The [Unreleased] sections, for future use; the dialog does not show them. */
  unreleased: ChangelogSection[]
}

const VERSION = /\[?v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]+)?)\]?/
const DATE = /(\d{4}-\d{2}-\d{2})/

// parseReleaseHeading reads "## [0.2.2] — 2026-11-08", "## 0.2.2" and
// "## [Unreleased]"; anything else (the "# Changelog" title, a stray heading)
// starts no release and its lines are ignored.
function parseReleaseHeading(text: string): { version: string | null; date: string | null } | null {
  const heading = text.replace(/<!--.*?-->/g, "").trim()
  if (!heading) return null
  if (/^\[?unreleased\]?$/i.test(heading)) return { version: null, date: null }
  const version = VERSION.exec(heading)
  if (!version) return null
  const date = DATE.exec(heading)
  return { version: version[1], date: date ? date[1] : null }
}

// Version compare is semantic enough for release notes: numbers first, then a
// prerelease suffix (0.2.3-rc.1 sorts before 0.2.3). A leading "v" and build
// metadata are ignored so tags and CHANGELOG headings compare alike.
export function compareVersions(a: string, b: string): number {
  const left = splitVersion(a)
  const right = splitVersion(b)
  for (let i = 0; i < Math.max(left.core.length, right.core.length); i += 1) {
    const diff = (left.core[i] ?? 0) - (right.core[i] ?? 0)
    if (diff !== 0) return diff < 0 ? -1 : 1
  }
  if (left.pre.length === 0 && right.pre.length === 0) return 0
  if (left.pre.length === 0) return 1
  if (right.pre.length === 0) return -1
  for (let i = 0; i < Math.max(left.pre.length, right.pre.length); i += 1) {
    const l = left.pre[i]
    const r = right.pre[i]
    if (l === undefined) return -1
    if (r === undefined) return 1
    const ln = /^\d+$/.test(l) ? Number(l) : null
    const rn = /^\d+$/.test(r) ? Number(r) : null
    if (ln !== null && rn !== null) {
      if (ln !== rn) return ln < rn ? -1 : 1
    } else if (ln !== null) {
      return -1
    } else if (rn !== null) {
      return 1
    } else if (l !== r) {
      return l < r ? -1 : 1
    }
  }
  return 0
}

function splitVersion(version: string): { core: number[]; pre: string[] } {
  const withoutPrefix = version.trim().replace(/^v/i, "").split("+")[0]
  const [coreText, preText = ""] = withoutPrefix.split("-", 2)
  const core = coreText.split(".").map((part) => Number.parseInt(part, 10) || 0)
  const pre = preText ? preText.split(".") : []
  return { core, pre }
}

// Range selection: "since" is the version seen last (exclusive, so the dialog
// starts at the release after it), "count" is the latest N releases. An
// unknown "since" older than the first entry means "show everything"; one at
// or above the latest release means there is nothing new and the caller
// decides whether to fall back to the current release.
export function selectReleases(
  releases: ChangelogRelease[],
  range: { since?: string | null; count?: number | null },
): ChangelogRelease[] {
  const sorted = [...releases].sort((a, b) => compareVersions(b.version, a.version))
  if (range.since) {
    const index = sorted.findIndex((release) => compareVersions(release.version, range.since as string) === 0)
    if (index === 0) return []
    if (index > 0) return sorted.slice(0, index)
    return compareVersions(range.since, sorted[0].version) < 0 ? sorted : []
  }
  if (range.count && range.count > 0) return sorted.slice(0, range.count)
  return sorted
}

export interface MergedItem {
  version: string
  text: string
}

export interface MergedSection {
  category: ChangeCategory
  label: string
  items: MergedItem[]
}

export interface MergedNotes {
  /** Oldest version in the range, without the v prefix. */
  from: string
  /** Newest version in the range, without the v prefix. */
  to: string
  versions: number
  sections: MergedSection[]
}

function itemKey(text: string): string {
  return text.toLowerCase().replace(/\s+/g, " ").trim()
}

// mergeReleases turns many releases into the one list the dialog shows: every
// bullet under its category, each tagged with the version it arrived in,
// newest release first and a repeated bullet kept only once. The dialog reads
// as one page instead of one page per release (magpie made the same call).
export function mergeReleases(releases: ChangelogRelease[]): MergedNotes | null {
  const sorted = [...releases].sort((a, b) => compareVersions(b.version, a.version))
  if (sorted.length === 0) return null
  const seen = new Set<string>()
  const sections = new Map<ChangeCategory, MergedItem[]>()
  for (const release of sorted) {
    for (const section of release.sections) {
      for (const text of section.items) {
        const key = itemKey(text)
        if (!key || seen.has(key)) continue
        seen.add(key)
        const bucket = sections.get(section.category) ?? []
        bucket.push({ version: release.version, text })
        sections.set(section.category, bucket)
      }
    }
  }
  const merged: MergedSection[] = []
  for (const category of CATEGORY_ORDER) {
    const items = sections.get(category)
    if (items?.length) merged.push({ category, label: changeCategoryLabel(category), items })
  }
  if (merged.length === 0) return null
  return {
    from: sorted[sorted.length - 1].version,
    to: sorted[0].version,
    versions: sorted.length,
    sections: merged,
  }
}

export type InlineToken =
  | { kind: "text"; text: string }
  | { kind: "code"; text: string }
  | { kind: "strong"; text: string }
  | { kind: "link"; text: string; href: string }

const INLINE = /(\*\*([^*]+)\*\*|`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)\))/g

// parseInline tokenizes the little Markdown a changelog bullet may carry. The
// dialog renders the tokens as React nodes, never as HTML; a link is only a
// link for http(s), so a manifest cannot smuggle another scheme into the page.
export function parseInline(text: string): InlineToken[] {
  const tokens: InlineToken[] = []
  let last = 0
  for (const match of text.matchAll(INLINE)) {
    const index = match.index ?? 0
    if (index > last) tokens.push({ kind: "text", text: text.slice(last, index) })
    if (match[2] !== undefined) tokens.push({ kind: "strong", text: match[2] })
    else if (match[3] !== undefined) tokens.push({ kind: "code", text: match[3] })
    else if (match[4] !== undefined && match[5] !== undefined) {
      const href = match[5]
      if (/^https?:\/\//i.test(href)) tokens.push({ kind: "link", text: match[4], href })
      else tokens.push({ kind: "text", text: match[0] })
    }
    last = index + match[0].length
  }
  if (last < text.length) tokens.push({ kind: "text", text: text.slice(last) })
  return tokens
}

interface WorkingRelease {
  version: string | null
  date: string | null
  sections: Map<ChangeCategory, string[]>
}

function sectionItems(sections: Map<ChangeCategory, string[]>): ChangelogSection[] {
  const result: ChangelogSection[] = []
  for (const category of CATEGORY_ORDER) {
    const items = sections.get(category)
    if (items?.length) result.push({ category, label: changeCategoryLabel(category), items })
  }
  return result
}

// parseChangelog is line based on purpose: a malformed release or an unknown
// "###" heading must not take the rest of the file with it. Bullets may wrap
// onto indented lines; a release with no bullets is left out like magpie does,
// so an empty "## [0.2.2]" cannot produce a blank dialog.
export function parseChangelog(markdown: string): ParsedChangelog {
  const releases: ChangelogRelease[] = []
  const unreleased: ChangelogSection[] = []
  const seenVersions = new Set<string>()
  let current: WorkingRelease | null = null
  let currentCategory: ChangeCategory | null = null
  let currentItem: string[] | null = null

  const closeItem = () => {
    if (!current || !currentCategory || !currentItem) {
      currentItem = null
      return
    }
    const text = currentItem.join(" ").trim()
    if (text) {
      const items = current.sections.get(currentCategory) ?? []
      items.push(text)
      current.sections.set(currentCategory, items)
    }
    currentItem = null
  }

  const finishRelease = () => {
    closeItem()
    if (!current) return
    const sections = sectionItems(current.sections)
    if (sections.length === 0) return
    if (current.version === null) {
      unreleased.push(...sections)
      return
    }
    if (seenVersions.has(current.version)) return
    seenVersions.add(current.version)
    releases.push({ version: current.version, date: current.date, sections })
  }

  const lines = markdown
    .replace(/\r\n?/g, "\n")
    .replace(/<!--[\s\S]*?-->/g, "")
    .split("\n")

  for (const line of lines) {
    if (/^##\s+/.test(line)) {
      finishRelease()
      const heading = parseReleaseHeading(line.slice(2))
      current = heading
        ? { version: heading.version, date: heading.date, sections: new Map() }
        : null
      currentCategory = null
      continue
    }
    if (/^###\s+/.test(line)) {
      closeItem()
      if (!current) continue
      currentCategory = categoryOf(line.slice(3))
      continue
    }
    if (!current) continue
    const bullet = /^\s*[-*]\s+(.+)$/.exec(line)
    if (bullet) {
      closeItem()
      currentItem = [bullet[1].trim()]
      continue
    }
    if (line.trim() === "") {
      closeItem()
      continue
    }
    if (currentItem) {
      currentItem.push(line.trim())
      continue
    }
  }
  finishRelease()

  releases.sort((a, b) => compareVersions(b.version, a.version))
  return { releases, unreleased }
}
