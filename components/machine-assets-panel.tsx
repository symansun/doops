"use client"

import { useEffect, useState, type FormEvent } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { assetRequest } from "@/lib/assets"
import { Badge, Button, Checkbox, Input, PageTabs, SegmentedControl, Select, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"
import { MachineDiagnosticPanel } from "./machine-diagnostic-panel"
import { AssetCoveragePanel } from "./asset-coverage-panel"
import { AssetInspectionPanel } from "./asset-inspection-panel"
import { InspectionCenterPanel } from "./inspection-center-panel"
import { consoleFetch } from "@/lib/network-error"
import { inspectionKey } from "@/lib/machine-inspection"
import { useMachineInspections } from "./use-machine-inspections"
import { MachineCsvImport } from "./machine-csv-import"
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
  last_seen?: string
  last_sample_ms?: number
  tags?: string[]
  revision: number
  login_accounts?: { username: string; credential_id: string }[]
}
type SubTab = "ledger" | "probe_coverage" | "coverage" | "inspections"
type PanelProps = { session: Session; targets: Target[]; initialPage?: MachinePage; renderTargetAction: RenderAssetTargetAction }
type Screen = { page: "list" | "create" | "csv" | "text" } | { page: "detail" | "edit" | "login"; id: string }
const kindLabel = { physical: "物理机", vm: "虚拟机" }
const kindOptions = [{ value: "all", label: "全部" }, { value: "physical", label: "物理机" }, { value: "vm", label: "虚拟机" }] as const
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

export function MachineAssetsPanel({ session, targets, initialPage, renderTargetAction }: PanelProps) {
  const identity = JSON.stringify([session.gateway, session.username, session.tokenId, session.issuedAt, session.isAdmin])
  return <Inventory key={identity} session={session} targets={targets} initialPage={initialPage} renderTargetAction={renderTargetAction} />
}

