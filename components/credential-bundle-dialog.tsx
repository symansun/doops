"use client"

import { Badge, Button, Checkbox, Input, Modal, SegmentedControl, Select, StatusBanner } from "./ui"
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
      <Button
        onClick={() => setOpen(true)}
        title="创建或查看凭据组"
        size="sm"
      >
        <LayersIcon width={14} height={14} /> 凭据组
      </Button>
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
    <Modal title="凭据组" size="lg" onClose={onClose}>
      <div className="space-y-3">
        <p className="text-xs text-muted-foreground">
          凭据组是若干凭据 ID 的静态集合，只保存名称、归属和凭据 ID，不包含凭据内容。创建后不能修改或吊销，
          发布时仍需按单个凭据名称引用。
        </p>
        <SegmentedControl
          value={mode}
          ariaLabel="凭据组操作模式"
          onChange={(item) => {
            setMode(item)
            setError("")
          }}
          options={[
            { value: "view", label: "按名称查看" },
            { value: "create", label: "新建凭据组" },
          ]}
        />
        {error && (
          <StatusBanner tone="error" compact>
            {error}
          </StatusBanner>
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
    </Modal>
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
        <Input
          value={name}
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => event.key === "Enter" && name.trim() && !busy && submit()}
          placeholder={session.demo ? "release-demo" : "release"}
        />
      </label>
      <Button
        onClick={submit}
        disabled={busy || !name.trim()}
        variant="primary"
      >
        {busy ? "查询中" : "查看"}
      </Button>
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
    // The managed list does not depend on the target, and the target poll
    // replaces that object on every heartbeat.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, onError])

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
          <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="release" />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          归属
          <Select
            className="min-w-32"
            value={scope}
            onChange={(event) => setScope(event.target.value as CredentialScope)}
          >
            <option value="personal">个人</option>
            <option value="platform">平台（仅平台管理员）</option>
          </Select>
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
                <Checkbox
                  checked={selected.includes(credential.id)}
                  onChange={() => toggle(credential.id)}
                />
                <span className="font-medium">{credential.name}</span>
                <span className="text-muted-foreground">{credential.scope === "platform" ? "平台" : "个人"}</span>
                <span className="ml-auto font-mono text-2xs text-muted-foreground">{credential.id}</span>
              </label>
            ))
          )}
        </div>
      </fieldset>
      <div className="flex justify-end">
        <Button
          onClick={submit}
          disabled={busy || session.demo}
          title={session.demo ? "演示模式不执行凭据写操作" : undefined}
          variant="primary"
          size="sm"
        >
          {busy ? "创建中" : `创建凭据组（${selected.length} 个凭据）`}
        </Button>
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
        <Badge variant={bundle.state === "active" ? "success" : "default"}>{bundle.state}</Badge>
        <span className="ml-auto font-mono text-2xs text-muted-foreground">{bundle.id}</span>
      </div>
      <div className="text-muted-foreground">包含 {bundle.items.length} 个凭据：</div>
      <ul className="space-y-0.5 font-mono text-2xs text-foreground">
        {bundle.items.map((item) => (
          <li key={item.credential_id}>{item.credential_id}</li>
        ))}
      </ul>
    </div>
  )
}
