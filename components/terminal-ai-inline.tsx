"use client"

import { useEffect, useRef, useState } from "react"
import type { TerminalInlineContextResult } from "@/lib/terminal-ai-context"
import type { TerminalAiDispatchMode } from "@/lib/terminal-intent"
import { Button, Checkbox, Textarea } from "./ui"
import { SendIcon, SparkIcon } from "./icons"

export interface TerminalSingleShotCommand {
  id: string
  command: string
  description?: string
  risk: string | null
}

export type TerminalSingleShotState =
  | { status: "idle" }
  | { status: "loading"; question: string; aiBlockId?: string }
  | {
      status: "ready"
      id: string
      question: string
      model: string
      provider: string
      explanation: string
      commands: TerminalSingleShotCommand[]
      aiBlockId?: string
    }
  | {
      status: "error"
      question: string
      message: string
      code?: string
      nextAction?: string
      aiBlockId?: string
    }

export function TerminalAiInline({
  targetLabel,
  aiMode = "suggest",
  open,
  initialQuestion,
  contextPreview,
  includeHistory,
  onToggleHistory,
  autoDetect,
  onToggleAutoDetect,
  aiDisabled,
  aiDisabledReason,
  atPrompt,
  inAlternateScreen,
  supportsInputSource,
  aiState,
  onClose,
  onSubmitQuestion,
  onCancelRequest,
  onDismissResult,
  onRetry,
  onEscalateToAssistant,
  onInsertCommand,
  onRunCommand,
  onCopyCommand,
}: {
  targetLabel: string
  aiMode?: TerminalAiDispatchMode
  open: boolean
  initialQuestion: string
  contextPreview: TerminalInlineContextResult | null
  includeHistory: boolean
  onToggleHistory: () => void
  autoDetect: boolean
  onToggleAutoDetect: (next: boolean) => void
  aiDisabled?: boolean
  aiDisabledReason?: string
  atPrompt: boolean
  inAlternateScreen: boolean
  supportsInputSource: boolean
  aiState: TerminalSingleShotState
  onClose: () => void
  onSubmitQuestion: (question: string, mode?: TerminalAiDispatchMode) => void
  onCancelRequest: () => void
  onDismissResult: () => void
  onRetry: (question: string) => void
  onEscalateToAssistant?: (question: string) => void
  onInsertCommand: (command: string, suggestionId: string) => Promise<boolean> | void
  onRunCommand: (command: string, suggestionId: string) => Promise<boolean> | void
  onCopyCommand: (command: string) => void
}) {
  const [question, setQuestion] = useState(initialQuestion)
  const [showPreview, setShowPreview] = useState(false)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (open) {
      setQuestion(initialQuestion)
      setShowPreview(false)
      setTimeout(() => inputRef.current?.focus(), 0)
    }
  }, [open, initialQuestion])

  const canInsert = atPrompt && !inAlternateScreen
  const insertDisabledReason = inAlternateScreen
    ? "处于全屏程序，暂不可插入"
    : !atPrompt
      ? "等待当前命令结束回到提示符"
      : undefined

  const hasResultCard = aiState.status !== "idle"
  if (!open && !hasResultCard) return null

  const submitAs = (mode: TerminalAiDispatchMode) => {
    const trimmed = question.trim()
    if (!trimmed || aiDisabled) return
    onSubmitQuestion(trimmed, mode)
    setQuestion("")
    onClose()
  }

  return (
    <div className="shrink-0 space-y-1.5 border-t border-white/10 bg-card px-3 py-2 text-xs text-foreground/95">
      {aiState.status === "loading" && (
        <div
          data-testid="terminal-ai-loading"
          className="flex items-center justify-between gap-2 px-1 py-0.5"
        >
          <div className="flex min-w-0 items-center gap-2 text-primary">
            <SparkIcon width={12} height={12} aria-hidden="true" className="shrink-0 animate-pulse" />
            <span className="shrink-0">生成命令中…</span>
            <span className="truncate text-muted-foreground">{aiState.question}</span>
          </div>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            data-testid="terminal-ai-cancel-btn"
            onClick={onCancelRequest}
            className="h-6 shrink-0 px-2 text-2xs text-muted-foreground"
          >
            取消
          </Button>
        </div>
      )}

      {aiState.status === "error" && (
        <div
          data-testid="terminal-ai-error"
          role="alert"
          className="flex items-center justify-between gap-2 rounded border border-destructive/30 bg-destructive/15 px-2.5 py-1.5"
        >
          <div className="min-w-0">
            <span className="text-destructive">{aiState.message}</span>
            {aiState.nextAction && <span className="ml-2 text-2xs text-destructive/80">{aiState.nextAction}</span>}
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={() => onRetry(aiState.question)}
              className="h-6 px-2 text-2xs"
            >
              重试
            </Button>
            {onEscalateToAssistant && (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => {
                  onDismissResult()
                  onEscalateToAssistant(aiState.question)
                }}
                className="h-6 bg-primary/15 px-2 text-2xs text-primary hover:bg-primary/25 hover:text-primary"
              >
                派给 AI
              </Button>
            )}
            <Button
              type="button"
              size="icon-xs"
              variant="ghost"
              aria-label="关闭错误提示"
              onClick={onDismissResult}
              className="text-muted-foreground"
            >
              ×
            </Button>
          </div>
        </div>
      )}

      {aiState.status === "ready" && (
        <div data-testid="terminal-ai-suggestions" className="space-y-1.5">
          <div className="flex items-center justify-between gap-2 text-2xs">
            <div className="flex min-w-0 items-center gap-1.5 text-primary" title={targetLabel}>
              <SparkIcon width={12} height={12} aria-hidden="true" className="shrink-0" />
              <span className="shrink-0 font-medium">建议</span>
              {aiState.explanation && (
                <span data-testid="terminal-ai-explanation" className="truncate text-foreground/85">
                  {aiState.explanation}
                </span>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-1">
              {onEscalateToAssistant && (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  data-testid="terminal-ai-escalate-btn"
                  onClick={() => {
                    onDismissResult()
                    onEscalateToAssistant(aiState.question)
                  }}
                  className="h-6 px-2 text-2xs text-muted-foreground hover:text-primary"
                  title="交给 AI 助手自主多步排查"
                >
                  派给 AI →
                </Button>
              )}
              <Button
                type="button"
                size="icon-xs"
                variant="ghost"
                data-testid="terminal-ai-dismiss-btn"
                aria-label="收起建议"
                onClick={onDismissResult}
                className="text-muted-foreground"
              >
                ×
              </Button>
            </div>
          </div>

          {aiState.commands.length > 0 && (
            <div className="max-h-44 space-y-1 overflow-y-auto">
              {aiState.commands.map((sug) => {
                const canRun = canInsert && supportsInputSource && !sug.risk
                const runTitle = sug.risk
                  ? `高风险命令仅支持插入后手动回车：${sug.risk}`
                  : !supportsInputSource
                    ? "旧版 Agent 不支持来源过滤，仅支持插入后手动回车"
                    : insertDisabledReason || "经安全过滤后直接在当前终端运行"
                return (
                  <div
                    key={sug.id}
                    className="flex flex-wrap items-center justify-between gap-2 rounded bg-black/35 px-2.5 py-1.5"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="overflow-x-auto whitespace-pre-wrap break-all font-mono text-xs text-foreground">
                        <span className="select-none text-primary">$ </span>
                        {sug.command}
                      </div>
                      {sug.risk && (
                        <div className="mt-0.5 text-2xs text-destructive">
                          ⚠ 高风险：{sug.risk}（仅插入）
                        </div>
                      )}
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        disabled={!canInsert}
                        title={insertDisabledReason || "插入到终端提示符（不自动回车）"}
                        onClick={() => {
                          void Promise.resolve(onInsertCommand(sug.command, sug.id)).then((ok) => {
                            if (ok !== false) onDismissResult()
                          })
                        }}
                      >
                        插入
                      </Button>
                      {!sug.risk && (
                        <Button
                          type="button"
                          size="sm"
                          variant="primary"
                          disabled={!canRun}
                          title={runTitle}
                          onClick={() => {
                            void Promise.resolve(onRunCommand(sug.command, sug.id)).then((ok) => {
                              if (ok !== false) onDismissResult()
                            })
                          }}
                        >
                          运行
                        </Button>
                      )}
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        onClick={() => onCopyCommand(sug.command)}
                      >
                        复制
                      </Button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
          {insertDisabledReason && aiState.commands.length > 0 && (
            <div className="text-2xs text-warning">{insertDisabledReason}</div>
          )}
        </div>
      )}

      {open && (
        <div
          data-testid="terminal-ai-inline-composer"
          onKeyDown={(e) => {
            if (e.key === "Escape" && !e.nativeEvent.isComposing && e.nativeEvent.keyCode !== 229) {
              e.preventDefault()
              e.stopPropagation()
              onClose()
            }
          }}
          className="space-y-1.5"
        >
          <div className="flex flex-wrap items-center justify-between gap-2 text-2xs text-muted-foreground">
            <div className="flex flex-wrap items-center gap-2">
              {contextPreview && (
                <span className="inline-flex items-center gap-1.5">
                  <span>{contextPreview.summaryLabel}</span>
                  <Button
                    type="button"
                    size="sm"
                    variant="link"
                    onClick={() => setShowPreview((v) => !v)}
                    className="h-auto p-0 text-2xs"
                  >
                    {showPreview ? "隐藏" : "预览"}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={onToggleHistory}
                    className="h-5 px-1.5 text-2xs text-muted-foreground"
                  >
                    {includeHistory ? "不带历史" : "带历史"}
                  </Button>
                </span>
              )}
              <label
                className="inline-flex cursor-pointer items-center gap-1.5 whitespace-nowrap"
                title="开启后，在终端提示符直接输入中文自然语言并回车即可触发 AI"
              >
                <Checkbox
                  checked={autoDetect}
                  onChange={(e) => onToggleAutoDetect(e.target.checked)}
                />
                <span>自然语言识别</span>
              </label>
            </div>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={onClose}
              className="h-5 px-1.5 text-2xs text-muted-foreground"
            >
              Esc
            </Button>
          </div>

          {showPreview && contextPreview && (
            <pre className="max-h-32 overflow-auto rounded border border-white/10 bg-black/40 p-2 font-mono text-2xs text-foreground/85 whitespace-pre-wrap break-all">
              {contextPreview.prompt}
            </pre>
          )}

          <div className="flex items-end gap-1.5">
            <Textarea
              ref={inputRef}
              scale="sm"
              aria-label="终端 AI 提问"
              disabled={aiDisabled}
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return
                if (e.key === "Escape") {
                  e.preventDefault()
                  onClose()
                  return
                }
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault()
                  const alternateMode: TerminalAiDispatchMode = aiMode === "assistant" ? "suggest" : "assistant"
                  submitAs(e.metaKey || e.ctrlKey ? alternateMode : aiMode)
                }
              }}
              rows={1}
              placeholder={
                aiDisabled
                  ? aiDisabledReason || "AI 当前不可用"
                  : aiMode === "assistant"
                    ? "输入排查任务（Enter 派给 AI 执行 · ⌘↵ 仅提示命令）"
                    : "输入问题（Enter 提示命令 · ⌘↵ 派给 AI 执行）"
              }
              className="min-h-8 flex-1 resize-none border-white/15 bg-black/40 text-xs text-foreground placeholder:text-muted-foreground/80"
            />
            <Button
              type="button"
              size="sm"
              variant={aiMode === "suggest" ? "primary" : "ghost"}
              disabled={aiDisabled || !question.trim()}
              title="模式 1：单次生成建议命令，不打开 AI 助手"
              onClick={() => submitAs("suggest")}
            >
              <span>提示命令</span>
            </Button>
            {onEscalateToAssistant && (
              <Button
                type="button"
                size="sm"
                variant={aiMode === "assistant" ? "primary" : "ghost"}
                disabled={aiDisabled || !question.trim()}
                title="模式 2：附带终端上下文，直接拍给 AI 助手自主执行"
                onClick={() => submitAs("assistant")}
              >
                <SendIcon width={12} height={12} aria-hidden="true" />
                <span>派给 AI</span>
              </Button>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
