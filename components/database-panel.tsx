"use client"

import { Database, Send, Square } from "lucide-react"
import { useState } from "react"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"

type DatabaseMode = "query" | "plan" | "apply"

interface DatabaseLine {
  kind: "out" | "err" | "info"
  text: string
}

const QUICK_REQUESTS: Array<{ label: string; mode: DatabaseMode; request: string }> = [
  { label: "数据库巡检", mode: "query", request: "检查当前目标上的数据库健康、连接可用性、慢查询和明显结构风险，只读执行并返回证据。" },
  { label: "生成变更计划", mode: "plan", request: "根据我的需求解析数据库资产，生成可读的变更计划、影响范围、风险和验证步骤，不执行变更。" },
  { label: "回放执行证据", mode: "query", request: "检查最近一次 DoSql 操作的 journal 和 evidence，说明执行结果、验证状态和是否存在未知结果。" },
]

export function DatabasePanel({
  session,
  target,
  sessionId,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  onRunningChange: (running: boolean) => void
}) {
  const [mode, setMode] = useState<DatabaseMode>("query")
  const [request, setRequest] = useState("")
  const [databaseHint, setDatabaseHint] = useState("")
  const [credentials, setCredentials] = useState("")
  const [confirmed, setConfirmed] = useState(false)
  const [lines, setLines] = useState<DatabaseLine[]>([])
  const [running, setRunning] = useState(false)
  const [error, setError] = useState("")
  const [abortController, setAbortController] = useState<AbortController | null>(null)

  function push(line: DatabaseLine) {
    setLines((current) => [...current, line])
  }

  async function run() {
    const text = request.trim()
    if (!text || running) return
    if (mode === "apply" && !confirmed) {
      setError("执行变更前请确认已阅读变更说明，并由 DoSql 提供匹配的审批证据")
      return
    }
    const credentialRefs = credentials.split(/[\s,]+/).filter(Boolean)
    if (credentialRefs.some((value) => !/^[a-z0-9][a-z0-9.-]*$/.test(value))) {
      setError("受管凭据只能填写资源名")
      return
    }
    setError("")
    setLines([])
    setRunning(true)
    onRunningChange(true)
    const controller = new AbortController()
    setAbortController(controller)
    const operation = mode === "apply" ? "apply" : "ask"
    const instruction = [
      `使用 DoSql skill 处理目标 ${target.cluster}/${target.instance}。`,
      mode === "query" ? "保持只读，不执行数据库变更。" : "先输出结构化、可读的结果。",
      mode === "plan" ? "只生成变更计划，不执行 SQL 或 MongoDB 变更。" : "",
      mode === "apply"
        ? "只有在 Gateway managed credential、目标范围和匹配的 DoSql 签名审批证据都存在时才执行；缺少任一条件就明确失败。"
        : "",
      databaseHint.trim() ? `数据库提示：${databaseHint.trim()}` : "",
      `用户需求：${text}`,
    ].filter(Boolean).join("\n")
    push({ kind: "info", text: `目标 ${target.cluster}/${target.instance} · ${modeLabel(mode)}` })
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.prompt,
          arguments: {
            session_id: sessionId,
            operation,
            skill: "dosql",
            instruction,
            ...(credentialRefs.length ? { credential_refs: credentialRefs } : {}),
          },
          signal: controller.signal,
        },
        (event) => {
          if (event.type === "output") push({ kind: "out", text: event.data })
          if (event.type === "error") push({ kind: "err", text: event.error })
          if (event.type === "result") {
            const result = extractText(event.result)
            if (result) push({ kind: "out", text: result })
          }
        },
      )
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        const message = (err as Error).message
        setError(message)
        push({ kind: "err", text: message })
      }
    } finally {
      setRunning(false)
      onRunningChange(false)
      setAbortController(null)
    }
  }

  function stop() {
    abortController?.abort()
    push({ kind: "info", text: "已请求停止，等待 Gateway 返回终态" })
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b bg-card px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <Database aria-hidden="true" className="size-4 text-primary" />
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold text-foreground">数据库工作台</h2>
            <p className="truncate text-xs text-muted-foreground">DoSql · {target.cluster}/{target.instance}</p>
          </div>
        </div>
        <div className="flex items-center gap-1 rounded-md border bg-background p-0.5" role="group" aria-label="数据库操作模式">
          {(["query", "plan", "apply"] as const).map((value) => (
            <button
              key={value}
              type="button"
              aria-pressed={mode === value}
              disabled={running}
              onClick={() => {
                setMode(value)
                setConfirmed(false)
              }}
              className={`rounded px-2 py-1 text-xs transition-colors ${mode === value ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"}`}
            >
              {modeLabel(value)}
            </button>
          ))}
        </div>
      </header>

      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4">
        <div className="flex flex-wrap gap-2">
          {QUICK_REQUESTS.map((item) => (
            <button
              key={item.label}
              type="button"
              disabled={running}
              onClick={() => {
                setMode(item.mode)
                setRequest(item.request)
                setConfirmed(false)
                setError("")
              }}
              className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground transition-colors hover:border-primary/50 hover:text-foreground disabled:opacity-50"
            >
              {item.label}
            </button>
          ))}
        </div>

        <div className="grid gap-2 sm:grid-cols-2">
          <label className="flex min-w-0 flex-col gap-1.5">
            <span className="text-xs font-medium text-foreground">数据库提示</span>
            <input
              value={databaseHint}
              onChange={(event) => {
                setDatabaseHint(event.target.value)
                setConfirmed(false)
              }}
              disabled={running}
              placeholder="数据库名称或资产提示（可选）"
              className="min-w-0 rounded-md border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-ring disabled:opacity-60"
            />
          </label>
          <label className="flex min-w-0 flex-col gap-1.5">
            <span className="text-xs font-medium text-foreground">受管凭据</span>
            <input
              value={credentials}
              onChange={(event) => {
                setCredentials(event.target.value)
                setConfirmed(false)
              }}
              disabled={running}
              placeholder="资源名，多个用空格或逗号分隔（可选）"
              className="min-w-0 rounded-md border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-ring disabled:opacity-60"
            />
          </label>
        </div>

        <label className="flex min-h-32 grow shrink-0 flex-col gap-1.5">
          <span className="text-xs font-medium text-foreground">操作需求</span>
          <textarea
            value={request}
            onChange={(event) => {
              setRequest(event.target.value)
              setConfirmed(false)
            }}
            disabled={running}
            placeholder="描述数据库、环境和要查询或计划的业务目标，不要粘贴密码或连接串。"
            className="min-h-32 flex-1 resize-y rounded-md border bg-background px-3 py-2 text-sm leading-6 text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/20 disabled:opacity-60"
          />
        </label>

        {mode === "apply" && (
          <label className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
              disabled={running}
              className="mt-0.5 accent-primary"
            />
            <span>我已阅读变更说明，允许 DoSql 在 Gateway 授权和签名审批证据齐全时执行。</span>
          </label>
        )}

        {error && <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}

        <div className="flex items-center justify-end gap-2">
          {running ? (
            <button type="button" onClick={stop} className="flex items-center gap-1.5 rounded-md border border-destructive/50 px-3 py-2 text-sm text-destructive hover:bg-destructive/10">
              <Square aria-hidden="true" className="size-3.5" /> 停止
            </button>
          ) : (
            <button type="button" onClick={() => void run()} disabled={!request.trim()} className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50">
              <Send aria-hidden="true" className="size-3.5" /> {mode === "apply" ? "提交受控变更" : "运行 DoSql"}
            </button>
          )}
        </div>

        <section className="min-h-40 rounded-md border bg-background/60 p-3" aria-live="polite">
          {lines.length === 0 ? (
            <div className="flex min-h-32 items-center justify-center text-center text-xs text-muted-foreground">
              结果、journal 和 evidence 会由目标 Agent 返回，并进入统一 Gateway 审计。
            </div>
          ) : (
            <div className="space-y-2 font-mono text-xs leading-5">
              {lines.map((line, index) => (
                <pre key={`${index}-${line.text.slice(0, 12)}`} className={`whitespace-pre-wrap break-words ${line.kind === "err" ? "text-destructive" : line.kind === "info" ? "text-muted-foreground" : "text-foreground/90"}`}>
                  {line.text}
                </pre>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  )
}

function modeLabel(mode: DatabaseMode): string {
  if (mode === "query") return "只读查询"
  if (mode === "plan") return "变更计划"
  return "受控执行"
}
