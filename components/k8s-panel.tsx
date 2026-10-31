"use client"

import { Boxes, Play, Square } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target, type ToolCancel } from "@/lib/client"
import {
  EMPTY_K8S_INPUT,
  K8S_OPERATIONS,
  isK8sMutation,
  k8sSummary,
  runK8s,
  type K8sField,
  type K8sInput,
  type K8sOperation,
} from "@/lib/k8s"

interface K8sLine {
  kind: "out" | "err" | "info"
  text: string
}

const READ_OPERATIONS = (Object.keys(K8S_OPERATIONS) as K8sOperation[]).filter((op) => !K8S_OPERATIONS[op].mutation)
const WRITE_OPERATIONS = (Object.keys(K8S_OPERATIONS) as K8sOperation[]).filter((op) => K8S_OPERATIONS[op].mutation)

const OUTPUT_OPTIONS = ["", "wide", "yaml", "json", "name"]

const NAME_LABEL: Partial<Record<K8sOperation, string>> = {
  get: "名称（可选）",
  describe: "资源名称",
  "helm-history": "Release",
  "helm-status": "Release",
  "helm-values": "Release",
  cordon: "节点名称",
  uncordon: "节点名称",
  "delete-pod": "Pod 名称",
}

const RESOURCE_PLACEHOLDER: Partial<Record<K8sOperation, string>> = {
  logs: "pod/api-0 或 deployment/api",
  events: "可选：deployment/api",
  "rollout-status": "deployment/api",
  "rollout-restart": "deployment/api",
  scale: "deployment/api",
}

const inputClass = "min-w-0 rounded-md border bg-background px-3 py-2 text-sm outline-none focus:border-ring disabled:opacity-60"

