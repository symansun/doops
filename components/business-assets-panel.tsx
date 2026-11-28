"use client"

import { useEffect, useRef, useState, type FormEvent } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { assetRequest } from "@/lib/assets"
import { formatDateTime } from "@/lib/format"
import { Badge, Button, EmptyState, FilterBar, Input, PageHeader, Select, Skeleton, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"

type Deployment = { cluster: string; instance: string; namespace: string; application: string; database: string; verified_at: string }
type BusinessSystem = { asset_id: string; name: string; owner: string; active: boolean; revision: number; deployments: Deployment[] }
type Props = { session: Session; targets: Target[]; onOpenSituation?: () => void }
const sourceKey = (cluster: string, instance: string) => JSON.stringify([cluster, instance])
const edgeKey = (d: Omit<Deployment, "verified_at">) => JSON.stringify([d.cluster, d.instance, d.namespace, d.application, d.database])

export function BusinessAssetsPanel({ session, targets, onOpenSituation }: Props) {
  const admin = sessionIsAdmin(session)
  const scope = admin ? [] : targets.map(t => sourceKey(t.cluster, t.instance)).sort()
  const identity = JSON.stringify([session.gateway, session.username, session.tokenId, session.issuedAt, admin, scope])
  return <Ledger key={identity} session={session} targets={targets} onOpenSituation={onOpenSituation} />
}

function Ledger({ session, targets, onOpenSituation }: Props) {
  const admin = sessionIsAdmin(session)
  const [systems, setSystems] = useState<BusinessSystem[] | null>(null)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [query, setQuery] = useState("")
  const [version, setVersion] = useState(0)
  const [editor, setEditor] = useState<{ system?: BusinessSystem } | null>(null)
  function refresh() { setError(""); setSystems(null); setVersion(v => v + 1) }
  useEffect(() => {
    let current = true
    assetRequest<{ systems: BusinessSystem[] }>("systems").then(result => {
      if (!Array.isArray(result.systems)) throw new Error("Gateway 返回的系统列表无效")
      if (current) setSystems(result.systems)
    }).catch(cause => { if (current) setError(cause instanceof Error ? cause.message : String(cause)) })
    return () => { current = false }
  }, [version])
  const term = query.trim().toLowerCase()
  const filtered = systems?.filter(s => [s.asset_id, s.name, s.owner, ...s.deployments.flatMap(d => [d.cluster, d.instance, d.namespace, d.application, d.database])].some(field => field.toLowerCase().includes(term)))
  return <section aria-label="业务系统台账" className="card-surface min-w-0 space-y-4 p-4">
    <PageHeader
      title="业务系统台账"
      badge={systems ? <Badge variant="outline" className="font-mono">{systems.length}</Badge> : undefined}
      description="维护业务系统与集群部署、数据库依赖的核验关系；全局业务→集群→机器立体架构与拓扑请在「态势感知」查看。"
      actions={!editor ? <>
        {onOpenSituation && <Button size="sm" variant="ghost" onClick={onOpenSituation}>前往态势感知</Button>}
        <Button size="sm" onClick={refresh} disabled={!systems && !error}>刷新</Button>
        {admin && <Button size="sm" variant="primary" disabled={!systems} onClick={() => { setNotice(""); setEditor({}) }}>登记系统</Button>}
      </> : undefined}
    />
    {notice && <p role="status" className="break-all text-sm text-success">{notice}</p>}
    {error ? <StatusBanner tone="error" className="flex flex-wrap items-center gap-2">{error}<Button size="sm" onClick={refresh}>重新读取</Button></StatusBanner>
      : !systems ? <div role="status" className="space-y-2.5"><span className="sr-only">正在读取业务系统…</span><Skeleton className="h-12 w-full" /><Skeleton className="h-12 w-full" /><Skeleton className="h-12 w-full" /></div> : null}
    {editor && admin ? <SystemForm system={editor.system} targets={targets} onCancel={() => setEditor(null)} onReload={() => { setEditor(null); refresh() }} onSaved={id => {
      setEditor(null); setQuery(""); setNotice(`已保存业务系统 ${id}`); refresh()
    }} /> : systems && <><FilterBar><Input type="search" scale="sm" className="w-full sm:w-80" aria-label="搜索业务系统" placeholder="搜索编号、负责人、应用或数据库" value={query} onChange={ev => setQuery(ev.target.value)} /></FilterBar>
      {filtered?.length ? <div className="divide-y rounded-lg border">{filtered.map(s => <details className="min-w-0 p-3" key={s.asset_id}>
        <summary className="cursor-pointer break-words text-sm"><strong>{s.name}</strong> <span className="break-all font-mono text-xs text-muted-foreground">{s.asset_id}</span> · {s.active ? "在役" : "退役"} · {s.owner}</summary>
        <div className="mt-3 space-y-2"><div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold">部署与数据库依赖</h3>{admin && <Button size="sm" onClick={() => { setNotice(""); setEditor({ system: s }) }}>编辑系统</Button>}</div>
          {s.deployments.length ? <Table><TableHeader><TableRow className="hover:bg-transparent">{["目标 / 命名空间", "应用", "依赖数据库", "人工核验时间"].map(title => <TableHead key={title} className="px-2 py-1.5">{title}</TableHead>)}</TableRow></TableHeader>
            <TableBody>{s.deployments.map(d => <TableRow key={edgeKey(d)} className="break-all"><TableCell className="p-2">{d.cluster}/{d.instance}<small className="block text-muted-foreground">{d.namespace}</small></TableCell><TableCell className="p-2">{d.application}</TableCell><TableCell className="p-2">{d.database || "未登记"}</TableCell><TableCell className="p-2"><time dateTime={d.verified_at}>{formatDateTime(d.verified_at)}</time></TableCell></TableRow>)}</TableBody></Table> : <p className="text-sm text-muted-foreground">尚未登记部署关系。</p>}
        </div></details>)}</div> : <EmptyState role="status" title={term ? "没有匹配的业务系统。" : admin ? "尚未登记业务系统。" : "当前账号没有可见的业务系统。"} action={term ? <Button size="sm" onClick={() => setQuery("")}>清空筛选</Button> : undefined} />}</>}
  </section>
}

function SystemForm({ system, targets, onCancel, onSaved, onReload }: {
  system?: BusinessSystem; targets: Target[]; onCancel: () => void; onSaved: (id: string) => void; onReload: () => void
}) {
  const [rows, setRows] = useState(() => (system?.deployments ?? []).map((d, index) => ({ id: index, initial: d })))
  const next = useRef(rows.length)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const sources = new Map(targets.filter(t => t.cluster && t.instance).map(t => [sourceKey(t.cluster,t.instance), t]))
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy) return
    const data = new FormData(event.currentTarget)
    const field = (name: string) => String(data.get(name) ?? "").trim()
    const deployments = rows.map(({ id }) => {
      const [cluster, instance] = JSON.parse(field(`source-${id}`)) as [string, string]
      return { cluster, instance, namespace: field(`namespace-${id}`), application: field(`application-${id}`), database: field(`database-${id}`) }
    })
    if (new Set(deployments.map(edgeKey)).size !== deployments.length) { setError("存在相同的部署与数据库关系，请移除重复行。"); return }
    const value = { asset_id: field("asset_id"), name: field("name"), owner: field("owner"), active: field("active") === "true", deployments }
    setError(""); setBusy(true)
    try {
      await assetRequest("systems", system ? { action: "update", system: value, expected_revision: system.revision } : { action: "create", system: value })
      onSaved(value.asset_id)
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) } finally { setBusy(false) }
  }
  return <form data-assist-scope="asset-entry" onSubmit={save} className="space-y-4"><h3 className="font-semibold">{system ? "编辑业务系统" : "登记业务系统"}</h3>
    <fieldset disabled={busy} className="grid gap-3 text-sm sm:grid-cols-2 [&_label]:grid [&_label]:gap-1">
      <label>系统编号<Input name="asset_id" required readOnly={!!system} maxLength={128} defaultValue={system?.asset_id} /></label><label>系统名称<Input name="name" required maxLength={256} defaultValue={system?.name} /></label>
      <label>负责人<Input name="owner" required maxLength={256} defaultValue={system?.owner} /></label><label>服役状态<Select name="active" defaultValue={String(system?.active ?? true)}><option value="true">在役</option><option value="false">退役</option></Select></label>
    </fieldset>
    <div className="space-y-2"><h4 className="text-sm font-semibold">部署与数据库依赖</h4><p className="text-xs text-muted-foreground">请核对所选 Agent 下的命名空间、应用及数据库。保存新关系时会记录人工核验人和时间。</p>
      {rows.map(({ id, initial: d }, index) => { const binding = d.cluster ? sourceKey(d.cluster,d.instance) : ""
        return <fieldset key={id} disabled={busy} className="grid min-w-0 gap-2 rounded-lg border p-3 text-sm sm:grid-cols-2 [&_label]:grid [&_label]:min-w-0 [&_label]:gap-1"><legend className="px-1 text-xs">关系 {index + 1}</legend>
          <label>来源目标<Select name={`source-${id}`} required defaultValue={binding}><option value="">选择 Agent 目标</option>{binding && !sources.has(binding) && <option value={binding}>{d.cluster}/{d.instance}（原登记来源）</option>}
            {Array.from(sources, ([key, t]) => <option key={key} value={key}>{t.cluster}/{t.instance}{t.status === "offline" ? "（离线）" : ""}</option>)}</Select></label>
          <label>命名空间<Input name={`namespace-${id}`} required maxLength={63} defaultValue={d.namespace} /></label><label>应用标识<Input name={`application-${id}`} required maxLength={128} defaultValue={d.application} /></label>
          <label>依赖数据库（可选）<Input name={`database-${id}`} maxLength={128} defaultValue={d.database} /></label><Button type="button" size="sm" className="justify-self-start" onClick={() => setRows(old => old.filter(r => r.id !== id))}>移除此关系</Button>
        </fieldset>})}
      <Button type="button" size="sm" disabled={busy || !sources.size} onClick={() => { const id = next.current++; setRows(old => [...old, { id, initial: { cluster: "", instance: "", namespace: "", application: "", database: "", verified_at: "" } }]) }}>添加部署关系</Button>
      {!sources.size && <p className="text-xs text-muted-foreground">当前没有可选目标，仍可登记系统基本信息。</p>}
    </div>
    {error && <StatusBanner tone="error" className="space-y-2"><p>{error}</p><Button type="button" size="sm" onClick={onReload}>放弃草稿并重新读取</Button></StatusBanner>}
    <div className="flex flex-wrap gap-2"><Button type="submit" size="sm" variant="primary" disabled={busy}>{busy ? "正在保存…" : rows.length ? "确认关系并保存" : "保存系统"}</Button><Button type="button" size="sm" disabled={busy} onClick={onCancel}>取消</Button></div>
  </form>
}
