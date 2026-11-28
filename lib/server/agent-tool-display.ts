import { create } from "@bufbuild/protobuf"
import { StructuredPayloadSchema, type StructuredPayload } from "@agent-cloud/agent-ui/protocol"

const MAX_TOOL_COMMAND_CHARS = 4096
const MAX_TOOL_SUMMARY_CHARS = 240
const MAX_TOOL_CWD_CHARS = 256

const SHELL_TOOLS = new Set(["bash", "shell", "doops_shell", "mcp_doops_shell", "doops_exec"])
const FILE_READ_TOOLS = new Set(["read"])
const FILE_SEARCH_TOOLS = new Set(["glob", "grep"])
const FILE_WRITE_TOOLS = new Set(["write"])
const FILE_EDIT_TOOLS = new Set(["edit"])
const STRUCTURED_DOOPS_TOOLS = new Set([
  "mcp_doops_k8s_read",
  "mcp_doops_helm",
  "mcp_doops_web",
  "mcp_doops_web_read",
  "mcp_doops_kb_read",
  "mcp_dosql_read",
  "mcp_dosql_exec",
])

const SAFE_DOOPS_META_KEYS = [
  "operation",
  "action",
  "sql_action",
  "kind",
  "namespace",
  "name",
  "release",
  "site_id",
  "domain",
  "database",
  "table",
] as const

export interface AgentToolDisplayInput {
  command?: string
  summary?: string
  cwd?: string
  exitCode?: number
}

export function resolveAgentToolSemanticKey(toolName?: string): string {
  const normalized = (toolName ?? "").trim().toLowerCase()
  if (!normalized) return "tool.call"
  if (SHELL_TOOLS.has(normalized)) return "shell.execute"
  if (FILE_READ_TOOLS.has(normalized)) return "filesystem.read"
  if (FILE_SEARCH_TOOLS.has(normalized)) return "filesystem.search"
  if (FILE_WRITE_TOOLS.has(normalized)) return "filesystem.write"
  if (FILE_EDIT_TOOLS.has(normalized)) return "filesystem.edit"
  return "tool.call"
}

function capText(raw: string | undefined, maxChars: number): string | undefined {
  if (!raw) return undefined
  const trimmed = raw.trim()
  if (!trimmed) return undefined
  if (trimmed.length <= maxChars) return trimmed
  return trimmed.slice(0, maxChars - 1) + "…"
}

function readStringField(record: Record<string, unknown>, keys: readonly string[]): string | undefined {
  for (const key of keys) {
    const val = record[key]
    if (typeof val === "string" && val.trim()) return val.trim()
  }
  return undefined
}

export function extractHistoryToolDisplay(
  toolName: string | undefined,
  argumentsText: string | undefined,
): AgentToolDisplayInput {
  if (!argumentsText) return {}
  const trimmed = argumentsText.trim()
  if (!trimmed || trimmed === "{}" || trimmed === "null") return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return {}
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {}
  const obj = parsed as Record<string, unknown>
  const normalized = (toolName ?? "").trim().toLowerCase()

  if (SHELL_TOOLS.has(normalized)) {
    const command = capText(readStringField(obj, ["command", "cmd"]), MAX_TOOL_COMMAND_CHARS)
    const summary = capText(readStringField(obj, ["description"]), MAX_TOOL_SUMMARY_CHARS)
    const cwd = capText(readStringField(obj, ["cwd", "workdir"]), MAX_TOOL_CWD_CHARS)
    return { ...(command ? { command } : {}), ...(summary ? { summary } : {}), ...(cwd ? { cwd } : {}) }
  }

  if (
    FILE_READ_TOOLS.has(normalized) ||
    FILE_SEARCH_TOOLS.has(normalized) ||
    FILE_WRITE_TOOLS.has(normalized) ||
    FILE_EDIT_TOOLS.has(normalized)
  ) {
    const parts: string[] = []
    const pattern = readStringField(obj, ["pattern"])
    if (pattern) parts.push(pattern)
    const path = readStringField(obj, ["file_path", "path", "filePath"])
    if (path) parts.push(path)
    const summary = capText(parts.join(" · "), MAX_TOOL_SUMMARY_CHARS)
    return summary ? { summary } : {}
  }

  if (STRUCTURED_DOOPS_TOOLS.has(normalized)) {
    const parts: string[] = []
    for (const key of SAFE_DOOPS_META_KEYS) {
      const value = obj[key]
      if (typeof value === "string" && value.trim()) {
        parts.push(`${key}=${value.trim()}`)
      }
    }
    const summary = capText(parts.join(" "), MAX_TOOL_SUMMARY_CHARS)
    return summary ? { summary } : {}
  }

  return {}
}

export function encodeStructuredToolInput(payload: Record<string, unknown>): StructuredPayload {
  return create(StructuredPayloadSchema, {
    mediaType: "application/json",
    data: new TextEncoder().encode(JSON.stringify(payload)),
  })
}

export function buildToolDisplayFields(
  toolName: string | undefined,
  display: AgentToolDisplayInput,
): { input?: StructuredPayload; detail?: string } {
  const semanticKey = resolveAgentToolSemanticKey(toolName)
  const command = capText(display.command, MAX_TOOL_COMMAND_CHARS)
  const summary = capText(display.summary, MAX_TOOL_SUMMARY_CHARS)
  const cwd = capText(display.cwd, MAX_TOOL_CWD_CHARS)
  const exitCode = typeof display.exitCode === "number" && Number.isSafeInteger(display.exitCode)
    ? display.exitCode
    : undefined

  if (semanticKey === "shell.execute") {
    const payload: Record<string, unknown> = {}
    if (command) payload.command = command
    if (cwd) payload.cwd = cwd
    if (exitCode !== undefined) payload.exitCode = exitCode
    // Do NOT place description into input.description: AgentWorkspace's inline
    // preview hides `$ <command>` when input.description is truthy. Keep it in detail.
    const detailParts: string[] = []
    if (summary && summary !== command) detailParts.push(summary)
    if (cwd) detailParts.push(`cwd=${cwd}`)
    if (exitCode !== undefined) detailParts.push(`exit=${exitCode}`)
    return {
      ...(Object.keys(payload).length > 0 ? { input: encodeStructuredToolInput(payload) } : {}),
      ...(detailParts.length > 0 ? { detail: detailParts.join(" · ") } : {}),
    }
  }

  if (
    (semanticKey === "filesystem.read" || semanticKey === "filesystem.write" || semanticKey === "filesystem.edit") &&
    summary
  ) {
    return {
      input: encodeStructuredToolInput({ path: summary }),
      detail: summary,
    }
  }

  if (summary) {
    return { detail: summary }
  }
  return {}
}
