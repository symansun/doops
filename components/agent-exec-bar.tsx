"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { callTool, extractText, rpcManifestWarning, type Session, type Target, type ToolCancel } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import {
  agentExecBarDisplayMode,
  appendShellOutput,
  describeShellFailure,
  destructiveCommandReason,
  latestAssistantShellBlocks,
  shellResultOutcome,
  shellToolArguments,
  type AssistantMessageLike,
  type ShellFailure,
} from "@/lib/shell-blocks"
import { commandJournal } from "@/lib/console-persistence"
import { Button, StatusBanner, Textarea } from "./ui"
import { SendIcon, StopIcon, TerminalIcon } from "./icons"

interface ExecRun {
  command: string
  output: string
  progress: string
  status: "running" | "ok" | "failed"
  failure?: ShellFailure
  manifestWarning?: string
}

// AgentExecBar 是 AI 助手的“AI exec”入口：DoAgent 本身保持只读，命令一律由用户
// 主动提交后通过终端页同一条 doops_shell 路径执行（Gateway 目标授权 + 审计）。
export function AgentExecBar({
  session,
  target,
  sessionId,
  items,
  splitMode = false,
  onRunInTerminal,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  items: readonly AssistantMessageLike[]
  splitMode?: boolean
  onRunInTerminal?: (command: string, options?: { run?: boolean }) => void
  onRunningChange?: (running: boolean) => void
}) {
  const blocks = useMemo(
    () => latestAssistantShellBlocks(items).map((block) => ({ ...block, risk: destructiveCommandReason(block.command) })),
    [items],
  )
  const [open, setOpen] = useState(false)
  const [command, setCommand] = useState("")
  const [runs, setRuns] = useState<ExecRun[]>([])
  const [stopPending, setStopPending] = useState(false)
  const [stopError, setStopError] = useState("")
  const cancelRef = useRef<ToolCancel | null>(null)
  const stopRequestedRef = useRef(false)
  // Gateway 已确认取消（remote_canceled）：随后回来的 execution_outcome_unknown 是取消的
  // 预期结果，不是执行失败，见 lib/shell-blocks.ts 的 describeShellFailure。
  const cancelConfirmedRef = useRef(false)
  const running = runs.some((run) => run.status === "running")
  const where = `${target.cluster}/${target.instance}`

  useEffect(() => {
    onRunningChange?.(running)
  }, [running, onRunningChange])

  function update(index: number, change: (run: ExecRun) => ExecRun) {
    setRuns((prev) => prev.map((run, i) => (i === index ? change(run) : run)))
  }

  function fail(index: number, message: string) {
    const failure = describeShellFailure(message, {
      stopRequested: stopRequestedRef.current,
      cancelConfirmed: cancelConfirmedRef.current,
      target,
    })
    update(index, (r) => ({ ...r, progress: "", status: "failed", failure }))
  }

  function submit() {
    if (running || stopPending || !command.trim()) return
    void execute(command)
  }

  async function execute(raw: string) {
    if (running || stopPending) return
    let args: ReturnType<typeof shellToolArguments>
    try {
      args = shellToolArguments(sessionId, raw)
    } catch {
      return
    }
    const index = runs.length
    stopRequestedRef.current = false
    cancelConfirmedRef.current = false
    setRuns((prev) => [...prev, { command: args.command, output: "", progress: "", status: "running" }])
    // Only a short redacted summary is kept, so a reload can say a command was
    // cut off; it is cleared when the command ends.
    const journalId = commandJournal.begin(session, { targetKey: target.key, sessionId, command: args.command, source: "exec" })
    setCommand("")
    setStopError("")
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
          const warning = rpcManifestWarning(ev)
          if (warning) update(index, (r) => ({ ...r, manifestWarning: warning }))
          if (ev.type === "output") update(index, (r) => ({ ...r, ...appendShellOutput(r, ev.data) }))
          else if (ev.type === "cancel") {
            // 和终端一样：停止的处理进度必须说出来；只有 Gateway 真的确认取消才算数。
            if (ev.state === "remote_canceled") {
              cancelConfirmedRef.current = true
              setStopError("")
              update(index, (r) => ({ ...r, progress: "— 远端已确认取消，等待终态 —" }))
            } else if (ev.state === "rejected" || ev.state === "unavailable") {
              stopRequestedRef.current = false
              setStopPending(false)
              setStopError("停止没有生效：" + (ev.error || "Gateway 未确认取消"))
            }
          } else if (ev.type === "result") {
            const outcome = shellResultOutcome(extractText(ev.result))
            if (outcome.ok) update(index, (r) => ({ ...r, output: r.output + outcome.text, progress: "", status: "ok" }))
            else fail(index, outcome.text)
          }
        },
      )
    } catch (err) {
      fail(index, (err as Error).message)
    } finally {
      setRuns((prev) => prev.map((r, i) => (i === index && r.status === "running"
        ? { ...r, progress: "", status: "failed", failure: describeShellFailure("连接已结束，未收到工具执行结果", { stopRequested: false, target }) }
        : r)))
      commandJournal.end(session, journalId)
      setStopPending(false)
      cancelConfirmedRef.current = false
      cancelRef.current = null
    }
  }

  function stop() {
    if (!cancelRef.current || stopPending) return
    stopRequestedRef.current = true
    setStopPending(true)
    setStopError("")
    void cancelRef.current().catch((err) => {
      stopRequestedRef.current = false
      setStopPending(false)
      setStopError(`停止请求失败：${(err as Error).message}`)
    })
  }

  const displayMode = agentExecBarDisplayMode({ splitMode, open, blockCount: blocks.length })
  if (displayMode === "hidden") {
    return null
  }
  if (displayMode === "toggle") {
    return (
      <div className="flex justify-end px-4 pt-2">
        <Button
          type="button"
          size="sm"
          onClick={() => setOpen(true)}
        >
          <TerminalIcon width={13} height={13} />
          执行命令
        </Button>
      </div>
    )
  }

  return (
    <div className="mx-4 mt-2 flex max-h-64 shrink-0 flex-col gap-2 overflow-y-auto rounded-md border border-border bg-muted/30 p-2 text-xs">
      {blocks.length > 0 && (
        <div className="flex flex-col gap-1">
          <div className="text-muted-foreground">助手建议的命令 · {where}</div>
          {blocks.map((block) => (
            <div key={block.command} className="flex items-start gap-2">
              <div className="min-w-0 flex-1">
                <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded bg-background/70 px-2 py-1 font-mono">{block.command}</pre>
                {block.risk && <div className="mt-0.5 text-destructive">高风险：{block.risk}</div>}
              </div>
              {onRunInTerminal && (
                <Button
                  type="button"
                  size="sm"
                  variant={splitMode ? "primary" : "default"}
                  onClick={() => onRunInTerminal(block.command, { run: false })}
                >
                  填入终端
                </Button>
              )}
              {onRunInTerminal && !block.risk && splitMode ? (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  title="经安全过滤后直接在同屏交互终端运行"
                  onClick={() => onRunInTerminal(block.command, { run: true })}
                  disabled={running || stopPending}
                >
                  终端运行
                </Button>
              ) : (
                <Button
                  type="button"
                  size="sm"
                  variant={splitMode ? "ghost" : "default"}
                  title={splitMode ? "通过独立 doops_shell 单条执行（不经过同屏交互终端）" : undefined}
                  onClick={() => { setOpen(true); void execute(block.command) }}
                  disabled={running || stopPending}
                >
                  {splitMode ? "单条执行" : "在此执行"}
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      {!open ? (
        splitMode ? null : (
          <div className="flex justify-end">
            <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(true)}>
              <TerminalIcon width={13} height={13} />
              执行命令
            </Button>
          </div>
        )
      ) : (
        <>
          {runs.map((run, i) => (
            <div key={i} className="font-mono">
              <div className="flex gap-2"><span className="select-none text-primary">$</span><span className="whitespace-pre-wrap break-all">{run.command}</span></div>
              {run.output && <pre className="whitespace-pre-wrap break-all text-muted-foreground">{run.output}</pre>}
              {run.manifestWarning && <StatusBanner tone="warning" compact>{run.manifestWarning}</StatusBanner>}
              {run.status === "running" && <div className="text-muted-foreground">{run.progress || "执行中…"}</div>}
              {run.status === "ok" && <div className="text-muted-foreground">✓ 完成，退出码 0</div>}
              {run.status === "failed" && run.failure && (
                run.failure.tone === "warn" ? (
                  <StatusBanner tone="warning" compact className="mt-1">
                    {run.failure.text}
                  </StatusBanner>
                ) : (
                  <div
                    role={run.failure.tone === "error" ? "alert" : "status"}
                    className={run.failure.tone === "info" ? "text-muted-foreground" : "text-destructive"}
                  >
                    {run.failure.text}
                  </div>
                )
              )}
            </div>
          ))}
          {stopError && <div role="alert" className="text-destructive">{stopError}</div>}
          <div className="flex items-end gap-2">
            <Textarea
              scale="sm"
              aria-label="在当前机器执行的命令"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  submit()
                }
              }}
              rows={1}
              placeholder={`在 ${target.instance} 上执行（doops_shell，需要该目标的授权）`}
              className="flex-1 resize-y font-mono"
            />
            {running ? (
              <Button type="button" size="sm" icon variant="danger-outline" aria-label="停止" title={stopPending ? "停止中…" : "停止"} onClick={stop} disabled={stopPending}>
                <StopIcon width={14} height={14} />
              </Button>
            ) : (
              <Button type="button" size="sm" icon variant="primary" aria-label="执行" title="执行" onClick={() => submit()} disabled={!command.trim()}>
                <SendIcon width={14} height={14} />
              </Button>
            )}
            <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(false)} disabled={running}>收起</Button>
          </div>
        </>
      )}
    </div>
  )
}
