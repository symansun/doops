"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { X } from "lucide-react"
import type { Session, Target } from "@/lib/client"
import {
  formatExecutionDuration,
  type AgentExecutionEntry,
} from "@/lib/agent-execution-stream"
import { destructiveCommandReason, type TerminalPrefill } from "@/lib/shell-blocks"
import type { TerminalAiActionMode, TerminalModeKind } from "@/lib/terminal-ai"
import { TerminalPty, type TerminalPtyActions, type TerminalPtyChromeState } from "./terminal-pty"

export type AgentTerminalTab = "log" | "pty"

export function AgentTerminalPane({
  session,
  target,
  sessionId,
  targetOnline,
  activeTab,
  onTabChange,
  entries,
  focusedEntryId,
  onFocusEntry,
  ptyPrefill,
  onPtyPrefillHandled,
  onReplayCommand,
  onSendToAi,
  onAskAi,
  onClose,
}: {
  session: Session
  target: Target
  sessionId: string
  targetOnline: boolean
  activeTab: AgentTerminalTab
  onTabChange: (tab: AgentTerminalTab) => void
  entries: readonly AgentExecutionEntry[]
  focusedEntryId?: string
  onFocusEntry?: (id: string | undefined) => void
  ptyPrefill: TerminalPrefill | null
  onPtyPrefillHandled: (nonce: number, insertedIntoPty: boolean) => void
  onReplayCommand: (command: string, options?: { run?: boolean }) => void
  onSendToAi?: (request: { selection: string; mode: TerminalAiActionMode; cwd?: string; terminalMode: TerminalModeKind }) => void
  onAskAi?: (request: { prompt: string; fallbackPrefill: string }) => Promise<"sent" | "prefilled" | "error"> | void
  onClose: () => void
}) {
  const [onlyShell, setOnlyShell] = useState(false)
  const [ptyMounted, setPtyMounted] = useState(activeTab === "pty")
  const [ptyChrome, setPtyChrome] = useState<TerminalPtyChromeState>({
    status: "connecting",
    detail: "",
    cwd: "",
    actionNotice: "",
    hasBlocks: false,
  })
  const ptyActionsRef = useRef<TerminalPtyActions | null>(null)
  const logRef = useRef<HTMLDivElement>(null)
  const entryRefs = useRef(new Map<string, HTMLDivElement>())
  const prevCountRef = useRef(0)

  useEffect(() => {
    if (activeTab === "pty") setPtyMounted(true)
  }, [activeTab])

  const hasNonShell = useMemo(() => entries.some((e) => !e.isShell), [entries])
  const visibleEntries = useMemo(
    () => (onlyShell ? entries.filter((e) => e.isShell) : entries),
    [entries, onlyShell],
  )

  // 新命令流进来时自动滚到底部，方便实时观看 Agent 执行过程。
  useEffect(() => {
    if (activeTab !== "log") return
    if (visibleEntries.length > prevCountRef.current && !focusedEntryId) {
      logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
    }
    prevCountRef.current = visibleEntries.length
  }, [activeTab, focusedEntryId, visibleEntries.length])

  useEffect(() => {
    if (activeTab !== "log" || !focusedEntryId) return
    const node = entryRefs.current.get(focusedEntryId)
    node?.scrollIntoView({ behavior: "smooth", block: "nearest" })
  }, [activeTab, focusedEntryId])

  const targetOffline = !targetOnline || target.status === "offline"

  return (
    <section
      aria-label="执行过程与交互终端"
      data-testid="agent-terminal-pane"
      className="flex h-full min-h-0 flex-col bg-[#080d18] text-slate-200"
    >
      {/* 极简单行顶栏：执行过程 | 交互终端 + 按需操作 */}
      <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-white/10 bg-[#0d1424] px-2.5 text-xs">
        <button
          type="button"
          data-testid="ask-terminal-tab-log"
          aria-pressed={activeTab === "log"}
          onClick={() => onTabChange("log")}
          className={
            "shrink-0 whitespace-nowrap rounded px-2 py-0.5 transition " +
            (activeTab === "log" ? "bg-sky-500/20 text-sky-200 font-medium" : "text-slate-400 hover:bg-white/5 hover:text-slate-200")
          }
        >
          执行过程{entries.length > 0 ? ` (${entries.length})` : ""}
        </button>
        <button
          type="button"
          data-testid="ask-terminal-tab-pty"
          aria-pressed={activeTab === "pty"}
          onClick={() => onTabChange("pty")}
          className={
            "shrink-0 whitespace-nowrap rounded px-2 py-0.5 transition " +
            (activeTab === "pty" ? "bg-sky-500/20 text-sky-200 font-medium" : "text-slate-400 hover:bg-white/5 hover:text-slate-200")
          }
        >
          交互终端
        </button>
        {activeTab === "pty" && ptyMounted && (
          <span className="flex min-w-0 items-center gap-1.5 pl-1 text-[11px] text-slate-400">
            <span
              className={
                "inline-block h-1.5 w-1.5 shrink-0 rounded-full " +
                (ptyChrome.status === "ready"
                  ? "bg-emerald-400"
                  : ptyChrome.status === "connecting" || ptyChrome.status === "reconnecting"
                    ? "animate-pulse bg-amber-400"
                    : "bg-rose-400")
              }
            />
            <span className="truncate font-mono">{ptyChrome.cwd || (ptyChrome.status === "ready" ? "/workspace" : ptyChrome.detail)}</span>
          </span>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {activeTab === "log" && hasNonShell && (
            <button
              type="button"
              onClick={() => setOnlyShell((v) => !v)}
              className={
                "whitespace-nowrap rounded px-2 py-0.5 text-[11px] transition " +
                (onlyShell ? "bg-white/10 text-slate-100" : "text-slate-400 hover:bg-white/5 hover:text-slate-200")
              }
            >
              仅命令
            </button>
          )}
          {activeTab === "pty" && ptyChrome.actionNotice && (
            <span role="status" className="max-w-[10rem] truncate text-[11px] text-sky-300">
              {ptyChrome.actionNotice}
            </span>
          )}
          {activeTab === "pty" && (
            <button
              type="button"
              data-testid="ask-pty-ai-mirror-toggle"
              aria-pressed={ptyChrome.aiMirrorEnabled !== false}
              title={
                ptyChrome.aiMirrorEnabled !== false
                  ? "已开启：AI 执行的 Shell 命令与输出自动回显到本终端（点击暂停）"
                  : "已暂停 AI 命令回显（点击开启，实时同步 AI 执行命令到本终端）"
              }
              onClick={() => { ptyActionsRef.current?.toggleAiMirror() }}
              className={
                "whitespace-nowrap rounded px-2 py-0.5 text-[11px] transition " +
                (ptyChrome.aiMirrorEnabled !== false
                  ? "bg-white/10 text-sky-200 hover:bg-white/15"
                  : "text-slate-400 hover:bg-white/5 hover:text-slate-200")
              }
            >
              AI 回显
            </button>
          )}
          {activeTab === "pty" && onAskAi && (
            <button
              type="button"
              disabled={targetOffline}
              data-testid="ask-pty-send-to-ai"
              title="将右侧交互终端刚执行的命令与输出发回左侧 AI 继续分析"
              onClick={() => { ptyActionsRef.current?.sendHandbackToAi() }}
              className="whitespace-nowrap rounded bg-sky-500/15 px-2 py-0.5 text-[11px] text-sky-200 hover:bg-sky-500/25 disabled:opacity-40"
            >
              ← 发给 AI
            </button>
          )}
          {activeTab === "pty" && (ptyChrome.status === "error" || ptyChrome.status === "closed" || ptyChrome.status === "reconnecting") && (
            <button
              type="button"
              onClick={() => ptyActionsRef.current?.reconnect()}
              className="whitespace-nowrap rounded bg-emerald-500/15 px-2 py-0.5 text-[11px] text-emerald-300 hover:bg-emerald-500/25"
            >
              重连
            </button>
          )}
          <button
            type="button"
            aria-label="收起终端"
            data-testid="ask-terminal-close"
            onClick={onClose}
            className="rounded p-1 text-slate-400 hover:bg-white/10 hover:text-slate-100"
            title="收起右侧终端"
          >
            <X aria-hidden="true" className="size-3.5" />
          </button>
        </div>
      </div>

      {/* 页签 A：平铺终端风格的 Agent 执行过程流（无冗余卡片套娃） */}
      <div
        ref={logRef}
        hidden={activeTab !== "log"}
        data-testid="agent-execution-log"
        className={activeTab === "log" ? "min-h-0 flex-1 overflow-y-auto bg-[#070b15] px-3 py-2.5 font-mono text-xs leading-relaxed" : "hidden"}
      >
        {visibleEntries.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 font-sans text-xs text-slate-500">
            <span>Agent 执行的命令与输出会实时显示在这里</span>
            <button
              type="button"
              onClick={() => onTabChange("pty")}
              className="rounded px-2.5 py-1 text-sky-300 hover:bg-white/5"
            >
              打开交互终端 →
            </button>
          </div>
        ) : (
          <div className="space-y-2.5">
            {visibleEntries.map((entry) => {
              const focused = focusedEntryId === entry.id
              const failed = entry.status === "failed" || (entry.exitCode !== undefined && entry.exitCode !== 0)
              const running = entry.status === "running" || entry.status === "pending"
              const duration = formatExecutionDuration(entry.durationMs)
              const risky = entry.command ? destructiveCommandReason(entry.command) : null
              return (
                <div
                  key={entry.id}
                  ref={(node) => {
                    if (node) entryRefs.current.set(entry.id, node)
                    else entryRefs.current.delete(entry.id)
                  }}
                  data-testid="agent-exec-entry"
                  data-entry-id={entry.id}
                  onClick={() => onFocusEntry?.(entry.id)}
                  className={
                    "group rounded border-l-2 px-2.5 py-1.5 transition " +
                    (focused
                      ? "border-sky-400 bg-sky-500/10"
                      : failed
                        ? "border-rose-500/60 hover:bg-white/[0.03]"
                        : "border-transparent hover:bg-white/[0.03]")
                  }
                >
                  <div className="flex items-center gap-2">
                    <span
                      className={
                        "shrink-0 select-none " +
                        (running
                          ? "text-sky-400 animate-pulse"
                          : failed
                            ? "text-rose-400"
                            : "text-emerald-400")
                      }
                    >
                      {entry.isShell ? "$" : running ? "●" : failed ? "✗" : "✓"}
                    </span>
                    <span
                      className={
                        "min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-all " +
                        (failed ? "text-rose-200" : "text-slate-100")
                      }
                    >
                      {entry.command || (entry.summary ? `[${entry.toolName}] ${entry.summary}` : entry.toolName)}
                    </span>
                    <div className="flex shrink-0 items-center gap-1.5 font-sans text-[11px]">
                      {entry.exitCode !== undefined && entry.exitCode !== 0 && (
                        <span className="font-mono text-rose-300">exit {entry.exitCode}</span>
                      )}
                      {duration && <span className="font-mono text-slate-500">{duration}</span>}
                      {entry.command && (
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation()
                            onReplayCommand(entry.command!, { run: false })
                          }}
                          title={risky ? `高风险命令：填入交互终端手动确认（${risky}）` : "填入右侧交互终端重跑或改参数"}
                          className="whitespace-nowrap rounded bg-white/10 px-1.5 py-0.5 text-slate-200 opacity-0 transition hover:bg-white/15 group-hover:opacity-100 focus-visible:opacity-100"
                        >
                          在终端重跑
                        </button>
                      )}
                    </div>
                  </div>
                  {entry.cwd && (
                    <div className="mt-0.5 pl-4 text-[10px] text-slate-500">{entry.cwd}</div>
                  )}
                  {entry.output ? (
                    <pre
                      className={
                        "mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all pl-4 text-[11.5px] leading-5 " +
                        (failed ? "text-rose-300/90" : "text-slate-300")
                      }
                    >
                      {entry.output}
                    </pre>
                  ) : running ? (
                    <div className="mt-1 pl-4 font-sans text-[11px] text-slate-500">执行中…</div>
                  ) : null}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* 页签 B：复用同一 PTY 会话的交互终端（人机接管） */}
      {ptyMounted && (
        <div
          hidden={activeTab !== "pty"}
          className={activeTab === "pty" ? "flex min-h-0 flex-1 flex-col" : "hidden"}
        >
          <TerminalPty
            key={`ask-pty:${target.key}:${sessionId}`}
            session={session}
            target={target}
            sessionId={sessionId}
            targetOnline={targetOnline}
            hideHeader
            aiMode="assistant"
            onChromeState={setPtyChrome}
            actionsRef={ptyActionsRef}
            aiDisabled={targetOffline}
            aiDisabledReason={targetOffline ? "目标离线" : undefined}
            insertPrefill={ptyPrefill}
            onInsertPrefillHandled={onPtyPrefillHandled}
            aiExecutionEntries={entries}
            onFocusAiEntry={(id) => {
              onFocusEntry?.(id)
              onTabChange("log")
            }}
            onAskAi={onAskAi}
            onSendToAi={
              onSendToAi
                ? (selection, actionMode, cwd) => onSendToAi({ selection, mode: actionMode, cwd, terminalMode: "pty" })
                : undefined
            }
          />
        </div>
      )}
    </section>
  )
}
