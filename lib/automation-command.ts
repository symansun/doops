export interface ParsedEnvVar {
  key: string
  value: string
  masked: boolean
  displayValue: string
  hint?: string
}

export interface ParsedCommandStep {
  index: number
  operator?: "&&" | "||" | ";"
  raw: string
  binary: string
  args: string[]
  envVars: ParsedEnvVar[]
  description: string
}

export interface ParsedAutomationCommand {
  raw: string
  customTitle?: string
  title: string
  category: string
  workdir?: string
  workdirName?: string
  executable?: string
  envVars: ParsedEnvVar[]
  steps: ParsedCommandStep[]
  bodyCommand: string
  maybeTruncated: boolean
}

export interface TaskOutputSummary {
  exitCode?: number
  pid?: string
  logPath?: string
  headline?: string
}

const SENSITIVE_ENV_KEY_RE =
  /(?:^|_)(?:PASS|PASSWD|PASSWORD|SECRET|TOKEN|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|AUTH|COOKIE|CREDENTIAL|SIGNING_?KEY|PWD)(?:$|_)/i

const TIMESTAMP_ENV_KEY_RE = /(?:^|_)(?:AT|TIME|TIMESTAMP|EPOCH|START|END)$/i

const WRAPPER_BINARIES = new Set(["sudo", "nohup", "env", "time", "nice", "ionice", "stdbuf"])

const OUTPUT_BOILERPLATE_RE =
  /^(?:Task submitted\.|TaskID:\s*\S+|PID:\s*\d+|Log:\s*\S+|Task complete\.|Task failed\.|Task canceled\.|ExitCode:\s*-?\d+)$/

function stripQuotes(token: string): string {
  if (token.length >= 2) {
    const first = token[0]
    const last = token[token.length - 1]
    if ((first === "'" && last === "'") || (first === '"' && last === '"')) {
      return token.slice(1, -1)
    }
  }
  return token
}

function basename(path: string): string {
  const cleaned = stripQuotes(path).replace(/\/+$/, "")
  if (!cleaned) return ""
  const idx = cleaned.lastIndexOf("/")
  return idx >= 0 ? cleaned.slice(idx + 1) : cleaned
}

function formatEpochHint(key: string, value: string): string | undefined {
  if (!TIMESTAMP_ENV_KEY_RE.test(key)) return undefined
  if (!/^\d{10,13}$/.test(value)) return undefined
  const num = Number(value)
  const ms = value.length === 13 ? num : num * 1000
  // Reasonable operational epoch range: 2020-01-01 .. 2040-01-01
  if (ms < 1_577_836_800_000 || ms > 2_208_988_800_000) return undefined
  const d = new Date(ms)
  if (!Number.isFinite(d.getTime())) return undefined
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}/${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function makeEnvVar(key: string, rawValue: string): ParsedEnvVar {
  const value = stripQuotes(rawValue)
  const masked = SENSITIVE_ENV_KEY_RE.test(key)
  return {
    key,
    value,
    masked,
    displayValue: masked ? "••••••" : value,
    hint: masked ? undefined : formatEpochHint(key, value),
  }
}

function splitShellSteps(input: string): { operator?: "&&" | "||" | ";"; raw: string }[] {
  const steps: { operator?: "&&" | "||" | ";"; raw: string }[] = []
  let current = ""
  let nextOp: "&&" | "||" | ";" | undefined
  let quote: "'" | '"' | null = null
  let escaped = false

  const flush = (op?: "&&" | "||" | ";") => {
    const trimmed = current.trim()
    if (trimmed) {
      steps.push({ operator: nextOp, raw: trimmed })
    }
    current = ""
    nextOp = op
  }

  for (let i = 0; i < input.length; i++) {
    const ch = input[i]
    if (escaped) {
      current += ch
      escaped = false
      continue
    }
    if (ch === "\\" && quote !== "'") {
      current += ch
      escaped = true
      continue
    }
    if (quote) {
      current += ch
      if (ch === quote) quote = null
      continue
    }
    if (ch === "'" || ch === '"') {
      quote = ch
      current += ch
      continue
    }
    if (ch === "\n") {
      flush(";")
      continue
    }
    if (ch === "&" && input[i + 1] === "&") {
      flush("&&")
      i += 1
      continue
    }
    if (ch === "|" && input[i + 1] === "|") {
      flush("||")
      i += 1
      continue
    }
    if (ch === ";") {
      flush(";")
      continue
    }
    current += ch
  }
  flush(undefined)
  return steps
}

function tokenizeSimpleCommand(segment: string): string[] {
  const tokens: string[] = []
  let cur = ""
  let quote: "'" | '"' | null = null
  let escaped = false

  for (let i = 0; i < segment.length; i++) {
    const ch = segment[i]
    if (escaped) {
      cur += ch
      escaped = false
      continue
    }
    if (ch === "\\" && quote !== "'") {
      escaped = true
      continue
    }
    if (quote) {
      if (ch === quote) quote = null
      else cur += ch
      continue
    }
    if (ch === "'" || ch === '"') {
      quote = ch
      continue
    }
    if (/\s/.test(ch)) {
      if (cur) {
        tokens.push(cur)
        cur = ""
      }
      continue
    }
    cur += ch
  }
  if (cur) tokens.push(cur)
  return tokens
}

function firstNonFlagArg(args: string[], skipValueFlags: Set<string> = new Set()): string {
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (a.startsWith("-")) {
      if (skipValueFlags.has(a)) i += 1
      continue
    }
    return a
  }
  return ""
}

