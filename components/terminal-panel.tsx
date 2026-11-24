"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target, type ToolCancel } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import { listBackgroundTasks } from "@/lib/background-tasks"
import {
  appendShellOutput,
  describeShellFailure,
  prefillToApply,
  shellResultOutcome,
  type ShellOutputState,
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
  // Gateway 已回 remote_canceled：此时再收到 execution_outcome_unknown 是取消的预期结果，
  // 不是执行失败（见 lib/shell-blocks.ts 的 describeShellFailure）。
  const cancelConfirmedRef = useRef(false)
  const [history, setHistory] = useState<string[]>([])
  const [histIdx, setHistIdx] = useState(-1)
  const cancelRef = useRef<ToolCancel | null>(null)
  const logRef = useRef<HTMLDivElement>(null)

  // 「后台运行」是 Gateway 托管任务：不占用终端执行锁，离开页面只结束本地流。
  const [bgSupported, setBgSupported] = useState(false)
  const [bgRunning, setBgRunning] = useState(false)
  const bgAbortRef = useRef<AbortController | null>(null)

  // 流式输出必须跨事件累积：每个事件单独 appendShellOutput 会把一次输出切成若干段，
  // 段与段之间的换行/续行全部丢失（JSON、表格直接被撕碎）。
  const streamRef = useRef<ShellOutputState>({ output: "", progress: "" })
  const outputCharsRef = useRef(0)

  useEffect(() => {
    let active = true
    setBgSupported(Boolean(session.demo))
    if (!session.demo) {
      // detachedBackground 是 Gateway 自有能力：老 Gateway 没有 /v1/background-tasks。
      listBackgroundTasks(session, target)
        .then(() => { if (active) setBgSupported(true) })
        // 探测失败就是不可用：旧实现把 5xx/网络错误也当成支持，按钮能点但必然失败。
        .catch(() => { if (active) setBgSupported(false) })
    }
    return () => { active = false }
  }, [session, target])

  useEffect(() => () => bgAbortRef.current?.abort(), [])

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [lines])

  const push = useCallback((line: Line) => setLines((p) => [...p, line]), [])

  // 续写上一段输出，而不是每来一个事件就新起一段。
  const appendOut = useCallback((text: string) => {
    if (!text) return
    const cap = 200_000
    if (outputCharsRef.current >= cap) return
    const clipped = outputCharsRef.current + text.length > cap
      ? text.slice(0, cap - outputCharsRef.current) + "\n…（输出过长，已截断；请改用重定向或减少输出）"
      : text
    outputCharsRef.current += clipped.length
    setLines((p) => {
      const last = p[p.length - 1]
      if (last?.kind === "out") return [...p.slice(0, -1), { kind: "out", text: last.text + clipped }]
      return [...p, { kind: "out", text: clipped }]
    })
  }, [])

  // 把一次流式增量并入累积状态，只把新增的文本续写到界面。
  function consumeOutput(chunk: string) {
    const before = streamRef.current
    const next = appendShellOutput(before, chunk)
    streamRef.current = next
    if (next.progress) setProgress(next.progress)
    if (next.output.length > before.output.length) appendOut(next.output.slice(before.output.length))
  }

  function fail(message: string) {
    const failure = describeShellFailure(message, {
      stopRequested: stopRequestedRef.current,
      cancelConfirmed: cancelConfirmedRef.current,
      target,
    })
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
    cancelConfirmedRef.current = false
    cancelRef.current = null
    streamRef.current = { output: "", progress: "" }
    outputCharsRef.current = 0
    let settled = false
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.shell,
          arguments: { session_id: sessionId, command: cmd },
          onCancelReady: (cancel) => {
            cancelRef.current = cancel
            // 连接建立得慢时用户可能先点了停止：这里补发，别再静默丢弃。
            if (stopRequestedRef.current) void stop()
          },
        },
        (ev) => {
          if (ev.type === "output") {
            consumeOutput(ev.data)
          } else if (ev.type === "cancel") {
            // 停止请求的处理进度由服务端上报：requested / remote_canceled /
            // rejected / unavailable。停止没有生效必须说出来。
            if (ev.state === "remote_canceled") {
              cancelConfirmedRef.current = true
              push({ kind: "info", text: "— 远端已确认取消，等待终态 —" })
            } else if (ev.state === "rejected" || ev.state === "unavailable") {
              stopRequestedRef.current = false
              setStopPending(false)
              push({ kind: "warn", text: `停止没有生效：${ev.error || "Gateway 未确认取消"}` })
            }
            // requested / remote_canceled 都还没到终态：保持「停止中…」并锁住输入，
            // 否则按钮会立刻变回「停止」，用户以为没点上而反复点。
          } else if (ev.type === "result") {
            settled = true
            const outcome = shellResultOutcome(extractText(ev.result))
            if (!outcome.ok) {
              fail(outcome.text)
              return
            }
            if (outcome.text) appendOut(outcome.text + "\n")
            // doops_shell 成功只回 "Operation complete."，没有退出码；只有确实读到
            // 退出码时才显示，否则退化成「✓ 完成」，不要编一个 0。
            push({ kind: "info", text: outcome.exitCode === undefined ? "✓ 完成" : `✓ 完成，退出码 ${outcome.exitCode}` })
          }
        },
      )
    } catch (err) {
      settled = true
      const message = (err as Error).message
      // 演示模式直接中断本地流；真实链路里停止后仍等远端终态，不会走到这里。
      // 自己请求停止导致的 abort 不是错误，别再抛一行英文 AbortError。
      if (stopRequestedRef.current && /abort/i.test(message)) {
        push({ kind: "info", text: "— 已停止本地执行流 —" })
      } else {
        fail(message)
      }
    } finally {
      if (!settled) fail("连接已结束，未收到工具执行结果")
      commandJournal.end(session, journalId)
      setRunning(false)
      onRunningChange?.(false)
      setProgress("")
      setStopPending(false)
      stopRequestedRef.current = false
      cancelConfirmedRef.current = false
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
            push({ kind: "info", text: `已提交后台任务 #${ev.audit_id}，离开页面不会中断；在左侧「后台任务」页查看或停止` })
            return
          }
          if (ev.type === "output") {
            consumeOutput(ev.data)
          } else if (ev.type === "result") {
            const outcome = shellResultOutcome(extractText(ev.result))
            if (!outcome.ok) {
              fail(outcome.text)
              return
            }
            if (outcome.text) appendOut(outcome.text + "\n")
          }
        },
      )
    } catch (err) {
      // 本地流被释放不代表任务停止；已提交的任务仍由 Gateway 托管。
      if (submitted) {
        push({ kind: "info", text: "本地流已结束；任务仍在后台运行，在左侧「后台任务」页查看终态或停止" })
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
    if (stopPending) return
    stopRequestedRef.current = true
    const cancel = cancelRef.current
    if (!cancel) {
      // 执行请求还没返回（连接建立中）。旧实现在这里直接 return，点「停止」
      // 完全没有任何反应；现在记录意图，连接就绪后由 onCancelReady 补发。
      push({ kind: "info", text: "— 停止通道尚未就绪，连接建立后会立即发送停止请求 —" })
      return
    }
    setStopPending(true)
    push({ kind: "info", text: "— 已请求停止，等待远端终态；请勿立即重试 —" })
    // 服务端最多等 Gateway 35 秒；超过这个时间还没有任何终态，才把按钮还回去。
    const watchdog = setTimeout(() => {
      if (!stopRequestedRef.current) return
      setStopPending(false)
      push({ kind: "warn", text: "停止请求迟迟没有回应；命令可能仍在运行，请核对后重试" })
    }, 45_000)
    void cancel().then((result) => {
      if (result.state === "already_finished") {
        clearTimeout(watchdog)
        // 命令在点击之前已经结束（或请求停在别的实例上）：没有可停止的命令。
        stopRequestedRef.current = false
        setStopPending(false)
        push({ kind: "info", text: "该命令已经结束，没有正在运行的命令需要停止" })
      }
      // 已下发：保持「停止中…」，直到远端终态或服务端明确的停止结果。
    }).catch((err) => {
      clearTimeout(watchdog)
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
