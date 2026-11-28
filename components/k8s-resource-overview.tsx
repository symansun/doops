"use client"

import { Boxes, RefreshCw } from "lucide-react"
import { useCallback, useMemo, useState } from "react"
import { fetchK8sOverview, K8S_OVERVIEW_QUERIES, type K8sOverview, type K8sOverviewKey, type K8sResource } from "@/lib/k8s-overview"
import { formatDateTime } from "@/lib/format"
import { useLoader } from "@/lib/use-loader"
import type { Session, Target } from "@/lib/client"
import { Button, DataTable, DataTableColumnHeader, EmptyState, FilterBar, Input, Skeleton, type ColumnDef } from "./ui"

export function K8sResourceOverview({ session, target, sessionId, initialNamespace = "", running, onOpenDetails }: {
  session: Session
  target: Target
  sessionId: string
  initialNamespace?: string
  running: boolean
  onOpenDetails: (kind: K8sOverviewKey, resource: K8sResource) => void
}) {
  const [refreshKey, setRefreshKey] = useState(0)
  const [kind, setKind] = useState<K8sOverviewKey>("pods")
  const [filter, setFilter] = useState("")
  const [namespace, setNamespace] = useState(initialNamespace)
  const [namespaceInput, setNamespaceInput] = useState(initialNamespace)
  const { cluster, instance } = target
  const changeScope = useCallback((value: string) => {
    setNamespaceInput(value)
    if (value === namespace) return
    setRefreshKey((n) => n + 1)
    setFilter("")
    setKind("pods")
    setNamespace(value)
  }, [namespace])

  const { data: overview, loading, error: loadError } = useLoader(
    (signal) => fetchK8sOverview(session, { cluster, instance }, sessionId, signal, namespace),
    [session, cluster, instance, sessionId, refreshKey, namespace],
  )

  const group = overview?.[kind]
  const namespaced = kind === "deployments" || kind === "pods"
  const term = filter.trim().toLowerCase()
  const items = useMemo(
    () => group?.items.filter((item) => item.name.includes(term) || item.namespace?.includes(term)) ?? [],
    [group, term],
  )

  const columns = useMemo<ColumnDef<K8sResource, unknown>[]>(() => {
    const cols: ColumnDef<K8sResource, unknown>[] = [
      {
        accessorKey: "name",
        header: ({ column }) => <DataTableColumnHeader column={column} title="名称" />,
        cell: ({ row }) => (
          <Button
            type="button"
            variant="link"
            size="sm"
            disabled={running}
            onClick={() => onOpenDetails(kind, row.original)}
            className="h-auto p-0 break-all text-left font-mono"
          >
            {row.original.name}
          </Button>
        ),
      },
    ]
    if (namespaced) {
      cols.push({
        accessorKey: "namespace",
        header: ({ column }) => <DataTableColumnHeader column={column} title="命名空间" />,
        cell: ({ row }) => <span className="break-all text-muted-foreground">{row.original.namespace}</span>,
      })
    }
    cols.push({
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title="状态" />,
    })
    if (namespaced) {
      cols.push({
        accessorKey: "ready",
        header: "就绪",
        cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.ready ?? "—"}</span>,
      })
    }
    if (kind === "nodes") {
      cols.push(
        {
          accessorKey: "cpu",
          header: ({ column }) => <DataTableColumnHeader column={column} title="CPU 容量" />,
          cell: ({ row }) => row.original.cpu ?? "未上报",
        },
        {
          accessorKey: "memory",
          header: ({ column }) => <DataTableColumnHeader column={column} title="内存容量" />,
          cell: ({ row }) => row.original.memory ?? "未上报",
        },
      )
    }
    if (kind === "namespaces") {
      cols.push({
        id: "workloads",
        header: "工作负载",
        enableSorting: false,
        cell: ({ row }) => (
          <Button
            size="sm"
            variant="ghost"
            disabled={running}
            aria-label={`查看 ${row.original.name} 工作负载`}
            onClick={() => changeScope(row.original.name)}
          >
            查看工作负载
          </Button>
        ),
      })
    }
    return cols
  }, [changeScope, kind, namespaced, onOpenDetails, running])

  return (
    <section aria-label="Kubernetes 资源" className="card-surface min-w-0 p-4">
      <header className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <Boxes aria-hidden="true" className="size-5 shrink-0 text-primary" />
          <div className="min-w-0">
            <h2 className="text-base font-semibold">Kubernetes 资源</h2>
            <p className="break-all font-mono text-xs text-muted-foreground">{cluster}/{instance}{namespace && ` · ${namespace}`}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span role="status" className="text-xs text-muted-foreground">{loading ? "读取中…" : loadError ? "读取失败" : overview && `${formatDateTime(overview.fetchedAt)} 更新`}</span>
          <Button size="sm" aria-label="刷新 Kubernetes 资源" onClick={() => setRefreshKey((value) => value + 1)} disabled={loading || running}>
            <RefreshCw aria-hidden="true" className={`size-3.5 ${loading ? "motion-safe:animate-spin" : ""}`} />刷新
          </Button>
        </div>
      </header>
      {loadError && (
        <p role="alert" className="mb-3 rounded-lg bg-warning/10 px-3 py-2 text-sm text-warning [overflow-wrap:anywhere]">
          读取 Kubernetes 资源失败 · {loadError}
        </p>
      )}
      <form className="mb-3 flex flex-wrap items-center gap-2" onSubmit={(event) => { event.preventDefault(); changeScope(namespaceInput.trim()) }}>
        <label className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          查询命名空间
          <Input scale="sm" className="w-48" value={namespaceInput} maxLength={63} pattern="[a-z0-9]([a-z0-9\-]*[a-z0-9])?"
            placeholder="留空查询整个集群" title="小写字母、数字和短横线，最多 63 个字符"
            onChange={(event) => setNamespaceInput(event.target.value)} />
        </label>
        <Button type="submit" size="sm" disabled={running || namespaceInput === namespace}>应用范围</Button>
        {namespace && <Button type="button" size="sm" variant="ghost" disabled={running} onClick={() => changeScope("")}>返回整个集群</Button>}
      </form>
      <FilterBar className="mb-3 justify-between">
        <div role="group" aria-label="资源类别" className="flex flex-wrap gap-1">
          {K8S_OVERVIEW_QUERIES.filter((query) => !namespace || query.allNamespaces).map(({ key, label }) => {
            const result = overview?.[key]
            return (
              <Button key={key} size="sm" aria-pressed={kind === key} variant={kind === key ? "primary" : "ghost"}
                onClick={() => { setKind(key); setFilter("") }}>
                {label}<span className="tabular-nums">{result?.error ? "读取失败" : result?.items.length ?? "—"}</span>
              </Button>
            )
          })}
        </div>
        <Input type="search" scale="sm" className="w-full sm:w-60" aria-label="筛选资源" value={filter} onChange={(event) => setFilter(event.target.value)} placeholder={namespaced ? "按名称或命名空间筛选" : "按名称筛选"} />
      </FilterBar>
      {!group ? (
        <div role="status" className="space-y-2 py-2">
          <span className="sr-only">正在读取资源…</span>
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-36 w-full" />
        </div>
      ) : group.error ? (
        <p role="alert" className="rounded-lg bg-warning/10 px-3 py-3 text-sm text-warning [overflow-wrap:anywhere]">{group.error}</p>
      ) : items.length === 0 ? (
        <EmptyState
          role="status"
          icon={<Boxes className="size-6" aria-hidden="true" />}
          title={group.items.length ? "没有匹配的资源" : namespace ? `命名空间 ${namespace} 的查询返回 0 项` : "当前目标没有此类资源"}
          action={filter ? <Button size="sm" onClick={() => setFilter("")}>清空筛选</Button> : undefined}
        />
      ) : (
        <DataTable
          columns={columns}
          data={items}
          getRowId={(item) => `${item.namespace ?? ""}/${item.name}`}
          paginate={items.length > 20}
          initialPageSize={20}
        />
      )}
    </section>
  )
}