function looksLikeLoadTest(binary: string, args: string[], workdirName: string, envVars: ParsedEnvVar[]): boolean {
  const binLower = binary.toLowerCase()
  if (["k6", "wrk", "hey", "ab", "locust", "vegeta", "jmeter"].includes(binLower)) return true
  const joined = `${workdirName} ${args.join(" ")}`.toLowerCase()
  if (/(?:^|[-_/])(lt|loadtest|bench|stress|perf)(?:$|[-_/.0-9])/.test(joined)) return true
  return envVars.some((e) => e.key === "VUS" || e.key === "CONCURRENCY" || e.key === "HOLD_SECONDS")
}

function shortImageRef(ref: string): string {
  const cleaned = stripQuotes(ref)
  const parts = cleaned.split("/")
  return parts.length > 2 ? parts.slice(-2).join("/") : cleaned
}

function describeStep(
  binary: string,
  args: string[],
  envVars: ParsedEnvVar[],
  workdirName: string,
): { description: string; category: string } {
  const binName = basename(binary)
  const isLoadTest = looksLikeLoadTest(binName, args, workdirName, envVars)

  if (!binary) {
    if (isLoadTest) {
      return {
        description: workdirName ? `在 ${workdirName} 中执行压测任务` : "执行压测任务",
        category: "压测脚本",
      }
    }
    if (envVars.length > 0) {
      return {
        description: workdirName ? `在 ${workdirName} 中执行带参数任务` : "执行带环境变量任务",
        category: "脚本执行",
      }
    }
    return { description: "执行 Shell 命令", category: "后台命令" }
  }

  if (binName === "cd") {
    const dest = args[0] || "~"
    return { description: `切换工作目录至 ${dest}`, category: "后台命令" }
  }

  if (binName === "export") {
    return { description: `设置 ${envVars.length || args.length} 项环境变量`, category: "后台命令" }
  }

  if (binName === "k6") {
    const sub = args[0] === "run" ? firstNonFlagArg(args.slice(1), new Set(["-o", "--out", "-e", "--env", "--vus", "--duration"])) : firstNonFlagArg(args)
    return {
      description: sub ? `运行 k6 压测（${basename(sub)}）` : "运行 k6 压测任务",
      category: "压测脚本",
    }
  }

  if (["bash", "sh", "zsh"].includes(binName)) {
    const cIdx = args.findIndex((a) => a === "-c" || a === "-lc")
    if (cIdx >= 0 && args[cIdx + 1]) {
      const inner = args[cIdx + 1].trim()
      return {
        description: `执行内联脚本（${inner.slice(0, 32)}${inner.length > 32 ? "…" : ""}）`,
        category: isLoadTest ? "压测脚本" : "脚本执行",
      }
    }
    const script = firstNonFlagArg(args)
    if (script) {
      const label = basename(script)
      return {
        description: isLoadTest ? `运行压测脚本 ${label}` : `运行 Shell 脚本 ${label}`,
        category: isLoadTest ? "压测脚本" : "脚本执行",
      }
    }
    return { description: "运行 Shell 脚本", category: "脚本执行" }
  }

  if (binName.startsWith("python")) {
    const mIdx = args.indexOf("-m")
    if (mIdx >= 0 && args[mIdx + 1]) {
      return { description: `运行 Python 模块 ${args[mIdx + 1]}`, category: isLoadTest ? "压测脚本" : "脚本执行" }
    }
    const script = firstNonFlagArg(args, new Set(["-c"]))
    return {
      description: script ? `运行 Python 脚本 ${basename(script)}` : "运行 Python 任务",
      category: isLoadTest ? "压测脚本" : "脚本执行",
    }
  }

  if (["node", "tsx", "bun", "deno"].includes(binName)) {
    const script = firstNonFlagArg(args, new Set(["-e", "--eval"]))
    return {
      description: script ? `运行 ${binName} 脚本 ${basename(script)}` : `运行 ${binName} 任务`,
      category: isLoadTest ? "压测脚本" : "脚本执行",
    }
  }

  if (["pnpm", "npm", "yarn"].includes(binName)) {
    const sub = args.slice(0, 2).join(" ")
    return { description: sub ? `执行 ${binName} ${sub}` : `执行 ${binName}`, category: "构建发布" }
  }

  if (binName === "go") {
    const sub = args[0]
    if (sub === "test") return { description: "运行 Go 测试", category: "构建发布" }
    if (sub === "build") return { description: "构建 Go 程序", category: "构建发布" }
    if (sub === "run") return { description: `运行 Go 程序 ${basename(args[1] || "")}`.trim(), category: "脚本执行" }
    return { description: `执行 go ${sub || ""}`.trim(), category: "构建发布" }
  }

  if (binName === "tar") {
    const flags = args.find((a) => a.startsWith("-") || /^[cxztvjfz]+$/.test(a)) || ""
    const fIdx = args.findIndex((a) => a === "-f" || (a.startsWith("-") && a.endsWith("f")))
    const archive = fIdx >= 0 && args[fIdx + 1] ? basename(args[fIdx + 1]) : ""
    if (flags.includes("x")) {
      return { description: archive ? `解压归档 ${archive}` : "解压 tar 归档", category: "文件归档" }
    }
    return { description: archive ? `归档压缩 ${archive}` : "创建 tar 归档", category: "文件归档" }
  }

  if (binName === "zip" || binName === "gzip") {
    const target = firstNonFlagArg(args)
    return { description: target ? `压缩打包 ${basename(target)}` : "压缩文件", category: "文件归档" }
  }

  if (binName === "unzip" || binName === "gunzip") {
    const target = firstNonFlagArg(args)
    return { description: target ? `解压文件 ${basename(target)}` : "解压文件", category: "文件归档" }
  }

  if (binName === "pg_dump") {
    const db = firstNonFlagArg(args, new Set(["-f", "-F", "-h", "-p", "-U", "-d", "--file", "--format", "--host", "--port", "--username", "--dbname"]))
    return { description: db ? `备份 PostgreSQL 数据库 ${db}` : "备份 PostgreSQL 数据库", category: "数据备份" }
  }

  if (binName === "pg_restore") {
    return { description: "恢复 PostgreSQL 数据库", category: "数据运维" }
  }

  if (binName === "mysqldump") {
    const db = firstNonFlagArg(args, new Set(["-h", "-P", "-u", "--host", "--port", "--user", "--result-file"]))
    return { description: db ? `备份 MySQL 数据库 ${db}` : "备份 MySQL 数据库", category: "数据备份" }
  }

  if (binName === "mongodump") {
    return { description: "备份 MongoDB 数据库", category: "数据备份" }
  }

  if (binName === "docker" || binName === "nerdctl" || binName === "podman") {
    const cleaned: string[] = []
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-n" || args[i] === "--namespace" || args[i] === "-H") {
        i += 1
        continue
      }
      if (args[i].startsWith("-")) continue
      cleaned.push(args[i])
    }
    const [sub, sub2, sub3] = cleaned
    if (sub === "pull" || (sub === "image" && sub2 === "pull")) {
      const img = sub === "pull" ? sub2 : sub3
      return { description: img ? `拉取容器镜像 ${shortImageRef(img)}` : "拉取容器镜像", category: "容器镜像" }
    }
    if (sub === "build") return { description: "构建容器镜像", category: "容器镜像" }
    if (sub === "system" && sub2 === "prune") return { description: "清理容器无用资源", category: "系统维护" }
    if (sub === "image" && sub2 === "prune") return { description: "清理无用容器镜像", category: "系统维护" }
    if (sub === "compose") return { description: `执行容器编排（compose ${sub2 || ""}）`.trim(), category: "容器镜像" }
    if (sub === "restart") return { description: `重启容器 ${sub2 || ""}`.trim(), category: "容器镜像" }
    if (sub === "logs") return { description: `查看容器日志 ${sub2 || ""}`.trim(), category: "容器镜像" }
    return { description: `执行 ${binName} ${cleaned.slice(0, 2).join(" ")}`.trim(), category: "容器镜像" }
  }

  if (binName === "kubectl") {
    const sub = firstNonFlagArg(args, new Set(["-n", "--namespace", "--context", "--kubeconfig"]))
    if (sub === "apply") return { description: "应用 Kubernetes 资源配置", category: "集群运维" }
    if (sub === "rollout") return { description: "执行 Kubernetes 滚动发布操作", category: "集群运维" }
    if (sub === "delete") return { description: "删除 Kubernetes 资源", category: "集群运维" }
    if (sub === "logs") return { description: "查看 Kubernetes Pod 日志", category: "集群运维" }
    if (sub === "get") return { description: "查询 Kubernetes 资源状态", category: "集群运维" }
    return { description: sub ? `执行 kubectl ${sub}` : "执行 Kubernetes 操作", category: "集群运维" }
  }

  if (binName === "helm") {
    const sub = firstNonFlagArg(args, new Set(["-n", "--namespace"]))
    if (sub === "upgrade") return { description: "升级 Helm Release", category: "集群运维" }
    if (sub === "install") return { description: "安装 Helm Release", category: "集群运维" }
    if (sub === "rollback") return { description: "回滚 Helm Release", category: "集群运维" }
    return { description: sub ? `执行 helm ${sub}` : "执行 Helm 操作", category: "集群运维" }
  }

  if (binName === "certbot") {
    const dryRun = args.includes("--dry-run")
    if (args.includes("renew")) {
      return { description: dryRun ? "演练检查 TLS 证书续期" : "续期 TLS 证书", category: "系统维护" }
    }
    return { description: "执行 Certbot 证书管理", category: "系统维护" }
  }

  if (binName === "nginx") {
    if (args.includes("-t") || args.includes("-T")) return { description: "校验 Nginx 配置", category: "系统维护" }
    if (args.includes("reload")) return { description: "热重载 Nginx 配置", category: "系统维护" }
    return { description: "执行 Nginx 控制命令", category: "系统维护" }
  }

  if (binName === "systemctl") {
    const verb = args[0]
    const unit = args[1] || ""
    const verbMap: Record<string, string> = {
      restart: "重启服务",
      reload: "重载服务",
      start: "启动服务",
      stop: "停止服务",
      status: "检查服务状态",
    }
    return {
      description: verb && verbMap[verb] ? `${verbMap[verb]} ${unit}`.trim() : `执行 systemctl ${verb || ""}`.trim(),
      category: "系统维护",
    }
  }

  if (binName === "apt" || binName === "apt-get" || binName === "yum" || binName === "dnf" || binName === "apk") {
    const sub = firstNonFlagArg(args)
    if (sub === "update") return { description: "更新软件包索引", category: "系统维护" }
    if (sub === "upgrade") return { description: "升级系统软件包", category: "系统维护" }
    if (sub === "install" || sub === "add") {
      const idx = args.indexOf(sub)
      const pkgs = args.slice(idx + 1).filter((a) => !a.startsWith("-"))
      const label = pkgs.length > 0 ? pkgs.slice(0, 3).join("、") + (pkgs.length > 3 ? ` 等 ${pkgs.length} 项` : "") : ""
      return { description: label ? `安装软件包 ${label}` : "安装系统软件包", category: "系统维护" }
    }
    return { description: `执行包管理（${binName} ${sub || ""}）`.trim(), category: "系统维护" }
  }

  if (binName === "rsync" || binName === "scp") {
    return { description: binName === "rsync" ? "同步目录 / 文件" : "远程拷贝文件", category: "文件归档" }
  }

  if (binName === "curl" || binName === "wget") {
    const url = args.find((a) => /^https?:\/\//i.test(a))
    if (url) {
      try {
        const parsed = new URL(url)
        return { description: `${binName === "wget" ? "下载" : "请求"} ${parsed.host}${parsed.pathname === "/" ? "" : parsed.pathname}`, category: "网络请求" }
      } catch {
        // fallback below
      }
    }
    return { description: binName === "wget" ? "下载远程资源" : "发起 HTTP 请求", category: "网络请求" }
  }

  if (binName === "git") {
    const sub = firstNonFlagArg(args, new Set(["-C"]))
    if (sub === "pull") return { description: "拉取 Git 最新代码", category: "构建发布" }
    if (sub === "clone") return { description: "克隆 Git 代码仓库", category: "构建发布" }
    if (sub === "fetch") return { description: "同步 Git 远端引用", category: "构建发布" }
    return { description: sub ? `执行 git ${sub}` : "执行 Git 操作", category: "构建发布" }
  }

  if (binName === "sleep") {
    const dur = args[0] || ""
    return { description: dur ? `定时等待（${dur}${/^\d+$/.test(dur) ? " 秒" : ""}）` : "定时等待", category: "后台命令" }
  }

  if (binary.startsWith("./") || binary.startsWith("/") || /\.(?:sh|py|js|ts|mjs|rb|pl)$/i.test(binName)) {
    return {
      description: isLoadTest ? `运行压测脚本 ${binName}` : `运行脚本 ${binName}`,
      category: isLoadTest ? "压测脚本" : "脚本执行",
    }
  }

  const hintArg = firstNonFlagArg(args)
  return {
    description: hintArg ? `执行 ${binName} ${basename(hintArg)}` : `执行 ${binName}`,
    category: isLoadTest ? "压测脚本" : "后台命令",
  }
}

