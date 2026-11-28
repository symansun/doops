"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { callTool, extractText, rpcManifestWarning, type Session, type Target, type ToolCancel } from "@/lib/client"
import type { AgentExecutionEntry } from "@/lib/agent-execution-stream"
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
import type { TerminalAiActionMode, TerminalModeKind } from "@/lib/terminal-ai"
import type { TerminalAiDispatchMode } from "@/lib/terminal-intent"
import { Button, Textarea } from "./ui"
import { TerminalIcon, SendIcon, SparkIcon, StopIcon } from "./icons"
import { SessionInput } from "./session-input"
import {
  TerminalContextMenu,
  copyTerminalText,
  getSelectionTextWithin,
  type TerminalContextMenuState,
} from "./terminal-context-menu"
import { TerminalPty, type TerminalPtyActions, type TerminalPtyChromeState } from "./terminal-pty"

const TERMINAL_AI_MODE_KEY = "doops.terminal.aiMode"

function readTerminalAiMode(): TerminalAiDispatchMode {
  try {
    return typeof window !== "undefined" && window.localStorage.getItem(TERMINAL_AI_MODE_KEY) === "assistant"
      ? "assistant"
      : "suggest"
  } catch {
    return "suggest"
  }
}

function writeTerminalAiMode(mode: TerminalAiDispatchMode): void {
  try {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(TERMINAL_AI_MODE_KEY, mode)
    }
  } catch {
    /* noop */
  }
}

interface Line {
  kind: "cmd" | "out" | "err" | "warn" | "info"
  text: string
}

