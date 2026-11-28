"use client"

import { ChevronRight, Terminal } from "lucide-react"
import { Markdown } from "@/components/markdown"
import { agentRunTraceLines, type AgentRun } from "@/lib/agent-output"
import { extractShellBlocks } from "@/lib/shell-blocks"
import { Button } from "@/components/ui"

// Result area of the DoSql, DoWeb, and declarative Skill plugin pages: the answer
// as Markdown, tool trace in a collapsed section, runnable shell blocks that can
// prefill the interactive terminal (never auto-run), and errors as plain red text.
export function AgentRunOutput({
  run,
  running,
  empty,
  onFillTerminal,
}: {
  run: AgentRun | null
  running: boolean
  empty: string
  onFillTerminal?: (command: string) => void
}) {
  if (!run) {
    return <div className="flex min-h-32 items-center justify-center text-center text-xs text-muted-foreground">{empty}</div>
  }
  const trace = agentRunTraceLines(run)
  const answer = run.answer.trim()
  const errors = run.notes.filter((note) => note.kind === "err")
  const shellBlocks = !running && answer && onFillTerminal ? extractShellBlocks(answer).slice(0, 6) : []
  return (
    <div className="space-y-3">
      {run.notes.filter((note) => note.kind === "info").map((note, index) => (
        <p key={index} className="whitespace-pre-wrap break-words font-mono text-xs leading-5 text-muted-foreground">{note.text}</p>
      ))}
      {trace.length > 0 && (
        <details className="group rounded-md border bg-muted/30">
          <summary className="flex cursor-pointer list-none items-center gap-1.5 px-2.5 py-1.5 text-xs text-muted-foreground hover:text-foreground">
            <ChevronRight aria-hidden="true" className="size-3.5 transition-transform group-open:rotate-90" />
            执行过程（{trace.length} 条）
          </summary>
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words border-t px-2.5 py-2 font-mono text-xs leading-5 text-muted-foreground">{trace.join("\n")}</pre>
        </details>
      )}
      {answer ? <Markdown content={answer} allowImages={false} streaming={running} /> : !running && errors.length === 0 && (
        <p className="text-xs text-muted-foreground">Agent 未返回结果正文。</p>
      )}
      {shellBlocks.length > 0 && onFillTerminal && (
        <div className="space-y-1.5 rounded-md border bg-muted/20 p-2.5">
          <div className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
            <Terminal className="size-3.5 text-primary" aria-hidden="true" />
            <span>终端联动命令（点击仅预填至交互终端，按回车确认执行）</span>
          </div>
          <div className="space-y-1.5">
            {shellBlocks.map((block, idx) => (
              <div key={`${idx}-${block.command.slice(0, 32)}`} className="flex items-start justify-between gap-2 rounded border bg-background/80 px-2.5 py-1.5">
                <code className="min-w-0 flex-1 whitespace-pre-wrap break-all font-mono text-[11px] text-foreground">{block.command}</code>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="shrink-0"
                  onClick={() => onFillTerminal(block.command)}
                >
                  填入终端
                </Button>
              </div>
            ))}
          </div>
        </div>
      )}
      {errors.map((note, index) => (
        <pre key={index} className="whitespace-pre-wrap break-words font-mono text-xs leading-5 text-destructive">{note.text}</pre>
      ))}
    </div>
  )
}