function parseStepTokens(
  raw: string,
  workdirName: string,
): { binary: string; args: string[]; envVars: ParsedEnvVar[]; description: string; category: string } {
  const tokens = tokenizeSimpleCommand(raw)
  const envVars: ParsedEnvVar[] = []
  let idx = 0

  if (tokens[0] === "export") {
    for (let i = 1; i < tokens.length; i++) {
      const m = /^([A-Za-z_][A-Za-z0-9_]*)=(.*)$/.exec(tokens[i])
      if (m) envVars.push(makeEnvVar(m[1], m[2]))
    }
    const { description, category } = describeStep("export", tokens.slice(1), envVars, workdirName)
    return { binary: "export", args: tokens.slice(1), envVars, description, category }
  }

  while (idx < tokens.length) {
    const tok = tokens[idx]
    const envMatch = /^([A-Za-z_][A-Za-z0-9_]*)=(.*)$/.exec(tok)
    if (envMatch) {
      envVars.push(makeEnvVar(envMatch[1], envMatch[2]))
      idx += 1
      continue
    }
    if (WRAPPER_BINARIES.has(basename(tok))) {
      idx += 1
      while (idx < tokens.length && tokens[idx].startsWith("-")) {
        idx += 1
      }
      continue
    }
    if (basename(tok) === "timeout") {
      idx += 1
      while (idx < tokens.length && tokens[idx].startsWith("-")) idx += 1
      if (idx < tokens.length) idx += 1 // duration argument
      continue
    }
    break
  }

  // If the trailing token looks like a truncated env assignment (e.g. `LOGIN_SPREAD_SECONDS...`),
  // treat it as incomplete metadata instead of pretending it's an executable binary.
  let binary = tokens[idx] || ""
  let args = idx < tokens.length ? tokens.slice(idx + 1) : []
  if (binary && args.length === 0 && /^[A-Z][A-Z0-9_]{2,}(?:\.\.\.|…)$/.test(binary)) {
    binary = ""
  }

  const { description, category } = describeStep(binary, args, envVars, workdirName)
  return { binary, args, envVars, description, category }
}

