"use client"

import { useEffect, useMemo, useState } from "react"
import { assetRequest } from "@/lib/assets"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import type { TargetRef } from "@/lib/console-refresh"
import { filterWorkspaceAssets, resourceKindLabel, type WorkspaceAssetRow } from "@/lib/target-manifest"
import { AgentPicker } from "./agent-picker"
import { TargetManifestCard } from "./target-manifest-card"
import { Button, EmptyState, Input, PageHeader, SegmentedControl, Select, Skeleton, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"

type ScopeTab = "manifest" | "workspace"

export function WorkspaceScopeSection({ target, manage, allowManageToggle = false }: {
  target: TargetRef
  manage: boolean
  allowManageToggle?: boolean
}) {
  const [editing, setEditing] = useState(manage)
  const effectiveManage = manage || editing
  const [assets, setAssets] = useState<WorkspaceAssetRow[] | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [kindFilter, setKindFilter] = useState("all")
  const [query, setQuery] = useState("")

  useEffect(() => {
    let active = true
    const params = new URLSearchParams({ cluster: target.cluster, instance: target.instance })
    if (effectiveManage) params.set("view", "manage")
    setAssets(null)
    setError("")
    assetRequest<{ assets: WorkspaceAssetRow[] }>("workspace", undefined, params)
      .then(data => { if (active) setAssets(data.assets) })
      .catch(err => { if (active) setError((err as Error).message) })
    return () => { active = false }
  }, [target.cluster, target.instance, effectiveManage, revision])

  const filtered = useMemo(
    () => (assets ? filterWorkspaceAssets(assets, { kind: kindFilter, query }) : []),
    [assets, kindFilter, query],
  )

  async function toggle(row: WorkspaceAssetRow) {
    setBusy(true)
    setError("")
    try {
      await assetRequest("workspace", {
        cluster: target.cluster,
        instance: target.instance,
        kind: row.kind,
        asset_id: row.asset_id,
        assigned: !row.assigned,
      })
      setRevision(v => v + 1)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="card-surface min-w-0 space-y-3 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-semibold">
          {target.cluster}/{target.instance} · {effectiveManage ? "台账资产分配范围" : "台账关联与分配资产"}
          {assets ? ` (${assets.length})` : ""}
        </h3>
        <div className="flex flex-wrap gap-2">
          {allowManageToggle && !manage && (
            <Button size="sm" variant={editing ? "primary" : "default"} disabled={busy} onClick={() => setEditing(v => !v)}>
              {editing ? "完成分配" : "管理分配范围"}
            </Button>
          )}
          <Button size="sm" disabled={busy} onClick={() => setRevision(v => v + 1)}>刷新</Button>
        </div>
      </div>
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {assets && assets.length > 0 && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Select aria-label="按台账类型筛选" scale="sm" value={kindFilter} onChange={e => setKindFilter(e.target.value)}>
            <option value="all">全部类型</option>
            <option value="machine">机器</option>
            <option value="cluster">集群</option>
            <option value="system">业务系统</option>
          </Select>
          <Input
            type="search"
            scale="sm"
            className="w-full sm:w-64"
            aria-label="搜索台账资产"
            placeholder="搜索名称、编号或管理地址"
            value={query}
            onChange={e => setQuery(e.target.value)}
          />
        </div>
      )}
      {assets === null ? (
        !error && <p role="status" className="text-sm text-muted-foreground">正在读取资产…</p>
      ) : !assets.length ? (
        <p className="text-sm text-muted-foreground">
          {effectiveManage
            ? "请先在平台资产台账登记机器、集群或业务系统。"
            : "当前 Agent 暂无台账关联或手工分配的资产；平台管理员可点击右上角「管理分配范围」就地分配，或切换至「实时发现资源」查看 Agent 自动探测到的集群、命名空间、数据库与凭据。"}
        </p>
      ) : !filtered.length ? (
        <div className="space-y-2 py-4 text-center text-sm text-muted-foreground" role="status">
          <p>没有匹配当前筛选条件的台账资产。</p>
          <Button size="sm" onClick={() => { setKindFilter("all"); setQuery("") }}>清除筛选</Button>
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              {["类型", "名称 / 编号", "管理地址", "范围来源", ...(effectiveManage ? ["调整范围"] : [])].map(label => (
                <TableHead key={label}>{label}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map(row => (
              <TableRow key={`${row.kind}:${row.asset_id}`}>
                <TableCell className="whitespace-nowrap">{resourceKindLabel(row.kind)}</TableCell>
                <TableCell>
                  {row.name}
                  <small className="block font-mono text-muted-foreground">{row.asset_id}</small>
                </TableCell>
                <TableCell className="font-mono text-xs">{row.address || "—"}</TableCell>
                <TableCell>{[row.linked && "台账关联", row.assigned && "平台分配"].filter(Boolean).join("、") || "未分配"}</TableCell>
                {effectiveManage && (
                  <TableCell>
                    <Button size="sm" disabled={busy || (row.linked && !row.assigned)} onClick={() => void toggle(row)}>
                      {row.assigned ? "撤销分配" : row.linked ? "已关联" : "分配"}
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

export function AgentAssets({ target, session, manage = false, onOpenMonitor }: {
  target: TargetRef & { manifest_revision?: number; status?: string }
  session?: Session
  manage?: boolean
  onOpenMonitor?: () => void
}) {
  const demo = Boolean(session?.demo)
  const admin = session ? sessionIsAdmin(session) : false
  const [tab, setTab] = useState<ScopeTab>(demo ? "workspace" : "manifest")

  if (manage) {
    return <WorkspaceScopeSection target={target} manage />
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">{target.cluster}/{target.instance} · 资源对账与范围</h2>
          <p className="text-xs text-muted-foreground">
            统一查看该 Agent 实时探测并同步的资源清单（集群、命名空间、数据库、容器、凭据等）与平台台账分配范围。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {!demo && (
            <SegmentedControl
              ariaLabel="资源对账视图"
              value={tab}
              options={[
                { value: "manifest", label: "实时发现资源" },
                { value: "workspace", label: "台账分配范围" },
              ]}
              onChange={setTab}
            />
          )}
          {onOpenMonitor && (
            <Button type="button" size="sm" variant="ghost" onClick={onOpenMonitor}>
              前往资源监控
            </Button>
          )}
        </div>
      </div>
      {tab === "manifest" && !demo ? (
        <TargetManifestCard target={target} mode="full" />
      ) : (
        <WorkspaceScopeSection target={target} manage={false} allowManageToggle={admin} />
      )}
    </div>
  )
}

export function AgentDirectory({ targets, error, loading, isAdmin, onRefresh, onEnter, onDeploy }: {
  targets: Target[]
  error: string
  loading: boolean
  isAdmin: boolean
  onRefresh: () => void
  onEnter: (target: Target) => void
  onDeploy: () => void
}) {
  return <section className="space-y-4 overflow-auto p-4">
    <PageHeader
      title="选择 Agent"
      actions={
        <>
          <Button size="sm" disabled={loading} onClick={onRefresh}>刷新列表</Button>
          {isAdmin && <Button size="sm" variant="primary" onClick={onDeploy}>接入 Agent</Button>}
        </>
      }
    />
    {error && <StatusBanner tone="error">{error}</StatusBanner>}
    {targets.length > 0 ? (
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{targets.map(target => (
        <button key={target.key} type="button" className="card-surface space-y-2 p-4 text-left hover:border-primary" onClick={() => onEnter(target)}>
          <span className="block break-all font-semibold">{target.key}</span>
          <span className="text-sm text-muted-foreground">{target.status === "offline" ? "离线 · 查看资产与历史指标" : "在线 · 进入工作区"}</span>
        </button>
      ))}</div>
    ) : !error ? (
      loading ? (
        <div role="status" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <span className="sr-only">正在读取 Agent…</span>
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : (
        <EmptyState role="status" title="当前账号暂无可见 Agent。" action={isAdmin ? <Button size="sm" variant="primary" onClick={onDeploy}>接入 Agent</Button> : undefined} />
      )
    ) : null}
  </section>
}

export function PlatformAssetScope({ targets, loading, error }: { targets: TargetRef[]; loading: boolean; error: string }) {
  return <div className="space-y-4">
    <h2 className="text-lg font-semibold">Agent管理</h2>
    <p className="text-sm text-muted-foreground">跨 Agent 集中分配机器、集群或业务系统（单目标就地调整与实时发现资源对账请前往「目标运维 → Agent 概览」）。同一资产可分配给多个 Agent；台账直接关联由对应资产详情维护。</p>
    {error ? <StatusBanner tone="error">{error}</StatusBanner> : targets.length ? (
      <AgentPicker targets={targets} label="目标 Agent">{target => <WorkspaceScopeSection target={target} manage />}</AgentPicker>
    ) : <p role="status">{loading ? "正在读取 Agent…" : "平台尚未登记 Agent。"}</p>}
  </div>
}
