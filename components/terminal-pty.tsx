"use client"

// 交互式终端：把 xterm.js 接到 Agent 的 PTY 会话（doops_terminal）上。
//
// 传输经由 BFF 池化的 Gateway WebSocket（/api/terminal-input + /api/terminal-stream）：
// - 新 Agent（features.long_poll=true）：下行用 NDJSON 长连接流（服务端循环 read wait_ms=8000）；
// - 旧 Agent（缺少 features.long_poll）：自动退回 250ms 短轮询；
// - 输入按 25ms 批量发送，刷新页面仍由 Agent PTY 环形缓冲无损恢复。

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Terminal } from "@xterm/xterm"
import { FitAddon } from "@xterm/addon-fit"
import "@xterm/xterm/css/xterm.css"
import type { Session, Target } from "@/lib/client"
import type { AgentExecutionEntry } from "@/lib/agent-execution-stream"
import { destructiveCommandReason, type TerminalPrefill } from "@/lib/shell-blocks"
import {
  buildAiMirrorWritePayload,
  canFlushAiMirrorToPty,
  formatAiMirrorBlock,
  selectPendingAiMirrorEntries,
} from "@/lib/terminal-ai-mirror"
import {
  classifyTerminalDisconnect,
  nextTerminalReconnectDelayMs,
  requestTerminalAi,
  streamTerminalOutput,
  supportsTerminalLongPoll,
  TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS,
  TerminalAiError,
  terminalRequest,
  TerminalTransportError,
  type TerminalAiAction,
  type TerminalFeatures,
  type TerminalPayload,
} from "@/lib/terminal-transport"
import type { TerminalAiActionMode } from "@/lib/terminal-ai"
import {
  buildTerminalHandbackPrompt,
  buildTerminalInlineContext,
  type TerminalContextBlockInput,
  type TerminalFocusAction,
} from "@/lib/terminal-ai-context"
import {
  buildSuggestionId,
  classifyTerminalInput,
  classifyTerminalShortcut,
  formatBracketedPaste,
  resolveTerminalIntentTarget,
  type TerminalAiDispatchMode,
} from "@/lib/terminal-intent"
import {
  ShellIntegrationTracker,
  type TerminalBlock,
} from "@/lib/terminal-shell-integration"
import {
  TerminalContextMenu,
  copyTerminalText,
  getSelectionTextWithin,
  type TerminalContextMenuState,
} from "./terminal-context-menu"
import { TerminalBlocksOverlay } from "./terminal-blocks-overlay"
import { TerminalAiInline, type TerminalSingleShotState } from "./terminal-ai-inline"
import { Button } from "./ui"

const AUTO_INTENT_STORAGE_KEY = "doops.terminal.autoIntent"
const AI_MIRROR_STORAGE_KEY = "doops.terminal.aiMirror"

function readAutoIntentPref(): boolean {
  try {
    // 默认开启自然语言自动识别；仅当用户显式关闭（写入 "0"）时才退回仅 # / ？ 前缀触发。
    return typeof window !== "undefined" ? window.localStorage.getItem(AUTO_INTENT_STORAGE_KEY) !== "0" : true
  } catch {
    return true
  }
}

function writeAutoIntentPref(enabled: boolean): void {
  try {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(AUTO_INTENT_STORAGE_KEY, enabled ? "1" : "0")
    }
  } catch {
    /* noop */
  }
}

function readAiMirrorPref(): boolean {
  try {
    // 默认开启 AI 命令回显，让交互终端实时可见 AI 执行的 Shell 命令与输出。
    return typeof window !== "undefined" ? window.localStorage.getItem(AI_MIRROR_STORAGE_KEY) !== "0" : true
  } catch {
    return true
  }
}

function writeAiMirrorPref(enabled: boolean): void {
  try {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(AI_MIRROR_STORAGE_KEY, enabled ? "1" : "0")
    }
  } catch {
    /* noop */
  }
}

export type TerminalStatus = "connecting" | "ready" | "reconnecting" | "closed" | "error"

export interface TerminalPtyChromeState {
  status: TerminalStatus
  detail: string
  cwd: string
  actionNotice: string
  hasBlocks: boolean
  aiMirrorEnabled?: boolean
  aiMirrorPendingCount?: number
}

export interface TerminalPtyActions {
  openInlineAi: () => void
  reconnect: () => void
  disconnect: () => void
  sendHandbackToAi: () => boolean
  runCommand: (command: string, run: boolean) => Promise<boolean>
  toggleAiMirror: () => void
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = ""
  const step = 0x8000
  for (let i = 0; i < bytes.length; i += step) {
    binary += String.fromCharCode(...bytes.subarray(i, i + step))
  }
  return btoa(binary)
}

