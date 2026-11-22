"use client"

import { useCallback, useEffect, useState } from "react"
import { assetRequest } from "@/lib/assets"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { formatDateTime } from "@/lib/format"
import { Button, Input, Select } from "./ui"
import type { ClusterListState, ClusterSourcePage, RenderAssetTargetAction } from "./asset-inventory"
import { AssetNodeLinks } from "./asset-node-links"

type NamespaceRecord = { name: string; verified_at?: string; scope: "list_pods" | "list_namespaces" }
type ClusterRecord = {
  asset_id: string
  name: string
  cluster: string
  instance: string
  verified_at: string
  namespaces: NamespaceRecord[]
}
type Profile = {id:string;name:string;environment:string;contact:string;revision:number}
const emptyProfile:Profile={id:"",name:"",environment:"",contact:"",revision:0}
type ImportInput = { asset_id: string; name: string; cluster: string; instance: string; namespace?: string }
const emptyForm = { id: "", name: "", target: "", namespace: "" }

const observedTime = (value?: string) => formatDateTime(value)

export function ClusterAssetsPanel({ session, targets, selectedTarget, initialPage, onOpenTargetK8s, renderTargetAction }: {
  session: Session
  targets: Target[]
  selectedTarget?: Target | null
  onOpenTargetK8s?: (target: Target) => void
  initialPage?: ClusterListState
  renderTargetAction: RenderAssetTargetAction
}) {
  const [records, setRecords] = useState<ClusterRecord[]>([])
  const [profiles,setProfiles]=useState<Profile[]>([])
  const [editor,setEditor]=useState<Profile|null>(null)
  const [confirm,setConfirm]=useState<{profile:Profile;source?:ClusterRecord}|null>(null)
  const [reading, setReading] = useState(true)
  const [readError, setReadError] = useState("")
  const [query, setQuery] = useState(initialPage?.query ?? "")
  const [form, setForm] = useState({ ...emptyForm })
  const [open, setOpen] = useState(false)
  const [attaching,setAttaching]=useState(false)
  const [expanded, setExpanded] = useState(() => new Set(initialPage?.expanded))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [preview, setPreview] = useState<{ input: ImportInput; namespaces: NamespaceRecord[] } | null>(null)
  const online = targets.filter(target => target.status !== "offline")
  const admin = sessionIsAdmin(session)
  const read = useCallback(async () => {
    setReading(true)
    setReadError("")
    try {
      const [{clusters},{profiles}] = await Promise.all([assetRequest<{clusters: ClusterRecord[]}>("clusters"),assetRequest<{profiles:Profile[]}>("cluster-profiles")])
      setProfiles(profiles)
      setRecords(clusters)
    } catch (reason) {
      setRecords([]); setProfiles([])
      setReadError((reason as Error).message)
    } finally {
      setReading(false)
    }
  }, [])
  useEffect(() => { void read() }, [read])

  function toggleDetail(key: string, show: boolean) {
    setExpanded((previous) => {
      const next = new Set(previous)
      if (show) next.add(key)
      else next.delete(key)
      return next
    })
    if (show && !admin) void read()
  }

  function start(record?: ClusterRecord, pending?: Profile) {
    setEditor(null);setConfirm(null);setAttaching(!!pending)
    const target = record && online.find((item) => item.cluster === record.cluster && item.instance === record.instance)
    setForm(record ? { id: record.asset_id, name: record.name, target: target?.key || "", namespace: "" } : { ...emptyForm, id:pending?.id || "k-"+crypto.randomUUID(), name:pending?.name || "" })
    setError("")
    setNotice("")
    setPreview(null)
    setOpen(true)
  }
  function change(field: keyof typeof form, value: string) {
    setForm((old) => ({ ...old, [field]: value }))
    setPreview(null)
    setError("")
  }
  async function discover() {
    const target = online.find((item) => item.key === form.target)
    if (!target) { setError("请选择一个在线 Agent"); return }
    const input: ImportInput = {
      asset_id: form.id.trim(), name: form.name.trim(), cluster: target.cluster, instance: target.instance,
      ...(form.namespace.trim() ? { namespace: form.namespace.trim() } : {}),
    }
    setPreview(null)
    setError("")
    setBusy(true)
    try {
      const result = await assetRequest<{namespaces: NamespaceRecord[]}>("clusters", { action: "discover", ...input })
      setPreview({ input, namespaces: result.namespaces })
    } catch (reason) { setError((reason as Error).message) }
    finally { setBusy(false) }
  }
  async function save() {
    if (!preview) return
    setBusy(true)
    setError("")
    try {
      const result = await assetRequest<{namespaces: NamespaceRecord[]}>("clusters", { action: attaching ? "attach" : "import", ...preview.input })
      setNotice(`已保存 ${preview.input.asset_id}，本次验证 ${result.namespaces.length} 个命名空间。`)
      setOpen(false)
      setPreview(null)
      await read()
    } catch (reason) { setError((reason as Error).message) }
    finally { setBusy(false) }
  }
  async function maintain(op:string, profile:Profile, source?:ClusterRecord) {
    setBusy(true);setError("");setNotice("")
    try {await assetRequest("cluster-profiles",{...profile,op,...(source?{cluster:source.cluster,instance:source.instance,verified_at:source.verified_at}:{})});setEditor(null);setConfirm(null);setNotice(op==="unlink"?"已解除来源关联，未修改实际集群。":op==="delete"?"已移除待接入登记。":"集群资料已保存。");await read()}
    catch(e){setError((e as Error).message)} finally{setBusy(false)}
  }
  const term = query.toLowerCase().trim()
  const byId=new Map(profiles.map(p=>[p.id,p]));const pending=profiles.filter(p=>!records.some(r=>r.asset_id===p.id));const matches=(p:Profile)=>[p.id,p.name,p.environment,p.contact,"待接入"].join(" ").toLowerCase().includes(term)
  const visible = records.filter((row) => [byId.get(row.asset_id)?.environment ?? "", byId.get(row.asset_id)?.contact ?? "", row.asset_id, row.name, row.cluster, row.instance, ...row.namespaces.map((space) => space.name)]
    .some((value) => value.toLowerCase().includes(term)))
  const byAsset = new Map<string, ClusterRecord[]>()
  for (const source of visible) {
    const entries = byAsset.get(source.asset_id)
    if (entries) entries.push(source)
    else byAsset.set(source.asset_id, [source])
  }

  return <section aria-label="K8s 集群资产" className="space-y-4">
    <header className="flex flex-wrap items-center justify-between gap-3">
      <div><h1 className="text-xl font-semibold">Kubernetes 集群台账</h1><p className="text-sm text-muted-foreground">登记待接入的集群，或从 Agent 验证并纳管。命名空间来自实际发现。</p></div>
      <div className="flex flex-wrap gap-2">
        {selectedTarget && selectedTarget.status !== "offline" && selectedTarget.kubernetes_available !== false && onOpenTargetK8s && (
          <Button size="sm" disabled={busy} onClick={() => onOpenTargetK8s(selectedTarget)} title={`直接浏览 ${selectedTarget.cluster}/${selectedTarget.instance} 的实时 Pod、Deployment 与节点`}>
            直接查看当前目标（{selectedTarget.instance}）
          </Button>
        )}
        <Button size="sm" disabled={reading||busy||!!readError} onClick={()=>{setOpen(false);setConfirm(null);setError("");setEditor({...emptyProfile})}}>登记待接入集群</Button>
        <Button size="sm" disabled={reading || busy} onClick={() => void read()}>刷新列表</Button>
        <Button size="sm" variant="primary" disabled={busy || !online.length} onClick={() => start()}>{admin ? "从 Agent 导入" : "从 Agent 发现"}</Button>
      </div>
    </header>
    {notice && <p role="status" className="text-sm text-primary">{notice}</p>}
    {!open && error && <p role="alert" className="status-error">{error}</p>}
    {editor && <form data-assist-scope="asset-entry" className="card-surface space-y-3 p-4" onSubmit={e=>{e.preventDefault();void maintain(editor.id?"update":"create",editor)}}><h2>{editor.id?"编辑集群资料":"登记待接入集群"}</h2><fieldset className="grid gap-3 sm:grid-cols-3" disabled={busy}>{([["name","集群名称"],["environment","环境"],["contact","负责人"]] as const).map(([key,label])=><label key={key}>{label}<Input value={editor[key]} required={key==="name"} maxLength={80} onChange={e=>setEditor({...editor,[key]:e.target.value})}/></label>)}</fieldset><Button disabled={busy} type="submit">保存集群资料</Button> <Button type="button" onClick={()=>setEditor(null)}>取消</Button></form>}
    {confirm && <div className="card-surface space-y-2 p-3"><p>{confirm.source ? "解除 "+confirm.source.cluster+"/"+confirm.source.instance+" 的来源关联？此来源的命名空间和采集快照将移除；不会修改实际集群。" : "仅移除「"+confirm.profile.name+"」的待接入登记，不销毁集群。"}</p><Button disabled={busy} onClick={()=>void maintain(confirm.source?"unlink":"delete",confirm.profile,confirm.source)}>确认{confirm.source?"解除关联":"移除"}</Button> <Button disabled={busy} onClick={()=>setConfirm(null)}>取消</Button></div>}
    {open && <form data-assist-scope="asset-entry" className="card-surface space-y-3 p-4" onSubmit={(e) => { e.preventDefault(); void discover() }}>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {attaching && <p className="text-sm">纳管「{form.name}」后，对所选来源有授权的用户将能查看其资料。</p>}
        {admin && !attaching && <>
          <label className="text-sm">归入集群<Select value={form.id} disabled={busy} onChange={e=>{const p=profiles.find(x=>x.id===e.target.value);const r=records.find(x=>x.asset_id===e.target.value);setForm({...form,id:e.target.value,name:p?.name??r?.name??form.name});setPreview(null)}}>{!profiles.some(p=>p.id===form.id)&&!records.some(r=>r.asset_id===form.id)&&<option value={form.id}>新集群（自动编号）</option>}{[...new Map([...records.map(r=>[r.asset_id,r.name] as const),...profiles.map(p=>[p.id,p.name] as const)])].map(([id,name])=><option key={id} value={id}>{name} · {id}</option>)}</Select></label>
          <label className="text-sm">集群名称<Input required value={form.name} disabled={busy} onChange={(e) => change("name", e.target.value)} /></label>
        </>}
        <label className="text-sm">来源 Agent<Select required value={form.target} disabled={busy} onChange={(e) => change("target", e.target.value)}>
          <option value="">请选择目标</option>
          {online.map((t) => <option key={t.key} value={t.key}>{t.cluster}/{t.instance}</option>)}
        </Select></label>
        <label className="text-sm">指定命名空间<Input maxLength={63} value={form.namespace} disabled={busy} placeholder="留空发现整个集群" onChange={(e) => change("namespace", e.target.value)} /></label>
      </div>
      <p className="text-xs text-muted-foreground">{form.namespace.trim() ? "将验证该命名空间的 Pod 列举权限。" : "将读取来源 Agent 有权列举的命名空间。"}</p>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {preview && <div aria-label="发现结果" className="rounded-md border p-3 text-sm">
        <p className="font-medium">发现结果 · {preview.namespaces.length} 个命名空间</p>
        <ul className="mt-1 max-h-44 overflow-auto">{preview.namespaces.map((ns) => <li key={ns.name} className="font-mono">{ns.name}</li>)}</ul>
      </div>}
      <div className="flex flex-wrap gap-2">
        <Button disabled={busy} size="sm" type="submit">验证并预览</Button>
        {(admin || attaching) && preview && <Button disabled={busy} size="sm" type="button" variant="primary" onClick={() => void save()}>确认导入</Button>}
        <Button disabled={busy} type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>关闭</Button>
        {busy && <span role="status" className="self-center text-xs text-muted-foreground">正在处理…</span>}
      </div>
    </form>}
    <Input type="search" scale="sm" className="max-w-sm" aria-label="搜索集群资产" placeholder="搜索资产编号、集群或命名空间" value={query} onChange={(e) => setQuery(e.target.value)} />
    {!reading&&!readError&&pending.filter(matches).map(p=><article key={p.id} className="card-surface space-y-2 p-4"><h3 className="font-semibold">{p.name} · 待接入</h3><p className="text-sm">{p.environment||"未填环境"} · {p.contact||"未填负责人"} · 未验证命名空间</p><div className="flex flex-wrap gap-2"><Button size="sm" onClick={()=>{setOpen(false);setConfirm(null);setEditor(p)}}>编辑资料</Button><Button size="sm" disabled={!online.length} onClick={()=>start(undefined,p)}>关联来源 Agent</Button><Button size="sm" onClick={()=>{setOpen(false);setEditor(null);setConfirm({profile:p})}}>移出台账</Button></div></article>)}
    {readError ? <p role="alert" className="text-sm text-destructive">{readError}</p> : reading ? <p role="status">正在读取台账…</p>
      : !visible.length && !pending.some(matches) ? <p className="py-4 text-sm text-muted-foreground">{records.length ? "无匹配集群" : "当前没有可见的集群资产"}</p>
      : [...byAsset].map(([id, sources]) => <article key={id} className="card-surface space-y-3 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="font-semibold">{sources[0].name} <span className="text-xs font-normal text-muted-foreground">{id}</span></h3>
          <span className="text-sm">{byId.get(id)?.environment||"未填环境"} · {byId.get(id)?.contact||"未填负责人"}</span>
          <Button size="sm" onClick={()=>{setOpen(false);setConfirm(null);setEditor(byId.get(id)??{...emptyProfile,id,name:sources[0].name})}}>编辑资料</Button>
        </div>
        {sources.map((row) => {
        const target = online.find((t) => t.cluster === row.cluster && t.instance === row.instance)
        const source = JSON.stringify([id, row.cluster, row.instance])
        const origin: ClusterSourcePage = {
          section: "clusters", source: { assetId: id, cluster: row.cluster, instance: row.instance }, query, expanded: [...expanded],
        }
        const namespacesKey = `namespaces:${source}`
        const nodesKey = `nodes:${source}`
        return <section key={JSON.stringify([row.cluster, row.instance])} className="border-t pt-3">
          <div className="flex flex-wrap justify-between gap-2">
            <p className="mt-1 break-all text-xs text-muted-foreground">{row.cluster}/{row.instance} · {target ? "Agent 在线" : "Agent 离线"} · 最近验证 {observedTime(row.verified_at)}</p>
            <div className="flex flex-wrap gap-2">
              {admin && <Button size="sm" disabled={!target || busy} onClick={() => start(row)}>更新纳管范围</Button>}
              {admin && <Button size="sm" disabled={busy} onClick={()=>{setOpen(false);setEditor(null);setConfirm({profile:byId.get(id)??{...emptyProfile,id,name:row.name},source:row})}}>解除来源关联</Button>}
              {renderTargetAction({ target, origin, tab: "credentials", label: "查看目标凭据" })}
              {target?.kubernetes_available !== false && renderTargetAction({ target, origin, tab: "k8s", label: "进入 K8s 实时资源" })}
            </div>
          </div>
          <details open={expanded.has(namespacesKey)} className="mt-3 border-t pt-2 text-sm">
            <summary className="cursor-pointer" onClick={(event) => {
              event.preventDefault()
              toggleDetail(namespacesKey, !expanded.has(namespacesKey))
            }}>已纳管命名空间（{row.namespaces.length}）</summary>
            {expanded.has(namespacesKey) && <ul className="mt-2 max-h-60 space-y-1 overflow-auto">{row.namespaces.map((ns) => <li key={ns.name} className="flex flex-wrap items-center justify-between gap-2">
              <span className="font-mono">{ns.name}</span>
              <span className="ml-auto text-xs text-muted-foreground">{ns.scope === "list_pods" ? "已验证 Pod 列举" : "已读取命名空间对象"} · {observedTime(ns.verified_at)}</span>
              {target?.kubernetes_available !== false && renderTargetAction({ target, tab: "k8s", label: `查看 ${ns.name} 工作负载`,
                origin: { ...origin, source: { ...origin.source, namespace: ns.name } } })}
            </li>)}</ul>}
          </details>
          <AssetNodeLinks assetId={row.asset_id} cluster={row.cluster} instance={row.instance} session={session}
            open={expanded.has(nodesKey)} onOpenChange={(show) => toggleDetail(nodesKey, show)} />
        </section>
      })}</article>)}
  </section>
}
