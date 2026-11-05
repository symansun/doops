"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target, type ToolCancel } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import {
  appendShellOutput,
  describeShellFailure,
  destructiveCommandReason,
  prefillToApply,
  shellResultOutcome,
  terminalExecGate,
  type TerminalPrefill,
} from "@/lib/shell-blocks"
import { TerminalIcon, SendIcon, StopIcon } from "./icons"

interface Line {
  kind: "cmd" | "out" | "err" | "warn" | "info"
  text: string
}

export function TerminalPanel({
  session,
  target,
  sessionId,
  prefill = null,
  onPrefillApplied,
}: {
  session: Session
  target: Target
  sessionId: string
  prefill?: TerminalPrefill | null
  onPrefillApplied?: (nonce: number) => void
}) {
  const [command, setCommand] = useState("")
  const appliedPrefillRef = useRef(0)
  // 记下助手预填的原文：未经改动就执行破坏性命令时需要额外确认一次。
  const assistantCommandRef = useRef("")
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const [pendingConfirm, setPendingConfirm] = useState<{ command: string; reason: string } | null>(null)

  // AI 助手“在终端执行”只预填输入框，用户确认（Enter/执行）后才走 doops_shell。
  useEffect(() => {
    const next = prefillToApply(prefill, appliedPrefillRef.current)
    if (next === null || !prefill) return
    appliedPrefillRef.current = prefill.nonce
    assistantCommandRef.current = next
    setPendingConfirm(null)
    setCommand(next)
    inputRef.current?.focus()
    onPrefillApplied?.(prefill.nonce)
  }, [prefill, onPrefillApplied])
  const [lines, setLines] = useState<Line[]>([])
  const [running, setRunning] = useState(false)
  const [progress, setProgress] = useState("")
  const [stopPending, setStopPending] = useState(false)
  const stopRequestedRef = useRef(false)
  const [history, setHistory] = useState<string[]>([])
  const [histIdx, setHistIdx] = useState(-1)
  const cancelRef = useRef<ToolCancel | null>(null)
  const logRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [lines])

  const push = useCallback((line: Line) => setLines((p) => [...p, line]), [])

  function fail(message: string) {
    const failure = describeShellFailure(message, { stopRequested: stopRequestedRef.current, target })
    push({ kind: failure.tone === "info" ? "info" : failure.tone === "warn" ? "warn" : "err", text: failure.text })
  }

  async function run(confirmed = "") {
    const cmd = command.trim()
    if (running || stopPending) return
    const gate = terminalExecGate(cmd, { assistantCommand: assistantCommandRef.current, confirmed })
    if (gate === "empty") return
    if (gate === "confirm") {
      setPendingConfirm({ command: cmd, reason: destructiveCommandReason(cmd) ?? "" })
      return
    }
    setPendingConfirm(null)
    assistantCommandRef.current = ""
    setHistory((h) => [...h, cmd])
    setHistIdx(-1)
    setCommand("")
    push({ kind: "cmd", text: cmd })
    setRunning(true)
    setProgress("")
    setStopPending(false)
    stopRequestedRef.current = false
    cancelRef.current = null
    let settled = false
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.shell,
          arguments: { session_id: sessionId, command: cmd },
          onCancelReady: (cancel) => { cancelRef.current = cancel },
        },
        (ev) => {
          if (ev.type === "output") {
            const next = appendShellOutput({ output: "", progress: "" }, ev.data)
            if (next.progress) setProgress(next.progress)
            if (next.output) push({ kind: "out", text: next.output })
          } else if (ev.type === "result") {
            settled = true
            const outcome = shellResultOutcome(extractText(ev.result))
            if (!outcome.ok) {
              fail(outcome.text)
              return
            }
            if (outcome.text) push({ kind: "out", text: outcome.text })
            push({ kind: "info", text: "✓ 完成，退出码 0" })
          }
        },
      )
    } catch (err) {
      settled = true
      fail((err as Error).message)
    } finally {
      if (!settled) fail("连接已结束，未收到工具执行结果")
      setRunning(false)
      setProgress("")
      setStopPending(false)
      cancelRef.current = null
    }
  }

  function stop() {
    if (!cancelRef.current || stopPending) return
    stopRequestedRef.current = true
    setStopPending(true)
    push({ kind: "info", text: "— 已请求停止，等待远端终态；请勿立即重试 —" })
    void cancelRef.current().catch((err) => {
      stopRequestedRef.current = false
      setStopPending(false)
      push({ kind: "err", text: `停止请求失败：${(err as Error).message}` })
    })
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      void run()
    } else if (e.key === "ArrowUp" && !command.includes("\n")) {
      e.preventDefault()
      const idx = histIdx < 0 ? history.length - 1 : histIdx - 1
      if (idx >= 0) {
        setHistIdx(idx)
        setCommand(history[idx])
      }
    } else if (e.key === "ArrowDown" && histIdx >= 0) {
      e.preventDefault()
      const idx = histIdx + 1
      if (idx < history.length) {
        setHistIdx(idx)
        setCommand(history[idx])
      } else {
        setHistIdx(-1)
        setCommand("")
      }
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div
        ref={logRef}
        className="flex-1 overflow-y-auto bg-background/60 p-4 font-mono text-sm leading-relaxed"
      >
        {lines.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
            <TerminalIcon width={28} height={28} />
            <p className="text-sm">在 {target.instance} 上执行确定性 Shell 命令（doops_shell）</p>
            <p className="text-xs">需要 exec 权限 · Enter 执行 · Shift+Enter 换行 · ↑/↓ 历史</p>
          </div>
        )}
        {lines.map((l, i) => {
          if (l.kind === "cmd")
            return (
              <div key={i} className="mt-2 flex gap-2 text-foreground">
                <span className="select-none text-primary">$</span>
                <span className="whitespace-pre-wrap break-all">{l.text}</span>
              </div>
            )
          if (l.kind === "err")
            return (
              <pre key={i} className="whitespace-pre-wrap break-all text-destructive">
                {l.text}
              </pre>
            )
          if (l.kind === "warn")
            return (
              <div key={i} role="status" className="my-1 rounded border border-warning/40 bg-warning/10 px-2 py-1 text-xs text-warning">
                {l.text}
              </div>
            )
          if (l.kind === "info")
            return (
              <div key={i} className="my-1 text-xs text-muted-foreground">
                {l.text}
              </div>
            )
          return (
            <pre key={i} className="whitespace-pre-wrap break-all text-foreground/90">
              {l.text}
            </pre>
          )
        })}
      </div>

      <div className="border-t bg-card p-3">
        {progress && (
          <div role="status" className="mb-2 font-mono text-xs text-muted-foreground">{progress}</div>
        )}
        {pendingConfirm && (
          <div role="alert" className="mb-2 flex flex-wrap items-center gap-2 rounded-lg border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs">
            <span className="min-w-0 flex-1 text-destructive">
              这条助手建议的命令会{pendingConfirm.reason}，将在 {target.cluster}/{target.instance} 上执行。确认无误后再执行。
            </span>
            <button
              type="button"
              onClick={() => void run(pendingConfirm.command)}
              className="rounded-md bg-destructive px-2 py-1 font-medium text-destructive-foreground hover:opacity-90"
            >
              确认执行
            </button>
            <button type="button" onClick={() => setPendingConfirm(null)} className="rounded-md border px-2 py-1 hover:bg-muted">
              取消
            </button>
          </div>
        )}
        <div className="flex items-end gap-2">
          <span className="select-none pb-2 font-mono text-primary">$</span>
          <textarea
            ref={inputRef}
            value={command}
            onChange={(e) => {
              setCommand(e.target.value)
              setPendingConfirm(null)
            }}
            onKeyDown={onKeyDown}
            placeholder="hostname && kubectl get nodes"
            rows={1}
            className="max-h-32 min-h-9 flex-1 resize-none rounded-lg border bg-background px-3 py-2 font-mono text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
          />
          {running || stopPending ? (
            <button
              onClick={stop}
              disabled={stopPending}
              className="flex items-center gap-1.5 rounded-lg bg-destructive px-3 py-2 text-sm font-medium text-destructive-foreground transition-opacity hover:opacity-90 disabled:opacity-60"
            >
              <StopIcon width={16} height={16} /> {stopPending ? "停止中…" : "停止"}
            </button>
          ) : (
            <button
              onClick={() => void run()}
              disabled={!command.trim()}
              className="flex items-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-50"
            >
              <SendIcon width={16} height={16} /> 执行
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
