"use client"

import { useEffect, useRef, useState } from "react"
import { AssetRequestError, assetRequest, type ManifestResource, type TargetManifestResponse } from "@/lib/assets"
import type { Target } from "@/lib/gateway"
import { manifestStatus, resourceKindLabel, showsAppliedRevision } from "@/lib/target-manifest"
import { Badge, Button, Card, CardContent, StatusBanner } from "./ui"

const resourceStates = {
  verified: { label: "已核验", variant: "success" },
  discovered: { label: "已发现未登记", variant: "primary" },
  unverified: { label: "已登记未核验", variant: "warning" },
  lost: { label: "失联", variant: "danger" },
  conflict: { label: "冲突", variant: "danger" },
} as const

type Action = { action: "refresh" } | { action: "register"; ref: string }

export function TargetManifestCard({ target }: { target: Target }) {
  const { cluster, instance, manifest_revision: revision, status } = target
  const offline = status === "offline"
  const [data, setData] = useState<TargetManifestResponse>()
  const [loading, setLoading] = useState(true)
  const [readError, setReadError] = useState<Error>()
  const [actionError, setActionError] = useState("")
  const [notice, setNotice] = useState("")
  const [retry, setRetry] = useState(0)
  const [pending, setPending] = useState<Action>()
  const submitting = useRef<AbortController | null>(null)

  useEffect(() => () => submitting.current?.abort(), [])

  useEffect(() => {
    if (pending) return
    const request = new AbortController()
    setLoading(true)
    setReadError(undefined)
    void assetRequest<TargetManifestResponse>("target-manifest", undefined, new URLSearchParams({ cluster, instance }), request.signal)
      .then(result => {
        if (!request.signal.aborted) setData(result)
      })
      .catch((error: Error) => {
        if (request.signal.aborted) return
        setData(undefined)
        setReadError(error)
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false)
      })
    return () => request.abort()
  }, [cluster, instance, offline, pending, retry, revision])

  async function submit(action: Action) {
    if (submitting.current) return
    const request = new AbortController()
    submitting.current = request
    setPending(action)
    setActionError("")
    setNotice("")
    try {
      const result = await assetRequest<TargetManifestResponse>("target-manifest", { cluster, instance, ...action }, undefined, request.signal)
      if (request.signal.aborted) return
      setData(result)
      setNotice(action.action === "refresh" ? "已请求重新同步。" : "已登记到资产台账。")
    } catch (error) {
      if (!request.signal.aborted) setActionError((error as Error).message)
    } finally {
      submitting.current = null
      if (!request.signal.aborted) setPending(undefined)
    }
  }

  const forbidden = readError instanceof AssetRequestError && readError.status === 403
  const manifest = data?.manifest
  const sync = data ? manifestStatus(data) : null
  const resources = manifest?.resources ?? []
  const busy = loading || Boolean(pending)

  return (
    <Card as="section" aria-labelledby="target-manifest-title">
        <CardContent className="space-y-4 p-4 sm:p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <h3 id="target-manifest-title" className="text-base font-semibold">资源清单</h3>
              {sync && <Badge variant={sync.variant}>{sync.label}</Badge>}
            </div>
            {readError && !forbidden ? (
              <Button size="sm" onClick={() => setRetry(count => count + 1)}>重试</Button>
            ) : !forbidden && (
              <Button size="sm" disabled={busy || !data?.online || !data.supported} onClick={() => void submit({ action: "refresh" })}>
                {pending?.action === "refresh" ? "正在重新同步…" : "重新同步"}
              </Button>
            )}
          </div>
          {readError && <StatusBanner tone="error">{forbidden ? "当前账号无权查看此目标的资源清单。" : `无法读取资源清单。${readError.message}`}</StatusBanner>}
          {actionError && <StatusBanner tone="error">{actionError}</StatusBanner>}
          {notice && <StatusBanner tone="success" compact>{notice}</StatusBanner>}
          {loading && !data && <p role="status" className="text-sm text-muted-foreground">正在读取资源清单…</p>}
          {data && <>
            <dl className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
              <div><dt className="text-xs text-muted-foreground">清单版本</dt><dd>{manifest?.revision ?? "暂无"}</dd></div>
              {showsAppliedRevision(data) && (
                <div><dt className="text-xs text-muted-foreground">Agent 已应用版本</dt><dd>{data.applied_revision || "暂无"}</dd></div>
              )}
              <div>
                <dt className="text-xs text-muted-foreground">最后观测时间</dt>
                <dd>{data.last_observed_at ? <time dateTime={data.last_observed_at}>{new Date(data.last_observed_at).toLocaleString("zh-CN", { hour12: false })}</time> : "暂无"}</dd>
              </div>
            </dl>
            {!data.supported && <StatusBanner tone="warning" compact>升级 Agent 后可同步观测结果，现有台账登记仍可查看。</StatusBanner>}
            {manifest?.truncated && <StatusBanner compact>资源较多，此处显示部分条目。</StatusBanner>}
            {resources.length > 0 ? (
              <ul aria-label="资源与核验状态" className="divide-y rounded-lg border">
                {resources.map(resource => <ManifestRow key={resource.ref} resource={resource} disabled={busy}
                  registering={pending?.action === "register" && pending.ref === resource.ref}
                  onRegister={() => void submit({ action: "register", ref: resource.ref })} />)}
              </ul>
            ) : <p className="text-sm text-muted-foreground">此目标暂无资源记录。</p>}
          </>}
        </CardContent>
    </Card>
  )
}

function ManifestRow({ resource, disabled, registering, onRegister }: {
  resource: ManifestResource
  disabled: boolean
  registering: boolean
  onRegister: () => void
}) {
  const state = resourceStates[resource.state]
  const sources = resource.source.map(source => source === "declared" ? "台账登记" : source === "observed" ? "Agent 观测" : source)
  return (
    <li className="flex flex-wrap items-start gap-x-3 gap-y-2 px-3 py-2.5">
      <div className="min-w-0 flex-1 basis-40 space-y-1">
        <p className="break-all text-sm font-medium">{resource.name}</p>
        <p className="break-words text-xs text-muted-foreground">{[resourceKindLabel(resource.kind), ...sources].join(" · ")}</p>
        {resource.reason && <p className={`break-words text-xs ${state.variant === "danger" ? "text-destructive" : "text-muted-foreground"}`}>{resource.reason}</p>}
      </div>
      <Badge variant={state.variant}>{state.label}</Badge>
      {resource.state === "discovered" && resource.can_register && (
        <Button size="sm" disabled={disabled} onClick={onRegister} aria-label={`${registering ? "正在登记" : "登记"} ${resource.name}`}>
          {registering ? "正在登记…" : "登记到台账"}
        </Button>
      )}
    </li>
  )
}
