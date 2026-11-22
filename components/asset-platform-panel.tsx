"use client"

import { Fragment, useEffect, useState } from "react"
import { assetRequest } from "@/lib/assets"
import type { Session, Target } from "@/lib/client"
import { Button, PageTabs, Select } from "./ui"
import { AssetTopologyPanel } from "./asset-topology-panel"
import { AssetCoveragePanel, AssetCoverageSummary } from "./asset-coverage-panel"
import { AssetAlertSummary, AssetMonitorPanel, type AssetMonitorSelection } from "./asset-monitor-panel"
import { AssetInspectionPanel, AssetWeeklySummary } from "./asset-inspection-panel"

type ClusterRow = {
  asset_id: string; name: string; sources: number; online: number; namespaces: number
  machines: number; systems: number; verified_at: string
}
type Dashboard = { machines: number; systems: number; clusters: ClusterRow[]; unassigned: {asset_id: string; name: string}[] }
const views = [
  { id: "overview", label: "概览" }, { id: "topology", label: "拓扑" }, { id: "monitor", label: "监控" },
  { id: "inspections", label: "巡检" }, { id: "coverage", label: "覆盖率" },
] as const

type DashboardProps = {
  session: Session; targets: Target[]
  initialClusterId?: string
  onMachine: (id?: string) => void
}
export function AssetPlatformPanel({session, targets, initialClusterId, onMachine}: DashboardProps) {
  const [clusterId, setClusterId] = useState(initialClusterId)
  const [view,setView] = useState<(typeof views)[number]["id"]>("overview")
  const [metric, setMetric] = useState<AssetMonitorSelection>()
  const [runId, setRunId] = useState<number>()
  const [data,setData] = useState<Dashboard | null>(null)
  const [error,setError] = useState("")
  const [revision,setRevision] = useState(0)
  useEffect(() => {
    let current = true
    setData(null)
    setError("")
    assetRequest<Dashboard>("dashboard").then(result => { if(current) setData(result) })
      .catch(reason => { if(current) setError((reason as Error).message) })
    return () => { current = false }
  }, [revision])

  function selectScope(id?: string) {
    setMetric(undefined)
    setRunId(undefined)
    setClusterId(id)
  }
  const selected = clusterId ? data?.clusters.find(c => c.asset_id === clusterId) : undefined
  return <section className="space-y-4" aria-label="资产看板">
    <header className="flex flex-wrap items-center justify-between gap-3">
      <label className="flex min-w-0 items-center gap-2 text-sm">
        <span className="shrink-0">看板范围</span>
        {data && !data.clusters.length && !clusterId ? <span>全平台（可见资产）</span> :
        <Select aria-label="看板范围" scale="sm" className="min-w-0 max-w-72" value={clusterId ?? ""} disabled={!data}
          onChange={event => selectScope(event.target.value || undefined)}>
          <option value="">全平台（可见资产）</option>
          {clusterId && !selected && <option value={clusterId} disabled>{clusterId}</option>}
          {data?.clusters.map(row => <option key={row.asset_id} value={row.asset_id}>{row.name} · {row.asset_id}</option>)}
        </Select>}
      </label>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={() => setRevision(v => v+1)}>刷新看板</Button>
      </div>
    </header>
    {error ? <p role="alert" className="status-error">{error}</p> : !data ? <p role="status">正在读取资产…</p> : clusterId && !selected ? <p role="status">此集群已不在可见台账中，请切换看板范围。</p> : <Fragment key={JSON.stringify(clusterId ?? null)}>
      <PageTabs ariaLabel="看板视图" items={views} value={view} onChange={next => {
        setMetric(undefined)
        setRunId(undefined)
        setView(next)
      }} />
      {view === "overview" && <>
        <AssetCoverageSummary clusterId={clusterId} onOpen={() => setView("coverage")} />
        <dl className="grid grid-cols-2 gap-3 md:grid-cols-4">
          {Object.entries(selected ? {"访问来源": selected.sources,"已核验命名空间":selected.namespaces,"关联机器":selected.machines,"在役业务系统":selected.systems} : {"可见集群":data.clusters.length,"机器":data.machines,"在役业务系统":data.systems,"未关联集群的机器":data.unassigned.length}).map(([label,value])=><div key={label} className="card-surface p-4"><dt className="text-xs text-muted-foreground">{label}</dt><dd className="mt-1 font-mono text-2xl">{value}</dd></div>)}
        </dl>
        <AssetAlertSummary clusterId={clusterId} onOpen={selection => { setMetric(selection); setView("monitor") }} />
        <AssetWeeklySummary clusterId={clusterId} onOpen={id => { setRunId(id); setView("inspections") }} />
        <div className="card-surface space-y-2 p-4"><h3 className="font-semibold">{selected?"访问状态":"集群对比"}</h3>
          {data.clusters.length===0 ? <p className="text-sm text-muted-foreground">暂无可见集群，可在「集群台账」中查看发现入口。</p> : <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead><tr>{["集群","在线 / 来源","命名空间","机器","在役系统","最后核验"].map(label=><th key={label} className="p-2">{label}</th>)}</tr></thead><tbody>
            {(selected?[selected]:data.clusters).map(c=><tr key={c.asset_id} className="border-t"><td className="p-2">{selected?c.name:<Button size="sm" onClick={()=>selectScope(c.asset_id)}>{c.name}</Button>}<small className="block font-mono text-muted-foreground">{c.asset_id}</small></td><td>{c.online} / {c.sources}</td><td>{c.namespaces}</td><td>{c.machines}</td><td>{c.systems}</td><td className="whitespace-nowrap">{new Date(c.verified_at).toLocaleString()}</td></tr>)}
          </tbody></table></div>}
        </div>
        {!selected && <section className="card-surface space-y-2 p-4"><h3 className="font-semibold">未关联集群的机器</h3>{data.unassigned.length ? <div className="flex flex-wrap gap-2">{data.unassigned.map(m=><Button size="sm" key={m.asset_id} onClick={()=>onMachine(m.asset_id)}>{m.name} · {m.asset_id}</Button>)}</div>:<p className="text-sm text-muted-foreground">当前可见范围内没有待关联机器。</p>}</section>}
      </>}
      {view === "topology" && <AssetTopologyPanel clusterId={clusterId}/>}
      {view === "coverage" && <AssetCoveragePanel session={session} clusterId={clusterId}/>}
      {view === "monitor" && <AssetMonitorPanel session={session} targets={targets} clusterId={clusterId} initialSelection={metric} machineCount={selected?.machines ?? data.machines} onOpenMachines={() => onMachine()}/>}
      {view === "inspections" && <AssetInspectionPanel session={session} targets={targets} clusterId={clusterId} initialRunId={runId}/>}
    </Fragment>}
  </section>
}