export function parseAutomationCommand(rawInput: string): ParsedAutomationCommand {
  const raw = (rawInput || "").trim()
  const maybeTruncated = raw.endsWith("...") || raw.endsWith("…") || raw.length >= 510

  // Extract explicit task title from leading `# <title>` comment line (excluding `#!` shebangs).
  let customTitle: string | undefined
  let remainder = raw
  const lines = raw.split(/\r?\n/)
  if (lines.length > 0) {
    const first = lines[0].trim()
    if (first.startsWith("#") && !first.startsWith("#!")) {
      const candidate = first.replace(/^#+\s*/, "").trim()
      if (candidate) {
        customTitle = candidate
        remainder = lines.slice(1).join("\n").trim()
      }
    }
  }

  const rawSteps = splitShellSteps(remainder)
  let workdir: string | undefined
  let workdirName = ""
  let actionStartIndex = 0

  if (rawSteps.length > 0) {
    const firstTokens = tokenizeSimpleCommand(rawSteps[0].raw)
    if (firstTokens[0] === "cd" && firstTokens[1]) {
      workdir = stripQuotes(firstTokens[1])
      workdirName = basename(workdir) || workdir
      actionStartIndex = 1
    }
  }

  const steps: ParsedCommandStep[] = []
  const allEnvVars: ParsedEnvVar[] = []
  let primaryCategory = "后台命令"
  let executable: string | undefined

  rawSteps.forEach((step, idx) => {
    const parsed = parseStepTokens(step.raw, workdirName)
    steps.push({
      index: idx + 1,
      operator: step.operator,
      raw: step.raw,
      binary: parsed.binary,
      args: parsed.args,
      envVars: parsed.envVars,
      description: parsed.description,
    })
    for (const ev of parsed.envVars) {
      allEnvVars.push(ev)
    }
    if (idx >= actionStartIndex) {
      if (primaryCategory === "后台命令" && parsed.category !== "后台命令") {
        primaryCategory = parsed.category
      }
      if (!executable && parsed.binary && parsed.binary !== "export") {
        const argPreview = firstNonFlagArg(parsed.args)
        executable = argPreview ? `${basename(parsed.binary)} ${basename(argPreview)}` : basename(parsed.binary)
      }
    }
  })

  const actionSteps = steps.slice(actionStartIndex).filter((s) => s.binary !== "export")
  let title = customTitle || ""
  if (!title) {
    if (actionSteps.length === 1) {
      title = actionSteps[0].description
    } else if (actionSteps.length === 2) {
      title = `${actionSteps[0].description} → ${actionSteps[1].description}`
    } else if (actionSteps.length > 2) {
      title = `${actionSteps[0].description} 等 ${actionSteps.length} 个步骤`
    } else if (workdirName) {
      title = `在 ${workdirName} 中执行任务`
    } else if (steps.length > 0) {
      title = steps[0].description
    } else {
      title = "后台任务"
    }
  }

  // Reconstruct bodyCommand without leading `# title` and leading `cd <dir> &&` for structured refill.
  let bodyCommand = remainder
  if (workdir && rawSteps.length > 1) {
    const restParts: string[] = []
    for (let i = 1; i < rawSteps.length; i++) {
      const part = rawSteps[i]
      if (i === 1) restParts.push(part.raw)
      else restParts.push(`${part.operator === ";" ? ";" : ` ${part.operator || "&&"}`} ${part.raw}`)
    }
    bodyCommand = restParts.join("")
  }

  return {
    raw,
    customTitle,
    title,
    category: primaryCategory,
    workdir,
    workdirName: workdirName || undefined,
    executable,
    envVars: allEnvVars,
    steps,
    bodyCommand,
    maybeTruncated,
  }
}

// Across sibling tasks in the visible list, prioritize env vars whose values
// actually differ between runs so operators can immediately tell runs apart
// (e.g. VUS=5 vs VUS=1000, START_AT=...).
export function pickHighlightEnvVars(
  allParsed: readonly ParsedAutomationCommand[],
  current: ParsedAutomationCommand,
  max = 4,
): { highlights: ParsedEnvVar[]; remainingCount: number } {
  if (current.envVars.length === 0) return { highlights: [], remainingCount: 0 }

  const valueSets = new Map<string, Set<string>>()
  const keyPresence = new Map<string, number>()
  for (const item of allParsed) {
    const seenInItem = new Set<string>()
    for (const ev of item.envVars) {
      if (!valueSets.has(ev.key)) valueSets.set(ev.key, new Set())
      valueSets.get(ev.key)!.add(ev.displayValue)
      if (!seenInItem.has(ev.key)) {
        seenInItem.add(ev.key)
        keyPresence.set(ev.key, (keyPresence.get(ev.key) || 0) + 1)
      }
    }
  }

  const totalTasksWithEnv = allParsed.filter((item) => item.envVars.length > 0).length
  const scored = current.envVars.map((ev, index) => {
    const distinctValues = valueSets.get(ev.key)?.size ?? 1
    const presentInAll = (keyPresence.get(ev.key) ?? 0) === totalTasksWithEnv
    // Varying values or task-specific keys rank highest; shared constant keys rank lower.
    const score = (distinctValues > 1 ? 4 : 0) + (!presentInAll && totalTasksWithEnv > 1 ? 2 : 0)
    return { ev, index, score }
  })

  scored.sort((a, b) => b.score - a.score || a.index - b.index)
  const highlights = scored.slice(0, max).map((item) => item.ev)
  return {
    highlights,
    remainingCount: Math.max(0, current.envVars.length - highlights.length),
  }
}

export function summarizeTaskOutput(output?: string, status?: string): TaskOutputSummary {
  if (!output) return {}
  let exitCode: number | undefined
  let pid: string | undefined
  let logPath: string | undefined

  const exitMatch = output.match(/(?:^|\n)\s*(?:ExitCode:\s*|exit code[:= ]+)(-?\d+)/i)
  if (exitMatch) exitCode = Number(exitMatch[1])

  const pidMatch = output.match(/(?:^|\n)\s*PID:\s*(\d+)/)
  if (pidMatch) pid = pidMatch[1]

  const logMatch = output.match(/(?:^|\n)\s*Log:\s*(\S+)/)
  if (logMatch) logPath = logMatch[1]

  const lines = output
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !OUTPUT_BOILERPLATE_RE.test(line))

  let headline: string | undefined
  if (lines.length > 0) {
    if (status === "error" || status === "failed" || status === "timeout") {
      // Prefer the last line containing an error/failure signal; otherwise fall back to last line.
      const errLine = [...lines]
        .reverse()
        .find((line) => /(?:error|err|fail|denied|refused|timeout|exception|50[0-4]|40[0-9]|fatal|panic|cannot|not found)/i.test(line))
      headline = errLine || lines[lines.length - 1]
    } else {
      headline = lines[lines.length - 1]
    }
    if (headline && headline.length > 180) {
      headline = `${headline.slice(0, 179)}…`
    }
  }

  return { exitCode, pid, logPath, headline }
}

