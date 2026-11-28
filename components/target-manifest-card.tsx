"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { AssetRequestError, assetRequest, type ManifestResource, type TargetManifestResponse } from "@/lib/assets"
import type { TargetRef } from "@/lib/console-refresh"
import {
  filterManifestResources,
  manifestStatus,
  resourceKindLabel,
  showsAppliedRevision,
  summarizeManifestResources,
  truncatedResourceSummary,
} from "@/lib/target-manifest"
import { Badge, Button, Card, CardContent, Input, Select, StatusBanner } from "./ui"

const resourceStates = {
  verified: { label: "已核验", variant: "success" },
  discovered: { label: "已发现未登记", variant: "primary" },
  unverified: { label: "已登记未核验", variant: "warning" },
  lost: { label: "失联", variant: "danger" },
  conflict: { label: "冲突", variant: "danger" },
} as const

type Action = { action: "refresh" } | { action: "register"; ref: string }

export type TargetManifestCardProps = {
  target: TargetRef & { manifest_revision?: number; status?: string }
  mode?: "summary" | "full"
  onOpenAssets?: () => void
}

export function TargetManifestCard({ target, mode = "full", onOpenAssets }: TargetManifestCardProps) {
  const { cluster, instance, manifest_revision: revision, status } = target
  const offline = status === "offline"
  const [data, setData] = useState<TargetManifestResponse>()
  const [loading, setLoading] = useState(true)
  const [readError, setReadError] = useState<Error>()
  const [actionError, setActionError] = useState("")
  const [notice, setNotice] = useState("")
  const [retry, setRetry] = useState(0)
  const [pending, setPending] = useState<Action>()
  const [kindFilter, setKindFilter] = useState("all")
  const [stateFilter, setStateFilter] = useState("all")
  const [query, setQuery] = useState("")
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
  const resources = useMemo(() => manifest?.resources ?? [], [manifest])
  const summary = useMemo(() => summarizeManifestResources(resources), [resources])
  const filtered = useMemo(
    () => (mode === "full" ? filterManifestResources(resources, { kind: kindFilter, state: stateFilter, query }) : []),
    [mode, resources, kindFilter, stateFilter, query],
  )
  const busy = loading || Boolean(pending)
  const isSummary = mode === "summary"

  return (
    <Card as="section" aria-labelledby="target-manifest-title">
      <CardContent className="space-y-4 p-4 sm:p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <h3 id="target-manifest-title" className="text-base font-semibold">
              资源清单{data ? ` (${summary.total})` : ""}
            </h3>
            {sync && <Badge variant={sync.variant}>{sync.label}</Badge>}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {isSummary && onOpenAssets && (
              <Button size="sm" variant="primary" onClick={onOpenAssets}>
                {summary.total > 0 ? `查看全部资源 (${summary.total})` : "查看资源对账清单"}
              </Button>
            )}
            {readError && !forbidden ? (
              <Button size="sm" onClick={() => setRetry(count => count + 1)}>重试</Button>
            ) : !forbidden && (
              <Button size="sm" disabled={busy || !data?.online || !data.supported} onClick={() => void submit({ action: "refresh" })}>
                {pending?.action === "refresh" ? "正在重新同步…" : "重新同步"}
              </Button>
            )}
          </div>
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
          {manifest?.truncated && <StatusBanner compact>{truncatedResourceSummary(manifest)}</StatusBanner>}
          {summary.total > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              {summary.byKind.map(item => (
                isSummary ? (
                  <Badge key={item.kind} variant="outline">{item.label} {item.count}</Badge>
                ) : (
                  <button
                    key={item.kind}
                    type="button"
                    onClick={() => setKindFilter(current => current === item.kind ? "all" : item.kind)}
                    className={`rounded-md border px-2 py-0.5 text-xs transition ${
                      kindFilter === item.kind
                        ? "border-primary bg-primary/15 font-medium text-primary"
                        : "border-border bg-background/60 text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    {item.label} {item.count}
                  </button>
                )
              ))}
              {summary.byState.discovered > 0 && (
                <Badge variant="primary">待登记 {summary.byState.discovered}</Badge>
              )}
              {summary.byState.lost + summary.byState.conflict > 0 && (
                <Badge variant="danger">异常 {summary.byState.lost + summary.byState.conflict}</Badge>
              )}
            </div>
          )}
          {!isSummary && resources.length > 0 && (
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2">
                <Select aria-label="按资源类型筛选" scale="sm" value={kindFilter} onChange={e => setKindFilter(e.target.value)}>
                  <option value="all">全部类型 ({summary.total})</option>
                  {summary.byKind.map(item => (
                    <option key={item.kind} value={item.kind}>{item.label} ({item.count})</option>
                  ))}
                </Select>
                <Select aria-label="按核验状态筛选" scale="sm" value={stateFilter} onChange={e => setStateFilter(e.target.value)}>
                  <option value="all">全部状态</option>
                  {(Object.entries(resourceStates) as Array<[ManifestResource["state"], { label: string }]>).map(([k, cfg]) => (
                    <option key={k} value={k}>{cfg.label} ({summary.byState[k]})</option>
                  ))}
                </Select>
              </div>
              <Input
                type="search"
                scale="sm"
                className="w-full sm:w-64"
                aria-label="搜索资源清单"
                placeholder="搜索名称、标识或说明"
                value={query}
                onChange={e => setQuery(e.target.value)}
              />
            </div>
          )}
          {isSummary ? (
            summary.total === 0 && <p className="text-sm text-muted-foreground">此目标暂无资源记录。</p>
          ) : filtered.length > 0 ? (
            <ul aria-label="资源与核验状态" className="divide-y rounded-lg border">
              {filtered.map(resource => <ManifestRow key={resource.ref} resource={resource} disabled={busy}
                registering={pending?.action === "register" && pending.ref === resource.ref}
                onRegister={() => void submit({ action: "register", ref: resource.ref })} />)}
            </ul>
          ) : resources.length > 0 ? (
            <div className="space-y-2 py-4 text-center text-sm text-muted-foreground" role="status">
              <p>没有匹配当前筛选条件的资源。</p>
              <Button size="sm" onClick={() => { setKindFilter("all"); setStateFilter("all"); setQuery("") }}>清除筛选</Button>
            </div>
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
