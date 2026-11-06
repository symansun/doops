"use client"

import { ChevronRight } from "lucide-react"
import { Markdown } from "@/components/markdown"
import { agentRunTraceLines, type AgentRun } from "@/lib/agent-output"

// Result area of the DoSql and DoWeb pages: the answer as Markdown, tool trace
// in a collapsed section, errors as plain red text.
export function AgentRunOutput({ run, running, empty }: { run: AgentRun | null; running: boolean; empty: string }) {
  if (!run) {
    return <div className="flex min-h-32 items-center justify-center text-center text-xs text-muted-foreground">{empty}</div>
  }
  const trace = agentRunTraceLines(run)
  const answer = run.answer.trim()
  const errors = run.notes.filter((note) => note.kind === "err")
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
      {answer ? <Markdown content={answer} allowImages={false} /> : !running && errors.length === 0 && (
        <p className="text-xs text-muted-foreground">Agent 未返回结果正文。</p>
      )}
      {errors.map((note, index) => (
        <pre key={index} className="whitespace-pre-wrap break-words font-mono text-xs leading-5 text-destructive">{note.text}</pre>
      ))}
    </div>
  )
}