function base64ToBytes(encoded: string): Uint8Array {
  if (!encoded) return new Uint8Array(0)
  const binary = atob(encoded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
  return bytes
}

async function terminalCall(
  _session: Session,
  target: Target,
  sessionId: string,
  args: Record<string, unknown>,
): Promise<TerminalPayload> {
  return terminalRequest(target, sessionId, args)
}

function storageKey(target: Target, sessionId: string): string {
  return "doops-terminal:" + target.cluster + "/" + target.instance + "/" + sessionId
}

export function TerminalPty({
  session,
  target,
  sessionId,
  className,
  aiMode = "suggest",
  hideHeader = false,
  onSendToAi,
  onAskAi,
  insertPrefill = null,
  onInsertPrefillHandled,
  aiDisabled = false,
  aiDisabledReason,
  targetOnline = true,
  onChromeState,
  actionsRef,
  aiExecutionEntries = [],
  onFocusAiEntry,
}: {
  session: Session
  target: Target
  sessionId: string
  className?: string
  aiMode?: TerminalAiDispatchMode
  hideHeader?: boolean
  onSendToAi?: (selection: string, mode: TerminalAiActionMode, cwd?: string) => void
  onAskAi?: (request: { prompt: string; fallbackPrefill: string }) => Promise<"sent" | "prefilled" | "error"> | void
  insertPrefill?: TerminalPrefill | null
  onInsertPrefillHandled?: (nonce: number, insertedIntoPty: boolean) => void
  aiDisabled?: boolean
  aiDisabledReason?: string
  targetOnline?: boolean
  onChromeState?: (state: TerminalPtyChromeState) => void
  actionsRef?: React.MutableRefObject<TerminalPtyActions | null>
  aiExecutionEntries?: readonly AgentExecutionEntry[]
  onFocusAiEntry?: (entryId: string) => void
}) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const trackerRef = useRef<ShellIntegrationTracker | null>(null)
  const termIdRef = useRef("")
  const cursorRef = useRef(0)
  const aliveRef = useRef(true)
  const pendingRef = useRef<string[]>([])
  const flushingRef = useRef(false)
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const readTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const inputTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const noticeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const reconnectAttemptRef = useRef(0)
  const reconnectBusyRef = useRef(false)
  const manualDisconnectRef = useRef(false)
  const lastDisconnectRecoverableRef = useRef(false)
  const streamAbortRef = useRef<AbortController | null>(null)
  const aiAbortRef = useRef<AbortController | null>(null)
  const aiSeqRef = useRef(0)
  const longPollRef = useRef(false)
  const featuresRef = useRef<TerminalFeatures>({})
  const autoDetectRef = useRef(true)
  const aiModeRef = useRef<TerminalAiDispatchMode>(aiMode)
  aiModeRef.current = aiMode
  const blockJumpIdxRef = useRef(-1)
  const appliedInsertNonceRef = useRef(0)
  // L4: 跟踪提示符下尚未收到远端回显的乐观输入缓冲，避免高速粘贴或慢网速下回车读空。
  const optimisticInputRef = useRef("")
  const queueInputRef = useRef<((data: string) => void) | null>(null)
  // 父组件每次刷新会话都会给出新的 session/target 对象；回调只从 ref 读最新值，
  // 这样 xterm 实例只在真正换会话时重建，不会因为对象换了引用就丢屏重连。
  const ctxRef = useRef({ session, target, onAskAi, aiDisabled })
  ctxRef.current = { session, target, onAskAi, aiDisabled }
  // 每次挂载递增；旧的轮询链看到代号变了就退出，避免两条读循环同时推进游标丢字节。
  const runRef = useRef(0)
  const [status, setStatus] = useState<TerminalStatus>("connecting")
  const statusRef = useRef<TerminalStatus>("connecting")
  statusRef.current = status
  const [detail, setDetail] = useState("")
  const [cwd, setCwd] = useState("")
  const [blocks, setBlocks] = useState<readonly TerminalBlock[]>([])
  const [seenOsc133, setSeenOsc133] = useState(false)
  const [atPrompt, setAtPrompt] = useState(true)
  const [inAlternateScreen, setInAlternateScreen] = useState(false)
  const [supportsInputSource, setSupportsInputSource] = useState(false)
  const [inlineOpen, setInlineOpen] = useState(false)
  const [inlineInitialQuestion, setInlineInitialQuestion] = useState("")
  const [inlineFocusBlock, setInlineFocusBlock] = useState<TerminalBlock | null>(null)
  const [includeHistory, setIncludeHistory] = useState(true)
  const [autoDetect, setAutoDetect] = useState(true)
  const [singleShotState, setSingleShotState] = useState<TerminalSingleShotState>({ status: "idle" })
  const [menuState, setMenuState] = useState<TerminalContextMenuState | null>(null)
  const [actionNotice, setActionNotice] = useState("")
  const [aiMirrorEnabled, setAiMirrorEnabled] = useState(true)
  const [initialPromptReady, setInitialPromptReady] = useState(false)
  const initialPromptReadyRef = useRef(false)
  initialPromptReadyRef.current = initialPromptReady
  const promptReadyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const mirroredEntryIdsRef = useRef<Set<string>>(new Set())
  const [mirrorTick, setMirrorTick] = useState(0)
  const pendingMirrorCountRef = useRef(0)

  useEffect(() => {
    const saved = readAutoIntentPref()
    setAutoDetect(saved)
    autoDetectRef.current = saved
    setAiMirrorEnabled(readAiMirrorPref())
  }, [])

  const toggleAiMirror = useCallback(() => {
    setAiMirrorEnabled((prev) => {
      const next = !prev
      writeAiMirrorPref(next)
      return next
    })
  }, [])

  const scheduleInitialPromptReady = useCallback(() => {
    if (initialPromptReadyRef.current) return
    if (promptReadyTimerRef.current) clearTimeout(promptReadyTimerRef.current)
    promptReadyTimerRef.current = setTimeout(() => {
      promptReadyTimerRef.current = null
      initialPromptReadyRef.current = true
      setInitialPromptReady(true)
    }, 250)
  }, [])

  const handleToggleAutoDetect = useCallback((next: boolean) => {
    setAutoDetect(next)
    autoDetectRef.current = next
    writeAutoIntentPref(next)
  }, [])

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
    if (promptReadyTimerRef.current) clearTimeout(promptReadyTimerRef.current)
    aiAbortRef.current?.abort()
  }, [])

  const stopDataLoops = useCallback(() => {
    if (streamAbortRef.current) {
      streamAbortRef.current.abort()
      streamAbortRef.current = null
    }
    if (readTimerRef.current) {
      clearTimeout(readTimerRef.current)
      readTimerRef.current = null
    }
    if (inputTimerRef.current) {
      clearTimeout(inputTimerRef.current)
      inputTimerRef.current = null
    }
    if (resizeTimerRef.current) {
      clearTimeout(resizeTimerRef.current)
      resizeTimerRef.current = null
    }
    aliveRef.current = false
  }, [])

  const stopLoops = useCallback(() => {
    stopDataLoops()
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
    reconnectBusyRef.current = false
  }, [stopDataLoops])

  const handleDisconnectRef = useRef<(run: number, err: unknown) => void>(() => {})

  // 输入按 25ms 批处理：每次按键都发一个 RPC 会把连接压垮，而全量缓冲又能保证
  // 顺序（用户打字速度远低于这个间隔）。
  const flushInput = useCallback(async () => {
    if (flushingRef.current || !termIdRef.current) return
    if (pendingRef.current.length === 0) return
    const run = runRef.current
    flushingRef.current = true
    const chunk = pendingRef.current.join("")
    pendingRef.current = []
    try {
      // Agent 按 base64 收字节：直接发字符串会把多字节字符按 Latin-1 打散。
      await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
        op: "input",
        term: termIdRef.current,
        data: bytesToBase64(new TextEncoder().encode(chunk)),
      })
    } catch (err) {
      if (runRef.current === run && !manualDisconnectRef.current) {
        handleDisconnectRef.current(run, err)
      }
    } finally {
      flushingRef.current = false
      if (aliveRef.current && pendingRef.current.length > 0) void flushInput()
    }
  }, [sessionId])

  const scheduleResize = useCallback(() => {
    if (!termIdRef.current) return
    if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
    resizeTimerRef.current = setTimeout(() => {
      const term = termRef.current
      if (!term || !termIdRef.current) return
      void terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
        op: "resize",
        term: termIdRef.current,
        cols: term.cols,
        rows: term.rows,
      }).catch(() => undefined)
    }, 200)
  }, [sessionId])

  const readLoop = useCallback(async (run: number) => {
    if (!aliveRef.current || !termIdRef.current) return
    if (runRef.current !== run) return
    try {
      const payload = await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
        op: "read",
        term: termIdRef.current,
        cursor: cursorRef.current,
      })
      if (!aliveRef.current || runRef.current !== run) return
      if (payload.truncated) {
        termRef.current?.write("\r\n[输出过快，较早的部分已被丢弃]\r\n")
      }
      if (payload.data) {
        const bytes = base64ToBytes(payload.data)
        termRef.current?.write(bytes, () => {
          if (runRef.current === run) scheduleInitialPromptReady()
        })
      }
      if (typeof payload.cursor === "number") cursorRef.current = payload.cursor
      if (payload.alive === false) {
        stopLoops()
        lastDisconnectRecoverableRef.current = false
        setStatus("closed")
        setDetail("退出码 " + (payload.exit ?? 0))
        termRef.current?.write("\r\n[进程已退出，退出码 " + (payload.exit ?? 0) + "]\r\n")
        sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
        return
      }
    } catch (err) {
      if (!aliveRef.current || runRef.current !== run) return
      handleDisconnectRef.current(run, err)
      return
    }
    if (aliveRef.current && runRef.current === run) {
      readTimerRef.current = setTimeout(() => void readLoop(run), 250)
    }
  }, [sessionId, stopLoops])

  const streamLoop = useCallback(async (run: number) => {
    if (!aliveRef.current || !termIdRef.current) return
    if (runRef.current !== run) return
    const controller = new AbortController()
    streamAbortRef.current = controller
    let streamError: TerminalTransportError | null = null
    try {
      await streamTerminalOutput(
        {
          target: ctxRef.current.target,
          sessionId,
          term: termIdRef.current,
          cursor: cursorRef.current,
          waitMs: 8000,
          signal: controller.signal,
        },
        (ev) => {
          if (!aliveRef.current || runRef.current !== run) return
          if (ev.type === "truncated") {
            termRef.current?.write("\r\n[输出过快，较早的部分已被丢弃]\r\n")
          } else if (ev.type === "data") {
            if (ev.data) {
              const bytes = base64ToBytes(ev.data)
              termRef.current?.write(bytes, () => {
                if (runRef.current === run) scheduleInitialPromptReady()
              })
            }
            if (typeof ev.cursor === "number") cursorRef.current = ev.cursor
          } else if (ev.type === "exit") {
            if (typeof ev.cursor === "number") cursorRef.current = ev.cursor
            stopLoops()
            lastDisconnectRecoverableRef.current = false
            setStatus("closed")
            setDetail("退出码 " + (ev.exit ?? 0))
            termRef.current?.write("\r\n[进程已退出，退出码 " + (ev.exit ?? 0) + "]\r\n")
            sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
          } else if (ev.type === "error") {
            streamError = new TerminalTransportError(ev.error || "终端流已断开", ev.status, ev.code)
          }
        },
      )
      if (streamError && aliveRef.current && runRef.current === run) {
        throw streamError
      }
      // 下行流在存活态正常关闭（例如反向代理超时断流），自动续连，不丢屏。
      if (aliveRef.current && runRef.current === run && !controller.signal.aborted) {
        readTimerRef.current = setTimeout(() => void streamLoop(run), 250)
      }
    } catch (err) {
      if (controller.signal.aborted || !aliveRef.current || runRef.current !== run) return
      handleDisconnectRef.current(run, err)
    } finally {
      if (streamAbortRef.current === controller) streamAbortRef.current = null
    }
  }, [sessionId, stopLoops])

  const sendSuggestionInput = useCallback(
    async (command: string, opts: { run: boolean; suggestionId: string }) => {
      if (!aliveRef.current || !termIdRef.current) {
        flashNotice("交互终端未连接，无法写入命令")
        return false
      }
      const tracker = trackerRef.current
      if (tracker?.inAlternateScreen) {
        flashNotice("当前处于全屏程序，暂不可插入命令")
        return false
      }
      const bytes = formatBracketedPaste(command, {
        run: opts.run,
        clearLine: Boolean(tracker?.atPrompt),
      })
      if (!bytes) return false
      try {
        await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
          op: "input",
          term: termIdRef.current,
          data: bytesToBase64(new TextEncoder().encode(bytes)),
          ...(featuresRef.current.input_source
            ? { source: "ai_suggestion", suggestion_id: opts.suggestionId }
            : {}),
        })
        termRef.current?.focus()
        flashNotice(opts.run ? "已发送命令到终端运行" : "已插入到终端提示符，按 Enter 执行")
        return true
      } catch (err) {
        flashNotice("命令写入失败：" + (err as Error).message)
        return false
      }
    },
    [flashNotice, sessionId],
  )

  const collectContextBlocks = useCallback((): TerminalContextBlockInput[] => {
    const tracker = trackerRef.current
    if (!tracker) return []
    return tracker.blocks
      .filter((b) => b.state !== "prompt" && !b.isAiPrompt && b.command.trim())
      .map((b) => ({
        id: b.id,
        command: b.command,
        output: tracker.readOutput(b, 8000).text,
        exitCode: b.exitCode,
        durationMs: b.startedAt && b.endedAt ? b.endedAt - b.startedAt : undefined,
        cwd: b.cwd,
      }))
  }, [])

  const cancelSingleShotAi = useCallback(() => {
    if (aiAbortRef.current) {
      aiAbortRef.current.abort()
      aiAbortRef.current = null
    }
    setSingleShotState((prev) => {
      if (prev.status === "loading" && prev.aiBlockId) {
        trackerRef.current?.markBlockAiStatus(prev.aiBlockId, "error")
      }
      return { status: "idle" }
    })
  }, [])

  // 终端内 AI 始终走独立单次模型调用（POST /api/terminal-ai -> Gateway /v1/terminal/ai），
  // 不启动多轮 AI 助手（do-agent），不强行弹开右侧「AI 协同」侧栏。
  const submitSingleShotQuestion = useCallback(
    (
      question: string,
      opts: {
        focusBlock?: TerminalBlock | null
        focusAction?: TerminalFocusAction
        aiBlockId?: string
        withHistory?: boolean
      } = {},
    ) => {
      const trimmed = question.trim()
      if (!trimmed) return
      if (ctxRef.current.aiDisabled) {
        flashNotice("当前目标离线，无法调用终端 AI")
        return
      }
      if (ctxRef.current.session.demo) {
        flashNotice("演示模式不连接真实模型服务")
        return
      }

      aiAbortRef.current?.abort()
      const controller = new AbortController()
      aiAbortRef.current = controller
      const seq = ++aiSeqRef.current

      const tracker = trackerRef.current
      const currentCwd = tracker?.cwd || cwd
      const allBlocks = opts.withHistory === false ? [] : collectContextBlocks()
      const focusInput: TerminalContextBlockInput | undefined = opts.focusBlock
        ? {
            id: opts.focusBlock.id,
            command: opts.focusBlock.command,
            output: tracker ? tracker.readOutput(opts.focusBlock, 12_000).text : "",
            exitCode: opts.focusBlock.exitCode,
            durationMs:
              opts.focusBlock.startedAt && opts.focusBlock.endedAt
                ? opts.focusBlock.endedAt - opts.focusBlock.startedAt
                : undefined,
            cwd: opts.focusBlock.cwd,
          }
        : undefined
      const built = buildTerminalInlineContext({
        target: ctxRef.current.target,
        cwd: currentCwd,
        terminalMode: "pty",
        mode: "single_shot",
        question: trimmed,
        blocks: allBlocks,
        focusBlock: focusInput,
        focusAction: opts.focusAction,
      })

      const action: TerminalAiAction = opts.focusAction ?? "ask"
      if (opts.aiBlockId) tracker?.markBlockAiStatus(opts.aiBlockId, "pending")
      setSingleShotState({ status: "loading", question: trimmed, aiBlockId: opts.aiBlockId })

      void requestTerminalAi({
        target: ctxRef.current.target,
        sessionId,
        question: trimmed,
        action,
        cwd: currentCwd || undefined,
        contextPrompt: built.prompt,
        signal: controller.signal,
      })
        .then((res) => {
          if (aiSeqRef.current !== seq || controller.signal.aborted) return
          const turnKey = `term_ai_${seq}`
          const commands = res.commands.map((item, idx) => ({
            id: buildSuggestionId(turnKey, idx, item.command),
            command: item.command,
            description: item.description,
            risk: destructiveCommandReason(item.command),
          }))
          if (opts.aiBlockId) tracker?.markBlockAiStatus(opts.aiBlockId, "answered")
          setSingleShotState({
            status: "ready",
            id: turnKey,
            question: trimmed,
            model: res.model,
            provider: res.provider,
            explanation: res.explanation,
            commands,
            aiBlockId: opts.aiBlockId,
          })
        })
        .catch((err) => {
          if (aiSeqRef.current !== seq || controller.signal.aborted) return
          if (opts.aiBlockId) tracker?.markBlockAiStatus(opts.aiBlockId, "error")
          const message = err instanceof Error ? err.message : "单次 AI 调用失败"
          const code = err instanceof TerminalAiError ? err.code : undefined
          const nextAction = err instanceof TerminalAiError ? err.nextAction : undefined
          setSingleShotState({
            status: "error",
            question: trimmed,
            message,
            code,
            nextAction,
            aiBlockId: opts.aiBlockId,
          })
        })
        .finally(() => {
          if (aiAbortRef.current === controller) aiAbortRef.current = null
        })
    },
    [collectContextBlocks, cwd, flashNotice, sessionId],
  )

  // 仅当用户主动点击「派给 AI → / 转到 AI 协同」或处于模式 2（@ai / 派给 AI）时，转交给多轮 AI 助手。
  const escalateQuestionToAssistant = useCallback(
    (question: string, focusBlock?: TerminalBlock | null) => {
      const handler = ctxRef.current.onAskAi
      if (!handler || ctxRef.current.aiDisabled) {
        flashNotice("AI 协同当前不可用")
        return
      }
      const tracker = trackerRef.current
      const focusInput: TerminalContextBlockInput | undefined = focusBlock
        ? {
            id: focusBlock.id,
            command: focusBlock.command,
            output: tracker ? tracker.readOutput(focusBlock, 12_000).text : "",
            exitCode: focusBlock.exitCode,
            durationMs:
              focusBlock.startedAt && focusBlock.endedAt
                ? focusBlock.endedAt - focusBlock.startedAt
                : undefined,
            cwd: focusBlock.cwd,
          }
        : undefined
      const built = buildTerminalInlineContext({
        target: ctxRef.current.target,
        cwd: tracker?.cwd || cwd,
        terminalMode: "pty",
        mode: "assistant",
        question,
        blocks: collectContextBlocks(),
        focusBlock: focusInput,
        focusAction: focusBlock && focusBlock.exitCode !== undefined && focusBlock.exitCode !== 0 ? "fix" : focusBlock ? "followup" : undefined,
      })
      void Promise.resolve(handler({ prompt: built.prompt, fallbackPrefill: question }))
    },
    [collectContextBlocks, cwd, flashNotice],
  )

  const escalateRef = useRef(escalateQuestionToAssistant)
  escalateRef.current = escalateQuestionToAssistant

  const sendHandbackToAi = useCallback((): boolean => {
    const handler = ctxRef.current.onAskAi
    if (!handler || ctxRef.current.aiDisabled) {
      flashNotice("AI 助手当前不可用")
      return false
    }
    const selection = termRef.current?.getSelection()?.trim() || ""
    if (selection) {
      const snippet = selection.slice(0, 8000)
      void Promise.resolve(handler({
        prompt: `我已在右侧交互终端完成排查，以下是选中输出，请继续分析：\n\`\`\`console\n${snippet}\n\`\`\``,
        fallbackPrefill: snippet,
      }))
      flashNotice("已将选中内容发给 AI")
      return true
    }
    const tracker = trackerRef.current
    const recentBlocks = collectContextBlocks()
    if (recentBlocks.length > 0) {
      const built = buildTerminalHandbackPrompt({
        target: ctxRef.current.target,
        cwd: tracker?.cwd || cwd,
        blocks: recentBlocks,
      })
      void Promise.resolve(handler({
        prompt: built.prompt,
        fallbackPrefill: recentBlocks[recentBlocks.length - 1]?.command || "",
      }))
      flashNotice("已将终端最新执行结果发给 AI")
      return true
    }
    // 无 OSC 133（如 /bin/sh、ash、老版本 bash）时兜底读取光标及以上最近 40 行终端缓冲回传。
    const buf = termRef.current?.buffer?.active
    const tailLines: string[] = []
    if (buf && buf.length > 0) {
      const end = Math.min(buf.length, buf.baseY + buf.cursorY + 1)
      const start = Math.max(0, end - 40)
      for (let i = start; i < end; i++) {
        const line = buf.getLine(i)?.translateToString(true)
        if (line !== undefined) tailLines.push(line)
      }
    }
    const tail = tailLines.join("\n").trim()
    if (!tail) {
      flashNotice("终端暂无可回传的命令记录，可先执行命令或拖选输出")
      return false
    }
    const snippet = tail.slice(0, 8000)
    void Promise.resolve(handler({
      prompt: `我已在右侧交互终端完成排查，以下是最近终端输出，请继续分析：\n\`\`\`console\n${snippet}\n\`\`\``,
      fallbackPrefill: snippet,
    }))
    flashNotice("已将终端最新输出发给 AI")
    return true
  }, [collectContextBlocks, cwd, flashNotice])

  const submitQuestionRef = useRef(submitSingleShotQuestion)
  submitQuestionRef.current = submitSingleShotQuestion

  // M2: 从「AI 助手」切过来「填入终端」时，PTY 可能还在 connecting；
  // 等待 ready（最多 4 秒）再尝试插入，绝不提前消费 nonce 或立刻退回「命令」页。
  useEffect(() => {
    if (!insertPrefill || insertPrefill.nonce <= appliedInsertNonceRef.current) return
    const nonce = insertPrefill.nonce
    const command = insertPrefill.command
    const wantsRun = Boolean(insertPrefill.run)
    if (status === "connecting") {
      const timer = setTimeout(() => {
        if (appliedInsertNonceRef.current >= nonce) return
        appliedInsertNonceRef.current = nonce
        onInsertPrefillHandled?.(nonce, false)
      }, 4000)
      return () => clearTimeout(timer)
    }
    appliedInsertNonceRef.current = nonce
    const tracker = trackerRef.current
    const canDropIntoPty = status === "ready" && aliveRef.current && Boolean(termIdRef.current) && !tracker?.inAlternateScreen
    if (!canDropIntoPty) {
      onInsertPrefillHandled?.(nonce, false)
      return
    }
    // 高风险命令或旧版 Agent 强制仅插入不自动回车，防止绕过人工确认与来源审计。
    const safeToAutoRun = wantsRun && supportsInputSource && !destructiveCommandReason(command)
    void sendSuggestionInput(command, {
      run: safeToAutoRun,
      suggestionId: `prefill_${nonce}`,
    }).then((ok) => {
      onInsertPrefillHandled?.(nonce, ok)
    })
  }, [insertPrefill, onInsertPrefillHandled, sendSuggestionInput, status, supportsInputSource])

  const inlineContextPreview = useMemo(() => {
    if (!inlineOpen) return null
    const tracker = trackerRef.current
    const historyBlocks = includeHistory ? collectContextBlocks() : []
    const focusInput: TerminalContextBlockInput | undefined = inlineFocusBlock
      ? {
          id: inlineFocusBlock.id,
          command: inlineFocusBlock.command,
          output: tracker ? tracker.readOutput(inlineFocusBlock, 12_000).text : "",
          exitCode: inlineFocusBlock.exitCode,
          cwd: inlineFocusBlock.cwd,
        }
      : undefined
    return buildTerminalInlineContext({
      target,
      cwd: tracker?.cwd || cwd,
      terminalMode: "pty",
      mode: "single_shot",
      question: inlineInitialQuestion || "（在此输入你的问题）",
      blocks: historyBlocks,
      focusBlock: focusInput,
      focusAction: inlineFocusBlock ? "followup" : undefined,
    })
  }, [blocks, collectContextBlocks, cwd, includeHistory, inlineFocusBlock, inlineInitialQuestion, inlineOpen, target])

  const applyFeatures = useCallback((features?: TerminalFeatures) => {
    if (!features) return
    featuresRef.current = { ...featuresRef.current, ...features }
    setSupportsInputSource(Boolean(featuresRef.current.input_source))
  }, [])

  const connect = useCallback(async (term: Terminal, run: number): Promise<boolean> => {
    setStatus("connecting")
    setDetail("")
    if (promptReadyTimerRef.current) {
      clearTimeout(promptReadyTimerRef.current)
      promptReadyTimerRef.current = null
    }
    initialPromptReadyRef.current = false
    setInitialPromptReady(false)
    aliveRef.current = true
    longPollRef.current = false
    featuresRef.current = {}
    setSupportsInputSource(false)
    const saved = sessionStorage.getItem(storageKey(ctxRef.current.target, sessionId))
    if (saved) {
      // 刷新后先尝试接回同一会话：Agent 那边的 shell 还在，只是本页忘了 handle。
      try {
        const parsed = JSON.parse(saved) as { term?: string; cursor?: number }
        if (parsed.term) {
          // 刷新后从 0 重放整个环形缓冲：Agent 那边还留着最近 512KiB 输出，
          // 只续接增量会让页面看起来像「历史全丢了」。
          const probe = await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
            op: "read",
            term: parsed.term,
            cursor: 0,
          })
          // 防止 React StrictMode 双重挂载时旧挂起请求覆盖新挂载的 termId。
          if (runRef.current !== run) return false
          if (probe.ok && probe.alive !== false) {
            termIdRef.current = parsed.term
            cursorRef.current = probe.cursor ?? parsed.cursor ?? 0
            longPollRef.current = supportsTerminalLongPoll(probe)
            applyFeatures(probe.features)
            if (probe.data) {
              // H1: 重放历史环形缓冲期间保持回调静音，待 xterm 异步解析完重放字节后再恢复，
              // 防止历史 `# 问题` 注释被重新发给 AI。
              trackerRef.current?.setSuppressCallbacks(true)
              term.write(base64ToBytes(probe.data), () => {
                if (runRef.current === run) {
                  trackerRef.current?.setSuppressCallbacks(false)
                  scheduleInitialPromptReady()
                }
              })
            } else {
              trackerRef.current?.setSuppressCallbacks(false)
            }
            setStatus("ready")
            setCwd(String(probe.cwd || ""))
            return true
          }
          // 远端进程已明确退出（alive=false）：清理旧句柄后改开新会话。
          sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
        }
      } catch (probeErr) {
        if (runRef.current !== run) return false
        const probeDecision = classifyTerminalDisconnect(probeErr)
        // 只有远端 Agent 明确确认 PTY 句柄不存在/已关闭时才删除 sessionStorage 并重开新会话；
        // 遇到目标暂时离线（-32004）或 Gateway 重启（502/503）必须保留旧句柄并抛出给重连器继续续接！
        if (probeDecision.reason !== "session_gone") {
          throw probeErr
        }
        sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
      }
    }

    const opened = await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
      op: "open",
      cols: term.cols,
      rows: term.rows,
    })
    if (runRef.current !== run) {
      // 旧挂载刚打开的孤儿 PTY 立即关闭，避免覆盖新挂载句柄或泄漏远端 shell。
      if (opened.term) {
        void terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
          op: "close",
          term: opened.term,
        }).catch(() => undefined)
      }
      return false
    }
    if (!opened.term) throw new Error("Agent 没有返回终端句柄")
    termIdRef.current = opened.term
    cursorRef.current = opened.cursor ?? 0
    longPollRef.current = supportsTerminalLongPoll(opened)
    applyFeatures(opened.features)
    setCwd(String(opened.cwd || ""))
    sessionStorage.setItem(storageKey(ctxRef.current.target, sessionId), JSON.stringify({ term: opened.term, cursor: cursorRef.current }))
    trackerRef.current?.setSuppressCallbacks(false)
    setStatus("ready")
    return true
  }, [applyFeatures, sessionId])

  const persistCursor = useCallback(() => {
    if (!termIdRef.current) return
    sessionStorage.setItem(storageKey(ctxRef.current.target, sessionId), JSON.stringify({ term: termIdRef.current, cursor: cursorRef.current }))
  }, [sessionId])

  const startReader = useCallback(
    (run: number) => {
      if (!aliveRef.current || runRef.current !== run) return
      if (longPollRef.current) {
        void streamLoop(run)
      } else {
        void readLoop(run)
      }
    },
    [readLoop, streamLoop],
  )

  const attemptReconnect = useCallback(
    async (
      run: number,
      preferredAction: "resume" | "reopen",
      opts: { manual?: boolean; forceNew?: boolean } = {},
    ) => {
      if (runRef.current !== run) return
      if (!opts.manual && manualDisconnectRef.current) return
      if (reconnectBusyRef.current) return
      const term = termRef.current
      if (!term) return

      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
      stopDataLoops()
      reconnectBusyRef.current = true
      setStatus(opts.manual ? "connecting" : "reconnecting")
      setDetail(opts.manual ? "正在重新连接…" : `第 ${Math.max(1, reconnectAttemptRef.current)}/${TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS} 次尝试…`)

      try {
        // 1. 优先尝试按当前 cursor 接回原远端 PTY（不重放已渲染历史，避免刷屏或破坏全屏程序）；
        // 若首次挂载因目标离线失败、内存尚未拿到 termIdRef，则回读 sessionStorage 里保留的旧 term 并从 0 安全静音重放。
        let existingTerm = termIdRef.current
        let replayFromZero = false
        if (!existingTerm && !opts.forceNew && preferredAction !== "reopen") {
          try {
            const saved = sessionStorage.getItem(storageKey(ctxRef.current.target, sessionId))
            const parsed = saved ? (JSON.parse(saved) as { term?: string }) : null
            if (parsed?.term) {
              existingTerm = parsed.term
              replayFromZero = true
            }
          } catch {
            /* noop */
          }
        }
        if (!opts.forceNew && preferredAction !== "reopen" && existingTerm) {
          try {
            const probe = await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
              op: "read",
              term: existingTerm,
              cursor: replayFromZero ? 0 : cursorRef.current,
            })
            if (runRef.current !== run) return
            if (probe.ok && probe.alive !== false) {
              termIdRef.current = existingTerm
              if (probe.truncated) {
                term.write("\r\n[断线期间输出过快，较早的部分已被丢弃]\r\n")
              }
              if (probe.data) {
                if (replayFromZero) {
                  trackerRef.current?.setSuppressCallbacks(true)
                  term.write(base64ToBytes(probe.data), () => {
                    if (runRef.current === run) trackerRef.current?.setSuppressCallbacks(false)
                  })
                } else {
                  term.write(base64ToBytes(probe.data))
                }
              } else if (replayFromZero) {
                trackerRef.current?.setSuppressCallbacks(false)
              }
              if (typeof probe.cursor === "number") cursorRef.current = probe.cursor
              longPollRef.current = supportsTerminalLongPoll(probe)
              applyFeatures(probe.features)
              if (probe.cwd) setCwd(String(probe.cwd))
              reconnectAttemptRef.current = 0
              lastDisconnectRecoverableRef.current = false
              aliveRef.current = true
              setStatus("ready")
              setDetail("")
              flashNotice("终端已重新连接")
              scheduleResize()
              startReader(run)
              return
            }
            if (probe.alive === false) {
              lastDisconnectRecoverableRef.current = false
              termIdRef.current = ""
              sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
              setStatus("closed")
              setDetail("退出码 " + (probe.exit ?? 0))
              term.write("\r\n[进程已退出，退出码 " + (probe.exit ?? 0) + "]\r\n")
              return
            }
          } catch (probeErr) {
            if (runRef.current !== run) return
            const probeDecision = classifyTerminalDisconnect(probeErr)
            if (probeDecision.reason !== "session_gone") {
              throw probeErr
            }
            // 原 PTY 句柄在远端已失效（如 Agent Pod 重启或空闲回收），自动降级重开新会话。
            termIdRef.current = ""
            sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
          }
        }

        // 2. 重开全新远端 PTY 会话（原会话失效、手动「断开」后重连或进程退出后重连）。
        const hadPriorTerm = Boolean(existingTerm) || opts.forceNew
        termIdRef.current = ""
        cursorRef.current = 0
        sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
        const opened = await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
          op: "open",
          cols: term.cols,
          rows: term.rows,
        })
        if (runRef.current !== run) {
          if (opened.term) {
            void terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, {
              op: "close",
              term: opened.term,
            }).catch(() => undefined)
          }
          return
        }
        if (!opened.term) throw new TerminalTransportError("Agent 没有返回终端句柄", 502)
        termIdRef.current = opened.term
        cursorRef.current = opened.cursor ?? 0
        longPollRef.current = supportsTerminalLongPoll(opened)
        applyFeatures(opened.features)
        setCwd(String(opened.cwd || ""))
        sessionStorage.setItem(
          storageKey(ctxRef.current.target, sessionId),
          JSON.stringify({ term: opened.term, cursor: cursorRef.current }),
        )
        trackerRef.current?.setSuppressCallbacks(false)
        reconnectAttemptRef.current = 0
        lastDisconnectRecoverableRef.current = false
        aliveRef.current = true
        setStatus("ready")
        setDetail("")
        if (hadPriorTerm) {
          if (promptReadyTimerRef.current) {
            clearTimeout(promptReadyTimerRef.current)
            promptReadyTimerRef.current = null
          }
          initialPromptReadyRef.current = false
          setInitialPromptReady(false)
          term.write("\r\n[已建立新的远端终端会话]\r\n")
        }
        flashNotice("已重新连接终端")
        term.focus()
        startReader(run)
      } catch (err) {
        if (runRef.current !== run) return
        handleDisconnectRef.current(run, err)
      } finally {
        reconnectBusyRef.current = false
      }
    },
    [applyFeatures, flashNotice, scheduleResize, sessionId, startReader, stopDataLoops],
  )

  const handleDisconnect = useCallback(
    (run: number, err: unknown) => {
      if (runRef.current !== run || manualDisconnectRef.current) return
      stopDataLoops()
      const message = err instanceof Error ? err.message : String(err || "连接已断开")
      const unsupported =
        message.includes("doops_terminal") &&
        (message.includes("unknown doops action") || message.startsWith("Unknown tool over WS"))
      if (unsupported) {
        const shown = "该 Agent 版本不支持交互终端（需 Agent ≥ v0.3.32），请先用「命令」页签或升级该 Agent"
        lastDisconnectRecoverableRef.current = false
        setStatus("error")
        setDetail(shown)
        termRef.current?.write("\r\n[无法连接交互式终端：" + shown + "]\r\n")
        return
      }

      const decision = classifyTerminalDisconnect(err)
      if (!decision.retry) {
        lastDisconnectRecoverableRef.current = false
        if (reconnectTimerRef.current) {
          clearTimeout(reconnectTimerRef.current)
          reconnectTimerRef.current = null
        }
        setStatus("error")
        setDetail(message)
        termRef.current?.write("\r\n[终端已断开：" + message + "]\r\n")
        return
      }

      lastDisconnectRecoverableRef.current = true
      const nextAttempt = reconnectAttemptRef.current + 1
      if (nextAttempt > TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS) {
        if (reconnectTimerRef.current) {
          clearTimeout(reconnectTimerRef.current)
          reconnectTimerRef.current = null
        }
        setStatus("error")
        setDetail(`自动重连失败（已重试 ${TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS} 次）：${message}`)
        return
      }

      reconnectAttemptRef.current = nextAttempt
      const delayMs = nextTerminalReconnectDelayMs(nextAttempt - 1)
      setStatus("reconnecting")
      setDetail(
        `第 ${nextAttempt}/${TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS} 次 · ${Math.max(1, Math.round(delayMs / 1000))}s 后重试`,
      )
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = setTimeout(() => {
        reconnectTimerRef.current = null
        void attemptReconnect(run, decision.action === "reopen" ? "reopen" : "resume")
      }, delayMs)
    },
    [attemptReconnect, stopDataLoops],
  )
  handleDisconnectRef.current = handleDisconnect

  // 当控制台轮询发现目标 Agent 从离线恢复为在线时，立刻唤醒终端重连（不必干等剩余退避倒计时）。
  const prevTargetOnlineRef = useRef(targetOnline)
  useEffect(() => {
    const wasOnline = prevTargetOnlineRef.current
    prevTargetOnlineRef.current = targetOnline
    if (!wasOnline && targetOnline && !manualDisconnectRef.current) {
      const run = runRef.current
      if (!run) return
      if (statusRef.current === "reconnecting") {
        void attemptReconnect(run, "resume")
      } else if (statusRef.current === "error" && lastDisconnectRecoverableRef.current) {
        reconnectAttemptRef.current = 0
        void attemptReconnect(run, "resume")
      }
    }
  }, [attemptReconnect, targetOnline])

  const manualReconnect = useCallback(() => {
    const run = runRef.current
    if (!run || !termRef.current) return
    manualDisconnectRef.current = false
    reconnectAttemptRef.current = 0
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
    const forceNew = statusRef.current === "closed" || !termIdRef.current
    void attemptReconnect(run, forceNew ? "reopen" : "resume", { manual: true, forceNew })
  }, [attemptReconnect])

  const shutdown = useCallback(async () => {
    manualDisconnectRef.current = true
    lastDisconnectRecoverableRef.current = false
    reconnectAttemptRef.current = 0
    stopLoops()
    const term = termIdRef.current
    termIdRef.current = ""
    sessionStorage.removeItem(storageKey(ctxRef.current.target, sessionId))
    setStatus("closed")
    setDetail("已手动断开")
    if (!term) return
    try {
      await terminalCall(ctxRef.current.session, ctxRef.current.target, sessionId, { op: "close", term })
    } catch {
      // 关闭失败只影响远端清理，30 分钟空闲回收会兜底。
    }
  }, [sessionId, stopLoops])

  // 挂载 xterm 并启动连接/轮询；卸载时只关掉本地循环，会话由「断开」按钮或回收器结束。
  useEffect(() => {
    if (ctxRef.current.session.demo) {
      // 演示模式没有真实 Agent：不伪造 shell 输出，明确告知需要连真实目标。
      setStatus("error")
      setDetail("演示模式不连接真实集群")
      return
    }
    const run = ++runRef.current
    const host = hostRef.current
    if (!host) return
    const term = new Terminal({
      convertEol: true,
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      scrollback: 8000,
      theme: {
        background: "#0b1220",
        foreground: "#e2e8f0",
        cursor: "#38bdf8",
        selectionBackground: "#334155",
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    fit.fit()
    termRef.current = term
    fitRef.current = fit

    const tracker = new ShellIntegrationTracker(term, {
      maxBlocks: 500,
      suppressCallbacks: true,
    })
    trackerRef.current = tracker
    const unsubscribeTracker = tracker.onChange(() => {
      setBlocks([...tracker.blocks])
      setSeenOsc133(tracker.seenOsc133)
      const promptIdle = tracker.seenOsc133 ? tracker.atPrompt : !tracker.inAlternateScreen
      setAtPrompt(promptIdle)
      setInAlternateScreen(tracker.inAlternateScreen)
      if (tracker.cwd) setCwd(tracker.cwd)
      if (tracker.seenOsc133 && tracker.atPrompt && !initialPromptReadyRef.current) {
        if (promptReadyTimerRef.current) {
          clearTimeout(promptReadyTimerRef.current)
          promptReadyTimerRef.current = null
        }
        initialPromptReadyRef.current = true
        setInitialPromptReady(true)
      }
      if (promptIdle && pendingMirrorCountRef.current > 0) {
        setMirrorTick((v) => v + 1)
      }
    })

    const queueInput = (data: string) => {
      if (!aliveRef.current || !termIdRef.current || !data) return
      // 更新乐观行缓冲（兼容 OSC 133 与旧版 Agent，仅用于回显未到时的自然语言/前缀检测兜底）。
      const promptReady = tracker.seenOsc133 ? tracker.atPrompt : !tracker.inAlternateScreen
      if (promptReady && !tracker.inAlternateScreen) {
        const plain = data.replace(/\x1b\[200~/g, "").replace(/\x1b\[201~/g, "")
        if (
          plain.includes("\r") ||
          plain.includes("\n") ||
          plain.includes("\x03") ||
          plain.includes("\x15") ||
          plain.includes("\t")
        ) {
          optimisticInputRef.current = ""
        } else if (plain === "\x7f" || plain === "\b") {
          optimisticInputRef.current = optimisticInputRef.current.slice(0, -1)
        } else if (!plain.startsWith("\x1b")) {
          optimisticInputRef.current += plain
        } else {
          // 方向键等控制序列会改变光标位置，清空线性乐观缓冲并回退到 xterm 真实缓冲。
          optimisticInputRef.current = ""
        }
        if (optimisticInputRef.current === "" && pendingMirrorCountRef.current > 0) {
          setMirrorTick((v) => v + 1)
        }
      } else {
        optimisticInputRef.current = ""
      }
      // 攒 25ms 再发：逐键一个 RPC 会把每次按键变成一次 JSON-RPC 往返。
      pendingRef.current.push(data)
      if (inputTimerRef.current) return
      inputTimerRef.current = setTimeout(() => {
        inputTimerRef.current = null
        void flushInput()
      }, 25)
    }
    queueInputRef.current = queueInput

    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== "keydown") return true
      // M1: 备用屏一律放行；不抢占裸 Ctrl+I（Tab）与裸 Ctrl+↑↓。
      const shortcut = classifyTerminalShortcut(event, tracker.inAlternateScreen)
      if (shortcut === "inline_ai") {
        event.preventDefault()
        const draft = tracker.atPrompt ? tracker.readCurrentInput() : optimisticInputRef.current.trim()
        const cleaned = draft.replace(/^(?:##|[#?？]|@ai)\s*/i, "")
        setInlineFocusBlock(null)
        setInlineInitialQuestion(cleaned)
        setInlineOpen(true)
        return false
      }
      if (shortcut === "jump_prev" || shortcut === "jump_next") {
        const navBlocks = tracker.blocks.filter((b) => b.state !== "prompt" && b.command.trim())
        if (navBlocks.length === 0) return true
        event.preventDefault()
        const curIdx = blockJumpIdxRef.current
        const nextIdx =
          shortcut === "jump_prev"
            ? curIdx <= 0 ? navBlocks.length - 1 : curIdx - 1
            : curIdx < 0 || curIdx >= navBlocks.length - 1 ? 0 : curIdx + 1
        blockJumpIdxRef.current = nextIdx
        const targetBlock = navBlocks[nextIdx]
        if (targetBlock && targetBlock.promptLine >= 0) {
          term.scrollToLine(targetBlock.promptLine)
        }
        return false
      }
      // Enter：在提示符下识别 # / ？ / ? 前缀或自然语言（默认开启），改走单次 AI 调用。
      // 同时在远端 shell 侧安全变为注释行提交，避免报 command not found。
      if (event.key === "Enter" && !event.shiftKey && !event.isComposing && event.keyCode !== 229) {
        const promptReady = tracker.seenOsc133 ? tracker.atPrompt : !tracker.inAlternateScreen
        if (promptReady && !tracker.inAlternateScreen && aliveRef.current && termIdRef.current && !ctxRef.current.aiDisabled) {
          const screenLine = tracker.seenOsc133 ? tracker.readCurrentInput() : ""
          const optLine = optimisticInputRef.current.trim()
          const line =
            optLine.length > screenLine.length && (!screenLine || optLine.startsWith(screenLine))
              ? optLine
              : screenLine || optLine
          optimisticInputRef.current = ""
          const intent = classifyTerminalInput(line, {
            autoDetect: autoDetectRef.current,
            defaultAiMode: aiModeRef.current,
          })
          if (intent.kind === "ai" && intent.question) {
            const targetMode = resolveTerminalIntentTarget(intent, aiModeRef.current)
            const aiBlock = tracker.recordAiBlock(line)
            if (targetMode === "assistant" && ctxRef.current.onAskAi) {
              tracker.markBlockAiStatus(aiBlock.id, "answered")
              escalateRef.current(intent.question)
            } else {
              submitQuestionRef.current(intent.question, { aiBlockId: aiBlock.id })
            }
            if (line.trimStart().startsWith("#")) {
              // # 或 ## 本身就是 shell 注释，直接放行 Enter 提交。
              return true
            }
            event.preventDefault()
            if (tracker.seenOsc133) {
              // bash ≥ 4.4：Ctrl-A 移至行首插入 "# " 再回车，原样保留为安全注释。
              queueInput("\x01# \r")
            } else {
              // 兼容无 GNU readline 的 /bin/sh、dash、ash：用 POSIX 通用 Ctrl-U 清行再提交注释。
              const singleLine = line.replace(/[\r\n]+/g, " ").trim()
              queueInput(`\x15# ${singleLine}\r`)
            }
            return false
          }
        }
      }
      return true
    })

    term.onData(queueInput)
    term.onResize(() => scheduleResize())

    const refit = () => {
      if (!host.clientWidth || !host.clientHeight) return
      try { fit.fit() } catch { /* noop */ }
    }
    window.addEventListener("resize", refit)
    const resizeObserver = typeof ResizeObserver !== "undefined" ? new ResizeObserver(() => refit()) : null
    resizeObserver?.observe(host)

    manualDisconnectRef.current = false
    lastDisconnectRecoverableRef.current = false
    reconnectAttemptRef.current = 0
    reconnectBusyRef.current = false
    aliveRef.current = true

    const wakeReconnect = () => {
      if (runRef.current !== run || manualDisconnectRef.current) return
      if (statusRef.current === "reconnecting") {
        void attemptReconnect(run, "resume")
      } else if (statusRef.current === "error" && lastDisconnectRecoverableRef.current) {
        reconnectAttemptRef.current = 0
        void attemptReconnect(run, "resume")
      }
    }
    const onOnline = () => wakeReconnect()
    const onVisibilityChange = () => {
      if (typeof document !== "undefined" && document.visibilityState === "visible") {
        wakeReconnect()
      }
    }
    window.addEventListener("online", onOnline)
    document.addEventListener("visibilitychange", onVisibilityChange)

    void connect(term, run)
      .then((connected) => {
        if (!connected || runRef.current !== run) return
        startReader(run)
      })
      .catch((err) => {
        if (runRef.current !== run) return
        handleDisconnectRef.current(run, err)
      })

    const persistTimer = setInterval(persistCursor, 1000)

    return () => {
      clearInterval(persistTimer)
      window.removeEventListener("online", onOnline)
      document.removeEventListener("visibilitychange", onVisibilityChange)
      unsubscribeTracker()
      tracker.dispose()
      trackerRef.current = null
      resizeObserver?.disconnect()
      window.removeEventListener("resize", refit)
      queueInputRef.current = null
      stopLoops()
      // 只停本地循环：远端 shell 留给「断开」按钮显式关闭，避免刷新即丢会话。
      fitRef.current = null
      termRef.current = null
      term.dispose()
    }
  }, [connect, startReader, attemptReconnect, flushInput, scheduleResize, persistCursor, stopLoops])

  const handlePasteFromClipboard = useCallback(async () => {
    if (!aliveRef.current || !termIdRef.current) return
    try {
      if (typeof navigator === "undefined" || !navigator.clipboard?.readText) {
        flashNotice("当前环境不支持直接读取剪贴板，请用 Ctrl+V / ⌘V 粘贴")
        return
      }
      const text = await navigator.clipboard.readText()
      if (!text) {
        flashNotice("剪贴板中没有文本内容")
        return
      }
      // 统一经 queueInput 入队，确保右键粘贴的文本同步更新乐观行缓冲。
      if (queueInputRef.current) {
        queueInputRef.current(text)
      } else {
        pendingRef.current.push(text)
        if (!inputTimerRef.current) {
          inputTimerRef.current = setTimeout(() => {
            inputTimerRef.current = null
            void flushInput()
          }, 25)
        }
      }
      flashNotice("已粘贴到终端")
    } catch {
      flashNotice("浏览器未授权读取剪贴板，请用 Ctrl+V / ⌘V 粘贴")
    }
  }, [flashNotice, flushInput])

  const openInlineAiComposer = useCallback(() => {
    const draft = trackerRef.current?.atPrompt ? trackerRef.current.readCurrentInput() : optimisticInputRef.current.trim()
    setInlineFocusBlock(null)
    setInlineInitialQuestion(draft.replace(/^(?:##|[#?？]|@ai)\s*/i, ""))
    setInlineOpen((v) => !v)
  }, [])

  const aiMirrorSelection = useMemo(
    () => selectPendingAiMirrorEntries(aiExecutionEntries, mirroredEntryIdsRef.current),
    // mirrorTick 用于在已回显集合更新或输入清空时重算待刷入队列。
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [aiExecutionEntries, mirrorTick],
  )
  const pendingAiMirrorCount = aiMirrorEnabled ? aiMirrorSelection.readyToMirror.length : 0
  pendingMirrorCountRef.current = pendingAiMirrorCount

  // 当交互终端处于空闲提示符且无人工未提交输入时，将 AI 执行完成的脱敏 Shell 命令块
  // 无损回显到 xterm 画布并重建提示符锚点；输入中或全屏程序内自动排队等待空闲。
  useEffect(() => {
    const term = termRef.current
    const tracker = trackerRef.current
    if (!term || aiMirrorSelection.readyToMirror.length === 0) return
    const currentInput = tracker ? tracker.readCurrentInput() : ""
    const safeToFlush = canFlushAiMirrorToPty({
      enabled: aiMirrorEnabled,
      status,
      initialPromptReady,
      inAlternateScreen,
      atPrompt,
      currentInput,
      optimisticInput: optimisticInputRef.current,
    })
    if (!safeToFlush) return

    const prompt = tracker ? tracker.readCurrentPromptText() : { text: "", lines: 0 }
    const formatted = aiMirrorSelection.readyToMirror.map((entry) => formatAiMirrorBlock(entry))
    const payload = buildAiMirrorWritePayload(formatted, {
      text: prompt.text,
      lines: prompt.lines,
      seenOsc133: Boolean(tracker?.seenOsc133),
    })
    if (!payload) return

    for (const entry of aiMirrorSelection.readyToMirror) {
      mirroredEntryIdsRef.current.add(entry.id)
    }
    pendingMirrorCountRef.current = 0
    const run = runRef.current
    tracker?.setSuppressCallbacks(true)
    term.write(payload, () => {
      if (runRef.current === run) {
        trackerRef.current?.setSuppressCallbacks(false)
      }
    })
    setMirrorTick((v) => v + 1)
  }, [
    aiMirrorEnabled,
    aiMirrorSelection,
    atPrompt,
    inAlternateScreen,
    initialPromptReady,
    status,
  ])

  useEffect(() => {
    onChromeState?.({
      status,
      detail,
      cwd,
      actionNotice,
      hasBlocks: blocks.some((b) => b.state !== "prompt" && !b.isAiPrompt && b.command.trim().length > 0),
      aiMirrorEnabled,
      aiMirrorPendingCount: pendingAiMirrorCount,
    })
  }, [actionNotice, aiMirrorEnabled, blocks, cwd, detail, onChromeState, pendingAiMirrorCount, status])

  useEffect(() => {
    if (!actionsRef) return
    actionsRef.current = {
      openInlineAi: openInlineAiComposer,
      reconnect: manualReconnect,
      disconnect: () => { void shutdown() },
      sendHandbackToAi,
      runCommand: (cmd: string, run: boolean) => {
        const safeRun = run && supportsInputSource && !destructiveCommandReason(cmd)
        return sendSuggestionInput(cmd, { run: safeRun, suggestionId: buildSuggestionId("pty", Date.now() % 1000, cmd) })
      },
      toggleAiMirror,
    }
    return () => {
      actionsRef.current = null
    }
  }, [actionsRef, manualReconnect, openInlineAiComposer, sendHandbackToAi, sendSuggestionInput, shutdown, supportsInputSource, toggleAiMirror])

  const statusText =
    status === "ready"
      ? "已连接"
      : status === "connecting"
        ? "连接中…"
        : status === "reconnecting"
          ? "重连中…"
          : status === "closed"
            ? "已结束"
            : "连接失败"

  return (
    <div className={"flex min-h-0 flex-1 flex-col " + (className || "")}>
      {!hideHeader && (
      <div className="flex items-center gap-2 border-b border-white/10 px-2.5 py-1 text-2xs text-muted-foreground">
        <span
          className={
            "inline-block h-2 w-2 shrink-0 rounded-full " +
            (status === "ready"
              ? "bg-success"
              : status === "connecting"
                ? "bg-warning"
                : status === "reconnecting"
                  ? "animate-pulse bg-warning"
                  : "bg-destructive")
          }
        />
        <span data-testid="terminal-status-text" className="shrink-0 whitespace-nowrap">
          {statusText}{detail ? " · " + detail : ""}
        </span>
        {cwd ? <span className="min-w-0 truncate font-mono opacity-70">{cwd}</span> : null}
        <span className="ml-auto flex shrink-0 items-center gap-1.5">
          {actionNotice ? (
            <span role="status" className="max-w-56 truncate text-primary">{actionNotice}</span>
          ) : null}
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={aiDisabled}
            data-testid="terminal-inline-ai-trigger"
            title="终端 AI 提问（⌘I / Ctrl+Shift+I）"
            className="h-6 shrink-0 bg-primary/15 px-2 text-2xs text-primary hover:bg-primary/25 hover:text-primary"
            onClick={openInlineAiComposer}
          >
            AI 提问
          </Button>
          {status === "error" || status === "closed" || status === "reconnecting" ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              data-testid="terminal-reconnect-btn"
              className="h-6 shrink-0 bg-success/15 px-2 text-2xs text-success hover:bg-success/25 hover:text-success"
              onClick={manualReconnect}
            >
              {status === "reconnecting" ? "立即重连" : "重新连接"}
            </Button>
          ) : null}
          {status === "ready" || status === "connecting" || status === "reconnecting" ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              data-testid="terminal-disconnect-btn"
              className="h-6 shrink-0 px-2 text-2xs text-muted-foreground"
              onClick={() => { void shutdown() }}
            >
              断开
            </Button>
          ) : null}
        </span>
      </div>
      )}
      {(aiMirrorSelection.activeRunning || pendingAiMirrorCount > 0) && (
        <div
          data-testid="terminal-ai-live-bar"
          className="flex h-7 shrink-0 items-center gap-2 border-b border-primary/20 bg-primary/10 px-2.5 text-2xs text-primary"
        >
          <span className="inline-block size-1.5 shrink-0 animate-pulse rounded-full bg-primary" />
          {aiMirrorSelection.activeRunning ? (
            <span className="min-w-0 flex-1 truncate font-mono">
              <span className="font-sans font-medium text-primary">AI 执行中：</span>
              {aiMirrorSelection.activeRunning.command
                ? `$ ${aiMirrorSelection.activeRunning.command}`
                : aiMirrorSelection.activeRunning.summary
                  ? `${aiMirrorSelection.activeRunning.toolName} · ${aiMirrorSelection.activeRunning.summary}`
                  : aiMirrorSelection.activeRunning.toolName}
            </span>
          ) : (
            <span className="min-w-0 flex-1 truncate text-muted-foreground">
              AI 新增 {pendingAiMirrorCount} 条命令输出（提示符空闲后自动回显）
            </span>
          )}
          {onFocusAiEntry && aiMirrorSelection.activeRunning && (
            <button
              type="button"
              data-testid="terminal-ai-live-inspect"
              onClick={() => onFocusAiEntry(aiMirrorSelection.activeRunning!.id)}
              className="shrink-0 whitespace-nowrap rounded bg-primary/20 px-1.5 py-0.5 text-2xs text-primary hover:bg-primary/30"
            >
              查看过程
            </button>
          )}
        </div>
      )}
      <TerminalBlocksOverlay
        blocks={blocks}
        seenOsc133={seenOsc133}
        inAlternateScreen={inAlternateScreen}
        aiDisabled={aiDisabled}
        onJumpToBlock={(block) => {
          if (block.promptLine >= 0) termRef.current?.scrollToLine(block.promptLine)
        }}
        onCopyCommand={(block) => {
          void copyTerminalText(block.command).then((ok) => flashNotice(ok ? "已复制命令" : "复制失败"))
        }}
        onCopyOutput={(block) => {
          const out = trackerRef.current?.readOutput(block).text || ""
          void copyTerminalText(out || block.command).then((ok) => flashNotice(ok ? "已复制输出" : "复制失败"))
        }}
        onBlockAiAction={(block, action) => {
          if (action === "followup") {
            setInlineFocusBlock(block)
            setInlineInitialQuestion("")
            setInlineOpen(true)
            return
          }
          const q =
            action === "fix"
              ? `这条命令执行失败（exit ${block.exitCode ?? "非零"}），请定位原因并给出修复命令。`
              : "请解释这条命令的作用与输出结果的关键信息。"
          submitSingleShotQuestion(q, { focusBlock: block, focusAction: action })
        }}
        onEscalateBlock={
          onAskAi
            ? (block) => {
                const q =
                  block.exitCode !== undefined && block.exitCode !== 0
                    ? `命令 \`${block.command}\` 执行失败（exit ${block.exitCode}），请定位原因并给出修复方案。`
                    : `请分析命令 \`${block.command}\` 的输出结果并继续排查。`
                escalateQuestionToAssistant(q, block)
              }
            : undefined
        }
        onAskBlockAsNaturalLanguage={(block) => {
          const cleaned = block.command.replace(/^(?:##|[#?？]|@ai)\s*/i, "")
          if (aiModeRef.current === "assistant" && ctxRef.current.onAskAi) {
            escalateQuestionToAssistant(cleaned)
          } else {
            submitSingleShotQuestion(cleaned, { withHistory: includeHistory })
          }
        }}
        onReopenAiBlock={(block) => {
          const cleaned = block.command.replace(/^(?:##|[#?？]|@ai)\s*/i, "")
          if (aiModeRef.current === "assistant" && ctxRef.current.onAskAi) {
            escalateQuestionToAssistant(cleaned)
          } else {
            submitSingleShotQuestion(cleaned, {
              aiBlockId: block.id,
              withHistory: includeHistory,
            })
          }
        }}
      />
      <div
        ref={hostRef}
        data-testid="terminal-pty-viewport"
        onContextMenu={(event) => {
          if (event.shiftKey) return
          event.preventDefault()
          const selected = termRef.current?.getSelection() || getSelectionTextWithin(hostRef.current)
          setMenuState({ x: event.clientX, y: event.clientY, selection: selected })
        }}
        className="min-h-0 flex-1 overflow-hidden bg-background p-1"
      />
      <TerminalAiInline
        targetLabel={`${target.cluster}/${target.instance}`}
        aiMode={aiMode}
        open={inlineOpen}
        initialQuestion={inlineInitialQuestion}
        contextPreview={inlineContextPreview}
        includeHistory={includeHistory}
        onToggleHistory={() => setIncludeHistory((v) => !v)}
        autoDetect={autoDetect}
        onToggleAutoDetect={handleToggleAutoDetect}
        aiDisabled={aiDisabled}
        aiDisabledReason={aiDisabledReason}
        atPrompt={atPrompt}
        inAlternateScreen={inAlternateScreen}
        supportsInputSource={supportsInputSource}
        aiState={singleShotState}
        onClose={() => {
          setInlineOpen(false)
          setInlineFocusBlock(null)
          termRef.current?.focus()
        }}
        onSubmitQuestion={(q, submittedMode) => {
          const modeToUse = submittedMode ?? aiModeRef.current
          if (modeToUse === "assistant" && ctxRef.current.onAskAi) {
            escalateQuestionToAssistant(q, inlineFocusBlock)
            return
          }
          submitSingleShotQuestion(q, {
            focusBlock: inlineFocusBlock,
            focusAction: inlineFocusBlock ? "followup" : undefined,
            withHistory: includeHistory,
          })
        }}
        onCancelRequest={cancelSingleShotAi}
        onDismissResult={() => {
          setSingleShotState({ status: "idle" })
          termRef.current?.focus()
        }}
        onRetry={(q) => {
          submitSingleShotQuestion(q, { withHistory: includeHistory })
        }}
        onEscalateToAssistant={onAskAi ? escalateQuestionToAssistant : undefined}
        onInsertCommand={(cmd, sid) => sendSuggestionInput(cmd, { run: false, suggestionId: sid })}
        onRunCommand={(cmd, sid) => sendSuggestionInput(cmd, { run: true, suggestionId: sid })}
        onCopyCommand={(cmd) => {
          void copyTerminalText(cmd).then((ok) => flashNotice(ok ? "已复制命令" : "复制失败"))
        }}
      />
      <TerminalContextMenu
        state={menuState}
        onClose={(restoreFocus) => {
          setMenuState(null)
          if (restoreFocus) termRef.current?.focus()
        }}
        onCopy={(text) => {
          void copyTerminalText(text).then((ok) => {
            flashNotice(ok ? "已复制选中内容" : "复制失败，请使用快捷键复制")
          })
        }}
        onSingleShotAi={(text) => {
          const snippet = text.trim().slice(0, 3000)
          if (!snippet) return
          submitSingleShotQuestion(
            `请解释以下终端选中内容的关键信息或报错原因，并给出下一步只读排查命令：\n\`\`\`\n${snippet}\n\`\`\``,
            { focusAction: "explain", withHistory: false },
          )
        }}
        onSendToAi={
          onSendToAi
            ? (text, mode) => {
                void copyTerminalText(text)
                flashNotice(mode === "execute" ? "已复制并发送到 AI 执行" : "已复制并填入 AI 输入框")
                onSendToAi(text, mode, cwd)
              }
            : undefined
        }
        onPaste={() => { void handlePasteFromClipboard() }}
        aiDisabled={aiDisabled}
        aiDisabledReason={aiDisabledReason}
        pasteDisabled={status !== "ready"}
      />
    </div>
  )
}
