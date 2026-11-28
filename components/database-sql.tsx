"use client"

import { useEffect, useRef, useState } from "react"
import type { Session, Target, ToolCancel } from "@/lib/client"
import { queryDatabase } from "@/lib/database-browser"
import { describeGovernanceError, describeSegmentKind, lintDatabaseSql, type LintReport } from "@/lib/database-governance"
import type { DatabaseCandidate } from "./database-inventory"
import { Badge, Button, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Textarea } from "./ui"

type QueryResult = Awaited<ReturnType<typeof queryDatabase>>
type Statement = QueryResult["result"]["statements"][number]

export function DatabaseRows({ statement }: { statement: Statement }) {
  const { columns, rows, truncated } = statement
  return <div className="min-w-0">
    {!!columns?.length && (
      <Table wrapperClassName="max-h-80 max-w-full overflow-auto rounded-md border" className="w-max min-w-full font-mono text-xs">
        <TableHeader><TableRow className="hover:bg-transparent">{columns.map((column, i) => <TableHead key={i} className="whitespace-pre border-r last:border-r-0">{column}</TableHead>)}</TableRow></TableHeader>
        <TableBody>{rows?.map((row, i) => <TableRow key={i}>{row.map((cell, j) => <TableCell key={j} className="whitespace-pre border-r py-1.5 last:border-r-0">{cell}</TableCell>)}</TableRow>)}</TableBody>
      </Table>
    )}
    <p className="mt-1 text-xs text-muted-foreground">{rows?.length ?? 0} 行{truncated && " · 结果已截断"}</p>
  </div>
}

