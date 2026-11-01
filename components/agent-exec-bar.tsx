"use client"

import { useMemo, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target, type ToolCancel } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import { latestAssistantShellBlocks, shellToolArguments, type AssistantMessageLike } from "@/lib/shell-blocks"
import { SendIcon, StopIcon, TerminalIcon } from "./icons"

interface ExecRun {
  command: string
  output: string
  error: string
  running: boolean
}

// AgentExecBar 是 AI 助手的“AI exec”入口：DoAgent 本身保持只读，命令一律由用户
// 确认后通过终端页同一条 doops_shell 路径执行（Gateway exec 授权 + 审计）。
export function AgentExecBar({
  session,
  target,
  sessionId,
  items,
  onRunInTerminal,
}: {
  session: Session
  target: Target
  sessionId: string
  items: readonly AssistantMessageLike[]
  onRunInTerminal?: (command: string) => void
}) {
  const blocks = useMemo(() => latestAssistantShellBlocks(items), [items])
  const [open, setOpen] = useState(false)
  const [command, setCommand] = useState("")
  const [runs, setRuns] = useState<ExecRun[]>([])
  const [stopPending, setStopPending] = useState(false)
  const cancelRef = useRef<ToolCancel | null>(null)
  const running = runs.some((run) => run.running)

  function update(index: number, change: (run: ExecRun) => ExecRun) {
    setRuns((prev) => prev.map((run, i) => (i === index ? change(run) : run)))
  }

  async function run() {
    if (running || stopPending) return
    let args: ReturnType<typeof shellToolArguments>
    try {
      args = shellToolArguments(sessionId, command)
    } catch {
      return
    }
    const index = runs.length
    setRuns((prev) => [...prev, { command: args.command, output: "", error: "", running: true }])
    setCommand("")
    cancelRef.current = null
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.shell,
          arguments: args,
          onCancelReady: (cancel) => { cancelRef.current = cancel },
        },
        (ev) => {
          if (ev.type === "output") update(index, (r) => ({ ...r, output: r.output + ev.data }))
          else if (ev.type === "result") {
            const txt = extractText(ev.result)
            if (txt) update(index, (r) => ({ ...r, output: r.output + txt }))
          }
        },
      )
    } catch (err) {
      if ((err as Error).name !== "AbortError") update(index, (r) => ({ ...r, error: (err as Error).message }))
    } finally {
      update(index, (r) => ({ ...r, running: false }))
      setStopPending(false)
      cancelRef.current = null
    }
  }

  function stop() {
    if (!cancelRef.current || stopPending) return
    setStopPending(true)
    void cancelRef.current().catch(() => setStopPending(false))
  }

  if (!open && blocks.length === 0) {
    return (
      <div className="flex justify-end px-4 pt-2">
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="flex h-7 items-center gap-1.5 rounded-md border border-border px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <TerminalIcon width={13} height={13} />
          执行命令
        </button>
      </div>
    )
  }

  return (
    <div className="mx-4 mt-2 flex max-h-[45%] shrink-0 flex-col gap-2 overflow-y-auto rounded-md border border-border bg-muted/30 p-2 text-xs">
      {blocks.length > 0 && (
        <div className="flex flex-col gap-1">
          <div className="text-muted-foreground">助手建议的命令（需你确认后执行，走 doops_shell 审计路径）</div>
          {blocks.map((block) => (
            <div key={block.command} className="flex items-start gap-2">
              <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-all rounded bg-background/70 px-2 py-1 font-mono">{block.command}</pre>
              {onRunInTerminal && (
                <button
                  type="button"
                  onClick={() => onRunInTerminal(block.command)}
                  className="shrink-0 rounded-md border border-border px-2 py-1 hover:bg-muted"
                >
                  在终端执行
                </button>
              )}
              <button
                type="button"
                onClick={() => { setOpen(true); setCommand(block.command) }}
                className="shrink-0 rounded-md border border-border px-2 py-1 hover:bg-muted"
              >
                在此执行
              </button>
            </div>
          ))}
        </div>
      )}
      {!open ? (
        <div className="flex justify-end">
          <button type="button" onClick={() => setOpen(true)} className="flex items-center gap-1.5 rounded-md px-2 py-1 text-muted-foreground hover:bg-muted hover:text-foreground">
            <TerminalIcon width={13} height={13} />
            执行命令
          </button>
        </div>
      ) : (
        <>
          {runs.map((run, i) => (
            <div key={i} className="font-mono">
              <div className="flex gap-2"><span className="select-none text-primary">$</span><span className="whitespace-pre-wrap break-all">{run.command}</span></div>
              {run.output && <pre className="whitespace-pre-wrap break-all text-muted-foreground">{run.output}</pre>}
              {run.error && <pre className="whitespace-pre-wrap break-all text-destructive">{run.error}</pre>}
              {run.running && <div className="text-muted-foreground">执行中…</div>}
            </div>
          ))}
          <div className="flex items-end gap-2">
            <textarea
              aria-label="在当前机器执行的命令"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault()
                  void run()
                }
              }}
              rows={1}
              placeholder={`在 ${target.instance} 上执行（doops_shell，需要 exec 权限）`}
              className="min-h-8 flex-1 resize-y rounded-md border border-border bg-background px-2 py-1.5 font-mono outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
            {running ? (
              <button type="button" aria-label="停止" onClick={stop} disabled={stopPending} className="flex size-8 items-center justify-center rounded-md border border-border hover:bg-muted disabled:opacity-40">
                <StopIcon width={14} height={14} />
              </button>
            ) : (
              <button type="button" aria-label="执行" onClick={() => void run()} disabled={!command.trim()} className="flex size-8 items-center justify-center rounded-md border border-border hover:bg-muted disabled:opacity-40">
                <SendIcon width={14} height={14} />
              </button>
            )}
            <button type="button" onClick={() => setOpen(false)} disabled={running} className="h-8 rounded-md px-2 text-muted-foreground hover:bg-muted disabled:opacity-40">收起</button>
          </div>
        </>
      )}
    </div>
  )
}
