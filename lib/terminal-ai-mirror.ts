import {
  formatExecutionDuration,
  type AgentExecutionEntry,
} from "./agent-execution-stream.ts"

export const AI_MIRROR_DEFAULT_MAX_LINES = 40
export const AI_MIRROR_DEFAULT_MAX_CHARS = 4096

export interface AiMirrorFlushGuard {
  enabled: boolean
  status: string
  initialPromptReady: boolean
  inAlternateScreen: boolean
  atPrompt: boolean
  currentInput: string
  optimisticInput: string
}

export interface AiMirrorPromptSnapshot {
  text: string
  lines: number
  seenOsc133: boolean
}

export interface AiMirrorSelection {
  readyToMirror: AgentExecutionEntry[]
  activeRunning: AgentExecutionEntry | null
  shellTotal: number
}

// 仅允许安全 SGR 颜色序列进入 xterm 镜像区，剥离 OSC/DCS/清屏/光标移动等控制序列，
// 防止 AI 命令输出里的特殊转义字符篡改终端标题、剪贴板、备用屏或 OSC 133 块锚点。
const OSC_RE = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g
const DCS_APC_RE = /\x1b[PX^_][\s\S]*?\x1b\\/g
const UNSAFE_CSI_RE = /\x1b\[[0-?]*[ -/]*([@-ln-z~])/g
const TWO_BYTE_ESC_RE = /\x1b[@-Z\\-_]/g
const UNSAFE_CTRL_RE = /[\x00-\x08\x0b\x0c\x0e-\x1a\x1c-\x1f\x7f]/g

export function stripUnsafeTerminalEscapes(raw: string): string {
  if (!raw) return ""
  return raw
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n")
    .replace(OSC_RE, "")
    .replace(DCS_APC_RE, "")
    .replace(UNSAFE_CSI_RE, "")
    .replace(TWO_BYTE_ESC_RE, "")
    .replace(UNSAFE_CTRL_RE, "")
}

export function sanitizeAiMirrorOutput(
  raw: string,
  opts: { maxLines?: number; maxChars?: number } = {},
): { text: string; truncated: boolean; omittedLines: number } {
  const maxLines = Math.max(1, opts.maxLines ?? AI_MIRROR_DEFAULT_MAX_LINES)
  const maxChars = Math.max(128, opts.maxChars ?? AI_MIRROR_DEFAULT_MAX_CHARS)
  const cleaned = stripUnsafeTerminalEscapes(raw).replace(/\n+$/, "")
  if (!cleaned) return { text: "", truncated: false, omittedLines: 0 }

  const lines = cleaned.split("\n")
  let truncated = false
  let omittedLines = 0
  let kept = lines
  if (lines.length > maxLines) {
    kept = lines.slice(0, maxLines)
    omittedLines = lines.length - maxLines
    truncated = true
  }

  let joined = kept.join("\n")
  if (joined.length > maxChars) {
    joined = joined.slice(0, maxChars).replace(/\x1b\[[0-9;]*$/, "")
    truncated = true
  }

  return { text: joined, truncated, omittedLines }
}

export function isMirrorableAiEntry(entry: AgentExecutionEntry): boolean {
  if (!entry || !entry.isShell) return false
  if (!entry.command || !entry.command.trim()) return false
  return entry.status !== "running" && entry.status !== "pending" && entry.status !== "waiting"
}

export function selectPendingAiMirrorEntries(
  entries: readonly AgentExecutionEntry[],
  mirroredIds: ReadonlySet<string>,
): AiMirrorSelection {
  const readyToMirror: AgentExecutionEntry[] = []
  let activeRunning: AgentExecutionEntry | null = null
  let shellTotal = 0

  for (const entry of entries) {
    if (!entry) continue
    if (entry.isShell && entry.command?.trim()) {
      shellTotal += 1
    }
    if (entry.status === "running" || entry.status === "pending" || entry.status === "waiting") {
      // 优先展示正在执行的 Shell 命令；没有 Shell 运行中时也提示其他工具活动。
      if (!activeRunning || entry.isShell || !activeRunning.isShell) {
        activeRunning = entry
      }
      continue
    }
    if (isMirrorableAiEntry(entry) && !mirroredIds.has(entry.id)) {
      readyToMirror.push(entry)
    }
  }

  return { readyToMirror, activeRunning, shellTotal }
}

export function canFlushAiMirrorToPty(guard: AiMirrorFlushGuard): boolean {
  if (!guard.enabled) return false
  if (guard.status !== "ready") return false
  if (!guard.initialPromptReady) return false
  if (guard.inAlternateScreen) return false
  if (!guard.atPrompt) return false
  if (guard.currentInput.trim().length > 0) return false
  if (guard.optimisticInput.trim().length > 0) return false
  return true
}

export function formatAiMirrorBlock(
  entry: AgentExecutionEntry,
  opts: { maxLines?: number; maxChars?: number } = {},
): string {
  const safeCwd = entry.cwd ? stripUnsafeTerminalEscapes(entry.cwd).replace(/\n/g, " ").trim() : ""
  const safeCmd = stripUnsafeTerminalEscapes(entry.command || "").trim()
  const cmdLines = safeCmd ? safeCmd.split("\n") : [""]
  const out = sanitizeAiMirrorOutput(entry.output || "", opts)

  const failed = entry.status === "failed" || (entry.exitCode !== undefined && entry.exitCode !== 0)
  const ambiguous = entry.status === "cancelled" || entry.status === "unconfirmed" || entry.status === "unknown"
  const duration = formatExecutionDuration(entry.durationMs)
  const durSuffix = duration ? ` · ${duration}` : ""

  const lines: string[] = []
  const headerCwd = safeCwd ? ` \x1b[2m${safeCwd}\x1b[0m` : ""
  lines.push(`\x1b[38;5;75m╭─ AI · ${stripUnsafeTerminalEscapes(entry.toolName || "Bash")}\x1b[0m${headerCwd}`)

  cmdLines.forEach((line, idx) => {
    if (idx === 0) {
      lines.push(`\x1b[32m$\x1b[0m \x1b[1m${line}\x1b[0m`)
    } else {
      lines.push(`  \x1b[1m${line}\x1b[0m`)
    }
  })

  if (out.text) {
    for (const outLine of out.text.split("\n")) {
      // 重置每行末尾 SGR，避免命令自身未闭合颜色污染后续边框与提示符。
      lines.push(`${outLine}\x1b[0m`)
    }
  }

  if (out.truncated) {
    const omitText = out.omittedLines > 0 ? `已省略 ${out.omittedLines} 行` : "输出过长已截断"
    lines.push(`\x1b[2m… ${omitText}，完整输出见「执行过程」\x1b[0m`)
  }

  if (failed) {
    const codeText = entry.exitCode !== undefined ? `exit ${entry.exitCode}` : "执行失败"
    lines.push(`\x1b[31m╰─ ✗ ${codeText}${durSuffix}\x1b[0m`)
  } else if (ambiguous) {
    const statusText = entry.status === "cancelled" ? "已取消" : "结果未确认"
    lines.push(`\x1b[33m╰─ ! ${statusText}${durSuffix}\x1b[0m`)
  } else {
    const codeText = entry.exitCode !== undefined ? `exit ${entry.exitCode}` : "完成"
    lines.push(`\x1b[32m╰─ ✓ ${codeText}${durSuffix}\x1b[0m`)
  }

  return `${lines.join("\r\n")}\r\n`
}

export function buildAiMirrorWritePayload(
  blocks: readonly string[],
  prompt: AiMirrorPromptSnapshot,
): string {
  if (blocks.length === 0) return ""
  // 清除当前处于空闲态的本地提示符行（仅影响 xterm 画面，不向远端 PTY 发送字节）。
  const clearLines = Math.max(1, prompt.lines || 1)
  let clearPrefix = "\r\x1b[2K"
  for (let i = 1; i < clearLines; i++) {
    clearPrefix += "\x1b[1A\r\x1b[2K"
  }

  const body = blocks.join("")
  let promptRestore = ""
  if (prompt.text) {
    promptRestore = prompt.seenOsc133
      ? `\x1b]133;A\x07${prompt.text}\x1b]133;B\x07`
      : prompt.text
  }
  return `${clearPrefix}${body}${promptRestore}`
}