function Inventory({ session, targets, initialPage, renderTargetAction }: PanelProps) {
  const admin = sessionIsAdmin(session)
  const [machines, setMachines] = useState<Machine[] | null>(null)
  const inspection = useMachineInspections(machines)
  const [readError, setReadError] = useState("")
  const [version, setVersion] = useState(0)
  const [screen, setScreen] = useState<Screen>(initialPage ? { page: "detail", id: initialPage.machineId } : { page: "list" })
  const [subTab, setSubTab] = useState<SubTab>("ledger")
  const [inspectMode, setInspectMode] = useState<"auto" | "compliance">("auto")
  const [sampling, setSampling] = useState(false)
  const [sampleNotice, setSampleNotice] = useState("")
  const [nodesOpen, setNodesOpen] = useState(initialPage?.nodesOpen ?? false)
  const [kind, setKind] = useState<Machine["kind"] | "all">(initialPage?.kind ?? "all")
  const [selectedTags, setSelectedTags] = useState<string[]>(initialPage?.tags ?? [])
  const [query, setQuery] = useState(initialPage?.query ?? "")

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
  const selected = "id" in screen ? machines?.find((m) => m.asset_id === screen.id) : undefined
  function detail(id: string) {
    setNodesOpen(false)
    setScreen({ page: "detail", id })
    if (!admin) refresh()
  }
  const back = () => setScreen({ page: "list" })
  const availableTags = [...new Set([...(machines ?? []).flatMap(m => m.tags ?? []), ...selectedTags])].sort()
  const term = query.trim().toLowerCase()
  const filtered = screen.page === "list" ? machines?.filter((m) => (kind === "all" || m.kind === kind) &&
    selectedTags.every(tag => m.tags?.includes(tag)) &&
    [...(m.tags ?? []), m.asset_id, m.name, m.address, m.cluster && `${m.cluster}/${m.instance}`].some((field) => field?.toLowerCase().includes(term))) ?? [] : []

  function inspect(rows: Machine[]) {
    void inspection.inspect(rows, m => online.has(bindingKey(m)))
  }

  const nowMs = Date.now()
  const probeRows = (machines ?? []).map((m) => {
    const isOnline = online.has(bindingKey(m))
    const inspectAt = inspection.results[inspectionKey(m)]?.at ?? null
    const sampleAt = Math.max(m.last_sample_ms ?? 0, inspectAt ?? 0) || null
    const fresh = isOnline && sampleAt !== null && nowMs - sampleAt <= 300_000
    const reason = fresh
      ? "正常上报"
      : !bindingKey(m)
        ? "未绑定 Agent"
        : !isOnline
          ? "Agent 离线"
          : sampleAt === null
            ? "尚无监控样本"
            : "样本超过 5 分钟"
    return { machine: m, isOnline, sampleAt, fresh, reason }
  })
  const probeCovered = probeRows.filter((r) => r.fresh).length
  const probeTotal = probeRows.length
  const probePct = probeTotal > 0 ? ((probeCovered / probeTotal) * 100).toFixed(1) : null

  async function batchCollectMissing() {
    const candidates = probeRows.filter((r) => !r.fresh && r.isOnline && r.machine.cluster && r.machine.instance)
    if (!candidates.length || sampling) return
    setSampling(true)
    setSampleNotice("")
    let okCount = 0
    let failCount = 0
    for (const { machine: m } of candidates) {
      try {
        if (session.demo) {
          const { demoAssetMonitorWrite } = await import("@/lib/demo")
          await demoAssetMonitorWrite({ action: "collect", mode: "host", cluster: m.cluster, instance: m.instance, asset_id: m.asset_id })
          okCount++
        } else {
          const res = await consoleFetch("/api/assets/monitor", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action: "collect", mode: "host", cluster: m.cluster, instance: m.instance, asset_id: m.asset_id }),
          })
          if (res.ok) okCount++
          else failCount++
        }
      } catch {
        failCount++
      }
    }
    setSampling(false)
    setSampleNotice(`批量隧道采样完成：成功 ${okCount} 台${failCount ? `，失败 ${failCount} 台` : ""}`)
    refresh()
  }

  function exportProbeCsv() {
    const esc = (v: string) => `"${v.replaceAll('"', '""')}"`
    const lines = [
      ["asset_id", "name", "kind", "target", "tags", "probe_covered", "gap_reason", "last_sample_at"].join(","),
      ...probeRows.map((r) => [
        esc(r.machine.asset_id),
        esc(r.machine.name),
        esc(r.machine.kind),
        esc(bindingKey(r.machine) ? `${r.machine.cluster}/${r.machine.instance}` : ""),
        esc((r.machine.tags ?? []).join(";")),
        r.fresh ? "true" : "false",
        esc(r.reason),
        esc(r.sampleAt ? new Date(r.sampleAt).toISOString() : ""),
      ].join(",")),
    ]
    const blob = new Blob(["\uFEFF" + lines.join("\n")], { type: "text/csv;charset=utf-8" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = `probe-coverage-${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(url)
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
        <h2 className="text-xl font-semibold">机器台账与巡检覆盖</h2>
        {admin && screen.page === "create" && <Button size="sm" onClick={() => setScreen({ page: "text" })}>文本导入</Button>}
        {subTab === "ledger" && screen.page === "list" && <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="primary" disabled={!filtered.length || inspection.running} onClick={() => inspect(filtered)}>{inspection.running ? "巡检中…" : "发起巡检"}</Button>
          <Button size="sm" onClick={refresh} disabled={inspection.running || (!machines && !readError)}>刷新</Button>
          {admin && <>
            <Button size="sm" disabled={!machines} onClick={() => setScreen({ page: "text" })}>文本导入</Button>
            <Button size="sm" disabled={!machines} onClick={() => setScreen({ page: "csv" })}>CSV 导入</Button>
            <Button size="sm" variant="primary" disabled={!machines} onClick={() => setScreen({ page: "create" })}>手工新增</Button>
          </>}
        </div>}
      </header>
      {screen.page === "list" && (
        <PageTabs
          ariaLabel="机器与巡检视图"
          value={subTab}
          onChange={setSubTab}
          items={[
            { id: "ledger", label: `机器清单${machines ? ` (${machines.length})` : ""}` },
            { id: "probe_coverage", label: `探针覆盖率${probePct !== null ? ` (${probePct}%)` : ""}` },
            { id: "coverage", label: "纳管覆盖率 (80%)" },
            { id: "inspections", label: "智能巡检" },
          ]}
        />
      )}
      {subTab === "coverage" && screen.page === "list" ? (
        <AssetCoveragePanel session={session} />
      ) : subTab === "inspections" && screen.page === "list" ? (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <SegmentedControl
              ariaLabel="巡检模式切换"
              value={inspectMode}
              onChange={(v) => setInspectMode(v as "auto" | "compliance")}
              options={[
                { value: "auto", label: "自动策略巡检（Agent 执行）" },
                { value: "compliance", label: "合规周检台账（离线证据）" },
              ]}
            />
          </div>
          {inspectMode === "auto" ? (
            <InspectionCenterPanel session={session} targets={targets} />
          ) : (
            <AssetInspectionPanel session={session} targets={targets} />
          )}
        </div>
      ) : subTab === "probe_coverage" && screen.page === "list" ? (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-background/60 p-4">
            <div className="space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="text-base font-semibold">探针机器覆盖率（目标 100%）</h3>
                <Badge variant={probeTotal > 0 && probeCovered === probeTotal ? "success" : "warning"}>
                  {probeTotal === 0 ? "待登记机器" : probeCovered === probeTotal ? "已达标 100%" : `缺口 ${probeTotal - probeCovered} 台`}
                </Badge>
              </div>
              <p className="text-xs text-muted-foreground">
                口径：已绑定在线 Agent 且最近 5 分钟内有有效监控样本的机器数 ÷ 当前可见登记机器数 ={" "}
                <strong className="font-mono text-foreground">{probeCovered} / {probeTotal}</strong>
                {probePct !== null ? `（${probePct}%）` : ""}
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="primary"
                disabled={sampling || !probeRows.some((r) => !r.fresh && r.isOnline)}
                onClick={() => void batchCollectMissing()}
              >
                {sampling ? "采集中…" : "批量补采在线缺口"}
              </Button>
              <Button size="sm" disabled={!probeRows.length} onClick={exportProbeCsv}>
                导出 CSV
              </Button>
              <Button size="sm" onClick={refresh}>刷新</Button>
            </div>
          </div>
          {sampleNotice && <p role="status" className="text-sm">{sampleNotice}</p>}
          {!machines ? (
            <p role="status" className="text-sm text-muted-foreground">正在读取机器台账…</p>
          ) : !probeRows.length ? (
            <p className="py-6 text-center text-sm text-muted-foreground">当前没有可见的登记机器</p>
          ) : (
            <Table wrapperClassName="rounded-lg border">
              <TableHeader className="bg-muted/40">
                <TableRow>
                  <TableHead>机器 / 编号</TableHead>
                  <TableHead>类型 / 标签</TableHead>
                  <TableHead>关联 Agent</TableHead>
                  <TableHead>覆盖状态与原因</TableHead>
                  <TableHead>最近有效样本</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {probeRows.map(({ machine: m, isOnline, sampleAt, fresh, reason }) => (
                  <TableRow key={m.asset_id}>
                    <TableCell>
                      <button type="button" onClick={() => { setSubTab("ledger"); detail(m.asset_id) }} className="text-left font-medium text-primary hover:underline">
                        {m.name}
                      </button>
                      <span className="block font-mono text-2xs text-muted-foreground">{m.asset_id}</span>
                    </TableCell>
                    <TableCell className="text-xs">
                      <div>{kindLabel[m.kind]}</div>
                      {!!m.tags?.length && (
                        <div className="mt-1 flex flex-wrap gap-1">
                          {m.tags.map((t) => <Badge key={t} variant="outline">{t}</Badge>)}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {bindingKey(m) ? `${m.cluster}/${m.instance}` : "—"}
                    </TableCell>
                    <TableCell>
                      <Badge variant={fresh ? "success" : isOnline ? "warning" : "default"}>
                        {reason}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {sampleAt ? new Date(sampleAt).toLocaleString("zh-CN", { hour12: false }) : "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button size="sm" onClick={() => { setSubTab("ledger"); detail(m.asset_id) }}>
                        查看机器
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      ) : <>
      {readError ? <div role="alert" className="status-error flex flex-wrap items-center justify-between gap-2">
        <span className="break-words">读取机器台账失败。{readError}</span><Button size="sm" onClick={refresh}>重新读取台账</Button>
      </div> : !machines && <p role="status" className="text-sm text-muted-foreground">正在读取机器台账…</p>}
      {admin && (screen.page === "csv" || screen.page === "text") && <MachineCsvImport key={screen.page} textMode={screen.page === "text"} machines={machines ?? []} onClose={back} onReadback={refresh} />}
      {admin && screen.page === "login" && <MachineCsvImport machines={[]} machine={{asset_id: screen.id, name: selected?.name ?? screen.id}} onClose={() => detail(screen.id)} onReadback={refresh} />}
      {machines && screen.page === "list" && <>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <SegmentedControl ariaLabel="机器类型" value={kind} options={kindOptions} onChange={setKind} />
          <Input type="search" scale="sm" className="w-full sm:w-72" aria-label="搜索机器" placeholder="搜索编号、名称、地址、目标或标签" value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
        {availableTags.length > 0 && <fieldset className="space-y-2 rounded-lg border bg-background/40 p-3"><legend className="px-1 text-xs font-medium text-muted-foreground">按自定义标签筛选（支持 key=value 分组，多选求交集）</legend>
          {Object.entries(groupedTags).map(([group, groupItems]) => (
            <div key={group} className="flex flex-wrap items-center gap-2 text-sm">
              <span className="font-mono text-xs text-muted-foreground">{group}：</span>
              {groupItems.map(tag => {
                const eq = tag.indexOf("=")
                const display = eq > 0 ? tag.slice(eq + 1) : tag
                return <label key={tag} className="inline-flex items-center gap-1.5 text-sm"><Checkbox checked={selectedTags.includes(tag)} onChange={e => setSelectedTags(current => e.target.checked ? [...current, tag] : current.filter(t => t !== tag))} /><span className="font-mono text-xs">{display}</span></label>
              })}
            </div>
          ))}
          {selectedTags.length > 0 && <Button size="sm" onClick={() => setSelectedTags([])}>清除标签</Button>}
        </fieldset>}
        <p className="text-xs text-muted-foreground">巡检范围：当前筛选的 {filtered.length} 台机器。列表显示最近样本，CPU 或内存使用率达到 90% 时提示偏高。</p>
        {inspection.progress && <p role="status" className="text-sm">{inspection.running ? "巡检进行中" : "巡检结束"}：已处理 {inspection.progress.done}/{inspection.progress.total} 台，跳过 {inspection.progress.skipped} 台，请求失败 {inspection.progress.failed} 台。</p>}
        {filtered.length ? <Table wrapperClassName="rounded-lg border">
          <TableHeader className="bg-muted/40"><tr>
            <TableHead>机器 / 资产编号</TableHead><TableHead>类型</TableHead>
            <TableHead className="hidden sm:table-cell">管理地址</TableHead><TableHead>Agent</TableHead>
            <TableHead>主机巡检 / 采样时间</TableHead><TableHead>操作</TableHead>
          </tr></TableHeader>
          <TableBody>{filtered.map((m) => {
            const target = online.get(bindingKey(m))
            const result = inspection.results[inspectionKey(m)]
            return <TableRow key={m.asset_id}>
              <TableCell><button type="button" onClick={() => detail(m.asset_id)} className="block break-all text-left font-medium text-primary hover:underline">{m.name}<span className="block font-mono text-xs text-muted-foreground">{m.asset_id}</span></button>{!!m.tags?.length && <div className="mt-1 flex flex-wrap gap-1">{m.tags.map(tag => <Badge key={tag}>{tag}</Badge>)}</div>}</TableCell>
              <TableCell className="whitespace-nowrap">{kindLabel[m.kind]}</TableCell>
              <TableCell className="hidden break-all font-mono text-xs sm:table-cell">{m.address || "未登记"}</TableCell>
              <TableCell className="text-xs"><span className={target ? "text-success" : "text-muted-foreground"}>{connection(m, target)}</span>
                {bindingKey(m) && <span className="hidden break-all font-mono text-muted-foreground md:block">{m.cluster}/{m.instance}</span>}</TableCell>
              <TableCell className="text-xs"><strong>{result?.label ?? (bindingKey(m) ? "正在读取样本…" : "未接入 Agent")}</strong>
                {result?.metrics && <span className="block">{result.metrics}</span>}
                {result?.at != null && <time className="block text-muted-foreground" dateTime={new Date(result.at).toISOString()}>{new Date(result.at).toLocaleString("zh-CN", { hour12: false })}</time>}</TableCell>
              <TableCell><div className="flex flex-wrap gap-1">
                <Button size="sm" disabled={inspection.running || !bindingKey(m)} onClick={() => inspect([m])}>巡检</Button>
                <Button size="sm" onClick={() => detail(m.asset_id)}>监控详情</Button>
              </div></TableCell>
            </TableRow>
          })}</TableBody>
        </Table> : <div className="space-y-2 py-8 text-center text-sm text-muted-foreground" role="status">
          <p>{machines.length ? "没有匹配的机器" : admin ? "尚未登记机器，可手工新增、粘贴文本或从 CSV 导入。" : "当前账号没有可查看的机器。"}</p>
          {machines.length > 0 && <Button size="sm" onClick={() => { setQuery(""); setKind("all"); setSelectedTags([]) }}>清除筛选</Button>}
        </div>}
      </>}
      {machines && "id" in screen && !selected && <div role="status" className="space-y-2 text-sm"><p>这台机器已不在可见台账中。</p><Button size="sm" onClick={back}>返回台账</Button></div>}
      {screen.page === "detail" && selected && machines && <>
        <MachineDetail machine={selected} machines={machines} target={online.get(bindingKey(selected))}
          onBack={back} onSelect={detail} onEdit={admin ? () => setScreen({ page: "edit", id: selected.asset_id }) : undefined}
          origin={{ section: "machines", machineId: selected.asset_id, kind, query, tags: selectedTags, nodesOpen }} renderTargetAction={renderTargetAction} />
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
      </>}
    </section>
  )
}

function MachineDetail({ machine, machines, target, onBack, onSelect, onEdit, origin, renderTargetAction }: {
  machine: Machine; machines: Machine[]; target?: Target; onBack: () => void; onSelect: (id: string) => void
  onEdit?: () => void; origin: MachinePage; renderTargetAction: RenderAssetTargetAction
}) {
  const host = machines.find((m) => m.kind === "physical" && m.asset_id === machine.host_asset_id)
  const children = machines.filter((m) => m.kind === "vm" && m.host_asset_id === machine.asset_id)
  const lastSeen = target?.last_seen || machine.last_seen
  const facts = [
    ["资产编号", machine.asset_id], ["类型", kindLabel[machine.kind]], ["管理地址", machine.address || "未登记"], ["标签", machine.tags?.join("、") || "未打标签"],
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
      {renderTargetAction({ target, origin, tab: "monitor", label: "查看虚机/主机与容器监控" })}
      {renderTargetAction({ target, origin, tab: "credentials", label: "查看当前目标可用凭据" })}
    </div> : <p className="text-xs text-muted-foreground">关联在线 Agent 后可进入目标运维、指标监控和查看凭据。</p>}
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
    const next = { asset_id, name, kind, tags: field("tags").split(/[,，、\s]+/u).filter(Boolean), address: field("address"), ...capacity,
      host_asset_id: kind === "vm" ? field("host_asset_id") || undefined : undefined, cluster: target?.cluster, instance: target?.instance }
    setBusy(true)
    setError("")
    try {
      await assetRequest("machines", machine ? { action: "update", machine: next, expected_revision: machine.revision } : { action: "create", machine: next })
      onSaved(asset_id)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally { setBusy(false) }
  }
  return <form onSubmit={save} className="space-y-4">
    <h3 className="font-semibold">{machine ? "编辑机器" : "手工新增机器"}</h3>
    <fieldset disabled={busy} className="grid min-w-0 gap-3 text-xs text-muted-foreground sm:grid-cols-2 lg:grid-cols-3 [&_label]:grid [&_label]:min-w-0 [&_label]:gap-1.5">
      <label>资产编号（必填）<Input name="asset_id" required readOnly={!!machine} defaultValue={machine?.asset_id} /></label>
      <label>显示名称（必填）<Input name="name" required defaultValue={machine?.name} /></label>
      <label>机器类型<Select aria-label="机器类型" value={kind} onChange={(e) => setKind(e.target.value as Machine["kind"])}><option value="physical">物理机</option><option value="vm">虚拟机</option></Select></label>
      <label>自定义标签（支持 key=value，空格或逗号分隔）<Input name="tags" placeholder="例如：院系=计算机 机房=A1 用途=数据库" defaultValue={machine?.tags?.join(" ") ?? ""} /></label>
      <label>管理地址<Input name="address" placeholder="IP 或主机名" defaultValue={machine?.address} /></label>
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
