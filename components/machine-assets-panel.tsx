"use client"

import { useEffect, useState, type FormEvent } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { assetRequest } from "@/lib/assets"
import { Badge, Button, Checkbox, Input, SegmentedControl, Select, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"
import { MachineDiagnosticPanel } from "./machine-diagnostic-panel"
import { inspectionKey } from "@/lib/machine-inspection"
import { formatMachineCreatedDate, formatMachineCreatedDetail, resolveMachineCreatedAtPayload, toMachineDateInput } from "@/lib/machine-created-at"
import {
  DEFAULT_MACHINE_PAGE_SIZE,
  MACHINE_PAGE_SIZE_OPTIONS,
  paginateItems,
  visiblePageNumbers,
} from "@/lib/machine-pagination"
import { useMachineInspections } from "./use-machine-inspections"
import { MachineCsvImport } from "./machine-csv-import"
import { MachineBatchTagPanel } from "./machine-batch-tag-panel"
import type { MachinePage, RenderAssetTargetAction } from "./asset-inventory"
import { AssetNodeLinks } from "./asset-node-links"

type Machine = {
  asset_id: string
  name: string
  kind: "physical" | "vm"
  address: string
  cpu_count?: number | null
  memory_gib?: number | null
  disk_gib?: number | null
  host_asset_id?: string
  cluster?: string
  instance?: string
  created_at?: string
  last_seen?: string
  last_sample_ms?: number
  tags?: string[]
  revision: number
  login_accounts?: { username: string; credential_id: string }[]
}
type AgentFilter = "all" | "online" | "offline" | "unbound"
type PanelProps = { session: Session; targets: Target[]; directory?: Target[]; initialPage?: MachinePage; renderTargetAction: RenderAssetTargetAction; onOpenInspection?: () => void }
type Screen = { page: "list" | "create" | "csv" | "text" } | { page: "detail" | "edit" | "login"; id: string }
const kindLabel = { physical: "物理机", vm: "虚拟机" }
const kindOptions = [{ value: "all", label: "全部" }, { value: "physical", label: "物理机" }, { value: "vm", label: "虚拟机" }] as const
const agentFilterOptions = [
  { value: "all", label: "全部状态" },
  { value: "online", label: "Agent 在线" },
  { value: "offline", label: "Agent 离线" },
  { value: "unbound", label: "未接入" },
] as const
const capacities = [
  { key: "cpu_count", label: "CPU 数", unit: "核" },
  { key: "memory_gib", label: "内存", unit: "GiB" },
  { key: "disk_gib", label: "磁盘", unit: "GiB" },
] as const

function bindingKey({ cluster, instance }: Pick<Machine, "cluster" | "instance">) {
  return cluster && instance ? JSON.stringify([cluster, instance]) : ""
}

function connection(machine: Machine, target?: Target) {
  if (target) return "Agent 在线"
  return bindingKey(machine) ? "Agent 离线" : "未接入"
}

export function MachineAssetsPanel({ session, targets, directory, initialPage, renderTargetAction, onOpenInspection }: PanelProps) {
  const identity = JSON.stringify([session.gateway, session.username, session.tokenId, session.issuedAt, session.isAdmin])
  return <Inventory key={identity} session={session} targets={targets} directory={directory} initialPage={initialPage} renderTargetAction={renderTargetAction} onOpenInspection={onOpenInspection} />
}

function Inventory({ session, targets, directory, initialPage, renderTargetAction, onOpenInspection }: PanelProps) {
  const admin = sessionIsAdmin(session)
  const [machines, setMachines] = useState<Machine[] | null>(null)
  const [readError, setReadError] = useState("")
  const [version, setVersion] = useState(0)
  const [screen, setScreen] = useState<Screen>(initialPage ? { page: "detail", id: initialPage.machineId } : { page: "list" })
  const [nodesOpen, setNodesOpen] = useState(initialPage?.nodesOpen ?? false)
  const [kind, setKind] = useState<Machine["kind"] | "all">(initialPage?.kind ?? "all")
  const [agentFilter, setAgentFilter] = useState<AgentFilter>("all")
  const [selectedTags, setSelectedTags] = useState<string[]>(initialPage?.tags ?? [])
  const [query, setQuery] = useState(initialPage?.query ?? "")
  const [batchDiscoveryOpen, setBatchDiscoveryOpen] = useState(false)
  const [page, setPage] = useState<number>(initialPage?.page ?? 1)
  const [pageSize, setPageSize] = useState<number>(initialPage?.pageSize ?? DEFAULT_MACHINE_PAGE_SIZE)

  function refresh() {
    setMachines(null)
    setReadError("")
    setVersion((value) => value + 1)
  }
  useEffect(() => {
    let active = true
    async function load() {
      try {
        const result = await assetRequest<{ machines: Machine[] }>("machines")
        if (!Array.isArray(result.machines)) throw new Error("服务器返回的机器列表无效")
        if (active) setMachines(result.machines)
      } catch (error) {
        if (active) setReadError(error instanceof Error ? error.message : String(error))
      }
    }
    void load()
    return () => { active = false }
  }, [version])

  const online = new Map(targets.filter((t) => t.cluster && t.instance && t.status !== "offline").map((t) => [bindingKey(t), t]))
  const knownTargets = new Map((directory ?? targets).filter((t) => t.cluster && t.instance).map((t) => [bindingKey(t), t]))
  const selected = "id" in screen ? machines?.find((m) => m.asset_id === screen.id) : undefined
  function detail(id: string) {
    setNodesOpen(false)
    setScreen({ page: "detail", id })
    if (!admin) refresh()
  }
  const back = () => setScreen({ page: "list" })
  const availableTags = [...new Set([...(machines ?? []).flatMap(m => m.tags ?? []), ...selectedTags])].sort()
  const term = query.trim().toLowerCase()
  const filtered = screen.page === "list" ? machines?.filter((m) => {
    if (kind !== "all" && m.kind !== kind) return false
    if (agentFilter !== "all") {
      const bound = Boolean(bindingKey(m))
      const isOnline = bound && online.has(bindingKey(m))
      if (agentFilter === "online" && !isOnline) return false
      if (agentFilter === "offline" && (!bound || isOnline)) return false
      if (agentFilter === "unbound" && bound) return false
    }
    if (!selectedTags.every(tag => m.tags?.includes(tag))) return false
    return [...(m.tags ?? []), m.asset_id, m.name, m.address, m.cluster && `${m.cluster}/${m.instance}`].some((field) => field?.toLowerCase().includes(term))
  }) ?? [] : []
  const paged = paginateItems(filtered, page, pageSize)
  const inspection = useMachineInspections(machines, screen.page === "list" ? paged.items : [])

  function inspect(rows: Machine[]) {
    void inspection.inspect(rows, m => online.has(bindingKey(m)))
  }

  const groupedTags = availableTags.reduce<Record<string, string[]>>((acc, tag) => {
    const eq = tag.indexOf("=")
    const group = eq > 0 ? tag.slice(0, eq) : "通用标签"
    ;(acc[group] ||= []).push(tag)
    return acc
  }, {})

  return (
    <section aria-label="机器台账" className="card-surface min-w-0 space-y-4 p-4">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-xl font-semibold">机器台账{machines ? ` (${machines.length})` : ""}</h2>
        {admin && screen.page === "create" && <Button size="sm" onClick={() => setScreen({ page: "text" })}>文本导入</Button>}
        {screen.page === "list" && <div className="flex flex-wrap gap-2">
          {onOpenInspection && <Button size="sm" variant="ghost" onClick={onOpenInspection}>前往智能巡检</Button>}
          <Button
            size="sm"
            variant={batchDiscoveryOpen ? "primary" : "default"}
            disabled={!filtered.length || inspection.running}
            onClick={() => setBatchDiscoveryOpen((open) => !open)}
          >
            {batchDiscoveryOpen ? "收起部署挖掘打标" : "批量部署挖掘与打标"}
          </Button>
          <Button size="sm" variant="primary" disabled={!filtered.length || inspection.running} onClick={() => inspect(filtered)}>{inspection.running ? "检测中…" : "快捷主机采样"}</Button>
          <Button size="sm" onClick={refresh} disabled={inspection.running || (!machines && !readError)}>刷新</Button>
          {admin && <>
            <Button size="sm" disabled={!machines} onClick={() => setScreen({ page: "text" })}>文本导入</Button>
            <Button size="sm" disabled={!machines} onClick={() => setScreen({ page: "csv" })}>CSV 导入</Button>
            <Button size="sm" variant="primary" disabled={!machines} onClick={() => setScreen({ page: "create" })}>手工新增</Button>
          </>}
        </div>}
      </header>
      {machines === null && (
        readError ? <div role="alert" className="status-error flex flex-wrap items-center justify-between gap-2">
          <span className="break-words">读取机器台账失败。{readError}</span><Button size="sm" onClick={refresh}>重新读取台账</Button>
        </div> : <p role="status" className="text-sm text-muted-foreground">正在读取机器台账…</p>
      )}
      {admin && (screen.page === "csv" || screen.page === "text") && <MachineCsvImport key={screen.page} textMode={screen.page === "text"} machines={machines ?? []} onClose={back} onReadback={refresh} />}
      {admin && screen.page === "login" && <MachineCsvImport machines={[]} machine={{asset_id: screen.id, name: selected?.name ?? screen.id}} onClose={() => detail(screen.id)} onReadback={refresh} />}
      {machines && screen.page === "list" && <>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedControl ariaLabel="机器类型" value={kind} options={kindOptions} onChange={(next) => { setKind(next); setPage(1) }} />
            <SegmentedControl ariaLabel="Agent 状态" value={agentFilter} options={agentFilterOptions} onChange={(next) => { setAgentFilter(next); setPage(1) }} />
          </div>
          <Input type="search" scale="sm" className="w-full sm:w-72" aria-label="搜索机器" placeholder="搜索编号、名称、地址、目标或标签" value={query} onChange={(e) => { setQuery(e.target.value); setPage(1) }} />
        </div>
        {availableTags.length > 0 && <fieldset className="space-y-2 rounded-lg border bg-background/40 p-3"><legend className="px-1 text-xs font-medium text-muted-foreground">按自定义标签筛选（支持 key=value 分组，多选求交集）</legend>
          {Object.entries(groupedTags).map(([group, groupItems]) => (
            <div key={group} className="flex flex-wrap items-center gap-2 text-sm">
              <span className="font-mono text-xs text-muted-foreground">{group}：</span>
              {groupItems.map(tag => {
                const eq = tag.indexOf("=")
                const display = eq > 0 ? tag.slice(eq + 1) : tag
                return <label key={tag} className="inline-flex items-center gap-1.5 text-sm"><Checkbox checked={selectedTags.includes(tag)} onChange={e => { setSelectedTags(current => e.target.checked ? [...current, tag] : current.filter(t => t !== tag)); setPage(1) }} /><span className="font-mono text-xs">{display}</span></label>
              })}
            </div>
          ))}
          {selectedTags.length > 0 && <Button size="sm" onClick={() => { setSelectedTags([]); setPage(1) }}>清除标签</Button>}
        </fieldset>}
        <p className="text-xs text-muted-foreground">巡检范围：当前筛选的 {filtered.length} 台机器{paged.totalPages > 1 ? `（当前第 ${paged.page}/${paged.totalPages} 页，每页 ${paged.pageSize} 台）` : ""}。列表显示最近样本，CPU 或内存使用率达到 90% 时提示偏高。</p>
        {batchDiscoveryOpen && (
          <MachineBatchTagPanel
            session={session}
            machines={filtered}
            onlineTargets={online}
            onClose={() => setBatchDiscoveryOpen(false)}
            onApplied={refresh}
          />
        )}
        {inspection.progress && <p role="status" className="text-sm">{inspection.running ? "巡检进行中" : "巡检结束"}：已处理 {inspection.progress.done}/{inspection.progress.total} 台，跳过 {inspection.progress.skipped} 台，请求失败 {inspection.progress.failed} 台。</p>}
        {filtered.length ? <>
          <Table wrapperClassName="rounded-lg border">
            <TableHeader className="bg-muted/40"><tr>
              <TableHead>机器 / 资产编号</TableHead><TableHead>类型</TableHead>
              <TableHead className="hidden sm:table-cell">管理地址</TableHead><TableHead>Agent</TableHead>
              <TableHead className="whitespace-nowrap">录入时间</TableHead>
              <TableHead>主机巡检 / 采样时间</TableHead><TableHead>操作</TableHead>
            </tr></TableHeader>
            <TableBody>{paged.items.map((m) => {
              const target = online.get(bindingKey(m))
              const result = inspection.results[inspectionKey(m)]
              return <TableRow key={m.asset_id}>
                <TableCell><button type="button" onClick={() => detail(m.asset_id)} className="block break-all text-left font-medium text-primary hover:underline">{m.name}<span className="block font-mono text-xs text-muted-foreground">{m.asset_id}</span></button>{!!m.tags?.length && <div className="mt-1 flex flex-wrap gap-1">{m.tags.map(tag => <Badge key={tag}>{tag}</Badge>)}</div>}</TableCell>
                <TableCell className="whitespace-nowrap">{kindLabel[m.kind]}</TableCell>
                <TableCell className="hidden break-all font-mono text-xs sm:table-cell">{m.address || "未登记"}</TableCell>
                <TableCell className="text-xs"><span className={target ? "text-success" : "text-muted-foreground"}>{connection(m, target)}</span>
                  {bindingKey(m) && <span className="hidden break-all font-mono text-muted-foreground md:block">{m.cluster}/{m.instance}</span>}</TableCell>
                <TableCell className="whitespace-nowrap font-mono text-xs text-muted-foreground">
                  {m.created_at ? (
                    <time dateTime={m.created_at} title={formatMachineCreatedDetail(m.created_at)}>
                      {formatMachineCreatedDate(m.created_at)}
                    </time>
                  ) : "未登记"}
                </TableCell>
                <TableCell className="text-xs"><strong>{result?.label ?? (bindingKey(m) ? "正在读取样本…" : "未接入 Agent")}</strong>
                  {result?.metrics && <span className="block">{result.metrics}</span>}
                  {result?.at != null && <time className="block text-muted-foreground" dateTime={new Date(result.at).toISOString()}>{new Date(result.at).toLocaleString("zh-CN", { hour12: false })}</time>}</TableCell>
                <TableCell><div className="flex flex-wrap gap-1">
                  <Button size="sm" disabled={inspection.running || !bindingKey(m)} onClick={() => inspect([m])}>巡检</Button>
                  <Button size="sm" onClick={() => detail(m.asset_id)}>详情与诊断</Button>
                </div></TableCell>
              </TableRow>
            })}</TableBody>
          </Table>
          <nav aria-label="机器台账分页" className="flex flex-wrap items-center justify-between gap-3 pt-1 text-xs text-muted-foreground">
            <span>
              显示第 {paged.startIndex}–{paged.endIndex} 条，共 {paged.totalItems} 条
              {filtered.length !== machines.length ? `（台账共 ${machines.length} 台）` : ""}
            </span>
            <div className="flex flex-wrap items-center gap-2">
              <label className="inline-flex items-center gap-1.5">
                <span>每页</span>
                <Select
                  scale="sm"
                  aria-label="每页显示条数"
                  value={String(paged.pageSize)}
                  onChange={(e) => {
                    setPageSize(Number(e.target.value))
                    setPage(1)
                  }}
                >
                  {MACHINE_PAGE_SIZE_OPTIONS.map((size) => (
                    <option key={size} value={size}>{size} 条</option>
                  ))}
                </Select>
              </label>
              <div className="flex flex-wrap items-center gap-1">
                <Button size="sm" disabled={!paged.hasPrev} onClick={() => setPage(paged.page - 1)}>上一页</Button>
                {visiblePageNumbers(paged.page, paged.totalPages).map((entry, idx) =>
                  entry === "ellipsis" ? (
                    <span key={`ellipsis-${idx}`} aria-hidden="true" className="px-1.5">…</span>
                  ) : (
                    <Button
                      key={entry}
                      size="sm"
                      variant={entry === paged.page ? "primary" : "default"}
                      aria-current={entry === paged.page ? "page" : undefined}
                      onClick={() => setPage(entry)}
                    >
                      {entry}
                    </Button>
                  ),
                )}
                <Button size="sm" disabled={!paged.hasNext} onClick={() => setPage(paged.page + 1)}>下一页</Button>
              </div>
              <span className="whitespace-nowrap">第 {paged.page} / {paged.totalPages} 页</span>
            </div>
          </nav>
        </> : <div className="space-y-2 py-8 text-center text-sm text-muted-foreground" role="status">
          <p>{machines.length ? "没有匹配的机器" : admin ? "尚未登记机器，可手工新增、粘贴文本或从 CSV 导入。" : "当前账号没有可查看的机器。"}</p>
          {machines.length > 0 && <Button size="sm" onClick={() => { setQuery(""); setKind("all"); setAgentFilter("all"); setSelectedTags([]); setPage(1) }}>清除筛选</Button>}
        </div>}
      </>}
      {machines && "id" in screen && !selected && <div role="status" className="space-y-2 text-sm"><p>这台机器已不在可见台账中。</p><Button size="sm" onClick={back}>返回台账</Button></div>}
      {screen.page === "detail" && selected && machines && <>
        <MachineDetail
          machine={selected}
          machines={machines}
          target={online.get(bindingKey(selected))}
          monitorTarget={
            knownTargets.get(bindingKey(selected)) ??
            (selected.cluster && selected.instance
              ? { key: `${selected.cluster}/${selected.instance}`, cluster: selected.cluster, instance: selected.instance, status: "offline", sessions: [] }
              : undefined)
          }
          onBack={back} onSelect={detail} onEdit={admin ? () => setScreen({ page: "edit", id: selected.asset_id }) : undefined}
          origin={{ section: "machines", machineId: selected.asset_id, kind, query, tags: selectedTags, nodesOpen, page: paged.page, pageSize: paged.pageSize }} renderTargetAction={renderTargetAction} />
        <MachineDiagnosticPanel isAdmin={admin} key={JSON.stringify([selected.asset_id, selected.cluster, selected.instance])} assetId={selected.asset_id} cluster={selected.cluster} instance={selected.instance} />
        {admin && <section aria-label="机器登录凭据" className="space-y-2 border-t pt-3">
          <div className="flex flex-wrap items-center justify-between gap-2"><h4 className="font-medium">机器登录凭据</h4>
            <Button size="sm" onClick={() => setScreen({ page: "login", id: selected.asset_id })}>添加登录凭据</Button></div>
          {selected.login_accounts?.length ? <ul>{selected.login_accounts.map(login => <li className="text-sm" key={login.credential_id}>{login.username} · 已保存</li>)}</ul>
            : <p className="text-sm text-muted-foreground">{Array.isArray(selected.login_accounts) ? "尚无已保存的账号" : "未取得登录凭据状态"}</p>}
        </section>}
        <AssetNodeLinks key={selected.asset_id} machineId={selected.asset_id} session={session} open={nodesOpen} onOpenChange={(open) => {
          setNodesOpen(open)
          if (open && !admin) refresh()
        }} />
      </>}
      {admin && machines && (screen.page === "create" || (screen.page === "edit" && selected)) && <MachineForm initialKind={kind === "vm" ? "vm" : "physical"} key={selected?.asset_id ?? ""}
        machine={selected} machines={machines} online={online} onCancel={() => selected ? detail(selected.asset_id) : back()}
        onReload={refresh} onSaved={(id) => { detail(id); refresh() }} />}
    </section>
  )
}

function MachineDetail({ machine, machines, target, monitorTarget, onBack, onSelect, onEdit, origin, renderTargetAction }: {
  machine: Machine; machines: Machine[]; target?: Target; monitorTarget?: Target; onBack: () => void; onSelect: (id: string) => void
  onEdit?: () => void; origin: MachinePage; renderTargetAction: RenderAssetTargetAction
}) {
  const host = machines.find((m) => m.kind === "physical" && m.asset_id === machine.host_asset_id)
  const children = machines.filter((m) => m.kind === "vm" && m.host_asset_id === machine.asset_id)
  const lastSeen = target?.last_seen || machine.last_seen
  const facts = [
    ["资产编号", machine.asset_id], ["类型", kindLabel[machine.kind]], ["管理地址", machine.address || "未登记"], ["标签", machine.tags?.join("、") || "未打标签"],
    ["录入时间", formatMachineCreatedDetail(machine.created_at)],
    ...capacities.map(({ key, label, unit }) => [label, machine[key] == null ? "未登记" : `${machine[key]} ${unit}`]),
    ["Agent 状态", connection(machine, target)], ["关联目标", bindingKey(machine) ? `${machine.cluster}/${machine.instance}` : "未关联"],
    ...(lastSeen ? [["最后在线", lastSeen]] : []),
  ]
  const relation = (m: Machine) => <Button key={m.asset_id} size="sm" className="max-w-full whitespace-normal break-all text-left" onClick={() => onSelect(m.asset_id)}>{m.name} · {m.asset_id}</Button>
  return <div className="space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="break-all font-semibold">{machine.name}</h3>
      <div className="flex flex-wrap gap-2"><Button size="sm" onClick={onBack}>返回台账</Button>{onEdit && <Button variant="primary" size="sm" onClick={onEdit}>编辑机器</Button>}</div></div>
    <dl className="grid grid-cols-2 gap-4 rounded-lg border p-4 sm:grid-cols-3 lg:grid-cols-4">{facts.map(([label, value]) => <div key={label} className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt><dd className="mt-1 break-all text-sm">{value}</dd></div>)}</dl>
    <div className="space-y-2"><h4 className="text-sm font-medium">{machine.kind === "vm" ? "宿主物理机" : "名下虚拟机"}</h4>
      <div className="flex flex-wrap gap-2 text-sm text-muted-foreground">{machine.kind === "vm" ? host ? relation(host) : "暂无可查看的宿主" : children.length ? children.map(relation) : "暂无可见虚拟机"}</div></div>
    {bindingKey(machine) ? <div className="flex flex-wrap gap-2">
      {renderTargetAction({ target, origin, tab: "overview", label: "进入目标运维" })}
      {renderTargetAction({
        target: monitorTarget ?? target,
        origin,
        tab: "monitor",
        label: target ? "查看资源监控与告警" : "查看历史监控与告警",
      })}
      {renderTargetAction({ target, origin, tab: "credentials", label: "查看当前目标可用凭据" })}
    </div> : <p className="text-xs text-muted-foreground">关联 Agent 后可进入目标运维、资源监控和查看凭据（Agent 离线时仍可查阅历史监控与告警）。</p>}
  </div>
}

function MachineForm({ initialKind, machine, machines, online, onCancel, onSaved, onReload }: {
  initialKind: Machine["kind"]
  machine?: Machine; machines: Machine[]; online: Map<string, Target>; onCancel: () => void; onSaved: (id: string) => void; onReload: () => void
}) {
  const [kind, setKind] = useState(machine?.kind ?? initialKind)
  const savedBinding = machine ? bindingKey(machine) : ""
  const [binding, setBinding] = useState(savedBinding)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const unavailable = Boolean(binding && !online.has(binding) && binding !== savedBinding)
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || unavailable) return
    const data = new FormData(event.currentTarget)
    const field = (key: string) => String(data.get(key) ?? "").trim()
    const asset_id = field("asset_id"), name = field("name")
    if (!asset_id || !name) { setError("请填写资产编号和显示名称。"); return }
    const capacity: Partial<Pick<Machine, (typeof capacities)[number]["key"]>> = {}
    for (const { key, label } of capacities) {
      if (!field(key)) continue
      const value = Number(field(key))
      if (!Number.isFinite(value) || value <= 0 || (key === "cpu_count" && !Number.isInteger(value))) {
        setError(`${label}须为${key === "cpu_count" ? "正整数" : "正数"}。`); return
      }
      capacity[key] = value
    }
    const target = online.get(binding) ?? (binding && binding === savedBinding ? machine : undefined)
    const created_at = resolveMachineCreatedAtPayload(field("created_at"), machine?.created_at)
    const next = { asset_id, name, kind, tags: field("tags").split(/[,，、\s]+/u).filter(Boolean), address: field("address"), ...capacity,
      host_asset_id: kind === "vm" ? field("host_asset_id") || undefined : undefined, cluster: target?.cluster, instance: target?.instance,
      ...(created_at ? { created_at } : {}) }
    setBusy(true)
    setError("")
    try {
      await assetRequest("machines", machine ? { action: "update", machine: next, expected_revision: machine.revision } : { action: "create", machine: next })
      onSaved(asset_id)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally { setBusy(false) }
  }
  return <form data-assist-scope="asset-entry" onSubmit={save} className="space-y-4">
    <h3 className="font-semibold">{machine ? "编辑机器" : "手工新增机器"}</h3>
    <fieldset disabled={busy} className="grid min-w-0 gap-3 text-xs text-muted-foreground sm:grid-cols-2 lg:grid-cols-3 [&_label]:grid [&_label]:min-w-0 [&_label]:gap-1.5">
      <label>资产编号（必填）<Input name="asset_id" required readOnly={!!machine} defaultValue={machine?.asset_id} /></label>
      <label>显示名称（必填）<Input name="name" required defaultValue={machine?.name} /></label>
      <label>机器类型<Select aria-label="机器类型" value={kind} onChange={(e) => setKind(e.target.value as Machine["kind"])}><option value="physical">物理机</option><option value="vm">虚拟机</option></Select></label>
      <label>自定义标签（支持 key=value，空格或逗号分隔）<Input name="tags" placeholder="例如：院系=计算机 机房=A1 用途=数据库" defaultValue={machine?.tags?.join(" ") ?? ""} /></label>
      <label>管理地址<Input name="address" placeholder="IP 或主机名" defaultValue={machine?.address} /></label>
      <label>录入时间（选填，留空默认当前时间）<Input name="created_at" type="date" defaultValue={toMachineDateInput(machine?.created_at)} /></label>
      {capacities.map(({ key, label, unit }) => <label key={key}>{label}（{unit}）<Input name={key} type="number" min={key === "cpu_count" ? 1 : 0} step={key === "cpu_count" ? 1 : "any"} defaultValue={machine?.[key] ?? ""} /></label>)}
      {kind === "vm" && <label>宿主物理机<Select aria-label="宿主物理机" name="host_asset_id" defaultValue={machine?.host_asset_id ?? ""}><option value="">未登记宿主</option>
        {machines.filter((m) => m.kind === "physical" && m.asset_id !== machine?.asset_id).map((m) => <option key={m.asset_id} value={m.asset_id}>{m.name} · {m.asset_id}</option>)}
      </Select></label>}
      <label>关联 Agent 目标<Select aria-label="关联 Agent 目标" value={binding} onChange={(e) => setBinding(e.target.value)}><option value="">不关联目标</option>
        {savedBinding && !online.has(savedBinding) && <option value={savedBinding}>{machine?.cluster}/{machine?.instance}（已保存，离线）</option>}
        {unavailable && <option value={binding} disabled>所选目标已离线，请重新选择</option>}
        {Array.from(online, ([key, t]) => <option key={key} value={key}>{t.cluster}/{t.instance}</option>)}
      </Select></label>
    </fieldset>
    {error && <div role="alert" className="status-error space-y-2"><p className="break-words">{error}</p><Button type="button" size="sm" disabled={busy} onClick={onReload}>放弃草稿并重新读取台账</Button></div>}
    <div className="flex flex-wrap gap-2"><Button type="submit" size="sm" variant="primary" disabled={busy || unavailable}>{busy ? "正在保存…" : "保存机器"}</Button><Button type="button" size="sm" disabled={busy} onClick={onCancel}>取消</Button></div>
  </form>
}
