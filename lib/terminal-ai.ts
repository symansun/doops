import { agentTurnRunning, type AgentBusySnapshot } from "./agent-busy-banner.ts"

// 对话接口单次上限为 64 KiB（见 lib/server/agent-session.ts）；留出提示词外框与多字节裕量，
// 超长终端滚屏保留首尾关键输出并显式标注省略字符数，绝不让右键发送直接撞 400。
export const MAX_TERMINAL_AI_CHARS = 48_000

const ANSI_CSI = /\u001b\[[0-9;?]*[ -/]*[@-~]/g
const ANSI_OSC = /\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)/g

export type TerminalModeKind = "pty" | "command"
export type TerminalAiActionMode = "execute" | "prefill"

export interface TerminalSelectionSummary {
  hasSelection: boolean
  lines: number
  chars: number
  label: string
}

export interface ClippedTerminalText {
  text: string
  truncated: boolean
  omittedChars: number
}

export interface TerminalComposerPrefillPayload {
  id: string
  sessionId: string
  text: string
  mode: "append"
}

export interface TerminalAiRuntimeLike {
  getSnapshot(sessionId: string): AgentBusySnapshot & {
    connection?: string
    error?: string | null
    capabilities?: { sendMessage?: boolean }
  }
  subscribe?: (sessionId: string, listener: () => void) => () => void
  open(sessionId: string): Promise<void>
  sendMessage(sessionId: string, commandId: string, input: { text: string }): Promise<void>
}

export interface TerminalAiRequest {
  id: string
  mode: TerminalAiActionMode
  selection: string
  cwd?: string
  terminalMode?: TerminalModeKind
}

export type TerminalAiDispatchResult =
  | { outcome: "sent"; prompt: string }
  | { outcome: "prefilled"; text: string }
  | { outcome: "prefilled_busy"; text: string; notice: string }
  | { outcome: "prefilled_on_error"; text: string; error: string }

function sliceSafeCodeUnits(text: string, start: number, end: number): string {
  let s = Math.max(0, Math.min(text.length, start))
  let e = Math.max(s, Math.min(text.length, end))
  // 避免在 UTF-16 代理对中间截断产生孤立代理项，否则服务端致命 UTF-8 解码会报错。
  if (s > 0 && s < text.length) {
    const code = text.charCodeAt(s)
    if (code >= 0xdc00 && code <= 0xdfff) s += 1
  }
  if (e > s && e < text.length) {
    const prev = text.charCodeAt(e - 1)
    if (prev >= 0xd800 && prev <= 0xdbff) e -= 1
  }
  return text.slice(s, e)
}

export function normalizeTerminalCopyText(raw: string): string {
  return raw.replace(/\u0000/g, "").replace(/\r\n/g, "\n")
}

export function stripTerminalControlChars(raw: string): string {
  return raw
    .replace(/\u0000/g, "")
    .replace(ANSI_OSC, "")
    .replace(ANSI_CSI, "")
    .replace(/\r\n?/g, "\n")
}

export function hasTerminalSelection(raw: string): boolean {
  return stripTerminalControlChars(raw).trim().length > 0
}

export function summarizeTerminalSelection(raw: string): TerminalSelectionSummary {
  const cleaned = stripTerminalControlChars(raw)
  if (!cleaned.trim()) {
    return {
      hasSelection: false,
      lines: 0,
      chars: 0,
      label: "未选中文本（拖选终端内容后可复制或发送到 AI）",
    }
  }
  const withoutTrailingNewline = cleaned.endsWith("\n") ? cleaned.slice(0, -1) : cleaned
  const lines = withoutTrailingNewline.split("\n").length
  const chars = cleaned.length
  return {
    hasSelection: true,
    lines,
    chars,
    label: `已选中 ${lines} 行 · ${chars} 字符`,
  }
}

export function clipTerminalTextForAi(raw: string, maxChars = MAX_TERMINAL_AI_CHARS): ClippedTerminalText {
  const cleaned = stripTerminalControlChars(raw).replace(/^\n+|\n+$/g, "")
  if (cleaned.length <= maxChars) {
    return { text: cleaned, truncated: false, omittedChars: 0 }
  }
  // 保留前 40%（通常是执行的命令与起始输出）与后 60%（通常是最终报错栈或退出状态）。
  const reserve = 64
  const budget = Math.max(64, maxChars - reserve)
  const headBudget = Math.max(16, Math.floor(budget * 0.4))
  const tailBudget = Math.max(16, budget - headBudget)
  const head = sliceSafeCodeUnits(cleaned, 0, headBudget)
  const tail = sliceSafeCodeUnits(cleaned, cleaned.length - tailBudget, cleaned.length)
  const omittedChars = Math.max(1, cleaned.length - head.length - tail.length)
  const marker = `\n...[中间省略 ${omittedChars} 字符，保留首尾关键输出]...\n`
  return {
    text: head + marker + tail,
    truncated: true,
    omittedChars,
  }
}

