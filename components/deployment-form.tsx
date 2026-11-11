"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { CheckCircle2, CircleAlert, CircleDashed, Rocket } from "lucide-react"
import type { Target } from "@/lib/gateway"
import {
  deploymentDraftText, deploymentFormControls, deploymentRequest, deploymentSubmitError, parseDeploymentDraft, singleFlight,
  type DeploymentInput,
} from "@/lib/deployment"

type TextKey = Exclude<keyof DeploymentInput, "selector">
type Field = { key: TextKey; label: string; hint?: string; placeholder?: string; optional?: boolean; mono?: boolean; wide?: boolean; multiline?: boolean }

const EMPTY: DeploymentInput = { services: "", release: "", namespace: "", chart: "", values: "", repository: "", selector: "digest", version: "", commit: "", ci: "", reason: "", acceptance: "", credentials: "" }
const REQUIRED: TextKey[] = ["services", "release", "namespace", "chart", "repository", "version", "commit", "ci", "reason", "acceptance"]

function sections(selector: DeploymentInput["selector"]): { title: string; description: string; fields: Field[]; selector?: boolean }[] {
  return [
    { title: "发布范围", description: "本次变更涉及的服务与 Helm release。", fields: [
      { key: "services", label: "服务范围", placeholder: "api, worker", wide: true },
      { key: "release", label: "Helm release", placeholder: "my-app", mono: true },
      { key: "namespace", label: "Namespace", placeholder: "apps", mono: true },
    ] },
    { title: "Helm 资产", description: "Agent 只使用这里指定的远程 Chart 和 values。", fields: [
      { key: "chart", label: "Chart 地址", placeholder: "https://charts.example.com/my-app-1.2.0.tgz", hint: "仅 HTTPS，不含内嵌凭据", mono: true, wide: true },
      { key: "values", label: "Values 地址", placeholder: "https://charts.example.com/values-prod.yaml", hint: "多个地址用空格分隔，按顺序叠加", optional: true, mono: true, wide: true },
    ] },
    { title: "镜像版本", description: "版本必须来自已通过的 CI，页面和 Agent 都不会猜测。", selector: true, fields: [
      { key: "repository", label: "镜像仓库", placeholder: "registry.example.com/team/my-app", hint: "不含 tag 或 digest", mono: true, wide: true },
      selector === "digest"
        ? { key: "version", label: "镜像 digest", placeholder: "sha256:…", hint: "sha256: 加 64 位十六进制", mono: true, wide: true }
        : { key: "version", label: "镜像 tag", placeholder: "v2.3.1", hint: "业务自有的发布 tag", mono: true, wide: true },
    ] },
    { title: "来源证据", description: "核对过的 CI 来源，以及发布需要的受管凭据。", fields: [
      { key: "commit", label: "来源提交 SHA", placeholder: "完整 40 位 SHA", mono: true, wide: true },
      { key: "ci", label: "成功 CI 记录", placeholder: "https://github.com/…/actions/runs/…", hint: "仅 HTTPS", mono: true, wide: true },
      { key: "credentials", label: "凭据资源名", placeholder: "registry-reader, helm-charts", hint: "受管凭据的资源名，逗号或空格分隔；不要填写密钥内容", optional: true, mono: true, wide: true },
    ] },
    { title: "变更说明", description: "原因和可机器核验的验收条件会原样交给 Agent。", fields: [
      { key: "reason", label: "变更原因", placeholder: "例如：修复资源回收问题", multiline: true, wide: true },
      { key: "acceptance", label: "验收条件", placeholder: "例如：Deployment 达到期望副本数，Pod imageID 与 digest 一致，/healthz 返回 200", multiline: true, wide: true },
    ] },
  ]
}

type Check = { state: "incomplete"; missing: number } | { state: "invalid"; message: string } | { state: "ok"; image: string; credentials: string[] }

