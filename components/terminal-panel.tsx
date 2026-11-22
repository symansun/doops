"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target, type ToolCancel } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import { BackgroundTaskError, listBackgroundTasks } from "@/lib/background-tasks"
import {
  appendShellOutput,
  describeShellFailure,
  prefillToApply,
  shellResultOutcome,
  type TerminalPrefill,
} from "@/lib/shell-blocks"
import { commandJournal } from "@/lib/console-persistence"
import { Button, Textarea } from "./ui"
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
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  prefill?: TerminalPrefill | null
  onPrefillApplied?: (nonce: number) => void
  onRunningChange?: (running: boolean) => void
}) {
  const [command, setCommand] = useState("")
  const appliedPrefillRef = useRef(0)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  // AI 助手“填入终端”只预填输入框，用户主动提交（Enter/执行）后才走 doops_shell。
  useEffect(() => {
    const next = prefillToApply(prefill, appliedPrefillRef.current)
    if (next === null || !prefill) return
    appliedPrefillRef.current = prefill.nonce
    setCommand(next)
    inputRef.current?.focus()
    onPrefillApplied?.(prefill.nonce)
  }, [prefill, onPrefillApplied])
  // 刷新留下的「结果未知」提示由 console-shell 的顶层横幅渲染，这里不再复制一份。
  const [lines, setLines] = useState<Line[]>([])
  const [running, setRunning] = useState(false)
  const [progress, setProgress] = useState("")
  const [stopPending, setStopPending] = useState(false)
  const stopRequestedRef = useRef(false)
  const [history, setHistory] = useState<string[]>([])
  const [histIdx, setHistIdx] = useState(-1)
  const cancelRef = useRef<ToolCancel | null>(null)
  const logRef = useRef<HTMLDivElement>(null)

  // 「后台运行」是 Gateway 托管任务：不占用终端执行锁，离开页面只结束本地流。
  const [bgSupported, setBgSupported] = useState(false)
  const [bgRunning, setBgRunning] = useState(false)
  const bgAbortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    let active = true
    setBgSupported(Boolean(session.demo))
    if (!session.demo) {
      // detachedBackground 是 Gateway 自有能力：老 Gateway 没有 /v1/background-tasks。
      listBackgroundTasks(session, target)
        .then(() => { if (active) setBgSupported(true) })
        .catch((err) => {
          if (active && !(err instanceof BackgroundTaskError && err.status === 404)) setBgSupported(true)
        })
    }
    return () => { active = false }
  }, [session, target])

  useEffect(() => () => bgAbortRef.current?.abort(), [])

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [lines])

  const push = useCallback((line: Line) => setLines((p) => [...p, line]), [])

  function fail(message: string) {
    const failure = describeShellFailure(message, { stopRequested: stopRequestedRef.current, target })
    push({ kind: failure.tone === "info" ? "info" : failure.tone === "warn" ? "warn" : "err", text: failure.text })
  }

  async function run(cmd: string) {
    if (running || stopPending || !cmd.trim()) return
    setHistory((h) => [...h, cmd])
    setHistIdx(-1)
    setCommand("")
    push({ kind: "cmd", text: cmd })
    // Only a short redacted summary is kept, so a reload can say a command was
    // cut off; it is cleared when the command ends.
    const journalId = commandJournal.begin(session, { targetKey: target.key, sessionId, command: cmd, source: "terminal" })
    setRunning(true)
    onRunningChange?.(true)
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
      commandJournal.end(session, journalId)
      setRunning(false)
      onRunningChange?.(false)
      setProgress("")
      setStopPending(false)
      cancelRef.current = null
    }
  }

  async function runDetached(cmd: string) {
    const targetOffline = target.status === "offline"
    if (bgRunning || !bgSupported || targetOffline || !cmd.trim()) return
    setHistory((h) => [...h, cmd])
    setHistIdx(-1)
    setCommand("")
    push({ kind: "cmd", text: cmd })
    const abort = new AbortController()
    bgAbortRef.current = abort
    setBgRunning(true)
    let submitted = false
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.bg,
          arguments: { session_id: sessionId, command: cmd, detach: true },
          signal: abort.signal,
        },
        (ev) => {
          if (ev.type === "background_task") {
            submitted = true
            push({ kind: "info", text: `已提交后台任务 #${ev.audit_id}，离开页面不会中断；在「自动化 → 后台运行任务」查看或停止` })
            return
          }
          if (ev.type === "output") {
            const next = appendShellOutput({ output: "", progress: "" }, ev.data)
            if (next.progress) setProgress(next.progress)
            if (next.output) push({ kind: "out", text: next.output })
          } else if (ev.type === "result") {
            const outcome = shellResultOutcome(extractText(ev.result))
            if (!outcome.ok) {
              fail(outcome.text)
              return
            }
            if (outcome.text) push({ kind: "out", text: outcome.text })
          }
        },
      )
    } catch (err) {
      // 本地流被释放不代表任务停止；已提交的任务仍由 Gateway 托管。
      if (submitted) {
        push({ kind: "info", text: "本地流已结束；任务仍在后台运行，在「自动化 → 后台运行任务」查看终态" })
      } else {
        fail((err as Error).message)
      }
    } finally {
      bgAbortRef.current = null
      setBgRunning(false)
      setProgress("")
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
    // Candidate confirmation/navigation belongs to the IME. Safari may only report keyCode 229.
    if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      if (!e.repeat) void run(e.currentTarget.value)
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
        <div className="flex items-end gap-2">
          <span className="select-none pb-2 font-mono text-primary">$</span>
          <Textarea
            ref={inputRef}
            aria-label="终端命令"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="hostname && kubectl get nodes"
            rows={1}
            className="max-h-32 min-h-9 flex-1 resize-none font-mono"
          />
          {running || stopPending ? (
            <Button
              onClick={stop}
              disabled={stopPending}
              variant="danger"
            >
              <StopIcon width={16} height={16} /> {stopPending ? "停止中…" : "停止"}
            </Button>
          ) : (
            <>
              <Button
                onClick={() => void run(command)}
                disabled={!command.trim()}
                variant="primary"
              >
                <SendIcon width={16} height={16} /> 执行
              </Button>
              <Button
                onClick={() => void runDetached(command)}
                disabled={!command.trim() || !bgSupported || target.status === "offline"}
                title={bgSupported && target.status !== "offline" ? "在 Gateway 托管的后台任务中执行，离开页面不会中断" : "Gateway 未启用后台任务"}
                variant="default"
              >
                <SendIcon width={16} height={16} /> {bgRunning ? "后台运行中…" : "后台运行"}
              </Button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
