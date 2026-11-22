"use client"

import { useEffect, useState } from "react"
import { assetRequest } from "@/lib/assets"
import { assetKinds, type CatalogKind } from "@/lib/console-views"
import { Button, Input, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"

type Row = { id: string; name: string; environment: string; revision: number }
const blank: Row = { id: "", name: "", environment: "", revision: 0 }
export function PersonalAssetPanel({ kind, demo, onOperate }: { kind: Exclude<CatalogKind, "credentials">; demo: boolean; onOperate: () => void }) {
  const [rows, setRows] = useState<Row[] | null>(null)
  const [draft, setDraft] = useState<Row | null>(null)
  const [remove, setRemove] = useState<Row | null>(null)
  const [query, setQuery] = useState("")
  const [version, setVersion] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  useEffect(() => {
    if (demo) return
    let active = true
    setRows(null); setError("")
    assetRequest<{ records: Row[] }>("records", undefined, new URLSearchParams({ kind }))
      .then(result => { if (active) setRows(result.records) }).catch(err => { if (active) setError(err.message) })
    return () => { active = false }
  }, [demo, kind, version])
  async function save(op: string, row: Row) {
    setBusy(true); setError("")
    try {
      await assetRequest("records", { kind, op, ...row }); setDraft(null); setRemove(null); setVersion(v => v + 1)
    } catch (err) { setError((err as Error).message) } finally { setBusy(false) }
  }
  const term = query.trim().toLocaleLowerCase()
  const filtered = rows?.filter(r => [r.id,r.name,r.environment].some(s => s.toLocaleLowerCase().includes(term)))
  return <main className="min-h-0 flex-1 space-y-4 overflow-auto p-4">
    <h1 className="text-xl font-semibold">{assetKinds[kind]}资产</h1>
    <p>我的登记只保存基本信息。删除登记不会删除实际资源，也不会改变 Agent 授权。请勿填写密码或连接串。</p>
    <div className="flex flex-wrap gap-2"><Button disabled={demo || !rows || busy} onClick={() => { setRemove(null); setDraft({...blank}) }}>新增登记</Button>
      <Button disabled={demo || busy} onClick={() => setVersion(v=>v+1)}>刷新</Button>
      <Button onClick={onOperate}>进入 Agent 操作台</Button></div>
    {demo && <p role="status">连接 Gateway 后可维护个人资产登记。</p>}
    {error && <StatusBanner tone="error">{error}</StatusBanner>}
    {draft && <form className="card-surface space-y-3 p-4" onSubmit={e => {e.preventDefault(); void save(draft.revision ? "update" : "create",draft)}}>
      <label className="flex flex-col gap-1 text-sm">资产编号<Input required disabled={busy || !!draft.revision} value={draft.id} onChange={e => setDraft({...draft,id:e.target.value})} className="font-mono" /></label>
      <label className="flex flex-col gap-1 text-sm">名称<Input required maxLength={80} value={draft.name} onChange={e => setDraft({...draft,name:e.target.value})} /></label>
      <label className="flex flex-col gap-1 text-sm">环境（选填）<Input maxLength={80} value={draft.environment} onChange={e => setDraft({...draft,environment:e.target.value})} /></label>
      <div className="flex gap-2"><Button type="submit" variant="primary" size="sm" disabled={busy}>保存</Button><Button type="button" size="sm" disabled={busy} onClick={()=>setDraft(null)}>取消</Button></div>
    </form>}
    {remove && <div className="card-surface flex flex-wrap items-center gap-2 p-4 text-sm"><span>确认删除「{remove.name}」的个人登记？</span><Button variant="danger" size="sm" disabled={busy} onClick={()=>void save("delete",remove)}>确认删除</Button><Button size="sm" disabled={busy} onClick={()=>setRemove(null)}>取消</Button></div>}
    {!demo && <><Input aria-label="搜索资产" placeholder="搜索编号、名称或环境" value={query} onChange={e=>setQuery(e.target.value)} />
      {!rows && !error && <p role="status" className="text-sm text-muted-foreground">正在读取…</p>}{filtered?.length === 0 && <p className="text-sm text-muted-foreground">{query ? "没有匹配的登记。" : "尚未登记此类资产。"}</p>}
      {!!filtered?.length && <Table wrapperClassName="rounded-lg border"><TableHeader className="bg-muted/40"><tr>{["编号","名称","环境","操作"].map(k=><TableHead key={k}>{k}</TableHead>)}</tr></TableHeader><TableBody>{filtered.map(row=><TableRow key={row.id}><TableCell className="font-mono text-xs">{row.id}</TableCell><TableCell className="font-medium">{row.name}</TableCell><TableCell>{row.environment || "—"}</TableCell><TableCell><div className="flex gap-1.5"><Button size="sm" disabled={busy} onClick={()=>{setDraft({...row});setRemove(null)}}>编辑</Button><Button size="sm" variant="danger-outline" disabled={busy} onClick={()=>{setRemove(row);setDraft(null)}}>删除</Button></div></TableCell></TableRow>)}</TableBody></Table>}</>}
  </main>
}
