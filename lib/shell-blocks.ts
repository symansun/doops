// AI 助手回复里的 shell 代码块只作为“建议命令”交给用户；执行一律由用户确认后
// 走终端页同一条 doops_shell 路径。这里只做纯文本解析和参数构造，不拼接凭据。

const SHELL_LANGUAGES = new Set(["bash", "sh", "shell", "zsh", "console"])
const FENCE = /^ {0,3}(`{3,}|~{3,})[ \t]*([^\s`]*)[^\n]*$/

export interface ShellBlock {
  language: string
  command: string
}

export interface AssistantMessageLike {
  kind: string
  role?: string
  text?: string
  status?: string
}

// extractShellBlocks 返回 markdown 中 ```bash/```sh/```shell 代码块的内容，
// 未闭合的代码块（流式输出尚未结束）不返回，避免执行半截命令。
export function extractShellBlocks(markdown: string): ShellBlock[] {
  const blocks: ShellBlock[] = []
  const lines = markdown.replace(/\r\n?/g, "\n").split("\n")
  let open: { fence: string; language: string; body: string[] } | null = null
  for (const line of lines) {
    if (!open) {
      const match = FENCE.exec(line)
      if (match) open = { fence: match[1], language: match[2].toLowerCase(), body: [] }
      continue
    }
    const trimmed = line.trim()
    if (trimmed.startsWith(open.fence[0].repeat(open.fence.length)) && /^[`~]+$/.test(trimmed)) {
      if (SHELL_LANGUAGES.has(open.language)) {
        const command = normalizeShellCommand(open.body.join("\n"), open.language)
        if (command) blocks.push({ language: open.language, command })
      }
      open = null
      continue
    }
    open.body.push(line)
  }
  return blocks
}

// console 代码块常带 “$ ” 提示符和输出，只保留提示符后的命令行。
function normalizeShellCommand(body: string, language: string): string {
  if (language === "console") {
    return body
      .split("\n")
      .filter((line) => /^\s*\$ /.test(line))
      .map((line) => line.replace(/^\s*\$ /, ""))
      .join("\n")
      .trim()
  }
  return body.trim()
}

// latestAssistantShellBlocks 取最近一条已完成助手消息里的 shell 代码块，去重。
export function latestAssistantShellBlocks(items: readonly AssistantMessageLike[]): ShellBlock[] {
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i]
    if (item.kind !== "message" || item.role !== "assistant") continue
    if (item.status !== "completed") return []
    const seen = new Set<string>()
    return extractShellBlocks(item.text ?? "").filter((block) => {
      if (seen.has(block.command)) return false
      seen.add(block.command)
      return true
    })
  }
  return []
}

// shellToolArguments 构造 doops_shell 参数：只有会话和用户确认过的原始命令。
export function shellToolArguments(sessionId: string, command: string): { session_id: string; command: string } {
  const trimmed = command.trim()
  if (!sessionId) throw new Error("缺少会话")
  if (!trimmed) throw new Error("命令为空")
  return { session_id: sessionId, command: trimmed }
}

// TerminalPrefill 是“在终端执行”跨标签传递的命令；nonce 让同一条命令可以重复预填。
export interface TerminalPrefill {
  command: string
  nonce: number
}

export function nextTerminalPrefill(previous: TerminalPrefill | null, command: string): TerminalPrefill | null {
  const trimmed = command.trim()
  if (!trimmed) return previous
  return { command: trimmed, nonce: (previous?.nonce ?? 0) + 1 }
}

// prefillToApply 判断终端是否要把预填命令写入输入框：每个 nonce 只写一次，不自动执行。
export function prefillToApply(prefill: TerminalPrefill | null, appliedNonce: number): string | null {
  if (!prefill || prefill.nonce <= appliedNonce) return null
  return prefill.command
}
