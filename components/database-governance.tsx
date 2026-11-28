"use client"

import { RefreshCw } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import type { Session, Target } from "@/lib/client"
import {
  buildDiffRefOptions,
  describeDiffChangeKind,
  describeGovernanceError,
  diffDatabaseSchema,
  filterHistoryRecords,
  getMigrationStatus,
  inspectDatabaseSchema,
  listDatabaseHistory,
  listDatabaseVersions,
  summarizeSchema,
  supportsSchemaInspect,
  type HistoryReport,
  type MigrationStatusReport,
  type SchemaDiffReport,
  type SchemaInspectReport,
  type VersionsReport,
} from "@/lib/database-governance"
import type { DatabaseCandidate } from "./database-inventory"
import { Badge, Button, Input, SegmentedControl, Select, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"

type GovernanceTab = "schema" | "versions" | "history" | "status"

export function DatabaseGovernance({
  session,
  target,
  sessionId,
  asset,
  database,
  credential,
  disabled,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  asset: DatabaseCandidate
  database: string
  credential: { id: string; name: string }
  disabled: boolean
  onRunningChange: (value: boolean) => void
}) {
  const canInspect = supportsSchemaInspect(asset.engine)
  const [tab, setTab] = useState<GovernanceTab>(canInspect ? "schema" : "versions")
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  // Schema inspect state
  const [schemaReport, setSchemaReport] = useState<SchemaInspectReport | null>(null)
  const [tableFilter, setTableFilter] = useState("")
  const [selectedTableName, setSelectedTableName] = useState("")

  // Versions + Drift diff state
  const [versionsReport, setVersionsReport] = useState<VersionsReport | null>(null)
  const [diffFrom, setDiffFrom] = useState("latest")
  const [diffTo, setDiffTo] = useState("live")
  const [diffRunning, setDiffRunning] = useState(false)
  const [diffError, setDiffError] = useState("")
  const [diffReport, setDiffReport] = useState<SchemaDiffReport | null>(null)

  // History state
  const [allSessions, setAllSessions] = useState(true)
  const [historyReport, setHistoryReport] = useState<HistoryReport | null>(null)
  const [historyMode, setHistoryMode] = useState("")
  const [historyQuery, setHistoryQuery] = useState("")

  // Migration status state
  const [statusReport, setStatusReport] = useState<MigrationStatusReport | null>(null)
  const onRunningChangeRef = useRef(onRunningChange)
  onRunningChangeRef.current = onRunningChange

  // Reset cached data when target/database/credential changes.
  useEffect(() => {
    setSchemaReport(null)
    setSelectedTableName("")
    setVersionsReport(null)
    setDiffReport(null)
    setDiffError("")
    setHistoryReport(null)
    setStatusReport(null)
    setError("")
    setTab(supportsSchemaInspect(asset.engine) ? "schema" : "versions")
  }, [target.cluster, target.instance, asset.database_asset_id, asset.engine, database, credential.id])

  // Load data for the active governance tab.
  useEffect(() => {
    const controller = new AbortController()
    let active = true
    async function load() {
      setLoading(true)
      setError("")
      onRunningChangeRef.current(true)
      const opts = {
        session,
        target: { cluster: target.cluster, instance: target.instance },
        sessionId,
        engine: asset.engine,
        database,
        credential: { id: credential.id, name: credential.name },
        signal: controller.signal,
      }
      try {
        if (tab === "schema") {
          const report = await inspectDatabaseSchema(opts)
          if (!active || controller.signal.aborted) return
          setSchemaReport(report)
          setSelectedTableName((prev) =>
            report.schema.tables.some((t) => t.name === prev) ? prev : (report.schema.tables[0]?.name ?? ""),
          )
        } else if (tab === "versions") {
          const report = await listDatabaseVersions(opts)
          if (!active || controller.signal.aborted) return
          setVersionsReport(report)
          if (report.snapshots.length > 0) {
            setDiffFrom((prev) => (prev === "latest" || prev === "live" || report.snapshots.some((v) => `v${v}` === prev) ? prev : "latest"))
          }
        } else if (tab === "history") {
          const report = await listDatabaseHistory({ ...opts, allSessions })
          if (!active || controller.signal.aborted) return
          setHistoryReport(report)
        } else {
          const report = await getMigrationStatus(opts)
          if (!active || controller.signal.aborted) return
          setStatusReport(report)
        }
      } catch (err) {
        if (!active || controller.signal.aborted) return
        setError(describeGovernanceError(err instanceof Error ? err.message : String(err)))
      } finally {
        if (active) {
          setLoading(false)
          onRunningChangeRef.current(false)
        }
      }
    }
    void load()
    return () => {
      active = false
      controller.abort()
      onRunningChangeRef.current(false)
    }
  }, [session, target.cluster, target.instance, sessionId, asset.engine, database, credential.id, credential.name, tab, allSessions, revision])

  async function runDiff() {
    if (diffRunning || loading || disabled) return
    setDiffRunning(true)
    setDiffError("")
    setDiffReport(null)
    onRunningChangeRef.current(true)
    try {
      const report = await diffDatabaseSchema({
        session,
        target,
        sessionId,
        engine: asset.engine,
        database,
        credential,
        from: diffFrom,
        to: diffTo,
      })
      setDiffReport(report)
    } catch (err) {
      setDiffError(describeGovernanceError(err instanceof Error ? err.message : String(err)))
    } finally {
      setDiffRunning(false)
      onRunningChangeRef.current(false)
    }
  }

  const schemaSummary = useMemo(
    () => (schemaReport ? summarizeSchema(schemaReport.schema) : null),
    [schemaReport],
  )
  const filteredTables = useMemo(() => {
    const tables = schemaReport?.schema.tables ?? []
    const term = tableFilter.trim().toLowerCase()
    if (!term) return tables
    return tables.filter((t) => t.name.toLowerCase().includes(term) || (t.comment ?? "").toLowerCase().includes(term))
  }, [schemaReport, tableFilter])
  const selectedTable = useMemo(
    () => filteredTables.find((t) => t.name === selectedTableName) ?? filteredTables[0] ?? null,
    [filteredTables, selectedTableName],
  )
  const diffOptions = useMemo(
    () => buildDiffRefOptions(versionsReport?.snapshots ?? []),
    [versionsReport],
  )
  const visibleHistory = useMemo(
    () => filterHistoryRecords(historyReport?.records ?? [], { mode: historyMode, query: historyQuery }),
    [historyReport, historyMode, historyQuery],
  )

  return (
    <section className="mt-2 min-w-0 space-y-4" aria-label="数据库结构治理">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <SegmentedControl
          value={tab}
          disabled={disabled || loading || diffRunning}
          ariaLabel="治理视图"
          className="max-w-full flex-wrap"
          onChange={setTab}
          options={[
            ...(canInspect ? [{ value: "schema" as const, label: "Schema 结构" }] : []),
            { value: "versions" as const, label: "版本与漂移" },
            { value: "history" as const, label: "变更流水" },
            { value: "status" as const, label: "迁移状态" },
          ]}
        />
        <Button
          type="button"
          size="sm"
          disabled={disabled || loading || diffRunning}
          onClick={() => setRevision((n) => n + 1)}
        >
          <RefreshCw aria-hidden="true" className={`size-3.5 ${loading ? "motion-safe:animate-spin" : ""}`} />
          刷新
        </Button>
      </div>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}

      {loading ? (
        <p role="status" className="py-6 text-center text-sm text-muted-foreground">正在从目标 Agent 读取 DoSql 治理信息…</p>
      ) : tab === "schema" && schemaReport && schemaSummary ? (
        <div className="space-y-3" aria-label="Schema 结构视图">
          <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border/60 bg-muted/20 px-3 py-2 text-xs">
            <Badge variant="primary">{schemaSummary.baseTableCount} 张基表</Badge>
            {schemaSummary.viewCount > 0 && <Badge variant="outline">{schemaSummary.viewCount} 个视图</Badge>}
            <Badge variant="default">{schemaSummary.columnCount} 列</Badge>
            <Badge variant="default">{schemaSummary.indexCount} 索引</Badge>
            <Badge variant="default">{schemaSummary.foreignKeyCount} 外键</Badge>
            <span className="ml-auto break-all font-mono text-2xs text-muted-foreground" title={schemaReport.schema.schema_hash}>
              {schemaReport.schema.schema_hash}
            </span>
          </div>

          <Input
            type="search"
            scale="sm"
            aria-label="筛选表结构"
            placeholder="按表名或注释筛选"
            value={tableFilter}
            onChange={(e) => setTableFilter(e.target.value)}
          />

          {filteredTables.length === 0 ? (
            <p role="status" className="py-5 text-center text-sm text-muted-foreground">没有匹配的表或视图</p>
          ) : (
            <div className="flex flex-col gap-3 lg:flex-row">
              <div className="max-h-96 overflow-auto rounded-lg border border-border/60 lg:w-64 lg:shrink-0">
                <ul className="divide-y divide-border/50 text-xs" aria-label="Schema 表列表">
                  {filteredTables.map((t) => {
                    const active = selectedTable?.name === t.name
                    return (
                      <li key={t.name}>
                        <button
                          type="button"
                          onClick={() => setSelectedTableName(t.name)}
                          aria-pressed={active}
                          className={`flex w-full items-center justify-between gap-2 px-3 py-2 text-left transition-colors ${active ? "bg-primary/10 text-primary" : "hover:bg-muted/30"}`}
                        >
                          <span className="truncate font-mono">{t.name}</span>
                          <span className="shrink-0 text-2xs text-muted-foreground">{t.columns.length} 列</span>
                        </button>
                      </li>
                    )
                  })}
                </ul>
              </div>

              {selectedTable && (
                <div className="min-w-0 flex-1 space-y-3 rounded-lg border border-border/60 p-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <h4 className="break-all font-mono text-sm font-semibold">{selectedTable.name}</h4>
                    <Badge variant="outline">{selectedTable.kind}</Badge>
                    {selectedTable.engine && <Badge variant="default">{selectedTable.engine}</Badge>}
                    {selectedTable.charset && <Badge variant="default">{selectedTable.charset}</Badge>}
                    {selectedTable.comment && <span className="text-xs text-muted-foreground">{selectedTable.comment}</span>}
                  </div>

                  <div>
                    <h5 className="mb-1.5 text-xs font-medium text-muted-foreground">列定义（{selectedTable.columns.length}）</h5>
                    <Table wrapperClassName="max-h-60 overflow-auto rounded border border-border/50" className="font-mono text-xs">
                      <TableHeader className="bg-muted/40 text-2xs">
                        <TableRow className="hover:bg-transparent">
                          <TableHead className="px-2.5 py-1.5">列名</TableHead>
                          <TableHead className="px-2.5 py-1.5">类型</TableHead>
                          <TableHead className="px-2.5 py-1.5">空值</TableHead>
                          <TableHead className="px-2.5 py-1.5">默认值</TableHead>
                          <TableHead className="px-2.5 py-1.5">附加</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {selectedTable.columns.map((col) => (
                          <TableRow key={col.name}>
                            <TableCell className="px-2.5 py-1.5 font-medium">{col.name}</TableCell>
                            <TableCell className="px-2.5 py-1.5 text-muted-foreground">{col.type}</TableCell>
                            <TableCell className="px-2.5 py-1.5">{col.nullable ? "NULL" : "NOT NULL"}</TableCell>
                            <TableCell className="px-2.5 py-1.5 text-muted-foreground">{col.default ?? "—"}</TableCell>
                            <TableCell className="px-2.5 py-1.5 text-muted-foreground">{col.extra || col.comment || "—"}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>

                  {selectedTable.indexes.length > 0 && (
                    <div>
                      <h5 className="mb-1.5 text-xs font-medium text-muted-foreground">索引（{selectedTable.indexes.length}）</h5>
                      <div className="flex flex-wrap gap-1.5">
                        {selectedTable.indexes.map((idx) => (
                          <Badge key={idx.name} variant={idx.primary ? "primary" : idx.unique ? "success" : "outline"}>
                            {idx.primary ? "PK" : idx.unique ? "UNIQUE" : "IDX"} {idx.name} ({idx.columns.join(", ")})
                          </Badge>
                        ))}
                      </div>
                    </div>
                  )}

                  {selectedTable.foreign_keys.length > 0 && (
                    <div>
                      <h5 className="mb-1.5 text-xs font-medium text-muted-foreground">外键约束（{selectedTable.foreign_keys.length}）</h5>
                      <ul className="space-y-1 font-mono text-xs text-muted-foreground">
                        {selectedTable.foreign_keys.map((fk) => (
                          <li key={fk.name}>
                            {fk.name}: ({fk.columns.join(", ")}) → {fk.referenced_table}({fk.referenced_columns.join(", ")})
                            {fk.on_update ? ` ON UPDATE ${fk.on_update}` : ""}
                            {fk.on_delete ? ` ON DELETE ${fk.on_delete}` : ""}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      ) : tab === "versions" && versionsReport ? (
        <div className="space-y-4" aria-label="版本与漂移视图">
          {canInspect && (
            <div className="space-y-3 rounded-lg border border-border/60 bg-muted/20 p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs font-semibold">结构漂移对比</span>
                <Select scale="sm" aria-label="基线版本" value={diffFrom} disabled={diffRunning || diffOptions.length < 2} onChange={(e) => setDiffFrom(e.target.value)}>
                  {diffOptions.map((opt) => <option key={opt.value} value={opt.value}>{opt.label}</option>)}
                </Select>
                <span className="text-xs text-muted-foreground">→</span>
                <Select scale="sm" aria-label="目标版本" value={diffTo} disabled={diffRunning || diffOptions.length < 2} onChange={(e) => setDiffTo(e.target.value)}>
                  {diffOptions.map((opt) => <option key={opt.value} value={opt.value}>{opt.label}</option>)}
                </Select>
                <Button type="button" size="sm" variant="primary" disabled={diffRunning || disabled || diffOptions.length < 2} onClick={() => void runDiff()}>
                  {diffRunning ? "对比中…" : "对比结构漂移"}
                </Button>
                {diffOptions.length < 2 && (
                  <span className="text-xs text-muted-foreground">尚无历史 Schema 快照，执行首次变更后即可对比漂移。</span>
                )}
              </div>

              {diffError && <StatusBanner tone="error" compact>{diffError}</StatusBanner>}

              {diffReport && (
                <div className="space-y-2" aria-label="结构漂移结果">
                  {diffReport.diff.identical ? (
                    <StatusBanner tone="success" compact>
                      无结构漂移：{diffReport.from} 与 {diffReport.to} 的 Schema 哈希完全一致（{diffReport.diff.to_hash}）。
                    </StatusBanner>
                  ) : (
                    <>
                      <StatusBanner tone={diffReport.diff.has_destructive ? "warning" : "info"} compact>
                        检测到 {diffReport.diff.changes.length} 项结构差异（{diffReport.from} → {diffReport.to}）
                        {diffReport.diff.has_destructive ? " · 包含破坏性变更" : ""}
                      </StatusBanner>
                      {diffReport.diff.possible_renames.length > 0 && (
                        <p className="text-xs text-warning">疑似重命名提示：{diffReport.diff.possible_renames.join("；")}</p>
                      )}
                      <ul className="space-y-1 text-xs">
                        {diffReport.diff.changes.map((ch, idx) => {
                          const meta = describeDiffChangeKind(ch.kind)
                          return (
                            <li key={`${ch.kind}-${ch.table}-${ch.object ?? idx}`} className="flex flex-wrap items-center gap-2 rounded border border-border/50 bg-background px-2.5 py-1.5">
                              <Badge variant={meta.tone}>{meta.label}</Badge>
                              <span className="font-mono font-medium">{ch.table}{ch.object ? `.${ch.object}` : ""}</span>
                              {ch.destructive && <Badge variant="danger">破坏性</Badge>}
                              {(ch.before || ch.after) && (
                                <span className="break-all font-mono text-2xs text-muted-foreground">
                                  {ch.before || "∅"} → {ch.after || "∅"}
                                </span>
                              )}
                            </li>
                          )
                        })}
                      </ul>
                      {diffReport.diff.suggested_ddl.length > 0 && (
                        <details className="rounded border border-border/50 bg-background p-2 text-xs">
                          <summary className="cursor-pointer font-medium">查看建议同步 DDL（{diffReport.diff.suggested_ddl.length} 条，只读参考）</summary>
                          <pre className="mt-2 max-h-60 overflow-auto whitespace-pre-wrap font-mono text-2xs">{diffReport.diff.suggested_ddl.join("\n")}</pre>
                        </details>
                      )}
                    </>
                  )}
                </div>
              )}
            </div>
          )}

          {versionsReport.versions.length === 0 ? (
            <p role="status" className="py-5 text-center text-sm text-muted-foreground">当前库尚无 DoSql 变更版本记录</p>
          ) : (
            <Table wrapperClassName="overflow-auto rounded-lg border border-border/60" className="text-xs">
              <TableHeader className="bg-muted/40">
                <TableRow className="hover:bg-transparent">
                  <TableHead>版本</TableHead>
                  <TableHead>操作 ID</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>提交态</TableHead>
                  <TableHead>隐式提交</TableHead>
                  <TableHead>Schema 快照</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {versionsReport.versions.map((ver) => {
                  const hasSnap = versionsReport.snapshots.includes(ver.version)
                  return (
                    <TableRow key={`${ver.version}-${ver.operation_id}`}>
                      <TableCell className="font-mono font-semibold">v{ver.version}</TableCell>
                      <TableCell className="font-mono text-muted-foreground">{ver.operation_id}</TableCell>
                      <TableCell>
                        <Badge variant={ver.status === "verified" ? "success" : ver.status === "unfinished" ? "warning" : "danger"}>{ver.status}</Badge>
                      </TableCell>
                      <TableCell>{ver.committed ? "已提交" : "未提交"}</TableCell>
                      <TableCell className="font-mono">{ver.implicit_commits}</TableCell>
                      <TableCell>
                        {hasSnap ? <Badge variant="primary">已保存</Badge> : <span className="text-muted-foreground">无</span>}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </div>
      ) : tab === "history" && historyReport ? (
        <div className="space-y-3" aria-label="变更流水视图">
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedControl
              value={allSessions ? "all" : "session"}
              ariaLabel="流水范围"
              onChange={(value) => setAllSessions(value === "all")}
              options={[
                { value: "all", label: "全部会话" },
                { value: "session", label: "当前会话" },
              ]}
            />
            <Select scale="sm" aria-label="筛选模式" value={historyMode} onChange={(e) => setHistoryMode(e.target.value)}>
              <option value="">全部模式</option>
              {["apply", "migrate", "backfill", "query", "dry_run", "backfill_dry_run"].map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </Select>
            <Input
              type="search"
              scale="sm"
              aria-label="筛选变更流水"
              className="w-full sm:ml-auto sm:w-64"
              placeholder="按 SQL、操作 ID 或发布标签筛选"
              value={historyQuery}
              onChange={(e) => setHistoryQuery(e.target.value)}
            />
          </div>

          {visibleHistory.length === 0 ? (
            <p role="status" className="py-5 text-center text-sm text-muted-foreground">暂无匹配的 SQL 变更流水记录</p>
          ) : (
            <Table wrapperClassName="max-h-96 overflow-auto rounded-lg border border-border/60" className="text-xs">
              <TableHeader className="sticky top-0 bg-card">
                <TableRow className="hover:bg-transparent">
                  <TableHead>#</TableHead>
                  <TableHead>模式 / 类型</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>语句</TableHead>
                  <TableHead>影响 / 耗时</TableHead>
                  <TableHead>操作 ID / 时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleHistory.slice(0, 200).map((rec) => (
                  <TableRow key={`${rec.seq}-${rec.operation_id}-${rec.stmt_hash}`}>
                    <TableCell className="whitespace-nowrap font-mono text-muted-foreground">
                      {rec.seq}{rec.version ? ` (v${rec.version})` : ""}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <div className="flex flex-wrap gap-1">
                        <Badge variant={rec.mode === "apply" || rec.mode === "migrate" ? "primary" : "default"}>{rec.mode}</Badge>
                        <Badge variant="outline">{rec.kind}</Badge>
                      </div>
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <Badge variant={rec.status === "succeeded" ? "success" : "danger"}>{rec.status}</Badge>
                    </TableCell>
                    <TableCell className="max-w-md font-mono">
                      <div className="line-clamp-2 break-all" title={rec.statement}>{rec.statement}</div>
                      {rec.error && <p className="mt-1 break-all text-2xs text-destructive">{rec.error}</p>}
                    </TableCell>
                    <TableCell className="whitespace-nowrap font-mono text-muted-foreground">
                      {rec.affected_rows ?? 0} 行 · {rec.duration_ms}ms
                    </TableCell>
                    <TableCell className="whitespace-nowrap font-mono text-2xs text-muted-foreground">
                      <div>{rec.operation_id}{rec.release_tag ? ` · ${rec.release_tag}` : ""}</div>
                      <div>{rec.timestamp}</div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      ) : tab === "status" && statusReport ? (
        <div className="space-y-3" aria-label="迁移状态视图">
          <StatusBanner tone="info" compact>
            只读检查 <code>_dosql_schema_history</code>，不会自动建表或修改库结构。当前版本：<strong>v{statusReport.status.current_version}</strong> · 已记录 {statusReport.status.applied.length} 条迁移。
          </StatusBanner>

          {statusReport.status.dirty && (
            <StatusBanner tone="error">
              检测到中断的脏迁移版本 v{statusReport.status.dirty.version}（{statusReport.status.dirty.description}），已执行 {statusReport.status.dirty.applied}/{statusReport.status.dirty.total} 条语句：{statusReport.status.dirty.error || "未知错误"}。修复后可通过 CLI <code>doops sql --mode migrate --resume</code> 断点续跑。
            </StatusBanner>
          )}

          {statusReport.status.applied.length === 0 ? (
            <p role="status" className="py-5 text-center text-sm text-muted-foreground">当前库尚未启用 <code>_dosql_schema_history</code> 迁移台账</p>
          ) : (
            <Table wrapperClassName="overflow-auto rounded-lg border border-border/60" className="text-xs">
              <TableHeader className="bg-muted/40">
                <TableRow className="hover:bg-transparent">
                  <TableHead>版本</TableHead>
                  <TableHead>描述</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>语句进度</TableHead>
                  <TableHead>发布标签</TableHead>
                  <TableHead>耗时 / 时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {statusReport.status.applied.map((rev) => (
                  <TableRow key={rev.version}>
                    <TableCell className="font-mono font-semibold">V{String(rev.version).padStart(3, "0")}</TableCell>
                    <TableCell>
                      <div>{rev.description}</div>
                      <div className="font-mono text-2xs text-muted-foreground">{rev.checksum}</div>
                    </TableCell>
                    <TableCell><Badge variant="outline">{rev.type}</Badge></TableCell>
                    <TableCell>
                      <Badge variant={rev.status === "succeeded" ? "success" : rev.status === "running" ? "warning" : "danger"}>{rev.status}</Badge>
                    </TableCell>
                    <TableCell className="font-mono">{rev.applied}/{rev.total}</TableCell>
                    <TableCell className="font-mono text-muted-foreground">{rev.release_tag || "—"}</TableCell>
                    <TableCell className="whitespace-nowrap font-mono text-2xs text-muted-foreground">
                      <div>{rev.duration_ms}ms</div>
                      <div>{rev.applied_at}</div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      ) : null}
    </section>
  )
}
