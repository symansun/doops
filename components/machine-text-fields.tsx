"use client"

import { useState } from "react"
import type { MachineKind, MachineTextPlan } from "@/lib/machine-text-import"
import { Checkbox, Select, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Textarea } from "./ui"

type Props = {
  source: string; onSource: (text: string) => void; plan: MachineTextPlan; busy: boolean
  kind: MachineKind; onKind: (kind: MachineKind) => void; saveLogins: boolean; onSaveLogins: (value: boolean) => void
}

export function MachineTextFields({ source, onSource, plan, busy, kind, onKind, saveLogins, onSaveLogins }: Props) {
  const [edit, setEdit] = useState(false)
  const masked = source ? plan.rows.map(row => [row.group, row.address, row.username, row.username && "••••••", row.internal, row.port !== 22 && row.port].filter(Boolean).join("  ")).join("\n") || "原文已隐藏" : ""
  return <div className="grid items-start gap-4 lg:grid-cols-[minmax(17rem,2fr)_minmax(0,3fr)]">
    <section className="min-w-0 space-y-3 rounded-lg border bg-background/50 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2"><label htmlFor="machine-text-source" className="font-medium">机器清单</label>
        <label className="flex items-center gap-2 text-sm"><Checkbox checked={edit} disabled={busy} onChange={e=>setEdit(e.target.checked)} />显示并编辑原文</label></div>
      <p className="text-sm text-muted-foreground">每行一台，依次填管理 IP、账号、密码，可追加内网 IP 和端口。分组名单独一行。</p>
      <Textarea id="machine-text-source" rows={9} value={edit ? source : masked} readOnly={!edit} disabled={busy}
        autoComplete="off" autoCapitalize="off" spellCheck={false} className="w-full resize-y font-mono"
        placeholder={'在此粘贴清单，例如\n测试组\n192.0.2.10 ops 示例密码 10.0.0.10 2022\n192.0.2.11 ops 示例密码 -p 2202'}
        onPaste={e=>{if (!edit) { e.preventDefault(); onSource(e.clipboardData.getData("text/plain")) }}}
        onChange={e=>onSource(e.target.value)} />
      <label className="grid gap-1.5 text-sm">新增机器类型<Select value={kind} disabled={busy} onChange={e=>onKind(e.target.value as MachineKind)}><option value="vm">虚拟机</option><option value="physical">物理机</option></Select></label>
      <label className="flex items-center gap-2 text-sm"><Checkbox checked={saveLogins} disabled={busy} onChange={e=>onSaveLogins(e.target.checked)} />同时保存登录账号和密码</label>
    </section>
    <section aria-label="字段映射预览" className="min-w-0 space-y-3 rounded-lg border bg-background/50 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2"><h4 className="font-medium">字段映射预览</h4><span role="status" className="text-sm">已识别 {plan.rows.length} 行</span></div>
      {plan.issues.length > 0 && <StatusBanner tone="error" className="max-h-40 overflow-auto"><ul>{plan.issues.map((issue,i)=><li key={i}>{issue.line > 0 && ('第 ' + issue.line + ' 行：')}{issue.message}</li>)}</ul></StatusBanner>}
      {plan.rows.length ? <Table wrapperClassName="max-h-96 overflow-auto rounded-md border">
        <TableHeader className="sticky top-0 bg-card"><TableRow className="hover:bg-transparent">{["源行", "显示名称 / 资产编号", "管理 IP", "内网 IP", "SSH 端口", "账号"].map(label=><TableHead key={label} className="whitespace-nowrap">{label}</TableHead>)}</TableRow></TableHeader>
        <TableBody>{plan.rows.map(row=><TableRow key={row.line}>
          <TableCell>{row.line}</TableCell><TableCell className="min-w-40">{row.name}<small className="block font-mono text-muted-foreground">{row.existing ? '已登记' : '新增'} · {row.id}</small></TableCell>
          {[row.address, row.internal || '—', row.port, row.username || '—'].map((value,i)=><TableCell key={i} className="whitespace-nowrap font-mono">{value}</TableCell>)}</TableRow>)}</TableBody></Table>
        : <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">粘贴后在这里核对机器。</p>}
      <p className="text-sm text-muted-foreground">分组、内网 IP 和显式端口写入新增机器的显示名称。已有机器按管理 IP 匹配，保留原名称。默认 SSH 端口为 22。</p>
    </section>
  </div>
}