export function K8sPanel({
  session,
  target,
  sessionId,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  onRunningChange: (running: boolean) => void
}) {
  const [input, setInput] = useState<K8sInput>(EMPTY_K8S_INPUT)
  const [lines, setLines] = useState<K8sLine[]>([])
  const [running, setRunning] = useState(false)
  const [stopPending, setStopPending] = useState(false)
  const [error, setError] = useState("")
  const cancelRef = useRef<ToolCancel | null>(null)
  const logRef = useRef<HTMLPreElement>(null)
  const spec = K8S_OPERATIONS[input.operation]
  const mutation = isK8sMutation(input.operation)
  const uses = (field: K8sField) => spec.fields.includes(field)

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [lines])

  function update(patch: Partial<K8sInput>) {
    // Any change to what will be sent invalidates a previous write confirmation.
    setInput((current) => ({ ...current, ...patch, confirm: patch.confirm ?? false }))
    setError("")
  }

  function selectOperation(operation: K8sOperation) {
    const kind = operation === "top" ? "pods" : operation === "get" || operation === "describe" ? input.kind || "pods" : input.kind
    update({ operation, kind })
  }

  function push(line: K8sLine) {
    setLines((current) => [...current, line])
  }

  async function run() {
    if (running || stopPending) return
    setError("")
    setRunning(true)
    setStopPending(false)
    onRunningChange(true)
    cancelRef.current = null
    let started = false
    try {
      await runK8s(
        (opts) => {
          started = true
          setLines([{ kind: "info", text: `目标 ${target.cluster}/${target.instance} · ${spec.label}` }])
          return callTool(
            session,
            { ...opts, onCancelReady: (cancel) => { cancelRef.current = cancel } },
            (event) => {
              if (event.type === "output") push({ kind: "out", text: event.data })
              if (event.type === "result") {
                const text = extractText(event.result)
                if (text) push({ kind: "info", text })
              }
            },
          )
        },
        target,
        sessionId,
        input,
      )
      if (mutation) setInput((current) => ({ ...current, confirm: false }))
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        const message = (err as Error).message
        setError(message)
        if (started) push({ kind: "err", text: message })
      }
    } finally {
      setRunning(false)
      setStopPending(false)
      onRunningChange(false)
      cancelRef.current = null
    }
  }

  function stop() {
    if (!cancelRef.current || stopPending) return
    setStopPending(true)
    push({ kind: "info", text: "已请求停止，等待远端终态；请勿立即重试" })
    void cancelRef.current().catch((err) => {
      setStopPending(false)
      push({ kind: "err", text: `停止请求失败：${(err as Error).message}` })
    })
  }

  const disabled = running || stopPending

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b bg-card px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <Boxes aria-hidden="true" className="size-4 text-primary" />
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold text-foreground">Kubernetes 运维</h2>
            <p className="truncate text-xs text-muted-foreground">doops_k8s · {target.cluster}/{target.instance} · 与 CLI doops k8s 同一工具</p>
          </div>
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4">
        <div className="flex flex-col gap-2">
          <OperationGroup title="只读" operations={READ_OPERATIONS} active={input.operation} disabled={disabled} onSelect={selectOperation} />
          <OperationGroup title="写操作（需确认）" operations={WRITE_OPERATIONS} active={input.operation} disabled={disabled} onSelect={selectOperation} danger />
        </div>
        <p className="font-mono text-xs text-muted-foreground">doops k8s {spec.cli}</p>

        <div className="grid gap-2 sm:grid-cols-3">
          {uses("kind") && (input.operation === "top" ? (
            <select aria-label="资源类型" value={input.kind} disabled={disabled} onChange={(e) => update({ kind: e.target.value })} className={inputClass}>
              <option value="pods">pods</option>
              <option value="nodes">nodes</option>
            </select>
          ) : (
            <input aria-label="资源类型" value={input.kind} disabled={disabled} onChange={(e) => update({ kind: e.target.value })} placeholder="资源类型，如 pods、deployment" className={inputClass} />
          ))}
          {uses("name") && (
            <input aria-label={NAME_LABEL[input.operation] || "名称"} value={input.name} disabled={disabled} onChange={(e) => update({ name: e.target.value })} placeholder={NAME_LABEL[input.operation] || "名称"} className={inputClass} />
          )}
          {uses("resource") && (
            <input aria-label="资源 kind/name" value={input.resource} disabled={disabled} onChange={(e) => update({ resource: e.target.value })} placeholder={RESOURCE_PLACEHOLDER[input.operation] || "kind/name"} className={inputClass} />
          )}
          {uses("namespace") && (
            <input aria-label="命名空间" value={input.namespace} disabled={disabled || input.allNamespaces} onChange={(e) => update({ namespace: e.target.value })} placeholder={input.operation.startsWith("helm-") && input.operation !== "helm-list" ? "命名空间（必填）" : "命名空间（留空为默认）"} className={inputClass} />
          )}
          {uses("selector") && (
            <input aria-label="标签选择器" value={input.selector} disabled={disabled} onChange={(e) => update({ selector: e.target.value })} placeholder="标签选择器，如 app=api" className={inputClass} />
          )}
          {uses("output") && (
            <select aria-label="输出格式" value={OUTPUT_OPTIONS.includes(input.output) ? input.output : ""} disabled={disabled} onChange={(e) => update({ output: e.target.value })} className={inputClass}>
              {OUTPUT_OPTIONS.map((value) => <option key={value} value={value}>{value ? `输出 ${value}` : "默认输出"}</option>)}
            </select>
          )}
          {uses("container") && (
            <input aria-label="容器" value={input.container} disabled={disabled} onChange={(e) => update({ container: e.target.value })} placeholder="容器（可选）" className={inputClass} />
          )}
          {uses("tail") && (
            <input aria-label="日志行数" inputMode="numeric" value={input.tail} disabled={disabled} onChange={(e) => update({ tail: e.target.value })} placeholder="最近行数，如 200" className={inputClass} />
          )}
          {uses("since") && (
            <input aria-label="时间范围" value={input.since} disabled={disabled} onChange={(e) => update({ since: e.target.value })} placeholder="时间范围，如 10m" className={inputClass} />
          )}
          {uses("timeout") && (
            <input aria-label="超时" value={input.timeout} disabled={disabled} onChange={(e) => update({ timeout: e.target.value })} placeholder="超时，如 120s" className={inputClass} />
          )}
          {uses("replicas") && (
            <input aria-label="副本数" inputMode="numeric" value={input.replicas} disabled={disabled} onChange={(e) => update({ replicas: e.target.value })} placeholder="副本数" className={inputClass} />
          )}
        </div>
        <div className="flex flex-wrap gap-4 text-xs text-muted-foreground">
          {uses("allNamespaces") && (
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={input.allNamespaces} disabled={disabled} onChange={(e) => update({ allNamespaces: e.target.checked, namespace: e.target.checked ? "" : input.namespace })} />
              全部命名空间（-A）
            </label>
          )}
          {uses("previous") && (
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={input.previous} disabled={disabled} onChange={(e) => update({ previous: e.target.checked })} />
              上一个容器实例（--previous）
            </label>
          )}
        </div>
        {input.operation === "helm-values" && <p className="text-xs text-muted-foreground">只返回 values 的键路径，不返回值。</p>}
        {(input.operation === "get" || input.operation === "describe") && <p className="text-xs text-muted-foreground">Secret 与 ServiceAccount 不开放读取。</p>}

        {mutation && (
          <label className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-foreground">
            <input type="checkbox" className="mt-0.5" checked={input.confirm} disabled={disabled} onChange={(e) => update({ confirm: e.target.checked })} />
            <span>
              我确认在 {target.cluster}/{target.instance} 上执行：<strong>{k8sSummary(input)}</strong>。此操作会改变集群状态（对应 CLI 的 --confirm）。
            </span>
          </label>
        )}

        <div className="flex items-center gap-2">
          {running ? (
            <button type="button" onClick={stop} disabled={stopPending || !cancelRef.current} className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs text-foreground disabled:opacity-50">
              <Square aria-hidden="true" className="size-3.5" /> {stopPending ? "正在停止" : "停止"}
            </button>
          ) : (
            <button type="button" onClick={() => void run()} disabled={disabled || (mutation && !input.confirm)} className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium disabled:opacity-50 ${mutation ? "bg-destructive text-destructive-foreground" : "bg-primary text-primary-foreground"}`}>
              <Play aria-hidden="true" className="size-3.5" /> {mutation ? "确认执行" : "执行"}
            </button>
          )}
          {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
        </div>

        <pre ref={logRef} aria-label="执行输出" className="min-h-48 flex-1 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs">
          {lines.length === 0 ? <span className="text-muted-foreground">选择操作并执行，输出显示在这里。</span> : lines.map((line, index) => (
            <span key={index} className={line.kind === "err" ? "text-destructive" : line.kind === "info" ? "text-muted-foreground" : "text-foreground"}>
              {line.text.endsWith("\n") ? line.text : line.text + "\n"}
            </span>
          ))}
        </pre>
      </div>
    </div>
  )
}

function OperationGroup({
  title,
  operations,
  active,
  disabled,
  danger,
  onSelect,
}: {
  title: string
  operations: K8sOperation[]
  active: K8sOperation
  disabled: boolean
  danger?: boolean
  onSelect: (operation: K8sOperation) => void
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={title}>
      <span className="w-28 shrink-0 text-xs text-muted-foreground">{title}</span>
      {operations.map((operation) => (
        <button
          key={operation}
          type="button"
          aria-pressed={active === operation}
          disabled={disabled}
          onClick={() => onSelect(operation)}
          className={`rounded-md border px-2 py-1 text-xs transition-colors disabled:opacity-50 ${
            active === operation
              ? danger ? "border-destructive bg-destructive text-destructive-foreground" : "border-primary bg-primary text-primary-foreground"
              : danger ? "text-destructive hover:border-destructive/60" : "text-muted-foreground hover:border-primary/50 hover:text-foreground"
          }`}
        >
          {K8S_OPERATIONS[operation].label}
        </button>
      ))}
    </div>
  )
}
