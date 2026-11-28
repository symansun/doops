"use client"

import { useState } from "react"
import type { TerminalBlock } from "@/lib/terminal-shell-integration"
import type { TerminalFocusAction } from "@/lib/terminal-ai-context"
import { looksLikeNaturalLanguageMistype } from "@/lib/terminal-intent"
import { SparkIcon } from "./icons"
import { Button } from "./ui"

export function TerminalBlocksOverlay({
  blocks,
  seenOsc133,
  inAlternateScreen,
  aiDisabled,
  onJumpToBlock,
  onCopyCommand,
  onCopyOutput,
  onBlockAiAction,
  onEscalateBlock,
  onAskBlockAsNaturalLanguage,
  onReopenAiBlock,
}: {
  blocks: readonly TerminalBlock[]
  seenOsc133: boolean
  inAlternateScreen: boolean
  aiDisabled?: boolean
  onJumpToBlock: (block: TerminalBlock) => void
  onCopyCommand: (block: TerminalBlock) => void
  onCopyOutput: (block: TerminalBlock) => void
  onBlockAiAction: (block: TerminalBlock, action: TerminalFocusAction) => void
  onEscalateBlock?: (block: TerminalBlock) => void
  onAskBlockAsNaturalLanguage?: (block: TerminalBlock) => void
  onReopenAiBlock?: (block: TerminalBlock) => void
}) {
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const visibleBlocks = blocks.filter((b) => b.state !== "prompt" && b.command.trim()).slice(-5)
  // 极简界面：空终端不展示静态占位引导条，有真实命令记录或进入全屏程序时才按需显现。
  if (!inAlternateScreen && (!seenOsc133 || visibleBlocks.length === 0)) return null

  const activeBlock =
    visibleBlocks.find((b) => b.id === selectedId) ?? visibleBlocks[visibleBlocks.length - 1] ?? null

  return (
    <div
      data-testid="terminal-blocks-bar"
      className="flex flex-wrap items-center gap-1.5 border-b border-white/10 bg-card px-2.5 py-1 text-2xs text-foreground/85"
    >
      {inAlternateScreen ? (
        <span className="text-warning">全屏程序运行中</span>
      ) : (
        <>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
            {visibleBlocks.map((block) => {
              const isSelected = activeBlock?.id === block.id
              const failed = block.state === "done" && block.exitCode !== undefined && block.exitCode !== 0
              const dotClass = block.isAiPrompt
                ? "bg-primary"
                : block.state === "running"
                  ? "bg-primary animate-pulse"
                  : block.state === "interrupted"
                    ? "bg-warning"
                    : failed
                      ? "bg-destructive"
                      : "bg-success/80"
              return (
                <button
                  key={block.id}
                  type="button"
                  onClick={() => {
                    setSelectedId(block.id)
                    onJumpToBlock(block)
                  }}
                  title={`${block.command}${block.exitCode !== undefined ? ` (exit ${block.exitCode})` : ""}`}
                  className={
                    "inline-flex max-w-52 items-center gap-1.5 rounded px-1.5 py-0.5 font-mono transition " +
                    (isSelected ? "bg-white/15 text-white" : "bg-white/5 text-foreground/85 hover:bg-white/10")
                  }
                >
                  <span className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${dotClass}`} />
                  <span className="truncate">{block.command}</span>
                  {failed && <span className="shrink-0 text-destructive">exit {block.exitCode}</span>}
                </button>
              )
            })}
          </div>
          {activeBlock && (
            <div className="flex shrink-0 items-center gap-1">
              {activeBlock.isAiPrompt ? (
                onReopenAiBlock ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={aiDisabled}
                    onClick={() => onReopenAiBlock(activeBlock)}
                    className="h-5 bg-primary/20 px-1.5 text-2xs text-primary hover:bg-primary/30 hover:text-primary"
                  >
                    重问
                  </Button>
                ) : null
              ) : (
                <>
                  {activeBlock.state === "done" &&
                    activeBlock.exitCode === 127 &&
                    onAskBlockAsNaturalLanguage &&
                    looksLikeNaturalLanguageMistype(activeBlock.command) && (
                      <Button
                        type="button"
                        size="sm"
                        variant="primary"
                        disabled={aiDisabled}
                        onClick={() => onAskBlockAsNaturalLanguage(activeBlock)}
                        className="h-5 px-1.5 text-2xs"
                      >
                        <SparkIcon width={11} height={11} aria-hidden="true" />
                        <span>转 AI</span>
                      </Button>
                    )}
                  {activeBlock.state === "done" && activeBlock.exitCode !== undefined && activeBlock.exitCode !== 0 && (
                    <Button
                      type="button"
                      size="sm"
                      variant="danger-outline"
                      disabled={aiDisabled}
                      onClick={() => onBlockAiAction(activeBlock, "fix")}
                      className="h-5 px-1.5 text-2xs"
                    >
                      <SparkIcon width={11} height={11} aria-hidden="true" />
                      <span>修复</span>
                    </Button>
                  )}
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={aiDisabled}
                    onClick={() => onBlockAiAction(activeBlock, "explain")}
                    className="h-5 px-1.5 text-2xs text-primary hover:text-primary"
                  >
                    解释
                  </Button>
                  {onEscalateBlock && (
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={aiDisabled}
                      onClick={() => onEscalateBlock(activeBlock)}
                      className="h-5 px-1.5 text-2xs text-foreground/85 hover:text-primary"
                      title="附带该命令输出直接派给 AI 助手分析"
                    >
                      派给 AI
                    </Button>
                  )}
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => onCopyOutput(activeBlock)}
                    className="h-5 px-1.5 text-2xs text-muted-foreground"
                    title="复制输出"
                  >
                    复制
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => onCopyCommand(activeBlock)}
                    className="sr-only"
                  >
                    复制命令
                  </Button>
                </>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
