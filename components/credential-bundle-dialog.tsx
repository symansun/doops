"use client"

import { useEffect, useState } from "react"
import type { Session, Target } from "@/lib/client"
import {
  createCredentialBundle,
  getCredentialBundle,
  listCredentials,
  validateCredentialBundleInput,
  type CredentialBundle,
  type CredentialResource,
  type CredentialScope,
} from "@/lib/credentials"
import { LayersIcon } from "./icons"

type Mode = "view" | "create"

export function CredentialBundleButton({ session, target }: { session: Session; target: Target }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button
        onClick={() => setOpen(true)}
        title="创建或查看凭据组"
        className="flex items-center gap-1.5 rounded-md border px-3 py-2 text-xs text-foreground hover:bg-muted"
      >
        <LayersIcon width={14} height={14} /> 凭据组
      </button>
      {open && <CredentialBundleDialog session={session} target={target} onClose={() => setOpen(false)} />}
    </>
  )
}

function CredentialBundleDialog({
  session,
  target,
  onClose,
}: {
  session: Session
  target: Target
  onClose: () => void
}) {
  const [mode, setMode] = useState<Mode>("view")
  const [error, setError] = useState("")
  const [bundle, setBundle] = useState<CredentialBundle | null>(null)

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
    >
      <div className="max-h-[90dvh] w-full max-w-xl overflow-auto rounded-lg border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="text-sm font-semibold text-foreground">凭据组</h3>
          <button onClick={onClose} className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted">
            关闭
          </button>
        </div>
        <div className="space-y-3 p-4">
          <p className="text-xs text-muted-foreground">
            凭据组是若干凭据 ID 的静态集合，只保存名称、归属和凭据 ID，不包含凭据内容。创建后不能修改或吊销，
            发布时仍需按单个凭据名称引用。
          </p>
          <div className="inline-flex rounded-md border bg-background p-0.5 text-xs">
            {(["view", "create"] as const).map((item) => (
              <button
                key={item}
                onClick={() => {
                  setMode(item)
                  setError("")
                }}
                className={`rounded px-2.5 py-1.5 ${mode === item ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
              >
                {item === "view" ? "按名称查看" : "新建凭据组"}
              </button>
            ))}
          </div>
          {error && (
            <div role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {error}
            </div>
          )}
          {mode === "view" ? (
            <ViewBundle session={session} onResult={setBundle} onError={setError} />
          ) : (
            <CreateBundle
              session={session}
              target={target}
              onCreated={(created) => {
                setBundle(created)
                setMode("view")
              }}
              onError={setError}
            />
          )}
          {bundle && <BundleSummary bundle={bundle} />}
        </div>
      </div>
    </div>
  )
}

function ViewBundle({
  session,
  onResult,
  onError,
}: {
  session: Session
  onResult: (bundle: CredentialBundle | null) => void
  onError: (message: string) => void
}) {
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true)
    onError("")
    onResult(null)
    try {
      onResult(await getCredentialBundle(session, name))
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex items-end gap-2">
      <label className="flex flex-1 flex-col gap-1 text-xs text-muted-foreground">
        凭据组名称
        <input
          className="input"
          value={name}
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => event.key === "Enter" && name.trim() && !busy && submit()}
          placeholder={session.demo ? "release-demo" : "release"}
        />
      </label>
      <button
        onClick={submit}
        disabled={busy || !name.trim()}
        className="rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
      >
        {busy ? "查询中" : "查看"}
      </button>
    </div>
  )
}

function CreateBundle({
  session,
  target,
  onCreated,
  onError,
}: {
  session: Session
  target: Target
  onCreated: (bundle: CredentialBundle) => void
  onError: (message: string) => void
}) {
  const [name, setName] = useState("")
  const [scope, setScope] = useState<CredentialScope>("personal")
  const [selected, setSelected] = useState<string[]>([])
  const [credentials, setCredentials] = useState<CredentialResource[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    listCredentials(session, target, "managed")
      .then((rows) => !cancelled && setCredentials(rows.filter((row) => row.state === "active")))
      .catch((err) => !cancelled && onError(`无法加载可管理的凭据：${(err as Error).message}`))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [session, target, onError])

  function toggle(id: string) {
    setSelected((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]))
  }

  async function submit() {
    const input = { name, scope, credentialIDs: selected }
    const invalid = validateCredentialBundleInput(input)
    if (invalid) {
      onError(invalid)
      return
    }
    setBusy(true)
    onError("")
    try {
      onCreated(await createCredentialBundle(session, input))
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-muted-foreground">
          凭据组名称
          <input className="input" value={name} onChange={(event) => setName(event.target.value)} placeholder="release" />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          归属
          <select
            className="input min-w-32"
            value={scope}
            onChange={(event) => setScope(event.target.value as CredentialScope)}
          >
            <option value="personal">个人</option>
            <option value="platform">平台（仅平台管理员）</option>
          </select>
        </label>
      </div>
      <fieldset className="rounded-md border">
        <legend className="px-2 text-xs text-muted-foreground">选择凭据（只列出你可管理的有效凭据）</legend>
        <div className="max-h-56 overflow-auto px-3 pb-2">
          {loading ? (
            <p className="py-3 text-xs text-muted-foreground">加载中</p>
          ) : !credentials.length ? (
            <p className="py-3 text-xs text-muted-foreground">没有可放入凭据组的凭据</p>
          ) : (
            credentials.map((credential) => (
              <label key={credential.id} className="flex items-center gap-2 py-1.5 text-xs text-foreground">
                <input
                  type="checkbox"
                  checked={selected.includes(credential.id)}
                  onChange={() => toggle(credential.id)}
                />
                <span className="font-medium">{credential.name}</span>
                <span className="text-muted-foreground">{credential.scope === "platform" ? "平台" : "个人"}</span>
                <span className="ml-auto font-mono text-[11px] text-muted-foreground">{credential.id}</span>
              </label>
            ))
          )}
        </div>
      </fieldset>
      <div className="flex justify-end">
        <button
          onClick={submit}
          disabled={busy || session.demo}
          title={session.demo ? "演示模式不执行凭据写操作" : undefined}
          className="rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
        >
          {busy ? "创建中" : `创建凭据组（${selected.length} 个凭据）`}
        </button>
      </div>
    </div>
  )
}

function BundleSummary({ bundle }: { bundle: CredentialBundle }) {
  return (
    <div className="space-y-2 rounded-md border bg-muted/30 p-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium text-foreground">{bundle.name}</span>
        <span className="text-muted-foreground">{bundle.scope === "platform" ? "平台" : "个人"}</span>
        <span
          className={`rounded px-1.5 py-0.5 ${bundle.state === "active" ? "bg-success/15 text-success" : "bg-muted text-muted-foreground"}`}
        >
          {bundle.state}
        </span>
        <span className="ml-auto font-mono text-[11px] text-muted-foreground">{bundle.id}</span>
      </div>
      <div className="text-muted-foreground">包含 {bundle.items.length} 个凭据：</div>
      <ul className="space-y-0.5 font-mono text-[11px] text-foreground">
        {bundle.items.map((item) => (
          <li key={item.credential_id}>{item.credential_id}</li>
        ))}
      </ul>
    </div>
  )
}
