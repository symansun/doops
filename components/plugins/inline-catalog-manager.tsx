"use client"

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import { Database, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react"
import { assetRequest } from "@/lib/assets"
import type { Target } from "@/lib/client"
import type { SkillPluginCatalogSchema } from "@/lib/plugins/types"
import {
  buildPluginCatalogPayload,
  createEmptyPluginCatalogRow,
  formatPluginCatalogAddress,
  pluginCatalogKind,
  validatePluginCatalogDraft,
  type PluginCatalogRow,
} from "@/lib/catalog-extensions"
import { Badge, Button, Card, CardContent, CardHeader, CardTitle, Input, Select, StatusBanner } from "@/components/ui"

export function InlineCatalogManager({
  skillSlug,
  schemas,
  targets,
  scopedTarget,
  defaultTarget,
}: {
  skillSlug: string
  schemas: SkillPluginCatalogSchema[]
  targets: Target[]
  scopedTarget?: Target | null
  defaultTarget?: Target | null
}) {
  const [activeKey, setActiveKey] = useState<string>(schemas[0]?.key ?? "")
  const [rows, setRows] = useState<PluginCatalogRow[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [draft, setDraft] = useState<PluginCatalogRow | null>(null)
  const [pendingDelete, setPendingDelete] = useState<PluginCatalogRow | null>(null)

  useEffect(() => {
    if (schemas.length > 0 && !schemas.some((s) => s.key === activeKey)) {
      setActiveKey(schemas[0].key)
      setDraft(null)
      setPendingDelete(null)
    }
  }, [schemas, activeKey])

  const activeSchema = useMemo(
    () => schemas.find((s) => s.key === activeKey) ?? schemas[0] ?? null,
    [schemas, activeKey],
  )
  const activeKind = activeSchema ? pluginCatalogKind(skillSlug, activeSchema.key) : ""

  const loadRows = useCallback(async () => {
    if (!activeSchema || !activeKind) return
    setLoading(true)
    setError("")
    try {
      const res = await assetRequest<{ records?: PluginCatalogRow[] }>(
        "catalog",
        undefined,
        new URLSearchParams({ kind: activeKind }),
      )
      const list = Array.isArray(res.records) ? res.records : []
      setRows(list)
    } catch (err) {
      setError(err instanceof Error ? err.message : `读取${activeSchema.label}失败`)
    } finally {
      setLoading(false)
    }
  }, [activeKind, activeSchema])

  useEffect(() => {
    void loadRows()
  }, [loadRows])

  const visibleRows = useMemo(() => {
    if (!rows) return []
    if (!scopedTarget) return rows
    return rows.filter(
      (r) =>
        (!r.cluster && !r.instance) ||
        (r.cluster === scopedTarget.cluster && r.instance === scopedTarget.instance),
    )
  }, [rows, scopedTarget])

  if (!activeSchema) return null

  function startCreate() {
    if (!activeSchema) return
    const preferred = scopedTarget ?? (defaultTarget && defaultTarget.status !== "offline" ? defaultTarget : null)
    setDraft(createEmptyPluginCatalogRow(skillSlug, activeSchema, preferred))
    setPendingDelete(null)
    setError("")
    setNotice("")
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (!draft || !activeSchema || busy) return
    const check = validatePluginCatalogDraft(activeSchema, draft)
    if (!check.ok) {
      setError(check.message)
      return
    }
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const op = check.normalized.id ? "update" : "create"
      await assetRequest("catalog", buildPluginCatalogPayload(check.normalized, op))
      setDraft(null)
      setNotice(
        `${op === "create" ? "已登记" : "已更新"}${activeSchema.label}「${check.normalized.name}」，下次执行插件时将自动注入 ${activeSchema.env_var}。`,
      )
      await loadRows()
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存插件资产目录记录失败")
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(row: PluginCatalogRow) {
    if (!activeSchema || busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      await assetRequest("catalog", buildPluginCatalogPayload(row, "delete"))
      setPendingDelete(null)
      setNotice(`已从台账移除${activeSchema.label}「${row.name}」（不影响线上实际服务）。`)
      await loadRows()
    } catch (err) {
      setError(err instanceof Error ? err.message : "移除插件资产目录记录失败")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
            <Database className="size-4 text-primary" aria-hidden="true" />
            <span>{activeSchema.label}</span>
            <Badge variant="outline">{visibleRows.length} 条</Badge>
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
              {activeSchema.env_var}
            </code>
          </CardTitle>
          <div className="flex flex-wrap items-center gap-1.5">
            {schemas.length > 1 &&
              schemas.map((s) => (
                <Button
                  key={s.key}
                  type="button"
                  size="sm"
                  variant={activeSchema.key === s.key ? "primary" : "default"}
                  onClick={() => {
                    setActiveKey(s.key)
                    setDraft(null)
                    setPendingDelete(null)
                    setNotice("")
                  }}
                >
                  {s.label}
                </Button>
              ))}
            <Button type="button" size="sm" variant="ghost" disabled={loading || busy} onClick={() => void loadRows()}>
              <RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />
              刷新
            </Button>
            <Button type="button" size="sm" variant="primary" disabled={busy} onClick={startCreate}>
              <Plus className="size-3.5" aria-hidden="true" />
              登记条目
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3 text-xs">
        {activeSchema.description && <p className="text-muted-foreground">{activeSchema.description}</p>}
        {error && <StatusBanner tone="error">{error}</StatusBanner>}
        {notice && <StatusBanner tone="success">{notice}</StatusBanner>}

        {draft && (
          <form
            data-assist-scope="asset-entry"
            onSubmit={handleSubmit}
            className="space-y-3 rounded-lg border bg-muted/20 p-3"
          >
            <div className="font-medium text-foreground">
              {draft.id ? `编辑「${activeSchema.label}」条目` : `登记新「${activeSchema.label}」条目`}
            </div>
            <fieldset disabled={busy} className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              <label className="space-y-1 text-muted-foreground">
                <span className="block font-medium text-foreground">显示名称</span>
                <Input
                  scale="sm"
                  required
                  maxLength={80}
                  placeholder={`例如：生产${activeSchema.label}`}
                  value={draft.name}
                  onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                />
              </label>
              <label className="space-y-1 text-muted-foreground">
                <span className="block font-medium text-foreground">环境</span>
                <Input
                  scale="sm"
                  maxLength={80}
                  placeholder="生产 / 预发 / 内网"
                  value={draft.environment}
                  onChange={(e) => setDraft({ ...draft, environment: e.target.value })}
                />
              </label>
              {activeSchema.fields.map((field) => (
                <label key={field.key} className="space-y-1 text-muted-foreground">
                  <span className="block font-medium text-foreground">{field.label}</span>
                  {field.type === "enum" && field.options && field.options.length > 0 ? (
                    <Select
                      scale="sm"
                      value={draft.details[field.key] ?? field.options[0]}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          details: { ...draft.details, [field.key]: e.target.value },
                        })
                      }
                    >
                      {field.options.map((opt) => (
                        <option key={opt} value={opt}>
                          {opt}
                        </option>
                      ))}
                    </Select>
                  ) : (
                    <Input
                      scale="sm"
                      required
                      maxLength={253}
                      className="font-mono"
                      placeholder={field.placeholder || ""}
                      value={draft.details[field.key] ?? ""}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          details: { ...draft.details, [field.key]: e.target.value },
                        })
                      }
                    />
                  )}
                </label>
              ))}
              <label className="space-y-1 text-muted-foreground">
                <span className="block font-medium text-foreground">归属 Agent</span>
                {scopedTarget ? (
                  <Input scale="sm" disabled className="font-mono" value={`${scopedTarget.cluster}/${scopedTarget.instance}`} />
                ) : (
                  <Select
                    scale="sm"
                    disabled={Boolean(draft.id && rows?.find((r) => r.id === draft.id)?.cluster)}
                    value={draft.cluster ? JSON.stringify([draft.cluster, draft.instance]) : ""}
                    onChange={(e) => {
                      const [cluster, instance] = e.target.value ? (JSON.parse(e.target.value) as [string, string]) : ["", ""]
                      setDraft({ ...draft, cluster, instance })
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
                )}
              </label>
            </fieldset>
            <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
              <span className="text-[11px] text-muted-foreground">
                仅登记非敏感拓扑定位元数据（存储键 <code>{activeKind}</code>），严禁填写密钥、口令或令牌。
              </span>
              <div className="flex gap-2">
                <Button type="submit" size="sm" variant="primary" disabled={busy}>
                  {busy ? "保存中…" : "保存"}
                </Button>
                <Button type="button" size="sm" variant="default" disabled={busy} onClick={() => setDraft(null)}>
                  取消
                </Button>
              </div>
            </div>
          </form>
        )}

        {pendingDelete && (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-danger/30 bg-danger/5 px-3 py-2">
            <span>
              确认从资产目录移除「{pendingDelete.name}」（{formatPluginCatalogAddress(activeSchema, pendingDelete)}）？不会影响线上实际服务。
            </span>
            <div className="flex gap-2">
              <Button type="button" size="sm" variant="danger" disabled={busy} onClick={() => void handleDelete(pendingDelete)}>
                确认移除
              </Button>
              <Button type="button" size="sm" variant="default" disabled={busy} onClick={() => setPendingDelete(null)}>
                取消
              </Button>
            </div>
          </div>
        )}

        {loading && !rows ? (
          <p role="status" className="text-muted-foreground">
            正在读取{activeSchema.label}…
          </p>
        ) : visibleRows.length === 0 ? (
          <div className="rounded-md border border-dashed bg-muted/15 px-3 py-2.5 text-muted-foreground">
            尚未登记任何「{activeSchema.label}」条目。点击右上角「登记条目」并绑定到目标 Agent 后，插件执行时将自动拉取并注入 <code>{activeSchema.env_var}</code>。
          </div>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2">
            {visibleRows.map((row) => {
              const target = targets.find(
                (t) => t.cluster === row.cluster && t.instance === row.instance && t.status !== "offline",
              )
              return (
                <div
                  key={row.id}
                  className="flex items-start justify-between gap-2 rounded-lg border bg-background/60 px-3 py-2"
                >
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-medium text-foreground">{row.name}</span>
                      {row.environment && <Badge variant="outline">{row.environment}</Badge>}
                      <Badge variant={row.cluster ? (target ? "success" : "warning") : "default"}>
                        {row.cluster ? `${row.cluster}/${row.instance}${target ? " · 在线" : " · 离线"}` : "待接入"}
                      </Badge>
                    </div>
                    <p className="break-all font-mono text-[11px] text-muted-foreground">
                      {formatPluginCatalogAddress(activeSchema, row)}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={busy}
                      aria-label={`编辑 ${row.name}`}
                      onClick={() => {
                        setDraft({ ...row, details: { ...row.details } })
                        setPendingDelete(null)
                        setError("")
                      }}
                    >
                      <Pencil className="size-3.5" aria-hidden="true" />
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={busy}
                      aria-label={`移除 ${row.name}`}
                      onClick={() => {
                        setPendingDelete(row)
                        setDraft(null)
                        setError("")
                      }}
                    >
                      <Trash2 className="size-3.5 text-danger" aria-hidden="true" />
                    </Button>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
