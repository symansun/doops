"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { TOOLS, randomSession } from "@/lib/gateway"
import { deploymentRequest, type DeploymentInput } from "@/lib/deployment"
import { DeploymentForm } from "./deployment-form"
import {
  SparkIcon,
  SendIcon,
  StopIcon,
  PlusIcon,
  ChevronRightIcon,
  CopyIcon,
  CheckIcon,
  RefreshIcon,
} from "./icons"
import { Markdown } from "./markdown"

interface Turn {
  id: string
  user: string
  process: string
  answer: string
  error?: string
  running: boolean
  elapsed?: number
}

const QUICK_PROMPTS = [
  "检查节点状态并给出健康巡检报告",
  "检查当前服务日志和健康探针，不执行变更",
  "分析最近一次发布失败的真实原因，不执行回滚",
]

export function AskPanel({
  session,
  target,
  sessionId,
  onConfigureModel,
  onSessionChange,
}: {
  session: Session
  target: Target
  sessionId: string
  onConfigureModel?: () => void
  onSessionChange?: (id: string) => void
}) {
  const [instruction, setInstruction] = useState("")
  const [turns, setTurns] = useState<Turn[]>([])
  const [running, setRunning] = useState(false)
  const [activeModel, setActiveModel] = useState<string | null>(null)
  // 连接状态：是否已为该节点配置可用大模型
  const [modelStatus, setModelStatus] = useState<"loading" | "connected" | "unconfigured" | "error">("loading")
  const [modelError, setModelError] = useState("")
  const [refreshModel, setRefreshModel] = useState(0)
  const [mode, setMode] = useState<"ask" | "apply">("ask")
  const abortRef = useRef<AbortController | null>(null)
  const logRef = useRef<HTMLDivElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)

  // Runtime metadata is the model authority; never read credential-bearing settings.
  useEffect(() => {
    let cancelled = false
    let buf = ""
    setModelStatus("loading")
    setActiveModel(null)
    setModelError("")
    callTool(
      session,
      {
        cluster: target.cluster,
        instance: target.instance,
        tool: TOOLS.prompt,
        arguments: { session_id: sessionId, mode: "metadata" },
      },
      (ev) => {
        if (ev.type === "result") buf = extractText(ev.result)
      },
    )
      .then(() => {
        if (cancelled) return
        try {
          const metadata = JSON.parse(buf)
          if (metadata.status === "connected") {
            setActiveModel(metadata.model || null)
            setModelStatus("connected")
          } else if (metadata.status === "unconfigured") {
            setModelStatus("unconfigured")
          } else {
            setModelStatus("error")
            setModelError(`Agent 模型状态：${metadata.status || "无效响应"}`)
          }
        } catch (err) {
          setModelStatus("error")
          setModelError(`模型元数据无效：${(err as Error).message}`)
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setModelStatus("error")
          setModelError((err as Error).message)
        }
      })
    return () => {
      cancelled = true
    }
  }, [session, target.cluster, target.instance, sessionId, refreshModel])

  useEffect(() => () => abortRef.current?.abort(), [])

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight, behavior: "smooth" })
  }, [turns])

  // 输入框自动增高
  useEffect(() => {
    const ta = taRef.current
    if (!ta) return
    ta.style.height = "auto"
    ta.style.height = `${Math.min(ta.scrollHeight, 200)}px`
  }, [instruction])

  const patchLast = useCallback((fn: (t: Turn) => Turn) => {
    setTurns((p) => {
      if (p.length === 0) return p
      const copy = p.slice()
      copy[copy.length - 1] = fn(copy[copy.length - 1])
      return copy
    })
  }, [])

  async function run(request?: { instruction: string; operation: string; credential_refs: string[] }) {
    const text = (request?.instruction || instruction).trim()
    if (!text || running || modelStatus !== "connected") return
    setInstruction("")
    const turn: Turn = { id: crypto.randomUUID(), user: text, process: "", answer: "", running: true }
    setTurns((p) => [...p, turn])
    setRunning(true)
    const started = Date.now()
    const ac = new AbortController()
    abortRef.current = ac
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.prompt,
          arguments: { session_id: sessionId, instruction: text, operation: request?.operation || "ask", ...(request?.credential_refs.length ? { credential_refs: request.credential_refs } : {}) },
          signal: ac.signal,
        },
        (ev) => {
          if (ev.type === "output") patchLast((t) => ev.kind === "assistant_delta" ? ({ ...t, answer: t.answer + ev.data }) : ({ ...t, process: t.process + ev.data }))
          else if (ev.type === "error") patchLast((t) => ({ ...t, error: ev.error }))
          else if (ev.type === "result") {
            const txt = extractText(ev.result)
            if (txt) patchLast((t) => ({ ...t, answer: t.answer || txt }))
          }
        },
      )
    } catch (err) {
      patchLast((t) => ({ ...t, error: (err as Error).name === "AbortError" ? "操作已取消" : (err as Error).message }))
    } finally {
      patchLast((t) => ({ ...t, running: false, elapsed: Math.round((Date.now() - started) / 1000) }))
      setRunning(false)
      abortRef.current = null
    }
  }

  function newConversation() {
    abortRef.current?.abort()
    setTurns([])
    setInstruction("")
    onSessionChange?.(randomSession())
    taRef.current?.focus()
  }

  function useQuick(q: string) {
    setInstruction(q)
    requestAnimationFrame(() => taRef.current?.focus())
  }

  async function deploy(input: DeploymentInput) {
    await run(deploymentRequest(target, input))
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b bg-card/60 px-4 py-2">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <SparkIcon width={14} height={14} className="text-primary" />
          <span className="font-medium text-foreground">ACP 对话</span>
          {onSessionChange ? (
            <label className="flex items-center gap-1">
              session
              <input
                value={sessionId}
                disabled={running}
                onChange={(e) => onSessionChange(e.target.value)}
                className="w-36 rounded-md border bg-background px-2 py-1 font-mono text-xs text-foreground outline-none focus:border-ring"
              />
            </label>
          ) : (
            <span className="font-mono">session: {sessionId}</span>
          )}
        </div>
        <div className="flex items-center gap-2">
          {modelStatus === "connected" ? (
            <span
              className="flex items-center gap-1.5 rounded-md border border-success/40 bg-success/10 px-2 py-1 font-mono text-xs text-foreground"
              title="AI 助手已连接大模型，可在「管理 → 配置中心」修改"
            >
              <span className="h-1.5 w-1.5 rounded-full bg-success" />
              {activeModel}
            </span>
          ) : modelStatus === "loading" ? (
            <span className="flex items-center gap-1.5 rounded-md border bg-muted/50 px-2 py-1 text-xs text-muted-foreground">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-muted-foreground" />
              检测连接中…
            </span>
          ) : (
            <span
              className="flex items-center gap-1.5 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1 text-xs text-destructive"
              title="该节点尚未配置大模型，无法使用 AI 助手"
            >
              <span className="h-1.5 w-1.5 rounded-full bg-destructive" />
              未连接大模型
            </span>
          )}
          <button
            onClick={newConversation}
            disabled={running}
            className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-foreground transition-colors hover:bg-muted disabled:opacity-40"
          >
            <PlusIcon width={13} height={13} /> 新会话
          </button>
        </div>
      </div>

      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2">
        {([ ["ask", "只读诊断"], ["apply", "部署"] ] as const).map(([value, label]) => <button key={value} aria-pressed={mode === value} disabled={running} onClick={() => setMode(value)} className={`rounded-md border px-3 py-1.5 text-xs ${mode === value ? "border-primary text-primary" : "text-muted-foreground"}`}>{label}</button>)}
        <span className="min-w-0 truncate text-xs text-muted-foreground">{target.cluster}/{target.instance}</span>
      </div>
      <div ref={logRef} className="min-h-0 flex-1 overflow-y-auto p-4">
        {modelStatus === "error" ? <div role="alert" className="space-y-3 text-sm"><p className="break-words text-destructive">{modelError}</p><button onClick={() => setRefreshModel((value) => value + 1)} className="flex items-center gap-2 rounded-md border px-3 py-2"><RefreshIcon width={14} height={14} />重新检查</button></div> : modelStatus === "unconfigured" ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 px-4 text-center text-muted-foreground">
            <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-destructive/10 text-destructive">
              <SparkIcon width={26} height={26} />
            </div>
            <p className="text-base font-medium text-foreground">AI 助手尚未连接大模型</p>
            <p className="max-w-sm text-pretty text-sm">
              {target.instance} 还没有配置可用的大模型，无法对话。请先在「管理 → 配置中心」添加大模型并应用到该节点。
            </p>
            {onConfigureModel && (
              <button
                onClick={onConfigureModel}
                className="mt-1 flex items-center gap-1.5 rounded-lg bg-primary px-3.5 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
              >
                <SparkIcon width={15} height={15} /> 去连接大模型
              </button>
            )}
            <p className="text-xs text-muted-foreground/80">配置好后回到此页即可开始对话</p>
          </div>
        ) : modelStatus === "loading" ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
            <span className="h-2 w-2 animate-pulse rounded-full bg-primary" />
            <p className="text-sm">正在检测大模型连接…</p>
          </div>
        ) : mode === "apply" && !running ? <DeploymentForm disabled={modelStatus !== "connected"} onDeploy={deploy} /> : turns.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
            <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/10 text-primary">
              <SparkIcon width={26} height={26} />
            </div>
            <p className="max-w-sm text-pretty text-center text-sm">
              与 {target.instance} 上的 ACP 智能体多轮对话，执行运维检查和问题诊断
            </p>
            <p className="text-xs">需要 ask 权限 · 上下文在同一 session 内保持</p>
            <div className="mt-2 flex max-w-md flex-col items-stretch gap-2">
              {QUICK_PROMPTS.map((q) => (
                <button
                  key={q}
                  onClick={() => useQuick(q)}
                  className="rounded-lg border px-3 py-2 text-left text-xs text-foreground transition-colors hover:border-primary/40 hover:bg-muted"
                >
                  {q}
                </button>
              ))}
            </div>

          </div>
        ) : (
          <div className="mx-auto flex max-w-3xl flex-col gap-5">
            {turns.map((t) => (
              <TurnView key={t.id} turn={t} />
            ))}
          </div>
        )}
      </div>

      {mode === "apply" && !running && turns.length > 0 && <div className="max-h-64 shrink-0 overflow-auto border-t px-4 py-3"><TurnView turn={turns[turns.length - 1]} /></div>}

      {(mode === "ask" || running) && <div className="border-t bg-card p-3">
        <div className="mx-auto max-w-3xl">
          <div className="flex items-end gap-2 rounded-xl border bg-background p-2 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30">
            <textarea
              ref={taRef}
              value={instruction}
              disabled={modelStatus !== "connected"}
              onChange={(e) => setInstruction(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  run()
                }
              }}
              placeholder={
                modelStatus === "connected"
                  ? "检查目标状态、日志或分析故障"
                  : "请先连接大模型后再使用 AI 助手"
              }
              rows={1}
              className="max-h-48 flex-1 resize-none bg-transparent px-2 py-1.5 text-sm text-foreground outline-none disabled:cursor-not-allowed disabled:opacity-60"
            />
            {running ? (
              <button
                onClick={() => abortRef.current?.abort()}
                className="flex shrink-0 items-center gap-1.5 rounded-lg bg-destructive px-3 py-2 text-sm font-medium text-destructive-foreground transition-opacity hover:opacity-90"
              >
                <StopIcon width={16} height={16} /> 停止
              </button>
            ) : (
              <button
                onClick={() => run()}
                disabled={!instruction.trim() || modelStatus !== "connected"}
                className="flex shrink-0 items-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-50"
              >
                <SendIcon width={16} height={16} /> 发送
              </button>
            )}
          </div>
          <p className="mt-1.5 px-1 text-xs text-muted-foreground">
            Enter 发送 · Shift + Enter 换行 · 同一 session 保持上下文
          </p>
        </div>
      </div>}
    </div>
  )
}