function precheck(target: Target, input: DeploymentInput): Check {
  const missing = REQUIRED.filter((key) => !input[key].trim()).length
  if (missing) return { state: "incomplete", missing }
  try {
    const request = deploymentRequest(target, input)
    return { state: "ok", image: `${input.repository}${input.selector === "digest" ? "@" : ":"}${input.version}`, credentials: request.credential_refs }
  } catch (error) {
    // `new URL` raises an English TypeError for malformed addresses.
    return { state: "invalid", message: error instanceof TypeError ? "Chart、Values 或 CI 地址不是有效 URL" : (error as Error).message }
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
  onDeploy: (input: DeploymentInput, confirmed: boolean) => Promise<void>
}) {
  const [input, setInput] = useState<DeploymentInput>(() => parseDeploymentDraft(loadDraft()) || EMPTY)
  const [confirmed, setConfirmed] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState("")
  const check = useMemo(() => precheck(target, input), [target, input])
  const controls = deploymentFormControls({ canSubmit, valid: check.state === "ok", submitting, confirmed })
  useEffect(() => { saveDraft(deploymentDraftText(input)) }, [saveDraft, input])
  // The guard lives across renders, so it calls the current onDeploy (which reads the current Agent availability).
  const deploy = useRef(onDeploy)
  deploy.current = onDeploy
  // A double click submits twice before React re-renders the disabled button; the second call is dropped.
  const submit = useRef(singleFlight(async (release: DeploymentInput, authorized: boolean) => {
    setError("")
    setSubmitting(true)
    try { await deploy.current(release, authorized); setConfirmed(false) } catch (err) { setError(deploymentSubmitError((err as Error).message)) } finally { setSubmitting(false) }
  }))
  function change(key: keyof DeploymentInput, value: string) {
    setInput((current) => ({ ...current, [key]: value }))
    setConfirmed(false)
    setError("")
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={(event) => {
      event.preventDefault()
      // Enter in a field also submits; the same gate as the button applies.
      if (controls.submitDisabled) return
      void submit.current(input, confirmed)
    }}>
      <fieldset disabled={controls.fieldsDisabled} className="flex min-w-0 flex-col gap-4">
        {sections(input.selector).map((section) => (
          <section key={section.title} className="rounded-lg border bg-card p-4">
            <header className="mb-3">
              <h3 className="text-sm font-medium">{section.title}</h3>
              <p className="mt-0.5 text-xs text-muted-foreground">{section.description}</p>
            </header>
            {section.selector && (
              <div role="radiogroup" aria-label="镜像版本方式" className="mb-3 inline-flex rounded-md border bg-muted/40 p-0.5">
                {([["digest", "Digest"], ["tag", "Release tag"]] as const).map(([value, label]) => (
                  <button key={value} type="button" role="radio" aria-checked={input.selector === value} onClick={() => change("selector", value)}
                    className={`rounded px-3 py-1 text-xs font-medium transition-colors ${input.selector === value ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>{label}</button>
                ))}
              </div>
            )}
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
          <dt className="text-muted-foreground">镜像</dt><dd className="break-all font-mono">{check.state === "ok" ? check.image : "—"}</dd>
          <dt className="text-muted-foreground">凭据</dt>
          <dd className="flex flex-wrap gap-1">{check.state === "ok" && check.credentials.length
            ? check.credentials.map((name) => <span key={name} className="rounded bg-muted px-1.5 py-0.5 font-mono">{name}</span>)
            : <span className="text-muted-foreground">无</span>}</dd>
        </dl>
        <p role="status" className={`mt-3 flex items-start gap-1.5 text-xs ${check.state === "ok" ? "text-emerald-600 dark:text-emerald-400" : check.state === "invalid" ? "text-destructive" : "text-muted-foreground"}`}>
          {check.state === "ok" ? <CheckCircle2 aria-hidden="true" className="mt-px size-3.5 shrink-0" /> : check.state === "invalid" ? <CircleAlert aria-hidden="true" className="mt-px size-3.5 shrink-0" /> : <CircleDashed aria-hidden="true" className="mt-px size-3.5 shrink-0" />}
          <span className="break-words">{check.state === "ok" ? "格式校验通过；提交后服务端会按同一规则再次校验" : check.state === "invalid" ? check.message : `还有 ${check.missing} 项必填信息未填写`}</span>
        </p>
        <label className={`mt-4 flex items-start gap-2 rounded-md border p-3 text-sm ${confirmed ? "border-primary/60 bg-primary/5" : ""} ${controls.confirmDisabled ? "opacity-60" : "cursor-pointer"}`}>
          <input type="checkbox" aria-label="授权本次目标变更" checked={confirmed && !controls.confirmDisabled} disabled={controls.confirmDisabled} onChange={(e) => setConfirmed(e.target.checked)} className="mt-0.5" />
          <span>授权本次目标变更。我已核对 CI 来源、镜像和发布范围。</span>
        </label>
        {error && <p role="alert" className="mt-3 break-words rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{error}</p>}
        <button disabled={controls.submitDisabled} className="mt-4 flex w-full items-center justify-center gap-2 rounded-md bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40">
          <Rocket aria-hidden="true" className="size-4" />{submitting ? "正在提交…" : "确认发布"}
        </button>
        {!canSubmit && blockedReason && <p className="mt-2 text-center text-[11px] text-muted-foreground">{blockedReason}</p>}
      </section>
    </form>
  )
}
