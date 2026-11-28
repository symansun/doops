export type TerminalIntentKind = "shell" | "ai"
export type TerminalIntentTrigger = "prefix_hash" | "prefix_question" | "prefix_agent" | "auto" | "shell"
export type TerminalAiDispatchMode = "suggest" | "assistant"

export interface TerminalIntentResult {
  kind: TerminalIntentKind
  trigger: TerminalIntentTrigger
  targetMode?: TerminalAiDispatchMode
  question?: string
  shellScore: number
  aiScore: number
}

const KNOWN_SHELL_COMMANDS = new Set([
  "kubectl", "k", "helm", "docker", "podman", "nerdctl", "crictl", "buildctl",
  "git", "gh", "curl", "wget", "http", "ssh", "scp", "sftp", "rsync",
  "ls", "ll", "la", "cd", "pwd", "cat", "tac", "less", "more", "head", "tail",
  "grep", "egrep", "fgrep", "rg", "ag", "find", "fd", "locate", "which", "whereis", "type",
  "awk", "sed", "cut", "tr", "sort", "uniq", "wc", "xargs", "tee", "jq", "yq",
  "ps", "top", "htop", "pgrep", "pkill", "kill", "killall", "nohup", "timeout", "watch",
  "df", "du", "free", "vmstat", "iostat", "uptime", "uname", "hostname", "hostnamectl",
  "whoami", "id", "who", "w", "last", "date", "cal", "env", "printenv", "export", "unset", "set", "alias",
  "ip", "ifconfig", "ss", "netstat", "lsof", "ping", "traceroute", "mtr", "dig", "nslookup", "nc", "telnet", "nmap",
  "systemctl", "journalctl", "service", "chkconfig", "crontab", "at",
  "nginx", "caddy", "haproxy", "apache2", "httpd", "envoy",
  "psql", "mysql", "mariadb", "sqlite3", "redis-cli", "mongosh", "mongo", "clickhouse-client", "etcdctl",
  "mkdir", "rmdir", "rm", "cp", "mv", "ln", "touch", "chmod", "chown", "chgrp", "stat", "file", "diff", "patch",
  "tar", "gzip", "gunzip", "zip", "unzip", "xz", "bzip2", "dd", "mount", "umount", "lsblk", "fdisk",
  "echo", "printf", "read", "test", "true", "false", "sleep", "clear", "reset", "history", "exit", "logout",
  "vi", "vim", "nvim", "nano", "emacs", "tmux", "screen",
  "sudo", "su", "doas", "bash", "sh", "zsh", "fish", "dash", "source", ".",
  "python", "python3", "pip", "pip3", "uv", "node", "npm", "npx", "pnpm", "yarn", "bun", "deno",
  "go", "gofmt", "cargo", "rustc", "java", "javac", "mvn", "gradle", "gcc", "g++", "make", "cmake",
  "apt", "apt-get", "yum", "dnf", "apk", "brew", "dpkg", "rpm",
  "openssl", "base64", "sha256sum", "md5sum", "doops", "doops-agent", "doops-gateway",
])

