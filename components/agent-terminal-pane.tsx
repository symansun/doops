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
import { Button } from "./ui"

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
      className="flex h-full min-h-0 flex-col bg-background text-foreground/95"
    >
      {/* 极简单行顶栏：执行过程 | 交互终端 + 按需操作 */}
      <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-white/10 bg-card px-2.5 text-xs">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          data-testid="ask-terminal-tab-log"
          aria-pressed={activeTab === "log"}
          onClick={() => onTabChange("log")}
          className={
            "h-6 shrink-0 px-2 text-xs " +
            (activeTab === "log" ? "bg-primary/20 font-medium text-primary hover:bg-primary/25 hover:text-primary" : "text-muted-foreground")
          }
        >
          执行过程{entries.length > 0 ? ` (${entries.length})` : ""}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          data-testid="ask-terminal-tab-pty"
          aria-pressed={activeTab === "pty"}
          onClick={() => onTabChange("pty")}
          className={
            "h-6 shrink-0 px-2 text-xs " +
            (activeTab === "pty" ? "bg-primary/20 font-medium text-primary hover:bg-primary/25 hover:text-primary" : "text-muted-foreground")
          }
        >
          交互终端
        </Button>
        {activeTab === "pty" && ptyMounted && (
          <span className="flex min-w-0 items-center gap-1.5 pl-1 text-2xs text-muted-foreground">
            <span
              className={
                "inline-block h-1.5 w-1.5 shrink-0 rounded-full " +
                (ptyChrome.status === "ready"
                  ? "bg-success"
                  : ptyChrome.status === "connecting" || ptyChrome.status === "reconnecting"
                    ? "animate-pulse bg-warning"
                    : "bg-destructive")
              }
            />
            <span className="truncate font-mono">{ptyChrome.cwd || (ptyChrome.status === "ready" ? "/workspace" : ptyChrome.detail)}</span>
          </span>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {activeTab === "log" && hasNonShell && (
            <Button
              type="button"
              size="sm"
              variant={onlyShell ? "secondary" : "ghost"}
              onClick={() => setOnlyShell((v) => !v)}
              className="h-6 px-2 text-2xs"
            >
              仅命令
            </Button>
          )}
          {activeTab === "pty" && ptyChrome.actionNotice && (
            <span role="status" className="max-w-40 truncate text-2xs text-primary">
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
                "whitespace-nowrap rounded px-2 py-0.5 text-2xs transition " +
                (ptyChrome.aiMirrorEnabled !== false
                  ? "bg-primary/15 text-primary hover:bg-primary/25"
                  : "text-muted-foreground hover:bg-muted/30 hover:text-foreground")
              }
            >
              AI 回显
            </button>
          )}
          {activeTab === "pty" && onAskAi && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={targetOffline}
              data-testid="ask-pty-send-to-ai"
              title="将右侧交互终端刚执行的命令与输出发回左侧 AI 继续分析"
              onClick={() => { ptyActionsRef.current?.sendHandbackToAi() }}
              className="h-6 bg-primary/15 px-2 text-2xs text-primary hover:bg-primary/25 hover:text-primary"
            >
              ← 发给 AI
            </Button>
          )}
          {activeTab === "pty" && (ptyChrome.status === "error" || ptyChrome.status === "closed" || ptyChrome.status === "reconnecting") && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => ptyActionsRef.current?.reconnect()}
              className="h-6 bg-success/15 px-2 text-2xs text-success hover:bg-success/25 hover:text-success"
            >
              重连
            </Button>
          )}
          <Button
            type="button"
            size="icon-xs"
            variant="ghost"
            aria-label="收起终端"
            data-testid="ask-terminal-close"
            onClick={onClose}
            className="text-muted-foreground"
            title="收起右侧终端"
          >
            <X aria-hidden="true" className="size-3.5" />
          </Button>
        </div>
      </div>

      {/* 页签 A：平铺终端风格的 Agent 执行过程流（无冗余卡片套娃） */}
      <div
        ref={logRef}
        hidden={activeTab !== "log"}
        data-testid="agent-execution-log"
        className={activeTab === "log" ? "min-h-0 flex-1 overflow-y-auto bg-background px-3 py-2.5 font-mono text-xs leading-relaxed" : "hidden"}
      >
        {visibleEntries.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 font-sans text-xs text-muted-foreground/80">
            <span>Agent 执行的命令与输出会实时显示在这里</span>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => onTabChange("pty")}
              className="text-primary hover:text-primary"
            >
              打开交互终端 →
            </Button>
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
                      ? "border-primary bg-primary/10"
                      : failed
                        ? "border-destructive/60 hover:bg-white/[0.03]"
                        : "border-transparent hover:bg-white/[0.03]")
                  }
                >
                  <div className="flex items-center gap-2">
                    <span
                      className={
                        "shrink-0 select-none " +
                        (running
                          ? "text-primary animate-pulse"
                          : failed
                            ? "text-destructive"
                            : "text-success")
                      }
                    >
                      {entry.isShell ? "$" : running ? "●" : failed ? "✗" : "✓"}
                    </span>
                    <span
                      className={
                        "min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-all " +
                        (failed ? "text-destructive" : "text-foreground")
                      }
                    >
                      {entry.command || (entry.summary ? `[${entry.toolName}] ${entry.summary}` : entry.toolName)}
                    </span>
                    <div className="flex shrink-0 items-center gap-1.5 font-sans text-2xs">
                      {entry.exitCode !== undefined && entry.exitCode !== 0 && (
                        <span className="font-mono text-destructive">exit {entry.exitCode}</span>
                      )}
                      {duration && <span className="font-mono text-muted-foreground/80">{duration}</span>}
                      {entry.command && (
                        <Button
                          type="button"
                          size="sm"
                          variant="secondary"
                          onClick={(e) => {
                            e.stopPropagation()
                            onReplayCommand(entry.command!, { run: false })
                          }}
                          title={risky ? `高风险命令：填入交互终端手动确认（${risky}）` : "填入右侧交互终端重跑或改参数"}
                          className="h-5 px-1.5 text-2xs opacity-0 transition group-hover:opacity-100 focus-visible:opacity-100"
                        >
                          在终端重跑
                        </Button>
                      )}
                    </div>
                  </div>
                  {entry.cwd && (
                    <div className="mt-0.5 pl-4 text-2xs text-muted-foreground/80">{entry.cwd}</div>
                  )}
                  {entry.output ? (
                    <pre
                      className={
                        "mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all pl-4 text-2xs leading-5 " +
                        (failed ? "text-destructive/90" : "text-foreground/85")
                      }
                    >
                      {entry.output}
                    </pre>
                  ) : running ? (
                    <div className="mt-1 pl-4 font-sans text-2xs text-muted-foreground/80">执行中…</div>
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
