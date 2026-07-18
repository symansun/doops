"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { Session, Target } from "@/lib/client"
import {
  createCredential,
  grantCredentialToSelf,
  listCredentials,
  putCredentialPayload,
  revokeCredential,
  type CredentialGrantScope,
  type CredentialResource,
  type CredentialScope,
  type CredentialType,
  type CredentialUse,
} from "@/lib/credentials"
import {
  BanIcon,
  CheckIcon,
  KeyIcon,
  PlusIcon,
  RefreshIcon,
  SaveIcon,
  ShieldIcon,
} from "./icons"

const TYPE_LABELS: Record<CredentialType, string> = {
  registry: "OCI Registry",
  tls: "TLS",
  opaque: "Opaque Secret",
  helmRepository: "Helm Repository",
  gitToken: "Git Token",
}

const TYPE_USES: Record<CredentialType, CredentialUse> = {
  registry: "imagePull",
  tls: "tls",
  opaque: "opaqueSecret",
  helmRepository: "helmPull",
  gitToken: "gitCheckout",
}

type Action =
  | { kind: "payload"; credential: CredentialResource }
  | { kind: "grant"; credential: CredentialResource }
  | null

function fmtTime(value?: string) {
  if (!value) return "-"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false })
}

export function CredentialPanel({ session, target }: { session: Session; target: Target }) {
  const [view, setView] = useState<"target" | "managed">("target")
  const [rows, setRows] = useState<CredentialResource[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [showCreate, setShowCreate] = useState(false)
  const [action, setAction] = useState<Action>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      setRows(await listCredentials(session, target, view))
    } catch (err) {
      setRows([])
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session, target, view])

  useEffect(() => {
    load()
  }, [load])

  function completed(message: string) {
    setNotice(message)
    window.setTimeout(() => setNotice(""), 4000)
  }

  async function revoke(resource: CredentialResource) {
    if (!window.confirm(`确认吊销凭据「${resource.name}」？已物化资源将进入清理流程。`)) return
    setError("")
    try {
      await revokeCredential(session, resource.id)
      completed(`已吊销 ${resource.name}`)
      await load()
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b bg-card px-4 py-3">
        <div>
          <div className="flex items-center gap-2">
            <KeyIcon width={17} height={17} className="text-primary" />
            <h2 className="text-sm font-semibold text-foreground">凭据资源</h2>
          </div>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {target.cluster}/{target.instance} · 仅显示元数据与授权范围
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="inline-flex rounded-md border bg-background p-0.5 text-xs">
            <button
              onClick={() => setView("target")}
              className={`rounded px-2.5 py-1.5 ${view === "target" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
            >
              当前目标可用
            </button>
            <button
              onClick={() => setView("managed")}
              className={`rounded px-2.5 py-1.5 ${view === "managed" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
            >
              我管理的
            </button>
          </div>
          <button
            onClick={load}
            disabled={loading}
            title="刷新凭据"
            className="flex h-8 w-8 items-center justify-center rounded-md border text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
          >
            <RefreshIcon width={15} height={15} className={loading ? "animate-spin" : ""} />
          </button>
          <button
            onClick={() => {
              setView("managed")
              setShowCreate(true)
            }}
            disabled={session.demo}
            title={session.demo ? "演示模式不执行凭据写操作" : "新建凭据"}
            className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            <PlusIcon width={14} height={14} /> 新建凭据
          </button>
        </div>
      </div>

      {error && (
        <div className="border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-xs text-destructive">
          {error}
        </div>
      )}
      {notice && (
        <div className="flex items-center gap-2 border-b border-success/30 bg-success/10 px-4 py-2 text-xs text-success">
          <CheckIcon width={14} height={14} /> {notice}
        </div>
      )}
      {showCreate && (
        <CreateCredentialForm
          session={session}
          onCancel={() => setShowCreate(false)}
          onCreated={async (resource) => {
            setShowCreate(false)
            completed(`已创建 ${resource.name}`)
            await load()
          }}
          onError={setError}
        />
      )}

      <div className="min-h-0 flex-1 overflow-auto">
        <table className="w-full min-w-[760px] border-collapse text-sm">
          <thead className="sticky top-0 z-10 bg-card">
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-4 py-2.5 font-medium">名称</th>
              <th className="px-3 py-2.5 font-medium">类型</th>
              <th className="px-3 py-2.5 font-medium">归属</th>
              <th className="px-3 py-2.5 font-medium">状态</th>
              <th className="px-3 py-2.5 font-medium">{view === "target" ? "授权范围" : "更新时间"}</th>
              <th className="px-4 py-2.5 text-right font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((resource) => (
              <tr key={resource.id} className="border-b border-border/60 align-top">
                <td className="px-4 py-3">
                  <div className="font-medium text-foreground">{resource.name}</div>
                  <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">{resource.id}</div>
                </td>
                <td className="px-3 py-3 text-xs text-foreground">{TYPE_LABELS[resource.type]}</td>
                <td className="px-3 py-3 text-xs text-muted-foreground">
                  {resource.scope === "platform" ? "平台" : "个人"}
                </td>
                <td className="px-3 py-3">
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${
                      resource.state === "active"
                        ? "bg-success/15 text-success"
                        : "bg-muted text-muted-foreground"
                    }`}
                  >
                    {resource.state}
                  </span>
                </td>
                <td className="max-w-md px-3 py-3 text-xs text-muted-foreground">
                  {view === "target" ? <GrantSummary resource={resource} /> : fmtTime(resource.updated_at)}
                </td>
                <td className="px-4 py-3">
                  <div className="flex justify-end gap-1.5">
                    {view === "managed" && resource.state === "active" && !session.demo && (
                      <>
                        <button
                          onClick={() => setAction({ kind: "payload", credential: resource })}
                          className="rounded-md border px-2 py-1 text-xs text-foreground hover:bg-muted"
                        >
                          写入版本
                        </button>
                        <button
                          onClick={() => setAction({ kind: "grant", credential: resource })}
                          className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-foreground hover:bg-muted"
                        >
                          <ShieldIcon width={12} height={12} /> 授权
                        </button>
                        <button
                          onClick={() => revoke(resource)}
                          title="吊销凭据"
                          className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/15 hover:text-destructive"
                        >
                          <BanIcon width={14} height={14} />
                        </button>
                      </>
                    )}
                  </div>
                </td>
              </tr>
            ))}
            {!rows.length && !loading && (
              <tr>
                <td colSpan={6} className="px-4 py-16 text-center text-sm text-muted-foreground">
                  {view === "target"
                    ? "当前用户在该目标上没有已授权凭据"
                    : "当前账号没有可管理的凭据"}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {action?.kind === "payload" && (
        <PayloadDialog
          session={session}
          credential={action.credential}
          onClose={() => setAction(null)}
          onDone={(message) => {
            setAction(null)
            completed(message)
          }}
          onError={setError}
        />
      )}
      {action?.kind === "grant" && (
        <GrantDialog
          session={session}
          target={target}
          credential={action.credential}
          onClose={() => setAction(null)}
          onDone={async (message) => {
            setAction(null)
            completed(message)
            await load()
          }}
          onError={setError}
        />
      )}
    </div>
  )
}

function GrantSummary({ resource }: { resource: CredentialResource }) {
  if (!resource.grants?.length) return <span>-</span>
  return (
    <div className="flex flex-col gap-1">
      {resource.grants.map((grant) => (
        <span key={grant.id}>
          {grant.scope.namespace} · {grant.scope.use} · {grant.scope.workload.kind}/
          {grant.scope.workload.name}
        </span>
      ))}
    </div>
  )
}

function CreateCredentialForm({
  session,
  onCancel,
  onCreated,
  onError,
}: {
  session: Session
  onCancel: () => void
  onCreated: (resource: CredentialResource) => void
  onError: (message: string) => void
}) {
  const [name, setName] = useState("")
  const [scope, setScope] = useState<CredentialScope>("personal")
  const [type, setType] = useState<CredentialType>("registry")
  const [busy, setBusy] = useState(false)

  async function submit() {
    if (!name.trim()) return
    setBusy(true)
    onError("")
    try {
      onCreated(await createCredential(session, { name: name.trim(), scope, type }))
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-wrap items-end gap-3 border-b bg-muted/30 px-4 py-3">
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-muted-foreground">
        资源名称
        <input
          className="input"
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder="registry-oilan"
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        归属
        <select
          className="input min-w-32"
          value={scope}
          onChange={(event) => setScope(event.target.value as CredentialScope)}
        >
          <option value="personal">个人</option>
          <option value="platform">平台</option>
        </select>
      </label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        类型
        <select
          className="input min-w-44"
          value={type}
          onChange={(event) => setType(event.target.value as CredentialType)}
        >
          {Object.entries(TYPE_LABELS).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
      </label>
      <button
        onClick={onCancel}
        className="rounded-md border px-3 py-2 text-xs text-foreground hover:bg-muted"
      >
        取消
      </button>
      <button
        onClick={submit}
        disabled={busy || !name.trim()}
        className="rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
      >
        {busy ? "创建中" : "创建"}
      </button>
    </div>
  )
}

function DialogFrame({
  title,
  children,
  onClose,
}: {
  title: string
  children: React.ReactNode
  onClose: () => void
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
    >
      <div className="max-h-[90dvh] w-full max-w-xl overflow-auto rounded-lg border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          <button
            onClick={onClose}
            className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
          >
            关闭
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}

function PayloadDialog({
  session,
  credential,
  onClose,
  onDone,
  onError,
}: {
  session: Session
  credential: CredentialResource
  onClose: () => void
  onDone: (message: string) => void
  onError: (message: string) => void
}) {
  const [payload, setPayload] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit() {
    try {
      JSON.parse(payload)
    } catch {
      onError("凭据内容必须是合法 JSON")
      return
    }
    setBusy(true)
    onError("")
    try {
      const version = await putCredentialPayload(session, credential.id, payload)
      setPayload("")
      onDone(`已写入暂存版本 ${version.id}`)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setPayload("")
      setBusy(false)
    }
  }

  return (
    <DialogFrame
      title={`写入新版本 · ${credential.name}`}
      onClose={() => {
        setPayload("")
        onClose()
      }}
    >
      <div className="space-y-3 p-4">
        <p className="text-xs text-muted-foreground">
          内容只发送一次并加密持久化，提交后不会在页面或 API 中回显。
        </p>
        <textarea
          value={payload}
          onChange={(event) => setPayload(event.target.value)}
          rows={9}
          spellCheck={false}
          autoComplete="off"
          className="input w-full resize-y font-mono text-xs"
          placeholder='{"server":"registry.example.com","username":"deploy","password":"..."}'
        />
        <div className="flex justify-end">
          <button
            onClick={submit}
            disabled={busy || !payload.trim()}
            className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
          >
            <SaveIcon width={14} height={14} /> {busy ? "写入中" : "写入暂存版本"}
          </button>
        </div>
      </div>
    </DialogFrame>
  )
}

function GrantDialog({
  session,
  target,
  credential,
  onClose,
  onDone,
  onError,
}: {
  session: Session
  target: Target
  credential: CredentialResource
  onClose: () => void
  onDone: (message: string) => void
  onError: (message: string) => void
}) {
  const use = TYPE_USES[credential.type]
  const [namespace, setNamespace] = useState("default")
  const [workloadKind, setWorkloadKind] = useState("deployment")
  const [workloadName, setWorkloadName] = useState("")
  const [imageRepository, setImageRepository] = useState("")
  const [imageDigest, setImageDigest] = useState("")
  const [helmRelease, setHelmRelease] = useState("")
  const [helmChart, setHelmChart] = useState("")
  const [requiredKeys, setRequiredKeys] = useState("")
  const [busy, setBusy] = useState(false)
  const requiredKeyList = useMemo(
    () =>
      requiredKeys
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean),
    [requiredKeys],
  )

  async function submit() {
    const scope: CredentialGrantScope = {
      cluster: target.cluster,
      instance: target.instance,
      namespace: namespace.trim(),
      use,
      workload: { kind: workloadKind, name: workloadName.trim() },
    }
    if (use === "imagePull") {
      scope.image_repository = imageRepository.trim()
      scope.image_digest = imageDigest.trim()
    } else if (use === "helmPull") {
      scope.helm_release = helmRelease.trim()
      scope.helm_chart = helmChart.trim()
    } else if (use === "opaqueSecret") {
      scope.required_keys = requiredKeyList
    }
    setBusy(true)
    onError("")
    try {
      await grantCredentialToSelf(session, credential.id, scope)
      onDone(`已授权 ${credential.name} 到 ${target.cluster}/${target.instance}`)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <DialogFrame title={`授权当前目标 · ${credential.name}`} onClose={onClose}>
      <div className="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2">
        <ReadOnlyField label="目标" value={`${target.cluster}/${target.instance}`} />
        <ReadOnlyField label="用途" value={use} />
        <TextField label="Namespace" value={namespace} onChange={setNamespace} />
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Workload kind
          <select
            className="input"
            value={workloadKind}
            onChange={(event) => setWorkloadKind(event.target.value)}
          >
            <option value="deployment">Deployment</option>
            <option value="statefulset">StatefulSet</option>
            <option value="daemonset">DaemonSet</option>
          </select>
        </label>
        <TextField label="Workload name" value={workloadName} onChange={setWorkloadName} />
        {use === "imagePull" && (
          <>
            <TextField
              label="Image repository"
              value={imageRepository}
              onChange={setImageRepository}
            />
            <TextField
              label="Image digest"
              value={imageDigest}
              onChange={setImageDigest}
              placeholder="sha256:..."
            />
          </>
        )}
        {use === "helmPull" && (
          <>
            <TextField label="Helm release" value={helmRelease} onChange={setHelmRelease} />
            <TextField label="Helm chart" value={helmChart} onChange={setHelmChart} />
          </>
        )}
        {use === "opaqueSecret" && (
          <TextField
            label="Required keys"
            value={requiredKeys}
            onChange={setRequiredKeys}
            placeholder="username,password"
          />
        )}
        <div className="flex justify-end sm:col-span-2">
          <button
            onClick={submit}
            disabled={busy || !namespace.trim() || !workloadName.trim()}
            className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
          >
            <ShieldIcon width={14} height={14} /> {busy ? "授权中" : "确认授权"}
          </button>
        </div>
      </div>
    </DialogFrame>
  )
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      <input
        className="input"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
      />
    </label>
  )
}

function ReadOnlyField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      <div className="input font-mono text-xs text-foreground">{value}</div>
    </div>
  )
}
