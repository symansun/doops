"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { CheckCircle2, CircleAlert, CircleDashed, Rocket } from "lucide-react"
import type { Target } from "@/lib/gateway"
import {
  deploymentDraftText, deploymentFormControls, deploymentRequest, deploymentSubmitError, parseDeploymentDraft, singleFlight,
  type DeploymentInput,
} from "@/lib/deployment"

type TextKey = keyof DeploymentInput
type Field = { key: TextKey; label: string; hint?: string; placeholder?: string; optional?: boolean; mono?: boolean; wide?: boolean; multiline?: boolean }

const EMPTY: DeploymentInput = { release: "", namespace: "", chart: "", values: "", message: "", credentials: "" }
const REQUIRED: TextKey[] = ["release", "namespace", "chart"]

function sections(): { title: string; description: string; fields: Field[] }[] {
  return [
    { title: "Helm 发布", description: "DoOps 只传递请求，目标 Agent 按原生 Helm 处理 Chart。", fields: [
      { key: "release", label: "Helm release", placeholder: "my-app", mono: true },
      { key: "namespace", label: "Namespace", placeholder: "apps", mono: true },
      { key: "chart", label: "Chart", placeholder: "https://charts.example.com/my-app-1.2.0.tgz 或 session 相对路径", hint: "地址、路径和资源类型由目标 Helm 处理", mono: true, wide: true },
      { key: "values", label: "Values", placeholder: "https://charts.example.com/values.yaml 或 session 相对路径", hint: "可选；多个值文件按填写顺序传给 Helm", optional: true, mono: true, wide: true },
    ] },
    { title: "请求说明", description: "可补充变更原因、镜像值、业务范围或验收要求；这些内容不会变成发布准入条件。", fields: [
      { key: "message", label: "任务说明", placeholder: "例如：升级现有 release，使用 values 中的镜像；发布后检查 /healthz", optional: true, multiline: true, wide: true },
      { key: "credentials", label: "凭据资源名", placeholder: "helm-charts, registry-reader", hint: "可选；只填写受管凭据资源名，不填写密钥内容", optional: true, mono: true, wide: true },
    ] },
  ]
}

type Check = { state: "incomplete"; missing: number } | { state: "invalid"; message: string } | { state: "ok"; credentials: string[] }

function precheck(target: Target, input: DeploymentInput): Check {
  const missing = REQUIRED.filter((key) => !input[key].trim()).length
  if (missing) return { state: "incomplete", missing }
  try {
    const request = deploymentRequest(target, input)
    return { state: "ok", credentials: request.credential_refs }
  } catch (error) {
    return { state: "invalid", message: error instanceof Error ? error.message : "发布字段无效" }
  }
}

const inputClass = "w-full min-w-0 rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground outline-none transition-colors placeholder:text-muted-foreground/60 focus:border-ring focus:ring-2 focus:ring-ring/20 disabled:cursor-not-allowed disabled:opacity-60"

