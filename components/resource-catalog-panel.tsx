"use client"

import { useEffect, useState } from "react"
import { assetRequest } from "@/lib/assets"
import { assetKinds } from "@/lib/console-views"
import type { Target } from "@/lib/client"
import { Badge, Button, Input, Select, StatusBanner } from "./ui"

export type ResourceKind = "database" | "website" | "image"
export type CatalogRow = {
  id: string
  kind: ResourceKind
  name: string
  environment: string
  cluster: string
  instance: string
  details: Record<string, string>
  revision: number
}
type Field = [key: string, label: string, example: string, options?: string[]]

const fields: Record<ResourceKind, Field[]> = {
  database: [
    ["engine", "引擎", "", ["PostgreSQL", "MySQL", "MongoDB"]],
    ["host", "实例主机", "db.example.com"],
    ["port", "端口", "5432"],
    ["database", "数据库名", "orders"],
  ],
  website: [
    ["scheme", "协议", "", ["https", "http"]],
    ["domain", "域名", "app.example.com"],
    ["path", "访问路径", "/"],
  ],
  image: [
    ["registry", "镜像仓库地址", "registry.example.com"],
    ["repository", "仓库中的镜像路径", "team/service"],
    ["version", "Tag 或 SHA256 Digest", "1.0.0"],
  ],
}

export function resourceAddress(r: CatalogRow) {
  const d = r.details
  return r.kind === "website"
    ? d.scheme + "://" + d.domain + d.path
    : r.kind === "image"
      ? d.registry + "/" + d.repository + (d.version.startsWith("sha256:") ? "@" : ":") + d.version
      : d.engine + " · " + d.host + ":" + d.port + " / " + d.database
}

