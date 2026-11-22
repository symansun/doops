"use client"

import { Play, Square } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target, type ToolCancel } from "@/lib/client"
import {
  EMPTY_K8S_INPUT,
  EMPTY_K8S_LOG,
  K8S_LOG_MAX_LINES,
  K8S_OPERATIONS,
  K8S_OUTPUT_HINTS,
  appendK8sLog,
  createK8sFormStore,
  describeK8sFailure,
  isK8sMutation,
  k8sFields,
  k8sResultHint,
  runK8s,
  type K8sField,
  type K8sInput,
  type K8sLog,
  type K8sLogKind,
  type K8sOperation,
} from "@/lib/k8s"
import type { K8sOverviewKey, K8sResource } from "@/lib/k8s-overview"
import { Button, Checkbox, Input, Select, StatusBanner } from "./ui"
import { K8sResourceOverview } from "./k8s-resource-overview"

const READ_OPERATIONS = (Object.keys(K8S_OPERATIONS) as K8sOperation[]).filter((op) => !K8S_OPERATIONS[op].mutation)
const WRITE_OPERATIONS = (Object.keys(K8S_OPERATIONS) as K8sOperation[]).filter((op) => K8S_OPERATIONS[op].mutation)

const OUTPUT_OPTIONS = ["", "wide", "yaml", "json", "name"]

