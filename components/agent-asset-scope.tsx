"use client"

import { useEffect, useState } from "react"
import { assetRequest } from "@/lib/assets"
import type { Target } from "@/lib/client"
import type { TargetRef } from "@/lib/console-refresh"
import { AgentPicker } from "./agent-picker"
import { Button, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"

const typeLabels = { machine: "机器", cluster: "集群", system: "业务系统" }
type AssetRow = { kind: keyof typeof typeLabels; asset_id: string; name: string; address?: string; linked: boolean; assigned: boolean }

export function AgentAssets({target, manage = false}: {target: TargetRef; manage?: boolean}) {
  const [assets,setAssets] = useState<AssetRow[] | null>(null)
  const [error,setError] = useState("")
  const [busy,setBusy] = useState(false)
  const [revision,setRevision] = useState(0)
  useEffect(() => {
    let active = true
    const query = new URLSearchParams({cluster: target.cluster, instance: target.instance})
    if (manage) query.set("view", "manage")
    setAssets(null); setError("")
    assetRequest<{assets:AssetRow[]}>("workspace", undefined, query)
      .then(data => { if(active) setAssets(data.assets) })
      .catch(err => { if(active) setError(err.message) })
    return () => { active = false }
  },[target.cluster,target.instance,manage,revision])

  async function toggle(row: AssetRow) {
    setBusy(true); setError("")
    try {
      await assetRequest("workspace", {cluster:target.cluster,instance:target.instance,kind:row.kind,asset_id:row.asset_id,assigned:!row.assigned})
      setRevision(v => v + 1)
    } catch(err) { setError((err as Error).message) }
    finally { setBusy(false) }
  }

  return <section className="card-surface min-w-0 space-y-3 p-4">
    <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="font-semibold">{target.cluster}/{target.instance} · {manage ? "资产使用范围" : "可用资产"}</h2>
      <Button size="sm" disabled={busy} onClick={()=>setRevision(v=>v+1)}>刷新</Button></div>
    {error && <StatusBanner tone="error">{error}</StatusBanner>}
    {assets === null ? !error && <p role="status" className="text-sm text-muted-foreground">正在读取资产…</p> : !assets.length ? <p className="text-sm text-muted-foreground">{manage ? "请先在平台资产台账登记资产。" : "当前 Agent 暂无关联或分配的资产。"}</p> :
      <Table><TableHeader><tr>{["类型","名称 / 编号","管理地址","范围来源",...(manage ? ["调整范围"] : [])].map(label=><TableHead key={label}>{label}</TableHead>)}</tr></TableHeader><TableBody>
        {assets.map(row=><TableRow key={row.kind+":"+row.asset_id}><TableCell>{typeLabels[row.kind]}</TableCell><TableCell>{row.name}<small className="block font-mono text-muted-foreground">{row.asset_id}</small></TableCell><TableCell className="font-mono text-xs">{row.address || "—"}</TableCell><TableCell>{[row.linked && "台账关联",row.assigned && "平台分配"].filter(Boolean).join("、") || "未分配"}</TableCell>
          {manage && <TableCell><Button size="sm" disabled={busy || (row.linked && !row.assigned)} onClick={()=>void toggle(row)}>{row.assigned ? "撤销分配" : row.linked ? "已关联" : "分配"}</Button></TableCell>}</TableRow>)}
      </TableBody></Table>}
  </section>
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
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h1 className="text-xl font-semibold">选择 Agent</h1>
      <div className="flex gap-2">
        <Button size="sm" disabled={loading} onClick={onRefresh}>刷新列表</Button>
        {isAdmin && <Button size="sm" onClick={onDeploy}>接入 Agent</Button>}
      </div>
    </div>
    {error && <p role="alert" className="status-error">{error}</p>}
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{targets.map(target => (
      <button key={target.key} className="card-surface space-y-2 p-4 text-left hover:border-primary" onClick={() => onEnter(target)}>
        <span className="block break-all font-semibold">{target.key}</span>
        <span className="text-sm text-muted-foreground">{target.status === "offline" ? "离线 · 查看资产与历史指标" : "在线 · 进入工作区"}</span>
      </button>
    ))}</div>
    {!targets.length && !error && <p role="status">{loading ? "正在读取 Agent…" : "当前账号暂无可见 Agent。"}</p>}
  </section>
}

export function PlatformAssetScope({ targets, loading, error }: { targets: TargetRef[]; loading: boolean; error: string }) {
  return <div className="space-y-4">
    <h2 className="text-lg font-semibold">Agent管理</h2>
    <p className="text-sm text-muted-foreground">选择已登记的 Agent，为其分配机器、集群或业务系统。同一资产可以分配给多个 Agent。台账关联请在对应资产的详情中维护。</p>
    {error ? <p role="alert" className="status-error">{error}</p> : targets.length ? (
      <AgentPicker targets={targets} label="目标 Agent">{target => <AgentAssets target={target} manage />}</AgentPicker>
    ) : <p role="status">{loading ? "正在读取 Agent…" : "平台尚未登记 Agent。"}</p>}
  </div>
}
