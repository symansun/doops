"use client"

import { Globe2, RefreshCw } from "lucide-react"
import { useMemo, useState } from "react"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import { useLoader } from "@/lib/use-loader"
import { Button, DataTable, DataTableColumnHeader, EmptyState, FilterBar, Input, Skeleton, type ColumnDef } from "./ui"

type Site = { site_id: string; domain: string; current_release: string; previous_release: string | null; updated_at: string }

function isSite(value: unknown): value is Site {
  if (!value || typeof value !== "object") return false
  const row = value as Record<string, unknown>
  return [row.site_id, row.domain, row.current_release].every((field) => typeof field === "string" && field.length > 0)
    && (row.previous_release === null || typeof row.previous_release === "string")
    && typeof row.updated_at === "string" && Number.isFinite(Date.parse(row.updated_at))
}

export function WebsiteInventory({ session, target, sessionId, revision, running, onRefresh, onInspect }: {
  session: Session
  target: Target
  sessionId: string
  revision: number
  running: boolean
  onRefresh: () => void
  onInspect: (siteId: string, domain: string) => void
}) {
  const [filter, setFilter] = useState("")
  const { cluster, instance } = target
  const { data, error, loading } = useLoader(async (signal) => {
    let text = ""
    await callTool(session, { cluster, instance, tool: TOOLS.web, arguments: { session_id: sessionId, operation: "site-list" }, signal }, (event) => {
      if (event.type === "result") text = extractText(event.result)
    })
    let result
    try { result = JSON.parse(text) } catch { throw new Error("站点列表响应不是有效的 JSON") }
    if (!Array.isArray(result?.sites) || !result.sites.every(isSite)) throw new Error("站点列表响应格式无效")
    return result.sites as Site[]
  }, [session, cluster, instance, sessionId, revision], { initialData: [] as Site[] })

  const sites = data ?? []
  const term = filter.trim().toLowerCase()
  const visible = useMemo(
    () => sites.filter((site) => site.site_id.includes(term) || site.domain.includes(term)),
    [sites, term],
  )
  const columns = useMemo<ColumnDef<Site, unknown>[]>(
    () => [
      {
        accessorKey: "site_id",
        header: ({ column }) => <DataTableColumnHeader column={column} title="站点 ID" />,
        cell: ({ row }) => (
          <Button
            type="button"
            variant="link"
            size="sm"
            title="带入站点巡检"
            disabled={running}
            onClick={() => onInspect(row.original.site_id, row.original.domain)}
            className="h-auto p-0 text-left font-mono"
          >
            {row.original.site_id}
          </Button>
        ),
      },
      {
        accessorKey: "domain",
        header: ({ column }) => <DataTableColumnHeader column={column} title="域名" />,
      },
      {
        accessorKey: "current_release",
        header: ({ column }) => <DataTableColumnHeader column={column} title="当前版本" />,
        cell: ({ row }) => <span className="font-mono">{row.original.current_release}</span>,
      },
      {
        accessorKey: "previous_release",
        header: "上一版本",
        cell: ({ row }) => <span className="font-mono">{row.original.previous_release ?? "—"}</span>,
      },
      {
        accessorKey: "updated_at",
        header: ({ column }) => <DataTableColumnHeader column={column} title="更新时间" />,
        cell: ({ row }) => (
          <span className="text-muted-foreground">{new Date(row.original.updated_at).toLocaleString()}</span>
        ),
      },
    ],
    [onInspect, running],
  )

  return (
    <section aria-label="已发布站点" className="card-surface min-w-0 p-4">
      <header className="mb-4 flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <Globe2 aria-hidden="true" className="size-5 shrink-0 text-primary" />
          <div className="min-w-0"><h2 className="text-base font-semibold">已发布站点</h2><p className="break-all text-xs text-muted-foreground">DoWeb · {cluster}/{instance}</p></div>
        </div>
        <Button size="sm" aria-label="刷新站点" onClick={onRefresh} disabled={loading || running}><RefreshCw aria-hidden="true" className={`size-3.5 ${loading ? "motion-safe:animate-spin" : ""}`} />刷新</Button>
      </header>
      {loading ? (
        <div role="status" className="space-y-2 py-2">
          <span className="sr-only">正在读取站点发布记录…</span>
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
      ) : error ? (
        <p role="alert" className="rounded-lg bg-warning/10 px-3 py-3 text-sm text-warning [overflow-wrap:anywhere]">读取站点失败 · {error}</p>
      ) : sites.length === 0 ? (
        <EmptyState role="status" icon={<Globe2 className="size-6" aria-hidden="true" />} title="当前目标没有 DoWeb 发布记录" />
      ) : (
        <div className="space-y-3">
          <FilterBar className="justify-between">
            <span className="text-xs text-muted-foreground">{visible.length} 个站点</span>
            <Input type="search" scale="sm" aria-label="筛选站点" placeholder="按站点 ID 或域名筛选" className="w-full sm:w-64" value={filter} onChange={(event) => setFilter(event.target.value)} />
          </FilterBar>
          {visible.length === 0 ? (
            <EmptyState role="status" title="没有匹配的站点" action={<Button size="sm" onClick={() => setFilter("")}>清空筛选</Button>} />
          ) : (
            <DataTable
              columns={columns}
              data={visible}
              getRowId={(site) => site.site_id}
              paginate={visible.length > 20}
              initialPageSize={20}
            />
          )}
        </div>
      )}
    </section>
  )
}
