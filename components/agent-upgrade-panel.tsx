"use client"

import { useEffect, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { RefreshIcon, RocketIcon, StopIcon } from "./icons"

const agentImagePattern = /^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:\/[a-z0-9]+(?:[._-][a-z0-9]+)*)(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}|@sha256:[a-f0-9]{64})$/

export function AgentUpgradePanel({ session, target, sessionId }: { session: Session; target: Target; sessionId: string }) {
  const [image, setImage] = useState("")
  const [commit, setCommit] = useState("")
  const [ci, setCI] = useState("")
  const [confirmed, setConfirmed] = useState(false)
  const [running, setRunning] = useState(false)
  const [output, setOutput] = useState("")
  const [error, setError] = useState("")
  const abortRef = useRef<AbortController | null>(null)
  useEffect(() => () => abortRef.current?.abort(), [])

  async function invoke(arguments_: Record<string, unknown>, signal: AbortSignal, verifyProtocol = false) {
    let result = ""
    await callTool(session, { cluster: target.cluster, instance: target.instance, tool: "doops_agent_upgrade", arguments: { session_id: sessionId, ...arguments_ }, signal, requiredProtocol: verifyProtocol ? "structuredSSH" : undefined }, (event) => {
      if (event.type === "result") result = extractText(event.result)
    })
    return result.trim()
  }
  async function run(rollback: boolean, preview: boolean) {
    if (running || (!preview && !confirmed)) return
    setError("")
    if (!rollback && !agentImagePattern.test(image)) { setError("Agent 镜像必须为 repository:tag 或 repository@sha256:<64 位 digest>"); return }
    if (!preview && !rollback && (!/^[a-f0-9]{40}$/.test(commit) || !ci.startsWith("https://"))) { setError("请填写完整来源提交和成功 CI 记录 URL"); return }
    const ac = new AbortController()
    abortRef.current = ac
    setRunning(true)
    setOutput("")
    try {
      const accepted = await invoke(rollback ? { rollback: true } : { image, dry_run: preview, mode: "k8s" }, ac.signal)
      setOutput(accepted)
      if (preview) {
        if (!accepted.startsWith("dry-run: ")) throw new Error("Agent 未返回有效预览")
        return
      }
      const fields = Object.fromEntries(accepted.split("\n", 1)[0].split(/\s+/).slice(1).map((field) => field.split("=")))
      if (!accepted.startsWith("self-upgrade-accepted ") || !fields.job || !fields.namespace || !fields.release) throw new Error("Agent 未接受升级 Job；请核对控制器版本与 Helm 运行配置")
      const deadline = Date.now() + 21 * 60 * 1000
      while (Date.now() < deadline) {
        await new Promise<void>((resolve, reject) => {
          const cancel = () => { clearTimeout(timer); reject(new DOMException("操作已取消", "AbortError")) }
          const timer = setTimeout(() => { ac.signal.removeEventListener("abort", cancel); resolve() }, 5000)
          if (ac.signal.aborted) cancel()
          else ac.signal.addEventListener("abort", cancel, { once: true })
        })
        const observation = await invoke({ observe_job: fields.job, namespace: fields.namespace }, ac.signal)
        setOutput(observation)
        const header = observation.split("\n", 1)[0]
        if (!header.includes(`job=${fields.job}`) || !header.includes(`namespace=${fields.namespace}`)) throw new Error("观察结果与本次升级 Job 不一致")
        if (header.startsWith("self-upgrade-failed ")) throw new Error("升级 Job 失败，请查看结果并验证回滚状态")
        if (header.startsWith("self-upgrade-succeeded ")) {
          await invoke({ observe_job: fields.job, namespace: fields.namespace }, ac.signal, true)
          setOutput(`${observation}\nJob 与 structuredSSH 协议已核验。仍需核对 Gateway 实际服务 Pod 的 UID、imageID、运行版本与功能验收。`)
          return
        }
        if (!header.startsWith("self-upgrade-running ")) throw new Error("Agent 返回了无效的升级状态")
      }
      throw new Error("升级 Job 观察超时，需要核对目标状态")
    } catch (err) { setError((err as Error).name === "AbortError" ? "已停止页面观察；已提交的 Job 继续由集群内控制器执行" : (err as Error).message) }
    finally { setRunning(false); abortRef.current = null; setConfirmed(false) }
  }
  return (
    <section className="mx-auto flex max-w-3xl flex-col gap-3 px-4 py-5">
      <h2 className="text-base font-medium">升级现有 Agent</h2>
      <p className="break-all text-sm text-muted-foreground">{target.cluster}/{target.instance} · {sessionId}</p>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {([ ["Agent 镜像", image, setImage], ["Agent 来源提交", commit, setCommit], ["Agent CI 记录 URL", ci, setCI] ] as const).map(([label, value, setter]) => <label key={label} className="flex min-w-0 flex-col gap-1 text-xs text-muted-foreground">{label}<input aria-label={label} value={value} disabled={running} onChange={(e) => { setter(e.target.value); setConfirmed(false) }} className="min-w-0 rounded-md border bg-background px-3 py-2 text-sm text-foreground" /></label>)}
      </div>
      <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={confirmed} disabled={running} onChange={(e) => setConfirmed(e.target.checked)} className="mt-1" />授权本次 Agent 变更，已核对 CI 来源和回滚范围</label>
      <div className="flex flex-wrap gap-2">
        <button disabled={running || !image} onClick={() => run(false, true)} className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm disabled:opacity-50"><RefreshIcon width={14} height={14} />参数预览</button>
        <button disabled={running || !confirmed || !image} onClick={() => run(false, false)} className="flex items-center gap-2 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50"><RocketIcon width={14} height={14} />确认升级</button>
        <button disabled={running || !confirmed} onClick={() => run(true, false)} className="rounded-md border px-3 py-2 text-sm disabled:opacity-50">回滚前一成功版本</button>
        {running && <button onClick={() => abortRef.current?.abort()} className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm"><StopIcon width={14} height={14} />停止观察</button>}
      </div>
      {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
      {output && <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md border p-3 text-xs">{output}</pre>}
    </section>
  )
}