export function composeBackgroundTaskCommand(input: {
  title?: string
  workdir?: string
  command: string
}): string {
  const cleanTitle = (input.title || "").replace(/[\r\n]+/g, " ").trim()
  const cleanWorkdir = (input.workdir || "").replace(/[\r\n]+/g, "").trim()
  const cleanCommand = input.command.trim()
  if (!cleanCommand) return ""

  let execPart = cleanCommand
  if (cleanWorkdir && !/^cd\s+/.test(cleanCommand)) {
    const escapedDir = cleanWorkdir.replace(/'/g, "'\\''")
    execPart = `cd '${escapedDir}' && ${cleanCommand}`
  }

  if (cleanTitle) {
    return `# ${cleanTitle}\n${execPart}`
  }
  return execPart
}

function redactCommandEnvValues(command: string): string {
  return command.replace(
    /\b([A-Za-z_][A-Za-z0-9_]*)=('[^']*'|"[^"]*"|\S+)/g,
    (full, key: string) => (SENSITIVE_ENV_KEY_RE.test(key) ? `${key}=REDACTED` : full),
  )
}

export function buildExplainTaskPrompt(
  task: { audit_id: number; status: string; cluster: string; instance: string; command_summary: string; output?: string },
  parsed: ParsedAutomationCommand,
  outputSummary: TaskOutputSummary,
): string {
  const parts: string[] = [
    `请用通俗易懂的中文解释下面这条自动化后台任务（#${task.audit_id}，目标 ${task.cluster}/${task.instance}，当前状态：${task.status}）：`,
    "1. 这条命令实际做了什么、每个步骤的作用；",
    "2. 各个环境变量与参数的含义；",
    "3. 结合执行输出判断结果或失败原因，并给出下一步排查/优化建议（只读解释，不要直接执行修改）。",
    "",
    `任务摘要：${parsed.title}`,
  ]
  if (parsed.workdir) parts.push(`工作目录：${parsed.workdir}`)
  parts.push("原始命令：", "```sh", redactCommandEnvValues(task.command_summary), "```")
  if (outputSummary.exitCode !== undefined) {
    parts.push(`退出码：${outputSummary.exitCode}`)
  }
  if (task.output?.trim()) {
    const trimmedOutput = task.output.trim()
    const tail = trimmedOutput.length > 2400 ? `…\n${trimmedOutput.slice(-2400)}` : trimmedOutput
    parts.push("执行输出（已脱敏）：", "```text", tail, "```")
  }
  return parts.join("\n")
}