const NAME_LABEL: Partial<Record<K8sOperation, string>> = {
  get: "名称（可选）",
  "configmap-keys": "ConfigMap 名称（可选）",
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

// Raw output kept for explaining a failure; kubectl prints its error near the end.
const FAILURE_EVIDENCE_CHARS = 16_000

// The panel unmounts when the tab changes. The form is kept for the same login
// and target only, so reopening the tab on another target or after another
// login never shows the previous namespace.
const savedForms = createK8sFormStore()

export function K8sPanel({
  session,
  target,
  sessionId,
  initialNamespace,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  initialNamespace?: string
  onRunningChange: (running: boolean) => void
}) {
  const targetKey = target.key
  const [input, setInput] = useState<K8sInput>(() => savedForms.load(session, targetKey))
  const [log, setLog] = useState<K8sLog>(EMPTY_K8S_LOG)
  const [running, setRunning] = useState(false)
  const [stopPending, setStopPending] = useState(false)
  const [error, setError] = useState("")
  const cancelRef = useRef<ToolCancel | null>(null)
  const stopRequestedRef = useRef(false)
  const evidenceRef = useRef("")
  const operationsRef = useRef<HTMLDetailsElement>(null)
  const logRef = useRef<HTMLPreElement>(null)
  const spec = K8S_OPERATIONS[input.operation]
  const mutation = isK8sMutation(input.operation)
  const fields = k8sFields(input)
  const uses = (field: K8sField) => fields.includes(field)

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [log])

  useEffect(() => {
    savedForms.save(session, targetKey, input)
  }, [session, targetKey, input])

  function update(patch: Partial<K8sInput>) {
    setInput((current) => ({ ...current, ...patch }))
    setError("")
  }

  function selectOperation(operation: K8sOperation) {
    const kind = operation === "top" ? "pods" : operation === "get" || operation === "describe" ? input.kind || "pods" : input.kind
    update({ operation, kind })
  }

  function push(kind: K8sLogKind, text: string) {
    setLog((current) => appendK8sLog(current, kind, text))
  }

  async function run(submitted = input) {
    if (running || stopPending) return
    const where = { cluster: target.cluster, instance: target.instance }
    setError("")
    setRunning(true)
    setStopPending(false)
    onRunningChange(true)
    cancelRef.current = null
    stopRequestedRef.current = false
    evidenceRef.current = ""
    let started = false
    try {
      await runK8s(
        (opts) => {
          started = true
          setLog(appendK8sLog(EMPTY_K8S_LOG, "info", `目标 ${where.cluster}/${where.instance} · ${K8S_OPERATIONS[submitted.operation].label}`))
          return callTool(
            session,
            { ...opts, onCancelReady: (cancel) => { cancelRef.current = cancel } },
            (event) => {
              if (event.type === "output") {
                evidenceRef.current = (evidenceRef.current + event.data).slice(-FAILURE_EVIDENCE_CHARS)
                push("out", event.data)
              }
              if (event.type === "result") {
                const text = extractText(event.result)
                if (text) push("info", text)
              }
            },
          )
        },
        where,
        sessionId,
        submitted,
      )
      const hint = k8sResultHint(evidenceRef.current)
      if (hint) push("info", hint)
    } catch (err) {
      if ((err as Error).name === "AbortError") {
        if (stopRequestedRef.current) push("info", "已停止")
      } else {
        const message = (err as Error).message
        if (!started) {
          // Validation failed in the browser; nothing was sent.
          setError(message)
        } else {
          const failure = describeK8sFailure(message, {
            target: where,
            output: evidenceRef.current,
            mutation: isK8sMutation(submitted.operation),
            stopRequested: stopRequestedRef.current,
          })
          setError(failure.text)
          push("err", failure.text)
          if (failure.text !== message.trim()) push("info", `原始错误：${message.trim()}`)
        }
      }
    } finally {
      setRunning(false)
      setStopPending(false)
      onRunningChange(false)
      cancelRef.current = null
    }
  }

  function showResourceDetails(kind: K8sOverviewKey, item: K8sResource) {
    const query: K8sInput = { ...EMPTY_K8S_INPUT, operation: "describe", kind, name: item.name, namespace: item.namespace ?? "" }
    setInput(query)
    if (operationsRef.current) {
      operationsRef.current.open = true
      operationsRef.current.scrollIntoView({ block: "start" })
      operationsRef.current.querySelector("summary")?.focus({ preventScroll: true })
    }
    void run(query)
  }

  function stop() {
    if (!cancelRef.current || stopPending) return
    stopRequestedRef.current = true
    setStopPending(true)
    push("info", "已请求停止，等待远端终态；请勿立即重试")
    void cancelRef.current().catch((err) => {
      setStopPending(false)
      push("err", `停止请求失败：${(err as Error).message}`)
    })
  }

  const disabled = running || stopPending
  const helmRelease = input.operation.startsWith("helm-") && input.operation !== "helm-list"

  return (
    <div className="h-full overflow-y-auto p-4">
      <K8sResourceOverview
        initialNamespace={initialNamespace}
        session={session}
        target={target}
        sessionId={sessionId}
        running={disabled}
        onOpenDetails={showResourceDetails}
      />
      <details ref={operationsRef} className="card-surface mt-4 p-4">
        <summary className="cursor-pointer text-sm font-semibold">运维操作</summary>
        <div className="mt-4 flex flex-col gap-3">
          <div className="flex flex-col gap-2">
            <OperationGroup title="只读" operations={READ_OPERATIONS} active={input.operation} disabled={disabled} onSelect={selectOperation} />
            <OperationGroup title="写操作" operations={WRITE_OPERATIONS} active={input.operation} disabled={disabled} onSelect={selectOperation} danger />
          </div>
          {spec.cli && <p className="font-mono text-xs text-muted-foreground">doops k8s {spec.cli}</p>}

          <div className="grid gap-2 sm:grid-cols-3">
            {uses("kind") && (input.operation === "top" ? (
              <Select aria-label="资源类型" value={input.kind} disabled={disabled} onChange={(e) => update({ kind: e.target.value })}>
                <option value="pods">pods</option>
                <option value="nodes">nodes</option>
              </Select>
            ) : (
              <Input aria-label="资源类型" value={input.kind} disabled={disabled} onChange={(e) => update({ kind: e.target.value })} placeholder="资源类型，如 pods、deployment" className="font-mono" />
            ))}
            {uses("name") && (
              <Input aria-label={NAME_LABEL[input.operation] || "名称"} value={input.name} disabled={disabled} onChange={(e) => update({ name: e.target.value })} placeholder={NAME_LABEL[input.operation] || "名称"} className="font-mono" />
            )}
            {uses("resource") && (
              <Input aria-label="资源 kind/name" value={input.resource} disabled={disabled} onChange={(e) => update({ resource: e.target.value })} placeholder={RESOURCE_PLACEHOLDER[input.operation] || "kind/name"} className="font-mono" />
            )}
            {uses("namespace") && (
              <Input aria-label="命名空间" value={input.namespace} disabled={disabled || input.allNamespaces} onChange={(e) => update({ namespace: e.target.value })} placeholder={helmRelease ? "命名空间（必填）" : "命名空间（留空为 Agent 默认）"} className="font-mono" />
            )}
            {uses("selector") && (
              <Input aria-label="标签选择器" value={input.selector} disabled={disabled} onChange={(e) => update({ selector: e.target.value })} placeholder="标签选择器，如 app=api" className="font-mono" />
            )}
            {uses("output") && (
              <Select aria-label="输出格式" value={OUTPUT_OPTIONS.includes(input.output) ? input.output : ""} disabled={disabled} onChange={(e) => update({ output: e.target.value })}>
                {OUTPUT_OPTIONS.map((value) => <option key={value} value={value}>{value ? `输出 ${value}` : "默认输出"}</option>)}
              </Select>
            )}
            {uses("container") && (
              <Input aria-label="容器" value={input.container} disabled={disabled} onChange={(e) => update({ container: e.target.value })} placeholder="容器（可选）" className="font-mono" />
            )}
            {uses("tail") && (
              <Input aria-label="日志行数" inputMode="numeric" value={input.tail} disabled={disabled} onChange={(e) => update({ tail: e.target.value })} placeholder="最近行数（留空为全部）" className="font-mono" />
            )}
            {uses("since") && (
              <Input aria-label="时间范围" value={input.since} disabled={disabled} onChange={(e) => update({ since: e.target.value })} placeholder="时间范围，如 10m" className="font-mono" />
            )}
            {uses("timeout") && (
              <Input aria-label="超时" value={input.timeout} disabled={disabled} onChange={(e) => update({ timeout: e.target.value })} placeholder="超时，如 120s" className="font-mono" />
            )}
            {uses("replicas") && (
              <Input aria-label="副本数" inputMode="numeric" value={input.replicas} disabled={disabled} onChange={(e) => update({ replicas: e.target.value })} placeholder="副本数" className="font-mono" />
            )}
          </div>
          <div className="flex flex-wrap gap-4 text-xs text-muted-foreground">
            {uses("allNamespaces") && (
              <label className="flex items-center gap-1.5">
                <Checkbox checked={input.allNamespaces} disabled={disabled} onChange={(e) => update({ allNamespaces: e.target.checked, namespace: e.target.checked ? "" : input.namespace })} />
                全部命名空间（-A）
              </label>
            )}
            {uses("previous") && (
              <label className="flex items-center gap-1.5">
                <Checkbox checked={input.previous} disabled={disabled} onChange={(e) => update({ previous: e.target.checked })} />
                上一个容器实例（--previous）
              </label>
            )}
          </div>
          {K8S_OUTPUT_HINTS[input.operation] && <p className="text-xs text-muted-foreground">{K8S_OUTPUT_HINTS[input.operation]}</p>}
          {(input.operation === "get" || input.operation === "describe") && <p className="text-xs text-muted-foreground">Secret 与 ServiceAccount 不开放读取。</p>}

          <div className="flex shrink-0 items-center gap-2">
            <Button
              type="button"
              size="sm"
              variant={mutation ? "danger" : "primary"}
              onClick={(event) => { if (event.detail <= 1) void run() }}
              disabled={disabled}
            >
              <Play aria-hidden="true" className="size-3.5 shrink-0" /> 执行
            </Button>
            {running && (
              <Button
                type="button"
                size="sm"
                onClick={stop}
                disabled={stopPending || !cancelRef.current}
                className="ml-auto"
              >
                <Square aria-hidden="true" className="size-3.5 shrink-0" /> {stopPending ? "正在停止" : "停止"}
              </Button>
            )}
          </div>
          {error && <StatusBanner tone="error" role="alert" compact className="min-w-0 shrink-0 [overflow-wrap:anywhere]">{error}</StatusBanner>}

          <pre ref={logRef} aria-label="执行输出" className="min-h-48 flex-1 overflow-auto whitespace-pre-wrap break-all rounded-lg border bg-background/60 p-3.5 font-mono text-xs">
            {log.entries.length === 0 ? <span className="text-muted-foreground">选择操作并执行，输出显示在这里。</span> : (
              <>
                {log.dropped > 0 && (
                  <span className="text-muted-foreground">{`… 已省略前 ${log.dropped} 行输出，仅显示最近 ${K8S_LOG_MAX_LINES} 行；需要完整日志请缩小行数或时间范围\n`}</span>
                )}
                {log.entries.map((entry, index) => (
                  <span key={index} className={entry.kind === "err" ? "text-destructive" : entry.kind === "info" ? "text-muted-foreground" : "text-foreground"}>
                    {entry.text}
                  </span>
                ))}
              </>
            )}
          </pre>
        </div>
      </details>
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
      <span className="w-28 shrink-0 text-2xs font-medium uppercase tracking-wider text-muted-foreground">{title}</span>
      {operations.map((operation) => (
        <Button
          key={operation}
          type="button"
          size="sm"
          variant={active === operation ? (danger ? "danger" : "primary") : (danger ? "danger-outline" : "default")}
          aria-pressed={active === operation}
          disabled={disabled}
          onClick={() => onSelect(operation)}
        >
          {K8S_OPERATIONS[operation].label}
        </Button>
      ))}
    </div>
  )
}