export function fencedTerminalBlock(text: string, language = "console"): string {
  let maxBackticks = 0
  for (const match of text.matchAll(/`+/g)) {
    if (match[0].length > maxBackticks) maxBackticks = match[0].length
  }
  const fence = "`".repeat(Math.max(3, maxBackticks + 1))
  return `${fence}${language}\n${text}\n${fence}`
}

export function buildTerminalAiPrompt(input: {
  selection: string
  target: { cluster: string; instance: string }
  cwd?: string
  terminalMode?: TerminalModeKind
  maxChars?: number
}): string {
  if (!hasTerminalSelection(input.selection)) {
    throw new Error("请先在终端选中要发送的内容")
  }
  const clipped = clipTerminalTextForAi(input.selection, input.maxChars)
  const scope = [
    `${input.target.cluster}/${input.target.instance}`,
    input.terminalMode === "pty" ? "交互终端" : input.terminalMode === "command" ? "命令终端" : "",
    input.cwd?.trim() ? `目录 ${input.cwd.trim()}` : "",
  ].filter(Boolean).join(" · ")
  const header = `以下是当前目标终端（${scope}）选中的内容，请结合当前目标环境直接分析处理（如需排查请调用只读工具核实；如涉及变更操作请给出可执行命令与说明）：`
  const note = clipped.truncated ? `\n（注：原始选中输出较长，已自动保留首尾并省略中间 ${clipped.omittedChars} 字符。）` : ""
  return `${header}\n${fencedTerminalBlock(clipped.text, "console")}${note}`
}

export function buildTerminalComposerPrefill(selection: string, maxChars = MAX_TERMINAL_AI_CHARS): string {
  if (!hasTerminalSelection(selection)) {
    throw new Error("请先在终端选中要发送的内容")
  }
  const clipped = clipTerminalTextForAi(selection, maxChars)
  return `${fencedTerminalBlock(clipped.text, "console")}\n\n`
}

export function clampContextMenuPosition(
  point: { x: number; y: number },
  menu: { width: number; height: number },
  viewport: { width: number; height: number },
  gap = 8,
): { left: number; top: number } {
  const maxLeft = Math.max(gap, viewport.width - menu.width - gap)
  const maxTop = Math.max(gap, viewport.height - menu.height - gap)
  const preferredLeft = point.x + menu.width + gap <= viewport.width ? point.x : point.x - menu.width
  const preferredTop = point.y + menu.height + gap <= viewport.height ? point.y : point.y - menu.height
  return {
    left: Math.max(gap, Math.min(preferredLeft, maxLeft)),
    top: Math.max(gap, Math.min(preferredTop, maxTop)),
  }
}

function isSessionReadyToSend(snapshot: ReturnType<TerminalAiRuntimeLike["getSnapshot"]>): boolean {
  if (snapshot.connection !== "connected") return false
  if (snapshot.capabilities && snapshot.capabilities.sendMessage === false) return false
  return true
}

async function ensureTerminalAiSessionReady(
  runtime: TerminalAiRuntimeLike,
  sessionId: string,
  timeoutMs = 5_000,
): Promise<void> {
  if (isSessionReadyToSend(runtime.getSnapshot(sessionId))) return
  // AgentWorkspace 首次挂载可能已将状态置为 connecting，且 open() resolve 后快照常在下一帧才切到 connected；
  // 统一调用幂等的 open() 并等待快照与 sendMessage 能力就绪，避免首次右键点击报 agent_workbench_session_not_ready。
  await runtime.open(sessionId)
  if (isSessionReadyToSend(runtime.getSnapshot(sessionId))) return

  await new Promise<void>((resolve, reject) => {
    let settled = false
    let unsubscribe: (() => void) | undefined
    let pollTimer: ReturnType<typeof setInterval> | undefined
    const timeout = setTimeout(() => {
      finish(() => reject(new Error(runtime.getSnapshot(sessionId).error || "AI 会话连接超时，请稍后重试")))
    }, timeoutMs)

    const finish = (done: () => void) => {
      if (settled) return
      settled = true
      clearTimeout(timeout)
      if (pollTimer) clearInterval(pollTimer)
      unsubscribe?.()
      done()
    }

    const check = () => {
      const snap = runtime.getSnapshot(sessionId)
      if (isSessionReadyToSend(snap)) {
        finish(resolve)
        return
      }
      if (snap.connection === "disconnected" && snap.error) {
        finish(() => reject(new Error(snap.error!)))
      }
    }

    if (runtime.subscribe) {
      unsubscribe = runtime.subscribe(sessionId, check)
    }
    pollTimer = setInterval(check, 20)
    check()
  })
}

export interface TerminalPromptDispatchRequest {
  id: string
  prompt: string
  fallbackPrefill: string
}

export async function dispatchTerminalPromptToAi(options: {
  runtime: TerminalAiRuntimeLike
  sessionId: string
  request: TerminalPromptDispatchRequest
  onPrefill: (payload: TerminalComposerPrefillPayload) => void
  readyTimeoutMs?: number
  createCommandId?: () => string
}): Promise<TerminalAiDispatchResult> {
  const { runtime, sessionId, request, onPrefill } = options
  const applyPrefill = () => {
    onPrefill({
      id: request.id,
      sessionId,
      text: request.fallbackPrefill,
      mode: "append",
    })
  }

  try {
    await ensureTerminalAiSessionReady(runtime, sessionId, options.readyTimeoutMs)
  } catch (error) {
    applyPrefill()
    const message = runtime.getSnapshot(sessionId).error || (error instanceof Error ? error.message : String(error))
    return { outcome: "prefilled_on_error", text: request.fallbackPrefill, error: message }
  }

  const live = runtime.getSnapshot(sessionId)
  if (agentTurnRunning(live)) {
    applyPrefill()
    return {
      outcome: "prefilled_busy",
      text: request.fallbackPrefill,
      notice: "当前 AI 仍在执行上一轮任务，已将提问内容填入输入框，待结束后可直接发送",
    }
  }

  const commandId = options.createCommandId ? options.createCommandId() : crypto.randomUUID()
  try {
    await runtime.sendMessage(sessionId, commandId, { text: request.prompt })
    return { outcome: "sent", prompt: request.prompt }
  } catch (error) {
    applyPrefill()
    const message = error instanceof Error ? error.message : String(error)
    return { outcome: "prefilled_on_error", text: request.fallbackPrefill, error: message }
  }
}

export async function dispatchTerminalAiRequest(options: {
  runtime: TerminalAiRuntimeLike
  sessionId: string
  target: { cluster: string; instance: string }
  request: TerminalAiRequest
  onPrefill: (payload: TerminalComposerPrefillPayload) => void
  readyTimeoutMs?: number
  createCommandId?: () => string
}): Promise<TerminalAiDispatchResult> {
  const { runtime, sessionId, target, request, onPrefill } = options
  const prefillText = buildTerminalComposerPrefill(request.selection)
  const applyPrefill = () => {
    onPrefill({
      id: request.id,
      sessionId,
      text: prefillText,
      mode: "append",
    })
  }

  if (request.mode === "prefill") {
    applyPrefill()
    if (runtime.getSnapshot(sessionId).connection === "disconnected") {
      void runtime.open(sessionId).catch(() => {
        // 连接异常会由 runtime 快照在面板内呈现。
      })
    }
    return { outcome: "prefilled", text: prefillText }
  }

  const prompt = buildTerminalAiPrompt({
    selection: request.selection,
    target,
    cwd: request.cwd,
    terminalMode: request.terminalMode,
  })

  try {
    await ensureTerminalAiSessionReady(runtime, sessionId, options.readyTimeoutMs)
  } catch (error) {
    applyPrefill()
    const message = runtime.getSnapshot(sessionId).error || (error instanceof Error ? error.message : String(error))
    return { outcome: "prefilled_on_error", text: prefillText, error: message }
  }

  const live = runtime.getSnapshot(sessionId)
  if (agentTurnRunning(live)) {
    applyPrefill()
    return {
      outcome: "prefilled_busy",
      text: prefillText,
      notice: "当前 AI 仍在执行上一轮任务，已将终端选中内容填入输入框，待结束后可直接发送",
    }
  }

  const commandId = options.createCommandId ? options.createCommandId() : crypto.randomUUID()
  try {
    await runtime.sendMessage(sessionId, commandId, { text: prompt })
    return { outcome: "sent", prompt }
  } catch (error) {
    applyPrefill()
    const message = error instanceof Error ? error.message : String(error)
    return { outcome: "prefilled_on_error", text: prefillText, error: message }
  }
}
