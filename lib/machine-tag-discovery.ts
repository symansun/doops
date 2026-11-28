export const MAX_MACHINE_TAGS = 20
export const MAX_MACHINE_TAG_RUNES = 32

// Matches Gateway machine_inventory_store.go machineTagPattern & normalizeMachineTags.
const MACHINE_TAG_PATTERN = /^[\p{L}\p{N}][\p{L}\p{M}\p{N}._-]*(?:=[\p{L}\p{N}][\p{L}\p{M}\p{N}._-]*)?$/u
const SENSITIVE_TAG_KEYWORDS = [
  "password",
  "passwd",
  "secret",
  "token",
  "apikey",
  "api_key",
  "private",
  "credential",
  "bearer",
  "accesskey",
]

export interface SuggestedMachineTag {
  tag: string
  category: string
  confidence: "high" | "medium" | "low"
  source: string
  evidence: string
}

export interface MachineInventorySummary {
  asset_id: string
  name: string
  kind: "physical" | "vm"
  address: string
  cpu_count?: number | null
  memory_gib?: number | null
  disk_gib?: number | null
  host_asset_id?: string
  cluster?: string
  instance?: string
  created_at?: string
  tags?: string[]
  revision: number
}

export function isValidMachineTag(raw: string): boolean {
  const s = raw.trim().toLowerCase()
  if (!s || Array.from(s).length > MAX_MACHINE_TAG_RUNES) return false
  if (!MACHINE_TAG_PATTERN.test(s)) return false
  if (SENSITIVE_TAG_KEYWORDS.some((kw) => s.includes(kw))) return false
  return true
}

export function normalizeSuggestedTags(artifactJson: unknown): SuggestedMachineTag[] {
  if (!artifactJson || typeof artifactJson !== "object" || Array.isArray(artifactJson)) {
    return []
  }
  const rawList = (artifactJson as Record<string, unknown>).suggested_tags
  if (!Array.isArray(rawList)) return []

  const seen = new Set<string>()
  const out: SuggestedMachineTag[] = []
  for (const item of rawList) {
    if (!item || typeof item !== "object" || Array.isArray(item)) continue
    const rec = item as Record<string, unknown>
    const tag = typeof rec.tag === "string" ? rec.tag.trim().toLowerCase() : ""
    if (!tag || seen.has(tag) || !isValidMachineTag(tag)) continue
    seen.add(tag)
    const rawConf = typeof rec.confidence === "string" ? rec.confidence.trim().toLowerCase() : "medium"
    const confidence: "high" | "medium" | "low" =
      rawConf === "high" || rawConf === "low" ? rawConf : "medium"
    out.push({
      tag,
      category: typeof rec.category === "string" && rec.category.trim() ? rec.category.trim() : "ops",
      confidence,
      source: typeof rec.source === "string" ? rec.source.trim() : "",
      evidence: typeof rec.evidence === "string" ? rec.evidence.trim() : "",
    })
  }
  return out
}

export interface MergedMachineTagsPlan {
  existing: string[]
  alreadyPresent: string[]
  added: string[]
  merged: string[]
  exceedsLimit: boolean
  overflowCount: number
}

export function computeMergedMachineTags(existingTags: string[], selectedSuggestedTags: string[]): MergedMachineTagsPlan {
  const existingSet = new Set<string>()
  for (const t of existingTags) {
    const clean = t.trim().toLowerCase()
    if (clean) existingSet.add(clean)
  }

  const alreadyPresent: string[] = []
  const added: string[] = []
  const addedSeen = new Set<string>()

  for (const raw of selectedSuggestedTags) {
    const tag = raw.trim().toLowerCase()
    if (!tag || !isValidMachineTag(tag)) continue
    if (existingSet.has(tag)) {
      if (!alreadyPresent.includes(tag)) alreadyPresent.push(tag)
      continue
    }
    if (!addedSeen.has(tag)) {
      addedSeen.add(tag)
      added.push(tag)
    }
  }

  const existing = [...existingSet].sort()
  const merged = [...new Set([...existing, ...added])].sort()
  const overflowCount = Math.max(0, merged.length - MAX_MACHINE_TAGS)
  return {
    existing,
    alreadyPresent: alreadyPresent.sort(),
    added: added.sort(),
    merged,
    exceedsLimit: overflowCount > 0,
    overflowCount,
  }
}

export function buildMachineTagUpdatePayload(machine: MachineInventorySummary, mergedTags: string[]) {
  const next: Record<string, unknown> = {
    asset_id: machine.asset_id,
    name: machine.name,
    kind: machine.kind,
    address: machine.address ?? "",
    tags: mergedTags,
  }
  if (typeof machine.cpu_count === "number" && machine.cpu_count > 0) next.cpu_count = machine.cpu_count
  if (typeof machine.memory_gib === "number" && machine.memory_gib > 0) next.memory_gib = machine.memory_gib
  if (typeof machine.disk_gib === "number" && machine.disk_gib > 0) next.disk_gib = machine.disk_gib
  if (machine.kind === "vm" && machine.host_asset_id) next.host_asset_id = machine.host_asset_id
  if (machine.cluster && machine.instance) {
    next.cluster = machine.cluster
    next.instance = machine.instance
  }
  if (machine.created_at) next.created_at = machine.created_at

  return {
    action: "update" as const,
    machine: next,
    expected_revision: machine.revision,
  }
}
