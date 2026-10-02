"use client"

import { useState } from "react"
import type { DeploymentInput } from "@/lib/deployment"
import { SendIcon } from "./icons"

export function DeploymentForm({ disabled, onDeploy }: { disabled: boolean; onDeploy: (input: DeploymentInput) => Promise<void> }) {
  const [input, setInput] = useState<DeploymentInput>({ services: "", release: "", namespace: "", chart: "", values: "", repository: "", selector: "digest", version: "", commit: "", ci: "", reason: "", acceptance: "", credentials: "" })
  const [confirmed, setConfirmed] = useState(false)
  const [error, setError] = useState("")
  function change(key: keyof DeploymentInput, value: string) {
    setInput((current) => ({ ...current, [key]: value }))
    setConfirmed(false)
    setError("")
  }
  const fields: { key: Exclude<keyof DeploymentInput, "selector" | "reason" | "acceptance">; label: string; optional?: boolean; placeholder?: string }[] = [
    { key: "services", label: "服务范围" }, { key: "release", label: "Helm release" }, { key: "namespace", label: "Namespace" },
    { key: "chart", label: "Chart", placeholder: "https://… 或 oci://…" }, { key: "values", label: "Values URL", optional: true },
    { key: "repository", label: "镜像仓库" }, { key: "version", label: input.selector === "digest" ? "镜像 digest" : "镜像 tag", placeholder: input.selector === "digest" ? "sha256:…" : "release-YYYYMMDD" },
    { key: "commit", label: "来源提交 SHA" }, { key: "ci", label: "成功 CI 记录 URL" }, { key: "credentials", label: "凭据资源名", optional: true },
  ]
  return (
    <form className="mx-auto flex w-full max-w-3xl flex-col gap-3" onSubmit={async (event) => {
      event.preventDefault()
      if (!confirmed || disabled) return
      setError("")
      try { await onDeploy(input) } catch (err) { setError((err as Error).message) }
    }}>
      <fieldset disabled={disabled} className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">镜像版本方式
          <select aria-label="镜像版本方式" value={input.selector} onChange={(e) => change("selector", e.target.value)} className="rounded-md border bg-background px-2 py-2 text-sm text-foreground">
            <option value="digest">Digest</option><option value="tag">Release tag</option>
          </select>
        </label>
        {fields.map((field) => <label key={field.key} className="flex min-w-0 flex-col gap-1 text-xs text-muted-foreground">
          {field.label}{field.optional ? "（可选）" : ""}
          <input aria-label={field.label} required={!field.optional} value={input[field.key]} onChange={(e) => change(field.key, e.target.value)} placeholder={field.placeholder} className="min-w-0 rounded-md border bg-background px-2 py-2 text-sm text-foreground outline-none focus:border-ring" />
        </label>)}
        {([ ["reason", "变更原因"], ["acceptance", "验收条件"] ] as const).map(([key, label]) => <label key={key} className="flex min-w-0 flex-col gap-1 text-xs text-muted-foreground">
          {label}<textarea aria-label={label} required value={input[key]} onChange={(e) => change(key, e.target.value)} rows={3} className="min-w-0 rounded-md border bg-background px-2 py-2 text-sm text-foreground outline-none focus:border-ring" />
        </label>)}
      </fieldset>
      <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={confirmed} disabled={disabled} onChange={(e) => setConfirmed(e.target.checked)} className="mt-1" />授权本次目标变更，已核对 CI 来源、镜像和发布范围</label>
      {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
      <button disabled={disabled || !confirmed} className="flex items-center justify-center gap-2 self-start rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground disabled:opacity-50"><SendIcon width={16} height={16} />确认部署</button>
    </form>
  )
}
