"use client"

import { Database, RefreshCw } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { useLoader } from "@/lib/use-loader"
import { TOOLS } from "@/lib/gateway"
import { Button, DataTable, DataTableColumnHeader, EmptyState, FilterBar, Input, Select, Skeleton, type ColumnDef } from "./ui"

export type DatabaseCandidate = {
  database_asset_id: string
  related_asset_ids?: string[]
  namespace: string
  service: string
  engine: string
  host: string
  port?: number
  database_name?: string
  status: string
  source: string
}

const ENGINES: Record<string, string> = { postgres: "PostgreSQL", mysql: "MySQL", mongodb: "MongoDB", mssql: "SQL Server" }

function isCandidate(value: unknown): value is DatabaseCandidate {
  if (!value || typeof value !== "object") return false
  const row = value as Record<string, unknown>
  return ["database_asset_id", "namespace", "service", "engine", "status", "source"].every((key) => typeof row[key] === "string" && row[key] !== "")
    && typeof row.host === "string" && (row.related_asset_ids === undefined || (Array.isArray(row.related_asset_ids) && row.related_asset_ids.every((id) => typeof id === "string")))
    && (row.database_name === undefined || typeof row.database_name === "string")
    && (row.port === undefined || typeof row.port === "number")
}

async function readInventory(session: Session, cluster: string, instance: string, sessionId: string, signal: AbortSignal): Promise<DatabaseCandidate[]> {
  let text = ""
  await callTool(session, { cluster, instance, tool: TOOLS.databaseInventory, arguments: { session_id: sessionId }, signal }, (event) => {
    if (event.type === "result") text = extractText(event.result)
  })
  let payload
  try {
    payload = JSON.parse(text)
  } catch {
    throw new Error("数据库发现接口返回了无效 JSON")
  }
  if (payload?.status !== "discovered" || !Array.isArray(payload.candidates) || !payload.candidates.every(isCandidate)) {
    throw new Error("数据库发现结果格式无效")
  }
  return payload.candidates
}

function rowKey(row: DatabaseCandidate) {
  return `${row.source}/${row.database_asset_id}`
}

export function DatabaseInventory({ session, target, sessionId, running, onOpen }: {
  session: Session
  target: Target
  sessionId: string
  running: boolean
  onOpen: (database: DatabaseCandidate) => void
}) {
  const [revision, setRevision] = useState(0)
  const [engine, setEngine] = useState("")
  const [filter, setFilter] = useState("")
  const { cluster, instance } = target

  const { data, error, loading } = useLoader(
    (signal) => readInventory(session, cluster, instance, sessionId, signal),
    [session, cluster, instance, sessionId, revision],
    { initialData: [] as DatabaseCandidate[] },
  )
  const rows = data ?? []
  // 选中的引擎在新结果里不存在时回到"全部"，避免筛选框停在一个已消失的选项上。
  useEffect(() => {
    setEngine((current) => (rows.some((row) => row.engine === current) ? current : ""))
  }, [rows])

  const term = filter.trim().toLowerCase()
  const visible = useMemo(
    () =>
      rows.filter(
        (row) =>
          (!engine || row.engine === engine) &&
          [row.service, row.namespace, row.database_name].some((value) => value?.toLowerCase().includes(term)),
      ),
    [rows, engine, term],
  )

  const columns = useMemo<ColumnDef<DatabaseCandidate, unknown>[]>(
    () => [
      {
        accessorKey: "service",
        header: ({ column }) => <DataTableColumnHeader column={column} title="实例" />,
        cell: ({ row }) => (
          <Button
            type="button"
            variant="link"
            size="sm"
            disabled={running}
            onClick={() => onOpen(row.original)}
            className="h-auto p-0 break-all text-left font-mono"
          >
            {row.original.service}
          </Button>
        ),
      },
      {
        accessorKey: "engine",
        header: ({ column }) => <DataTableColumnHeader column={column} title="引擎" />,
        cell: ({ row }) => ENGINES[row.original.engine] ?? row.original.engine,
      },
      {
        accessorKey: "namespace",
        header: ({ column }) => <DataTableColumnHeader column={column} title="命名空间" />,
        cell: ({ row }) => <span className="break-all text-muted-foreground">{row.original.namespace}</span>,
      },
    ],
    [onOpen, running],
  )

  return (
    <section aria-label="数据库资源" className="card-surface min-w-0 p-4">
      <header className="mb-4 flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <Database aria-hidden="true" className="size-5 shrink-0 text-primary" />
          <div className="min-w-0"><h2 className="text-base font-semibold">数据库实例</h2><p className="break-all font-mono text-xs text-muted-foreground">{cluster}/{instance}</p></div>
        </div>
        <Button size="sm" disabled={loading || running} onClick={() => setRevision((n) => n + 1)} aria-label="刷新数据库资源">
          <RefreshCw aria-hidden="true" className={`size-3.5 ${loading ? "motion-safe:animate-spin" : ""}`} />刷新
        </Button>
      </header>
      {loading ? (
        <div role="status" className="space-y-2 py-2">
          <span className="sr-only">正在发现数据库实例…</span>
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
      ) : error ? (
        <p role="alert" className="break-words rounded-lg bg-warning/10 px-3 py-3 text-sm text-warning">数据库发现失败 · {error}</p>
      ) : rows.length === 0 ? (
        <EmptyState role="status" icon={<Database className="size-6" aria-hidden="true" />} title="当前目标未发现数据库实例" />
      ) : (
        <div className="space-y-3">
          <FilterBar>
            <Select scale="sm" aria-label="数据库引擎" value={engine} onChange={(e) => setEngine(e.target.value)}>
              <option value="">全部引擎</option>
              {[...new Set(rows.map((row) => row.engine))].sort().map((key) => <option key={key} value={key}>{ENGINES[key] ?? key}</option>)}
            </Select>
            <span className="text-xs text-muted-foreground">{visible.length} 个实例</span>
            <Input type="search" scale="sm" aria-label="筛选数据库" className="w-full sm:ml-auto sm:w-64" placeholder="按实例、库名或命名空间筛选" value={filter} onChange={(e) => setFilter(e.target.value)} />
          </FilterBar>
          {!visible.length ? (
            <EmptyState role="status" title="没有匹配的数据库实例" action={<Button size="sm" onClick={() => { setEngine(""); setFilter("") }}>清空筛选</Button>} />
          ) : (
            <DataTable
              columns={columns}
              data={visible}
              getRowId={rowKey}
              paginate={visible.length > 20}
              initialPageSize={20}
            />
          )}
        </div>
      )}
    </section>
  )
}
