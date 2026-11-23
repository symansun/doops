import type { Session } from "./client.ts"
import { consoleFetch } from "./network-error.ts"
import type { MonitorTargetRef } from "./monitor-engine.ts"

export interface PlatformSkillFile {
  path: string
  content: string
}

export interface PlatformSkillRecord {
  slug: string
  name: string
  description: string
  scope: "system" | "custom"
  category: "inspection" | "security" | "ops"
  driver_mode: "builtin_strategy" | "agent_prompt"
  strategy_id?: string
  enabled: boolean
  skill_md?: string
  files?: PlatformSkillFile[]
  file_count: number
  sha256: string
  owner_id: string
  owner_name: string
  created_at_ms: number
  updated_at_ms: number
  last_run_at_ms?: number
  run_count: number
  can_edit: boolean
  can_delete: boolean
}

export interface SkillSyncTargetResult {
  cluster: string
  instance: string
  synced_count: number
  status: "ok" | "offline" | "error"
  detail?: string
}

export interface AgentLoadedSkillInfo {
  slug: string
  name: string
  description?: string
  kind: "dir" | "file" | string
  file_count: number
  size_bytes: number
  updated_at_ms: number
}

export const SKILL_CATEGORY_LABELS: Record<PlatformSkillRecord["category"], string> = {
  inspection: "巡检驱动",
  security: "安全巡检",
  ops: "运维与诊断",
}

export const SKILL_DRIVER_LABELS: Record<PlatformSkillRecord["driver_mode"], string> = {
  builtin_strategy: "内置策略直采",
  agent_prompt: "Agent Skill 驱动",
}

export function slugifySkillName(raw: string): string {
  const cleaned = raw
    .trim()
    .toLowerCase()
    .replace(/\.md$/i, "")
    .replace(/\.zip$/i, "")
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63)
  return /^[a-z0-9]/.test(cleaned) ? cleaned : cleaned ? `s-${cleaned}`.slice(0, 63) : ""
}

