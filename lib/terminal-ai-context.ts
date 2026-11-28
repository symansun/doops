import {
  clipTerminalTextForAi,
  MAX_TERMINAL_AI_CHARS,
  type TerminalModeKind,
} from "./terminal-ai.ts"

export interface TerminalContextBlockInput {
  id: string
  command: string
  output: string
  exitCode?: number
  durationMs?: number
  cwd?: string
}

export type TerminalFocusAction = "explain" | "fix" | "followup"

export interface TerminalInlineContextInput {
  target: { cluster: string; instance: string }
  cwd?: string
  terminalMode?: TerminalModeKind
  mode?: "single_shot" | "assistant"
  question: string
  blocks: readonly TerminalContextBlockInput[]
  focusBlock?: TerminalContextBlockInput
  focusAction?: TerminalFocusAction
  maxChars?: number
  historyBudgetChars?: number
  maxHistoryBlocks?: number
}

export interface TerminalInlineContextResult {
  prompt: string
  includedBlockIds: string[]
  charCount: number
  summaryLabel: string
}

function formatDuration(ms?: number): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return ""
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function formatBlockEntry(
  block: TerminalContextBlockInput,
  indexLabel: string,
  outputBudget: number,
): string {
  const cmdText = block.command.trim()
  const clippedOut = block.output.trim()
    ? clipTerminalTextForAi(block.output, Math.max(128, outputBudget))
    : { text: "", truncated: false, omittedChars: 0 }
  const meta = [
    block.exitCode !== undefined ? `exit=${block.exitCode}` : "",
    formatDuration(block.durationMs),
    block.cwd ? `cwd=${block.cwd}` : "",
  ].filter(Boolean).join(" ")

  const header = `  ${indexLabel} $ ${cmdText}${meta ? `  (${meta})` : ""}`
  if (!clippedOut.text) {
    return header
  }
  const indented = clippedOut.text
    .split("\n")
    .map((line) => `     ${line}`)
    .join("\n")
  return `${header}\n${indented}`
}

export function buildTerminalInlineContext(input: TerminalInlineContextInput): TerminalInlineContextResult {
  const totalCap = Math.min(MAX_TERMINAL_AI_CHARS, Math.max(1024, input.maxChars ?? MAX_TERMINAL_AI_CHARS))
  const historyBudget = Math.min(totalCap - 1024, Math.max(512, input.historyBudgetChars ?? 24_000))
  const maxHistory = input.focusBlock
    ? Math.max(0, input.maxHistoryBlocks ?? 1)
    : Math.max(0, input.maxHistoryBlocks ?? 3)

  const questionText = input.question.trim()

  const scopeParts = [
    `${input.target.cluster}/${input.target.instance}`,
    input.terminalMode === "pty" ? "交互终端" : input.terminalMode === "command" ? "命令终端" : "",
    input.cwd?.trim() ? `cwd=${input.cwd.trim()}` : "",
  ].filter(Boolean)

  const includedBlockIds: string[] = []
  let focusSection = ""
  let usedBudget = 0

  if (input.focusBlock) {
    const focusBudget = Math.floor(historyBudget * 0.65)
    const entryText = formatBlockEntry(input.focusBlock, "[聚焦]", focusBudget)
    includedBlockIds.push(input.focusBlock.id)
    usedBudget += entryText.length
    const actionLabel =
      input.focusAction === "fix" ? "请定位失败原因并给出可执行的修复命令"
      : input.focusAction === "explain" ? "请解释该命令与输出的关键信息"
      : "请结合该命令上下文回答"
    focusSection = `[聚焦的命令块（${actionLabel}）]\n${entryText}\n`
  }

  // 选取最近的非聚焦历史块（新→旧），按剩余预算填充。
  const candidates = input.blocks
    .filter((b) => b.command.trim() && b.id !== input.focusBlock?.id)
    .slice(-maxHistory)
    .reverse()

  const historyEntries: string[] = []
  let remaining = Math.max(0, historyBudget - usedBudget)
  for (let i = 0; i < candidates.length; i++) {
    if (remaining < 160) break
    const perBlockBudget = Math.max(256, Math.floor(remaining / (candidates.length - i)))
    const entryText = formatBlockEntry(candidates[i], `#${i + 1}`, perBlockBudget)
    if (entryText.length > remaining && historyEntries.length > 0) break
    includedBlockIds.push(candidates[i].id)
    historyEntries.push(entryText)
    remaining -= entryText.length
  }

  const historySection = historyEntries.length > 0
    ? `最近命令（新→旧，共 ${historyEntries.length} 条）：\n${historyEntries.join("\n")}\n`
    : ""

  const footer =
    input.mode === "single_shot"
      ? ""
      : "[回答要求]\n先用只读工具核实，再给结论。需要执行的命令放进 ```bash 代码块，每块一条命令，代码块前面用一句话说明用途与影响。不要假设已经执行。"

  const rawPrompt = [
    "[终端上下文]",
    `目标：${scopeParts.join(" · ")}`,
    historySection.trimEnd(),
    focusSection.trimEnd(),
    input.mode === "single_shot" ? "" : `[用户问题]\n${questionText}`,
    footer,
  ].filter(Boolean).join("\n")

  const finalClipped = clipTerminalTextForAi(rawPrompt, totalCap)
  const charCount = finalClipped.text.length
  const kLabel = charCount >= 1000 ? `${(charCount / 1000).toFixed(1)}k` : `${charCount}`
  const summaryParts = [
    `附带 ${includedBlockIds.length} 条命令`,
    `${kLabel} 字符`,
  ]

  return {
    prompt: finalClipped.text,
    includedBlockIds,
    charCount,
    summaryLabel: summaryParts.join(" · "),
  }
}

export function buildTerminalHandbackPrompt(input: {
  target: { cluster: string; instance: string }
  cwd?: string
  blocks: readonly TerminalContextBlockInput[]
  note?: string
}): TerminalInlineContextResult {
  const question = input.note?.trim()
    ? input.note.trim()
    : "我已在右侧交互终端完成了上述操作，请结合最新命令输出继续分析并给出下一步建议。"
  return buildTerminalInlineContext({
    target: input.target,
    cwd: input.cwd,
    terminalMode: "pty",
    mode: "assistant",
    question,
    blocks: input.blocks,
    maxHistoryBlocks: 3,
  })
}
