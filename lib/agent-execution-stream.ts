export type AgentExecutionStatus =
  | "pending"
  | "running"
  | "waiting"
  | "completed"
  | "failed"
  | "cancelled"
  | "unconfirmed"
  | "unknown"

export interface AgentTimelineToolItemLike {
  id: string
  kind: string
  identity?: {
    namespace?: string
    semanticKey?: string
    sourceToolName?: string
  }
  sourceToolName?: string
  title?: string
  detail?: string
  input?: string
  inputValue?: unknown
  output?: string
  status?: string
  results?: readonly { kind?: string; text?: string }[]
  failure?: { message?: string }
  timing?: { durationMillis?: number; startedAt?: string; finishedAt?: string }
  execution?: { turnId?: string }
}

export interface AgentExecutionEntry {
  id: string
  index: number
  turnId?: string
  toolName: string
  semanticKey: string
  isShell: boolean
  command?: string
  summary?: string
  cwd?: string
  exitCode?: number
  durationMs?: number
  status: AgentExecutionStatus
  output: string
}

const SHELL_TOOL_RE = /^(?:bash|shell|doops_shell|mcp_doops_shell|doops_exec)$/i

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function parseInputRecord(item: AgentTimelineToolItemLike): Record<string, unknown> | null {
  const direct = asRecord(item.inputValue)
  if (direct) return direct
  if (typeof item.input === "string") {
    const trimmed = item.input.trim()
    if (trimmed.startsWith("{") && trimmed.endsWith("}")) {
      try {
        return asRecord(JSON.parse(trimmed))
      } catch {
        return null
      }
    }
  }
  return null
}

function readDurationMs(timing?: AgentTimelineToolItemLike["timing"]): number | undefined {
  if (!timing) return undefined
  if (typeof timing.durationMillis === "number" && Number.isFinite(timing.durationMillis) && timing.durationMillis >= 0) {
    return timing.durationMillis
  }
  if (timing.startedAt && timing.finishedAt) {
    const diff = Date.parse(timing.finishedAt) - Date.parse(timing.startedAt)
    if (Number.isFinite(diff) && diff >= 0) return diff
  }
  return undefined
}

function normalizeStatus(raw?: string): AgentExecutionStatus {
  switch (raw) {
    case "pending":
    case "running":
    case "waiting":
    case "completed":
    case "failed":
    case "cancelled":
    case "unconfirmed":
    case "unknown":
      return raw
    default:
      return "completed"
  }
}

function readOutput(item: AgentTimelineToolItemLike): string {
  if (typeof item.output === "string" && item.output.trim()) return item.output
  if (Array.isArray(item.results)) {
    const texts = item.results
      .filter((r) => r && r.kind === "text" && typeof r.text === "string" && r.text.length > 0)
      .map((r) => r.text!)
    if (texts.length > 0) return texts.join("\n")
  }
  if (item.failure?.message) return item.failure.message
  return ""
}

export function extractAgentExecutionEntries(
  items: readonly AgentTimelineToolItemLike[],
): AgentExecutionEntry[] {
  const entries: AgentExecutionEntry[] = []
  for (const item of items) {
    if (!item || item.kind !== "tool") continue
    const toolName = (item.sourceToolName || item.identity?.sourceToolName || item.title || "tool").trim()
    const semanticKey = (item.identity?.semanticKey || "tool.call").trim()
    const isShell = semanticKey === "shell.execute" || SHELL_TOOL_RE.test(toolName)
    const inputObj = parseInputRecord(item)

    let command: string | undefined
    if (isShell) {
      const rawCmd =
        (typeof inputObj?.command === "string" && inputObj.command.trim()) ||
        (typeof inputObj?.cmd === "string" && inputObj.cmd.trim()) ||
        (!inputObj && typeof item.input === "string" && item.input.trim()) ||
        ""
      if (rawCmd) command = rawCmd
    }

    const cwd =
      (typeof inputObj?.cwd === "string" && inputObj.cwd.trim()) ||
      (typeof inputObj?.workdir === "string" && inputObj.workdir.trim()) ||
      undefined

    const rawExit = inputObj?.exitCode
    const exitCode = typeof rawExit === "number" && Number.isSafeInteger(rawExit) ? rawExit : undefined

    const summary =
      (typeof inputObj?.path === "string" && inputObj.path.trim()) ||
      (typeof item.detail === "string" && item.detail.trim()) ||
      (item.title && item.title.trim() !== toolName ? item.title.trim() : undefined)

    entries.push({
      id: item.id,
      index: entries.length + 1,
      turnId: item.execution?.turnId,
      toolName,
      semanticKey,
      isShell,
      ...(command ? { command } : {}),
      ...(summary ? { summary } : {}),
      ...(cwd ? { cwd } : {}),
      ...(exitCode !== undefined ? { exitCode } : {}),
      ...(readDurationMs(item.timing) !== undefined ? { durationMs: readDurationMs(item.timing) } : {}),
      status: normalizeStatus(item.status),
      output: readOutput(item),
    })
  }
  return entries
}

export function formatExecutionDuration(durationMs?: number): string {
  if (durationMs === undefined || !Number.isFinite(durationMs) || durationMs < 0) return ""
  if (durationMs < 1000) return `${Math.round(durationMs)}ms`
  const secs = durationMs / 1000
  if (secs < 10) return `${(Math.round(durationMs / 100) / 10).toFixed(1)}s`
  const whole = Math.round(secs)
  if (whole < 60) return `${whole}s`
  const mins = Math.floor(whole / 60)
  return `${mins}m${whole % 60}s`
}