export function parseSkillFrontmatter(md: string): { name: string; description: string } {
  const trimmed = md.trim()
  if (!trimmed.startsWith("---")) return { name: "", description: "" }
  const rest = trimmed.slice(3).replace(/^\r?\n/, "")
  const endIdx = rest.indexOf("\n---")
  if (endIdx < 0) return { name: "", description: "" }
  const header = rest.slice(0, endIdx)
  let name = ""
  let description = ""
  for (const rawLine of header.split(/\r?\n/)) {
    const line = rawLine.trim()
    const colon = line.indexOf(":")
    if (colon <= 0) continue
    const k = line.slice(0, colon).trim().toLowerCase()
    const v = line.slice(colon + 1).trim().replace(/^["']|["']$/g, "")
    if (k === "name" && !name) name = v
    else if (k === "description" && !description) description = v
  }
  return { name, description }
}

/**
 * Extract SKILL.md and relative text files from an uploaded .zip Skill archive.
 */
export async function extractSkillZipFile(file: File): Promise<{
  suggestedSlug: string
  name: string
  description: string
  skillMD: string
  files: PlatformSkillFile[]
}> {
  const zipjs = await import("@zip.js/zip.js")
  const reader = new zipjs.ZipReader(new zipjs.BlobReader(file))
  try {
    const entries = await reader.getEntries()
    const usable = entries.filter(
      (e) =>
        !e.directory &&
        !e.filename.startsWith("__MACOSX/") &&
        !e.filename.split("/").some((part) => part === ".DS_Store" || part === ".." || part === ""),
    )
    if (usable.length === 0) {
      throw new Error("压缩包中没有可用文件")
    }

    // Strip a common top-level wrapper folder (e.g., my-skill/SKILL.md -> SKILL.md)
    const firstSegs = new Set(usable.map((e) => (e.filename.includes("/") ? e.filename.split("/")[0] : "")))
    const stripPrefix = firstSegs.size === 1 && !firstSegs.has("") ? `${[...firstSegs][0]}/` : ""

    let skillMD = ""
    const files: PlatformSkillFile[] = []
    let totalBytes = 0

    for (const entry of usable) {
      const rel = stripPrefix && entry.filename.startsWith(stripPrefix) ? entry.filename.slice(stripPrefix.length) : entry.filename
      if (!rel || rel.startsWith("/") || rel.includes("..")) continue
      if (entry.uncompressedSize > 256 * 1024 && rel.toLowerCase() !== "skill.md") {
        throw new Error(`附属文件 ${rel} 超过 256 KiB 限制`)
      }
      if (entry.directory || !("getData" in entry) || typeof entry.getData !== "function") continue
      const content = await entry.getData(new zipjs.TextWriter())
      totalBytes += content.length
      if (totalBytes > 1024 * 1024) {
        throw new Error("Skill 包解压后总大小超过 1 MiB 限制")
      }
      if (rel.toLowerCase() === "skill.md") {
        skillMD = content
      } else if (files.length < 16) {
        files.push({ path: rel, content })
      }
    }

    if (!skillMD.trim()) {
      throw new Error("压缩包根目录中未找到 SKILL.md 文件")
    }

    const fm = parseSkillFrontmatter(skillMD)
    const folderSlug = stripPrefix ? slugifySkillName(stripPrefix.replace(/\/$/, "")) : ""
    const suggestedSlug = slugifySkillName(fm.name) || folderSlug || slugifySkillName(file.name)
    return {
      suggestedSlug,
      name: fm.name || suggestedSlug,
      description: fm.description,
      skillMD,
      files,
    }
  } finally {
    await reader.close()
  }
}

async function skillsRequest<T>(session: Session, path: string, body?: object, signal?: AbortSignal): Promise<T> {
  if (session.demo) {
    const { demoSkillsRequest } = await import("./demo.ts")
    return demoSkillsRequest(session, path, body as Record<string, unknown> | undefined) as Promise<T>
  }
  const url = !path ? "/api/skills" : path.startsWith("?") ? `/api/skills${path}` : `/api/skills/${path}`
  const res = await consoleFetch(url, {
    method: body ? "POST" : "GET",
    cache: "no-store",
    signal,
    ...(body ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {}),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(data.message || data.error || `Skill 接口返回 HTTP ${res.status}`)
  }
  return data as T
}

export function fetchPlatformSkills(
  session: Session,
  opts: { scope?: "system" | "custom"; category?: string; includeBody?: boolean; target?: string } = {},
  signal?: AbortSignal,
): Promise<{
  skills: PlatformSkillRecord[]
  agent_skills?: AgentLoadedSkillInfo[]
  agent_skills_root?: string
  agent_error?: string
}> {
  const qs = new URLSearchParams()
  if (opts.scope) qs.set("scope", opts.scope)
  if (opts.category) qs.set("category", opts.category)
  if (opts.includeBody) qs.set("include_body", "1")
  if (opts.target) qs.set("target", opts.target)
  const suffix = qs.toString() ? `?${qs.toString()}` : ""
  return skillsRequest(session, suffix, undefined, signal)
}

export function fetchPlatformSkillDetail(
  session: Session,
  slug: string,
  signal?: AbortSignal,
): Promise<{ skill: PlatformSkillRecord }> {
  return skillsRequest(session, encodeURIComponent(slug), undefined, signal)
}

export function upsertPlatformSkill(
  session: Session,
  payload: {
    slug: string
    name?: string
    description?: string
    scope?: "system" | "custom"
    category?: "inspection" | "security" | "ops"
    driver_mode?: "builtin_strategy" | "agent_prompt"
    strategy_id?: string
    enabled?: boolean
    skill_md: string
    files?: PlatformSkillFile[]
  },
): Promise<{ skill: PlatformSkillRecord }> {
  return skillsRequest(session, "", { action: "upsert", ...payload })
}

export function togglePlatformSkill(
  session: Session,
  slug: string,
  enabled: boolean,
): Promise<{ skill: PlatformSkillRecord }> {
  return skillsRequest(session, "", { action: "toggle", slug, enabled })
}

export function deletePlatformSkill(session: Session, slug: string): Promise<{ deleted: string }> {
  return skillsRequest(session, "", { action: "delete", slug })
}

export function resetSystemSkill(session: Session, slug: string): Promise<{ skill: PlatformSkillRecord }> {
  return skillsRequest(session, "", { action: "reset_system", slug })
}

export function syncSkillsToAgents(
  session: Session,
  opts: { slug?: string; targets?: MonitorTargetRef[] } = {},
): Promise<{ results: SkillSyncTargetResult[] }> {
  return skillsRequest(session, "", { action: "sync", slug: opts.slug || "", targets: opts.targets })
}