export function TerminalPanel({
  session,
  target,
  sessionId,
  onSessionChange,
  sessionDisabled = false,
  prefill = null,
  onPrefillApplied,
  onRunningChange,
  onSendToAi,
  onAskAi,
  aiOpen = false,
  aiBusy = false,
  aiExecutionEntries = [],
  targetOnline = true,
  onToggleAi,
}: {
  session: Session
  target: Target
  sessionId: string
  onSessionChange?: (next: string) => void
  sessionDisabled?: boolean
  prefill?: TerminalPrefill | null
  onPrefillApplied?: (nonce: number) => void
  onRunningChange?: (running: boolean) => void
  onSendToAi?: (request: { selection: string; mode: TerminalAiActionMode; cwd?: string; terminalMode: TerminalModeKind }) => void
  onAskAi?: (request: { prompt: string; fallbackPrefill: string }) => Promise<"sent" | "prefilled" | "error"> | void
  aiOpen?: boolean
  aiBusy?: boolean
  aiExecutionEntries?: readonly AgentExecutionEntry[]
  targetOnline?: boolean
  onToggleAi?: () => void
}) {
  const [command, setCommand] = useState("")
  const appliedPrefillRef = useRef(0)
  const pendingFocusRef = useRef(false)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  // 刷新留下的「结果未知」提示由 console-shell 的顶层横幅渲染，这里不再复制一份。
  // 默认给交互终端（真实 PTY）：一次性命令仍是「命令」页，用于确定性单条执行。
  const [mode, setMode] = useState<"pty" | "command">("pty")
  // 终端双模式：suggest（模式 1：AI 提示命令）/ assistant（模式 2：直接拍给 AI 助手执行）。
  const [aiMode, setAiMode] = useState<TerminalAiDispatchMode>("suggest")
  const [ptyChrome, setPtyChrome] = useState<TerminalPtyChromeState>({
    status: "connecting",
    detail: "",
    cwd: "",
    actionNotice: "",
    hasBlocks: false,
  })
  const ptyActionsRef = useRef<TerminalPtyActions | null>(null)

  useEffect(() => {
    setAiMode(readTerminalAiMode())
  }, [])

  const selectAiMode = useCallback((next: TerminalAiDispatchMode) => {
    setAiMode(next)
    writeTerminalAiMode(next)
  }, [])

  // AI 助手“填入终端”：在「命令」页时直接预填输入框；在「交互终端」页时优先交给 PTY 插入，
  // 若 PTY 尚未连接或处于全屏程序则自动切回「命令」页预填（均不自动执行）。
  useEffect(() => {
    if (mode === "pty") return
    const next = prefillToApply(prefill, appliedPrefillRef.current)
    if (next === null || !prefill) return
    appliedPrefillRef.current = prefill.nonce
    pendingFocusRef.current = true
    setMode("command")
    setCommand(next)
    onPrefillApplied?.(prefill.nonce)
  }, [mode, prefill, onPrefillApplied])

  const handlePtyPrefillHandled = useCallback(
    (nonce: number, insertedIntoPty: boolean) => {
      if (nonce <= appliedPrefillRef.current) return
      appliedPrefillRef.current = nonce
      if (!insertedIntoPty && prefill && prefill.nonce === nonce) {
        pendingFocusRef.current = true
        setMode("command")
        setCommand(prefill.command)
      }
      onPrefillApplied?.(nonce)
    },
    [onPrefillApplied, prefill],
  )

  useEffect(() => {
    if (mode === "command" && pendingFocusRef.current) {
      pendingFocusRef.current = false
      inputRef.current?.focus()
    }
  }, [mode, command])

  const [menuState, setMenuState] = useState<TerminalContextMenuState | null>(null)
  const [actionNotice, setActionNotice] = useState("")
  const noticeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const flashNotice = useCallback((text: string) => {
    setActionNotice(text)
    if (noticeTimerRef.current) clearTimeout(noticeTimerRef.current)
    noticeTimerRef.current = setTimeout(() => {
      noticeTimerRef.current = null
      setActionNotice("")
    }, 2400)
  }, [])
  useEffect(() => () => {
    if (noticeTimerRef.current) clearTimeout(noticeTimerRef.current)
  }, [])
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
          const warning = rpcManifestWarning(ev)
          if (warning) push({ kind: "warn", text: warning })
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
    const targetOffline = !targetOnline || target.status === "offline"
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
          const warning = rpcManifestWarning(ev)
          if (warning) push({ kind: "warn", text: warning })
          if (ev.type === "background_task") {
            submitted = true
            push({ kind: "info", text: `已提交后台任务 #${ev.audit_id}，离开页面不会中断；在左侧「自动化」页查看或停止` })
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
        push({ kind: "info", text: "本地流已结束；任务仍在后台运行，在左侧「自动化」页查看终态或停止" })
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

  const targetOffline = !targetOnline || target.status === "offline"
  const ptyStatusText =
    ptyChrome.status === "ready"
      ? "已连接"
      : ptyChrome.status === "connecting"
        ? "连接中…"
        : ptyChrome.status === "reconnecting"
          ? "重连中…"
          : ptyChrome.status === "closed"
            ? "已结束"
            : "连接失败"

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-1.5 border-b border-border bg-card/80 px-3 py-1.5 text-xs">
        {([
          ["pty", "交互终端"],
          ["command", "命令"],
        ] as const).map(([value, label]) => (
          <button
            key={value}
            type="button"
            aria-pressed={mode === value}
            onClick={() => setMode(value)}
            className={
              "shrink-0 whitespace-nowrap rounded px-2 py-0.5 transition " +
              (mode === value ? "bg-primary/15 text-primary font-medium" : "text-muted-foreground hover:bg-muted")
            }
          >
            {label}
          </button>
        ))}
        {mode === "pty" && (
          <div className="flex min-w-0 items-center gap-1.5 border-l border-border/70 pl-2 text-[11px] text-muted-foreground">
            <span
              className={
                "inline-block h-2 w-2 shrink-0 rounded-full " +
                (ptyChrome.status === "ready"
                  ? "bg-emerald-400"
                  : ptyChrome.status === "connecting"
                    ? "bg-amber-400"
                    : ptyChrome.status === "reconnecting"
                      ? "animate-pulse bg-amber-400"
                      : "bg-rose-400")
              }
              title={ptyChrome.detail ? `${ptyStatusText} · ${ptyChrome.detail}` : ptyStatusText}
            />
            {ptyChrome.status !== "ready" ? (
              <span data-testid="terminal-status-text" className="shrink-0 whitespace-nowrap">
                {ptyStatusText}{ptyChrome.detail ? ` · ${ptyChrome.detail}` : ""}
              </span>
            ) : (
              <span data-testid="terminal-status-text" className="min-w-0 truncate font-mono" title={`已连接 · ${ptyChrome.cwd || "/workspace"}`}>
                {ptyChrome.cwd || "已连接"}
              </span>
            )}
          </div>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-1.5">
          {((mode === "command" && actionNotice) || (mode === "pty" && ptyChrome.actionNotice)) && (
            <span role="status" className="max-w-[12rem] truncate text-[11px] text-primary">
              {mode === "command" ? actionNotice : ptyChrome.actionNotice}
            </span>
          )}
          {mode === "pty" && (
            <div
              role="group"
              aria-label="终端 AI 联动模式"
              data-testid="terminal-ai-mode-switch"
              className="inline-flex shrink-0 items-center rounded-md border border-border/80 bg-muted/40 p-0.5 text-[11px]"
            >
              <button
                type="button"
                aria-pressed={aiMode === "suggest"}
                data-testid="terminal-ai-mode-suggest"
                title="模式 1：输入自然语言 / # / ? 单次生成建议命令（不打开 AI 协同）"
                onClick={() => selectAiMode("suggest")}
                className={
                  "whitespace-nowrap rounded px-2 py-0.5 transition " +
                  (aiMode === "suggest" ? "bg-primary/20 text-primary font-medium" : "text-muted-foreground hover:text-foreground")
                }
              >
                提示命令
              </button>
              <button
                type="button"
                aria-pressed={aiMode === "assistant"}
                data-testid="terminal-ai-mode-assistant"
                disabled={!onAskAi || targetOffline}
                title="模式 2：输入自然语言或 @ai / ## 直接拍给右侧 AI 助手自主执行"
                onClick={() => selectAiMode("assistant")}
                className={
                  "whitespace-nowrap rounded px-2 py-0.5 transition disabled:opacity-40 " +
                  (aiMode === "assistant" ? "bg-primary/20 text-primary font-medium" : "text-muted-foreground hover:text-foreground")
                }
              >
                派给 AI
              </button>
            </div>
          )}
          {mode === "pty" && (
            <button
              type="button"
              disabled={targetOffline}
              data-testid="terminal-inline-ai-trigger"
              title="终端 AI 提问（⌘I / Ctrl+Shift+I；# 提示命令 · @ai 派给 AI）"
              onClick={() => ptyActionsRef.current?.openInlineAi()}
              className="shrink-0 whitespace-nowrap rounded px-2 py-0.5 text-[11px] text-primary hover:bg-primary/10 disabled:opacity-40"
            >
              AI 提问
            </button>
          )}
          {mode === "pty" && (aiOpen || aiExecutionEntries.length > 0) && (
            <button
              type="button"
              data-testid="terminal-ai-mirror-toggle"
              aria-pressed={ptyChrome.aiMirrorEnabled !== false}
              title={
                ptyChrome.aiMirrorEnabled !== false
                  ? "已开启：AI 协同执行的 Shell 命令与输出自动回显到本终端（点击暂停）"
                  : "已暂停 AI 命令回显（点击开启，实时同步 AI 协同执行的命令到本终端）"
              }
              onClick={() => ptyActionsRef.current?.toggleAiMirror()}
              className={
                "shrink-0 whitespace-nowrap rounded px-2 py-0.5 text-[11px] transition " +
                (ptyChrome.aiMirrorEnabled !== false
                  ? "bg-primary/15 text-primary font-medium hover:bg-primary/20"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground")
              }
            >
              AI 回显
            </button>
          )}
          {onToggleAi && (
            <Button
              type="button"
              size="sm"
              variant={aiOpen ? "primary" : "ghost"}
              aria-pressed={aiOpen}
              disabled={targetOffline}
              data-testid="terminal-ai-toggle"
              className="shrink-0 whitespace-nowrap"
              title={targetOffline ? "目标离线" : aiOpen ? "收起同屏 AI 协同侧栏" : "打开右侧多轮 AI 协同助手"}
              onClick={onToggleAi}
            >
              <SparkIcon width={13} height={13} aria-hidden="true" />
              <span>AI 协同</span>
              {aiBusy && <span aria-label="AI 运行中" className="size-1.5 rounded-full bg-accent animate-pulse" />}
            </Button>
          )}
          {mode === "pty" && (ptyChrome.status === "error" || ptyChrome.status === "closed" || ptyChrome.status === "reconnecting") && (
            <button
              type="button"
              data-testid="terminal-reconnect-btn"
              onClick={() => ptyActionsRef.current?.reconnect()}
              className="shrink-0 whitespace-nowrap rounded bg-emerald-500/15 px-2 py-0.5 text-[11px] text-emerald-400 hover:bg-emerald-500/25"
            >
              {ptyChrome.status === "reconnecting" ? "立即重连" : "重连"}
            </button>
          )}
          {mode === "pty" && (ptyChrome.status === "ready" || ptyChrome.status === "connecting" || ptyChrome.status === "reconnecting") && (
            <button
              type="button"
              data-testid="terminal-disconnect-btn"
              onClick={() => ptyActionsRef.current?.disconnect()}
              className="shrink-0 whitespace-nowrap rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
              title="断开当前 PTY 会话"
            >
              断开
            </button>
          )}
          {onSessionChange && (
            <SessionInput
              value={sessionId}
              disabled={sessionDisabled || running}
              onCommit={onSessionChange}
              className="hidden w-32 font-mono text-[11px] 2xl:inline-flex"
            />
          )}
        </div>
      </div>
      {mode === "pty" ? (
        <TerminalPty
          key={target.key + ":" + sessionId}
          session={session}
          targetOnline={targetOnline}
          target={target}
          sessionId={sessionId}
          aiMode={aiMode}
          hideHeader
          onChromeState={setPtyChrome}
          actionsRef={ptyActionsRef}
          aiDisabled={targetOffline}
          aiDisabledReason={targetOffline ? "目标离线" : undefined}
          insertPrefill={mode === "pty" ? prefill : null}
          onInsertPrefillHandled={handlePtyPrefillHandled}
          aiExecutionEntries={aiExecutionEntries}
          onAskAi={onAskAi}
          onSendToAi={
            onSendToAi
              ? (selection, actionMode, cwd) => onSendToAi({ selection, mode: actionMode, cwd, terminalMode: "pty" })
              : undefined
          }
        />
      ) : (
      <>
      <div
        ref={logRef}
        data-testid="terminal-command-output"
        onContextMenu={(event) => {
          if (event.shiftKey) return
          event.preventDefault()
          const selected = getSelectionTextWithin(logRef.current)
          setMenuState({ x: event.clientX, y: event.clientY, selection: selected })
        }}
        className="flex-1 overflow-y-auto bg-background/60 p-4 font-mono text-sm leading-relaxed"
      >
        {lines.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
            <TerminalIcon width={28} height={28} />
            <p className="text-sm">在 {target.instance} 上执行确定性 Shell 命令（doops_shell）</p>
            <p className="text-xs">需要 exec 权限 · Enter 执行 · Shift+Enter 换行 · ↑/↓ 历史 · 选中输出右键可复制或发给 AI</p>
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
      <TerminalContextMenu
        state={menuState}
        onClose={(restoreFocus) => {
          setMenuState(null)
          if (restoreFocus) inputRef.current?.focus()
        }}
        onCopy={(text) => {
          void copyTerminalText(text).then((ok) => {
            flashNotice(ok ? "已复制选中内容" : "复制失败，请使用快捷键复制")
          })
        }}
        onSendToAi={
          onSendToAi
            ? (text, actionMode) => {
                void copyTerminalText(text)
                flashNotice(actionMode === "execute" ? "已复制并发送到 AI 执行" : "已复制并填入 AI 输入框")
                onSendToAi({ selection: text, mode: actionMode, terminalMode: "command" })
              }
            : undefined
        }
        aiDisabled={targetOffline}
        aiDisabledReason={targetOffline ? "目标离线" : undefined}
      />
      </>
      )}
    </div>
  )
}