export function ResourceCatalogPanel({
  kind,
  targets,
  selectedTarget,
  onOperate,
  onOpenTargetTool,
}: {
  kind: ResourceKind
  targets: Target[]
  selectedTarget?: Target | null
  onOperate: (row: CatalogRow, target: Target) => void
  onOpenTargetTool?: (target: Target) => void
}) {
  const [rows, setRows] = useState<CatalogRow[] | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [query, setQuery] = useState("")
  const [version, refresh] = useState(0)
  const [draft, edit] = useState<CatalogRow | null>(null)
  const [remove, setRemove] = useState<CatalogRow | null>(null)

  const label = assetKinds[kind]
  const cols = fields[kind]
  const onlineSelected = selectedTarget && selectedTarget.status !== "offline" ? selectedTarget : null

  const fresh = (): CatalogRow => ({
    id: "",
    kind,
    revision: 0,
    name: "",
    environment: "",
    cluster: onlineSelected?.cluster ?? "",
    instance: onlineSelected?.instance ?? "",
    details: Object.fromEntries(
      cols.map(([key, , , options]) => [key, options?.[0] ?? (key === "port" ? "5432" : key === "path" ? "/" : "")]),
    ),
  })

  useEffect(() => {
    let live = true
    setRows(null)
    setError("")
    assetRequest<{ records: CatalogRow[] }>("catalog", undefined, new URLSearchParams({ kind }))
      .then((r) => {
        if (live) setRows(r.records)
      })
      .catch((e) => {
        if (live) setError(e.message)
      })
    return () => {
      live = false
    }
  }, [version, kind])

  async function save(row: CatalogRow, op: string) {
    setBusy(true)
    setError("")
    try {
      await assetRequest("catalog", { ...row, op })
      edit(null)
      setRemove(null)
      refresh((x) => x + 1)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const visible = rows?.filter((r) =>
    [r.name, r.environment, r.cluster, r.instance, resourceAddress(r)]
      .join(" ")
      .toLowerCase()
      .includes(query.trim().toLowerCase()),
  )

  const actionLabel =
    kind === "database" ? "打开数据库操作" : kind === "website" ? "打开站点操作" : "打开镜像操作"

  return (
    <main className="min-h-0 flex-1 space-y-4 overflow-auto p-4">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">{label}台账</h1>
          <p className="mt-0.5 text-xs text-muted-foreground">
            统一登记资产位置与归属 Agent；已选定在线目标时也可直接进入该节点的实时操作面板。
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {onlineSelected && onOpenTargetTool && (
            <Button
              size="sm"
              disabled={busy}
              onClick={() => onOpenTargetTool(onlineSelected)}
              title={`无需预先登记台账，直接进入 ${onlineSelected.cluster}/${onlineSelected.instance} 的${label}操作面板`}
            >
              直接操作当前目标（{onlineSelected.instance}）
            </Button>
          )}
          <Button size="sm" onClick={() => refresh((n) => n + 1)} disabled={busy}>
            刷新
          </Button>
          <Button
            size="sm"
            variant="primary"
            disabled={!rows || busy}
            onClick={() => {
              edit(fresh())
              setRemove(null)
            }}
          >
            登记{label}
          </Button>
        </div>
      </header>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}

      {draft && (
        <form
          data-assist-scope="asset-entry"
          className="card-surface space-y-3 p-4"
          onSubmit={(e) => {
            e.preventDefault()
            void save(draft, draft.id ? "update" : "create")
          }}
        >
          <h2 className="text-sm font-semibold">
            {draft.id ? "编辑" : "登记"}
            {label}
          </h2>
          <fieldset disabled={busy} className="grid gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              显示名称
              <Input
                required
                maxLength={80}
                value={draft.name}
                onChange={(e) => edit({ ...draft, name: e.target.value })}
              />
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              环境
              <Input
                maxLength={80}
                value={draft.environment}
                onChange={(e) => edit({ ...draft, environment: e.target.value })}
              />
            </label>
            {cols.map(([key, field, hint, options]) => (
              <label key={key} className="flex flex-col gap-1 text-xs text-muted-foreground">
                {field}
                {options ? (
                  <Select
                    value={draft.details[key]}
                    onChange={(e) =>
                      edit({
                        ...draft,
                        details: {
                          ...draft.details,
                          [key]: e.target.value,
                          ...(key === "engine"
                            ? { port: ({ MySQL: "3306", MongoDB: "27017", PostgreSQL: "5432" })[e.target.value as "MySQL"] }
                            : {}),
                        },
                      })
                    }
                  >
                    {options.map((v) => (
                      <option key={v} value={v}>
                        {v}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <Input
                    required
                    maxLength={253}
                    placeholder={hint}
                    type={key === "port" ? "number" : "text"}
                    min={key === "port" ? 1 : undefined}
                    max={key === "port" ? 65535 : undefined}
                    value={draft.details[key]}
                    onChange={(e) => edit({ ...draft, details: { ...draft.details, [key]: e.target.value } })}
                    className="font-mono"
                  />
                )}
              </label>
            ))}
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              管理 Agent
              <Select
                disabled={!!rows?.find((r) => r.id === draft.id)?.cluster}
                value={draft.cluster ? JSON.stringify([draft.cluster, draft.instance]) : ""}
                onChange={(e) => {
                  const [cluster, instance] = e.target.value ? JSON.parse(e.target.value) : ["", ""]
                  edit({ ...draft, cluster, instance })
                }}
              >
                <option value="">待接入（仅自己可见）</option>
                {targets.map((t) => (
                  <option key={t.key} value={JSON.stringify([t.cluster, t.instance])}>
                    {t.cluster}/{t.instance}
                    {t.status === "offline" ? " · 离线" : ""}
                  </option>
                ))}
              </Select>
            </label>
          </fieldset>
          <p className="text-xs text-muted-foreground">
            选择 Agent 后，该目标的授权用户共同维护这条资产。只登记连接位置，不填写密码或令牌。
          </p>
          <div className="flex gap-2">
            <Button type="submit" variant="primary" size="sm" disabled={busy}>
              保存
            </Button>
            <Button type="button" size="sm" disabled={busy} onClick={() => edit(null)}>
              取消
            </Button>
          </div>
        </form>
      )}

      {remove && (
        <div className="card-surface flex flex-wrap items-center gap-2 p-3 text-sm">
          <span>
            从台账移除「{remove.name}」？不会删除实际{label}。
          </span>
          <Button variant="danger" size="sm" disabled={busy} onClick={() => void save(remove, "delete")}>
            确认移除
          </Button>
          <Button size="sm" onClick={() => setRemove(null)}>
            取消
          </Button>
        </div>
      )}

      <Input
        type="search"
        aria-label="搜索资产"
        placeholder={
          kind === "database"
            ? "搜索实例、引擎或库名"
            : kind === "website"
              ? "搜索名称或域名"
              : "搜索仓库、镜像名或版本"
        }
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />

      {!rows && !error && <p role="status" className="text-sm text-muted-foreground">正在读取台账…</p>}
      {visible?.length === 0 && (
        <p role="status" className="text-sm text-muted-foreground">
          {query ? "没有匹配的资产。" : "尚未登记可见的" + label + "。"}
        </p>
      )}

      {visible?.map((row) => {
        const target = targets.find(
          (t) => t.cluster === row.cluster && t.instance === row.instance && t.status !== "offline",
        )
        return (
          <article className="card-surface space-y-2 p-4" key={row.id}>
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="font-semibold">{row.name}</h2>
              {row.environment && <Badge variant="outline">{row.environment}</Badge>}
              <Badge variant={row.cluster ? (target ? "success" : "warning") : "default"}>
                {row.cluster ? `${row.cluster}/${row.instance}${target ? " · 在线" : " · 离线"}` : "待接入"}
              </Badge>
            </div>
            <p className="break-all font-mono text-sm">{resourceAddress(row)}</p>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="primary" disabled={!target || busy} onClick={() => target && onOperate(row, target)}>
                {actionLabel}
              </Button>
              <Button
                size="sm"
                disabled={busy}
                onClick={() => {
                  edit({ ...row, details: { ...row.details } })
                  setRemove(null)
                }}
              >
                编辑
              </Button>
              <Button
                size="sm"
                variant="danger-outline"
                disabled={busy}
                onClick={() => {
                  edit(null)
                  setRemove(row)
                }}
              >
                移出台账
              </Button>
            </div>
          </article>
        )
      })}
    </main>
  )
}