const SHELL_SYNTAX_RE = /(\|\||&&|[|><]|`|\$\(|(^|\s)--?[A-Za-z][A-Za-z0-9_-]*|^[A-Za-z_][A-Za-z0-9_]*=\S)/
const CJK_RE = /[\u4e00-\u9fff]/g
const AI_LEAD_ZH_RE = /^(?:我想|我要|想要|能否|能不能|可以|麻烦|查看|查一下|查下|查一查|查询|看看|看下|帮我|帮忙|请问|请帮|请|为什么|为啥|怎么|如何|检查|排查|分析|解释|修复|诊断|列出|找出|告诉我|总结|对比|统计|监控|清理|释放|搜索|定位)/
const AI_LEAD_EN_RE = /^(?:how\s+(?:do|to|can|is|are)|why\s+(?:is|are|did|does|won't|can't)|what\s+(?:is|are|caused|happened)|show\s+me|explain\s+|fix\s+|check\s+why|help\s+me|can\s+you|please\s+)/i

const AGENT_PREFIX_RE = /^(?:@ai(?:\s+|(?=[\u4e00-\u9fff]))|##\s*)(.+)$/i

export function classifyTerminalInput(
  raw: string,
  options: {
    autoDetect?: boolean
    defaultAiMode?: TerminalAiDispatchMode
    extraCommands?: ReadonlySet<string>
  } = {},
): TerminalIntentResult {
  const trimmed = raw.trim()
  if (!trimmed) {
    return { kind: "shell", trigger: "shell", shellScore: 0, aiScore: 0 }
  }

  // 1. 确定性前缀：@ai / ## 固定走模式 2（直接拍给 AI 助手执行）；# / ? / ？ 固定走模式 1（AI 提示命令）。
  if (trimmed.startsWith("##")) {
    const question = trimmed.slice(2).trim()
    if (!question) return { kind: "shell", trigger: "shell", shellScore: 0, aiScore: 0 }
    return { kind: "ai", trigger: "prefix_agent", targetMode: "assistant", question, shellScore: 0, aiScore: 10 }
  }
  const agentMatch = AGENT_PREFIX_RE.exec(trimmed)
  if (agentMatch) {
    const question = agentMatch[1].trim()
    if (!question) return { kind: "shell", trigger: "shell", shellScore: 0, aiScore: 0 }
    return { kind: "ai", trigger: "prefix_agent", targetMode: "assistant", question, shellScore: 0, aiScore: 10 }
  }
  if (trimmed.startsWith("#")) {
    const question = trimmed.slice(1).trim()
    if (!question) return { kind: "shell", trigger: "shell", shellScore: 0, aiScore: 0 }
    return { kind: "ai", trigger: "prefix_hash", targetMode: "suggest", question, shellScore: 0, aiScore: 10 }
  }
  if (trimmed.startsWith("?") || trimmed.startsWith("？")) {
    const question = trimmed.slice(1).trim()
    if (!question) return { kind: "shell", trigger: "shell", shellScore: 0, aiScore: 0 }
    return { kind: "ai", trigger: "prefix_question", targetMode: "suggest", question, shellScore: 0, aiScore: 10 }
  }

  // 2. 自动识别（终端默认开启，可在行内 AI 面板关闭）
  if (!options.autoDetect) {
    return { kind: "shell", trigger: "shell", shellScore: 0, aiScore: 0 }
  }

  const tokens = trimmed.split(/\s+/)
  const firstRaw = tokens[0] || ""
  const firstBase = firstRaw.slice(firstRaw.lastIndexOf("/") + 1).toLowerCase()

  let shellScore = 0
  let aiScore = 0

  if (
    KNOWN_SHELL_COMMANDS.has(firstBase) ||
    options.extraCommands?.has(firstBase) ||
    firstRaw.startsWith("./") ||
    firstRaw.startsWith("/") ||
    firstRaw.startsWith("~/") ||
    firstRaw.startsWith("../")
  ) {
    shellScore += 3
  }

  if (SHELL_SYNTAX_RE.test(trimmed)) {
    shellScore += 2
  }

  const cjkMatches = trimmed.match(CJK_RE)
  const cjkCount = cjkMatches ? cjkMatches.length : 0
  const nonSpaceLen = trimmed.replace(/\s+/g, "").length
  const cjkRatio = nonSpaceLen > 0 ? cjkCount / nonSpaceLen : 0

  if (cjkCount >= 2 && cjkRatio >= 0.35 && shellScore === 0) {
    aiScore += 4
  } else if (cjkRatio > 0.3 && shellScore === 0) {
    aiScore += 3
  }
  const hasZhLead = AI_LEAD_ZH_RE.test(trimmed)
  const hasEnLead = AI_LEAD_EN_RE.test(trimmed)
  if (hasZhLead || hasEnLead) {
    aiScore += 2
    if (shellScore === 0 && ((hasEnLead && tokens.length >= 3) || (hasZhLead && cjkCount >= 4))) {
      aiScore += 2
    }
  }
  if (/[?？]$/.test(trimmed)) {
    aiScore += 1
  }

  const enoughContent = tokens.length >= 2 || cjkCount >= 3
  if (enoughContent && aiScore >= 4 && shellScore === 0) {
    return {
      kind: "ai",
      trigger: "auto",
      targetMode: options.defaultAiMode ?? "suggest",
      question: trimmed,
      shellScore,
      aiScore,
    }
  }

  return { kind: "shell", trigger: "shell", shellScore, aiScore }
}

export function resolveTerminalIntentTarget(
  intent: TerminalIntentResult,
  defaultMode: TerminalAiDispatchMode = "suggest",
): TerminalAiDispatchMode {
  if (intent.targetMode) return intent.targetMode
  if (intent.trigger === "prefix_agent") return "assistant"
  if (intent.trigger === "prefix_hash" || intent.trigger === "prefix_question") return "suggest"
  return defaultMode
}

// 判定一条报 command not found (exit 127) 的失败命令是否像被误当 shell 执行的自然语言提问。
export function looksLikeNaturalLanguageMistype(command: string): boolean {
  const res = classifyTerminalInput(command, { autoDetect: true })
  return res.kind === "ai"
}

// 构造写入 PTY 的安全粘贴字节序列（bracketed paste 防止多行命令被逐行提前执行）。
export function formatBracketedPaste(
  command: string,
  options: { run?: boolean; clearLine?: boolean } = {},
): string {
  const cleaned = command.replace(/\r\n?/g, "\n").trim()
  if (!cleaned) return ""
  const prefix = options.clearLine ? "\x15" : ""
  const suffix = options.run ? "\r" : ""
  return `${prefix}\x1b[200~${cleaned}\x1b[201~${suffix}`
}

// 为 AI 建议命令生成可审计追溯、跨轮次不重复的 suggestion_id（满足 ^[A-Za-z0-9._-]{1,64}$）。
export function buildSuggestionId(
  messageKey: string | undefined,
  index: number,
  command: string,
): string {
  let hash = 0x811c9dc5
  for (let i = 0; i < command.length; i++) {
    hash ^= command.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  const hex = hash.toString(16).padStart(8, "0")
  const rawTag = (messageKey || "msg").replace(/[^A-Za-z0-9_-]/g, "").slice(-12) || "msg"
  return `sug_${rawTag}_${index + 1}_${hex}`.slice(0, 64)
}

export type TerminalShortcutAction = "inline_ai" | "jump_prev" | "jump_next" | null

export interface TerminalKeyEventLike {
  type?: string
  key: string
  metaKey?: boolean
  ctrlKey?: boolean
  shiftKey?: boolean
  altKey?: boolean
}

// 统一快捷键分发：
// 1. 备用屏（vim/top/less）一律放行，不拦截任何组合键；
// 2. 绝不拦截裸 Ctrl+I（在 Linux/Windows 终端中等于 Tab 0x09），仅响应 ⌘I 或 Ctrl+Shift+I；
// 3. Block 跳转仅响应 ⌘+↑↓ 或 Ctrl+Shift+↑↓，避免抢占 readline 词移动。
export function classifyTerminalShortcut(
  event: TerminalKeyEventLike,
  inAlternateScreen: boolean,
): TerminalShortcutAction {
  if (event.type && event.type !== "keydown") return null
  if (inAlternateScreen) return null
  if (event.altKey) return null

  const key = event.key.toLowerCase()
  const isMacCmd = Boolean(event.metaKey) && !event.ctrlKey
  const isCtrlShift = Boolean(event.ctrlKey) && Boolean(event.shiftKey) && !event.metaKey

  if (key === "i" && ((isMacCmd && !event.shiftKey) || isCtrlShift)) {
    return "inline_ai"
  }
  if (event.key === "ArrowUp" && ((isMacCmd && !event.shiftKey) || isCtrlShift)) {
    return "jump_prev"
  }
  if (event.key === "ArrowDown" && ((isMacCmd && !event.shiftKey) || isCtrlShift)) {
    return "jump_next"
  }
  return null
}