export function DatabaseSql({ session, target, sessionId, asset, database, credential, initialSql = "", disabled, onRunningChange }: {
  session: Session; target: Target; sessionId: string; asset: DatabaseCandidate
  database: string; credential: { id: string; name: string }; initialSql?: string
  disabled: boolean; onRunningChange: (value: boolean) => void
}) {
  const [sql, setSql] = useState(initialSql)
  const [running, setRunning] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [cancel, setCancel] = useState<ToolCancel | null>(null)
  const [error, setError] = useState("")
  const [report, setReport] = useState<QueryResult | null>(null)
  const [lintReport, setLintReport] = useState<LintReport | null>(null)
  const [linting, setLinting] = useState(false)
  const active = useRef<AbortController | null>(null)
  const region = useRef<HTMLElement>(null)

  useEffect(() => {
    region.current?.scrollIntoView({ block: "nearest" })
    setSql(initialSql)
    setReport(null)
    setLintReport(null)
    setLinting(false)
    setError("")
    setRunning(false)
    setStopping(false)
    setCancel(null)
    return () => { active.current?.abort(); active.current = null }
  }, [session, target.cluster, target.instance, sessionId, asset.database_asset_id, asset.engine, database, credential.id, credential.name, initialSql])

  const canRun = !disabled && !linting && !!database.trim() && !!credential.id && !!credential.name && !!sql.trim()
  async function run() {
    if (!canRun || active.current) return
    const controller = new AbortController()
    active.current = controller
    controller.signal.addEventListener("abort", () => onRunningChange(false), { once: true })
    setRunning(true)
    onRunningChange(true)
    setError("")
    setReport(null)
    setLintReport(null)
    try {
      const receipt = await queryDatabase({ session, target, sessionId, engine: asset.engine, database, credential, sql,
        signal: controller.signal, onCancelReady: (stop) => { if (!controller.signal.aborted) setCancel(() => stop) },
      })
      if (!controller.signal.aborted) setReport(receipt)
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (!controller.signal.aborted) {
        active.current = null
        setCancel(null)
        setRunning(false)
        setStopping(false)
        onRunningChange(false)
      }
    }
  }

  async function runLint() {
    if (!canRun || running || active.current) return
    const controller = new AbortController()
    active.current = controller
    controller.signal.addEventListener("abort", () => onRunningChange(false), { once: true })
    setLinting(true)
    onRunningChange(true)
    setError("")
    setReport(null)
    setLintReport(null)
    try {
      const receipt = await lintDatabaseSql({ session, target, sessionId, engine: asset.engine, database, credential, sql, signal: controller.signal })
      if (!controller.signal.aborted) setLintReport(receipt)
    } catch (err) {
      if (!controller.signal.aborted) setError(describeGovernanceError(err instanceof Error ? err.message : String(err)))
    } finally {
      if (!controller.signal.aborted) {
        active.current = null
        setLinting(false)
        onRunningChange(false)
      }
    }
  }

  async function stop() {
    const request = active.current
    if (!request || !cancel || stopping) return
    setStopping(true)
    setError("")
    try { await cancel() } catch (err) {
      if (active.current === request) {
        setError(`停止请求失败：${String(err)}`)
        setStopping(false)
      }
    }
  }

  return <section className="card-surface mt-4 min-w-0 space-y-3 p-4" aria-label="只读 SQL 操作" ref={region}>
    <h3 className="text-sm font-semibold">只读 SQL 与门禁预检</h3>
    <p className="break-all font-mono text-xs text-muted-foreground">{target.cluster}/{target.instance} · {asset.namespace}/{asset.service} · {asset.engine} · {database} · {credential.name}</p>
    <label className="grid gap-1 text-xs">SQL 语句<Textarea className="min-h-24 w-full font-mono text-sm" disabled={disabled || running || linting} value={sql} onChange={(e) => setSql(e.target.value)} spellCheck={false} /></label>
    <div className="flex flex-wrap justify-end gap-2">
      {running ? (
        <Button onClick={stop} disabled={!cancel || stopping}>{stopping ? "等待停止终态…" : "停止 SQL"}</Button>
      ) : (
        <>
          <Button type="button" onClick={runLint} disabled={!canRun}>{linting ? "预检中…" : "门禁预检"}</Button>
          <Button type="button" variant="primary" onClick={run} disabled={!canRun}>执行只读 SQL</Button>
        </>
      )}
    </div>
    {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
    {lintReport && <div aria-label="SQL 门禁预检结果" role="region" className="min-w-0 space-y-2 rounded-md border border-border/60 bg-muted/20 p-3">
      <StatusBanner tone={lintReport.policy.passed ? (lintReport.policy.warnings > 0 ? "warning" : "success") : "error"} compact>
        {lintReport.policy.passed ? "门禁预检通过" : "门禁预检拦截"} · {lintReport.statements} 条语句 · {lintReport.policy.errors} 项错误 · {lintReport.policy.warnings} 项告警
      </StatusBanner>
      {lintReport.segments.length > 0 && <div className="flex flex-wrap items-center gap-1.5 text-xs">
        <span className="text-muted-foreground">执行分段：</span>
        {lintReport.segments.map((seg) => {
          const meta = describeSegmentKind(seg.kind)
          return <Badge key={seg.index} variant={meta.variant}>#{seg.index} {meta.label} ({seg.statements?.length ?? 0} 条{seg.atomic ? "·原子" : ""})</Badge>
        })}
      </div>}
      {lintReport.policy.findings.length > 0 && <ul className="space-y-1 text-xs">
        {lintReport.policy.findings.map((finding, i) => (
          <li key={`${finding.rule_id}-${finding.statement_index}-${i}`} className="flex flex-wrap items-center gap-1.5">
            <Badge variant={finding.severity === "error" ? "danger" : "warning"}>{finding.severity}</Badge>
            <code className="font-mono text-2xs">{finding.rule_id}</code>
            <span className="text-muted-foreground">语句 #{finding.statement_index}</span>
            <span>{finding.message}</span>
          </li>
        ))}
      </ul>}
    </div>}
    {report && <div aria-label="SQL 查询结果" role="region" className="min-w-0 space-y-2">
      <p className="break-all font-mono text-xs text-muted-foreground">{report.database} · {report.credential} · {report.operation_id}</p>
      {report.result.statements.map((statement, i) => <DatabaseRows key={i} statement={statement} />)}
    </div>}
  </section>
}