// canSubmit comes from the Agent session; it only gates submission, so a release can be drafted and
// checked locally on a target that cannot run it yet.
export function DeploymentForm({ target, loadDraft, saveDraft, canSubmit, blockedReason, onDeploy }: {
  target: Target
  // Where the draft is kept, so switching tabs or reloading keeps what was typed; the caller scopes it to the user.
  loadDraft: () => string | null
  saveDraft: (text: string | null) => void
  canSubmit: boolean
  blockedReason?: string
  onDeploy: (input: DeploymentInput) => Promise<void>
}) {
  const [input, setInput] = useState<DeploymentInput>(() => parseDeploymentDraft(loadDraft()) || EMPTY)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState("")
  const check = useMemo(() => precheck(target, input), [target, input])
  const controls = deploymentFormControls({ canSubmit, valid: check.state === "ok", submitting })
  useEffect(() => { saveDraft(deploymentDraftText(input)) }, [saveDraft, input])
  // The guard lives across renders, so it calls the current onDeploy (which reads the current Agent availability).
  const deploy = useRef(onDeploy)
  deploy.current = onDeploy
  // A double click submits twice before React re-renders the disabled button; the second call is dropped.
  const submit = useRef(singleFlight(async (release: DeploymentInput) => {
    setError("")
    setSubmitting(true)
    try { await deploy.current(release) } catch (err) { setError(deploymentSubmitError((err as Error).message)) } finally { setSubmitting(false) }
  }))
  function change(key: keyof DeploymentInput, value: string) {
    setInput((current) => ({ ...current, [key]: value }))
    setError("")
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={(event) => {
      event.preventDefault()
      // Enter in a field also submits; the same channel and validation checks apply.
      if (controls.submitDisabled) return
      void submit.current(input)
    }}>
      <fieldset disabled={controls.fieldsDisabled} className="flex min-w-0 flex-col gap-4">
        {sections().map((section) => (
          <section key={section.title} className="rounded-lg border bg-card p-4">
            <header className="mb-3">
              <h3 className="text-sm font-medium">{section.title}</h3>
              <p className="mt-0.5 text-xs text-muted-foreground">{section.description}</p>
            </header>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {section.fields.map((field) => (
                <label key={field.key} className={`flex min-w-0 flex-col gap-1.5 ${field.wide ? "sm:col-span-2" : ""}`}>
                  <span className="text-xs font-medium text-foreground">
                    {field.label}{field.optional && <span className="ml-1 font-normal text-muted-foreground">可选</span>}
                  </span>
                  {field.multiline
                    ? <textarea aria-label={field.label} required={!field.optional} rows={2} value={input[field.key]} placeholder={field.placeholder} onChange={(e) => change(field.key, e.target.value)} className={`${inputClass} resize-y`} />
                    : <input aria-label={field.label} required={!field.optional} value={input[field.key]} placeholder={field.placeholder} spellCheck={false} autoComplete="off" onChange={(e) => change(field.key, e.target.value)} className={`${inputClass} ${field.mono ? "font-mono text-[13px]" : ""}`} />}
                  {field.hint && <span className="text-[11px] text-muted-foreground">{field.hint}</span>}
                </label>
              ))}
            </div>
          </section>
        ))}
      </fieldset>

      <section aria-label="发布确认" className="rounded-lg border bg-card p-4">
        <h3 className="mb-3 text-sm font-medium">提交前核对</h3>
        <dl className="grid grid-cols-[5rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs">
          <dt className="text-muted-foreground">目标</dt><dd className="break-all font-mono">{target.cluster}/{target.instance}</dd>
          <dt className="text-muted-foreground">Release</dt><dd className="break-all font-mono">{input.namespace && input.release ? `${input.namespace}/${input.release}` : "—"}</dd>
          <dt className="text-muted-foreground">Chart</dt><dd className="break-all font-mono">{input.chart || "—"}</dd>
          <dt className="text-muted-foreground">凭据</dt>
          <dd className="flex flex-wrap gap-1">{check.state === "ok" && check.credentials.length
            ? check.credentials.map((name) => <span key={name} className="rounded bg-muted px-1.5 py-0.5 font-mono">{name}</span>)
            : <span className="text-muted-foreground">无</span>}</dd>
        </dl>
        <p role="status" className={`mt-3 flex items-start gap-1.5 text-xs ${check.state === "ok" ? "text-emerald-600 dark:text-emerald-400" : check.state === "invalid" ? "text-destructive" : "text-muted-foreground"}`}>
          {check.state === "ok" ? <CheckCircle2 aria-hidden="true" className="mt-px size-3.5 shrink-0" /> : check.state === "invalid" ? <CircleAlert aria-hidden="true" className="mt-px size-3.5 shrink-0" /> : <CircleDashed aria-hidden="true" className="mt-px size-3.5 shrink-0" />}
          <span className="break-words">{check.state === "ok" ? "格式校验通过；提交后服务端会按同一规则再次校验" : check.state === "invalid" ? check.message : `还有 ${check.missing} 项必填信息未填写`}</span>
        </p>
        {error && <p role="alert" className="mt-3 break-words rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{error}</p>}
        <button disabled={controls.submitDisabled} className="mt-4 flex w-full items-center justify-center gap-2 rounded-md bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40">
          <Rocket aria-hidden="true" className="size-4" />{submitting ? "正在提交…" : "提交发布"}
        </button>
        {!canSubmit && blockedReason && <p className="mt-2 text-center text-[11px] text-muted-foreground">{blockedReason}</p>}
      </section>
    </form>
  )
}
