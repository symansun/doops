"use client"

import { useState } from "react"
import type { TerminalBlock } from "@/lib/terminal-shell-integration"
import type { TerminalFocusAction } from "@/lib/terminal-ai-context"
import { looksLikeNaturalLanguageMistype } from "@/lib/terminal-intent"
import { SparkIcon } from "./icons"

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
      className="flex flex-wrap items-center gap-1.5 border-b border-white/10 bg-[#0d1424] px-2.5 py-1 text-[11px] text-slate-300"
    >
      {inAlternateScreen ? (
        <span className="text-amber-300">全屏程序运行中</span>
      ) : (
        <>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
            {visibleBlocks.map((block) => {
              const isSelected = activeBlock?.id === block.id
              const failed = block.state === "done" && block.exitCode !== undefined && block.exitCode !== 0
              const dotClass = block.isAiPrompt
                ? "bg-sky-400"
                : block.state === "running"
                  ? "bg-blue-400 animate-pulse"
                  : block.state === "interrupted"
                    ? "bg-amber-400"
                    : failed
                      ? "bg-rose-400"
                      : "bg-emerald-400/80"
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
                    "inline-flex max-w-[13rem] items-center gap-1.5 rounded px-1.5 py-0.5 font-mono transition " +
                    (isSelected ? "bg-white/15 text-white" : "bg-white/5 text-slate-300 hover:bg-white/10")
                  }
                >
                  <span className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${dotClass}`} />
                  <span className="truncate">{block.command}</span>
                  {failed && <span className="shrink-0 text-rose-300">exit {block.exitCode}</span>}
                </button>
              )
            })}
          </div>
          {activeBlock && (
            <div className="flex shrink-0 items-center gap-1">
              {activeBlock.isAiPrompt ? (
                onReopenAiBlock ? (
                  <button
                    type="button"
                    disabled={aiDisabled}
                    onClick={() => onReopenAiBlock(activeBlock)}
                    className="whitespace-nowrap rounded bg-sky-500/20 px-1.5 py-0.5 text-sky-200 hover:bg-sky-500/30 disabled:opacity-40"
                  >
                    重问
                  </button>
                ) : null
              ) : (
                <>
                  {activeBlock.state === "done" &&
                    activeBlock.exitCode === 127 &&
                    onAskBlockAsNaturalLanguage &&
                    looksLikeNaturalLanguageMistype(activeBlock.command) && (
                      <button
                        type="button"
                        disabled={aiDisabled}
                        onClick={() => onAskBlockAsNaturalLanguage(activeBlock)}
                        className="inline-flex items-center gap-1 whitespace-nowrap rounded bg-sky-500/25 px-1.5 py-0.5 text-sky-100 hover:bg-sky-500/35 disabled:opacity-40"
                      >
                        <SparkIcon width={11} height={11} aria-hidden="true" />
                        <span>转 AI</span>
                      </button>
                    )}
                  {activeBlock.state === "done" && activeBlock.exitCode !== undefined && activeBlock.exitCode !== 0 && (
                    <button
                      type="button"
                      disabled={aiDisabled}
                      onClick={() => onBlockAiAction(activeBlock, "fix")}
                      className="inline-flex items-center gap-1 whitespace-nowrap rounded bg-rose-500/20 px-1.5 py-0.5 text-rose-200 hover:bg-rose-500/30 disabled:opacity-40"
                    >
                      <SparkIcon width={11} height={11} aria-hidden="true" />
                      <span>修复</span>
                    </button>
                  )}
                  <button
                    type="button"
                    disabled={aiDisabled}
                    onClick={() => onBlockAiAction(activeBlock, "explain")}
                    className="whitespace-nowrap rounded px-1.5 py-0.5 text-sky-300 hover:bg-white/10 disabled:opacity-40"
                  >
                    解释
                  </button>
                  {onEscalateBlock && (
                    <button
                      type="button"
                      disabled={aiDisabled}
                      onClick={() => onEscalateBlock(activeBlock)}
                      className="whitespace-nowrap rounded px-1.5 py-0.5 text-slate-300 hover:bg-white/10 hover:text-sky-200 disabled:opacity-40"
                      title="附带该命令输出直接派给 AI 助手分析"
                    >
                      派给 AI
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => onCopyOutput(activeBlock)}
                    className="whitespace-nowrap rounded px-1.5 py-0.5 text-slate-400 hover:bg-white/10 hover:text-slate-200"
                    title="复制输出"
                  >
                    复制
                  </button>
                  <button
                    type="button"
                    onClick={() => onCopyCommand(activeBlock)}
                    className="sr-only"
                  >
                    复制命令
                  </button>
                </>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