function TurnView({ turn }: { turn: Turn }) {
  const [showProcess, setShowProcess] = useState(true)
  const [copied, setCopied] = useState(false)
  const hasProcess = turn.process.trim().length > 0

  // 任务完成后自动折叠执行过程，聚焦最终回答
  useEffect(() => {
    if (!turn.running && turn.answer) setShowProcess(false)
  }, [turn.running, turn.answer])

  function copyAnswer() {
    navigator.clipboard?.writeText(turn.answer).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="ml-auto max-w-[85%] rounded-xl rounded-br-sm bg-primary/15 px-3 py-2 text-sm text-foreground">
        <span className="whitespace-pre-wrap break-words">{turn.user}</span>
      </div>

      <div className="mr-auto flex w-full max-w-[92%] flex-col gap-2">
        {hasProcess && (
          <div className="rounded-xl border bg-card">
            <button
              onClick={() => setShowProcess((s) => !s)}
              className="flex w-full items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
            >
              <ChevronRightIcon
                width={13}
                height={13}
                className={`transition-transform ${showProcess ? "rotate-90" : ""}`}
              />
              执行过程
              {turn.running ? (
                <span className="ml-1 animate-pulse text-primary">●</span>
              ) : (
                <span className="ml-auto font-normal text-muted-foreground/70">
                  {showProcess ? "点击折叠" : "点击展开"}
                </span>
              )}
            </button>
            {showProcess && (
              <pre className="max-h-72 overflow-y-auto whitespace-pre-wrap break-words border-t px-3 py-2 font-mono text-xs leading-relaxed text-foreground/80">
                {turn.process}
              </pre>
            )}
          </div>
        )}

        {turn.answer && (
          <div className="group rounded-xl rounded-bl-sm border border-primary/30 bg-primary/5 px-3 py-2.5">
            <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-primary">
              <SparkIcon width={13} height={13} /> 智能体回答
              {turn.elapsed != null && (
                <span className="font-normal text-muted-foreground">· {turn.elapsed}s</span>
              )}
              <button
                onClick={copyAnswer}
                className="ml-auto flex items-center gap-1 rounded px-1.5 py-0.5 text-muted-foreground opacity-0 transition-opacity hover:bg-muted hover:text-foreground group-hover:opacity-100"
                title="复制回答"
              >
                {copied ? <CheckIcon width={13} height={13} /> : <CopyIcon width={13} height={13} />}
                {copied ? "已复制" : "复制"}
              </button>
            </div>
            <Markdown content={turn.answer} />
          </div>
        )}

        {turn.error && (
          <pre className="whitespace-pre-wrap break-words rounded-lg bg-destructive/15 px-3 py-2 font-mono text-xs text-destructive">
            {turn.error}
          </pre>
        )}

        {turn.running && !hasProcess && !turn.answer && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="animate-pulse text-primary">●</span> 智能体思考中…
          </div>
        )}
      </div>
    </div>
  )
}
