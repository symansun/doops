"use client"

import { useEffect, useMemo, useState } from "react"
import type { Session } from "@/lib/client"
import {
  MONITOR_METRIC_RANGES,
  buildTrendGeometry,
  deltaDirection,
  describeArtifactState,
  diffHasChanges,
  fetchMonitorMetricSeries,
  formatDeltaPercent,
  formatMetricNumber,
  formatMetricValue,
  formatSignedNumber,
  pickTrendSeries,
  summarizeRunDiff,
  type MonitorMetricRange,
  type MonitorMetricSeriesResponse,
  type MonitorRunDiff,
} from "@/lib/monitor-metrics"
import {
  formatChartValue,
  gaugeArcPath,
  mapChartPointsToViewBox,
  normalizeInspectionCharts,
  type InspectionBarChart,
  type InspectionChart,
  type InspectionChartTone,
  type InspectionGaugeChart,
  type InspectionLineChart,
} from "@/lib/inspection-charts"
import { renderFormalInspectionReportHtml } from "@/lib/formal-inspection-report"
import { Markdown } from "./markdown"
import {
  Badge,
  Button,
  Inline,
  Select,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui"

type ArtifactViewMode = "visual" | "markdown" | "json"

const SUMMARY_KEYS = new Set(["summary", "headline", "message", "conclusion", "description", "note"])
const STATUS_KEYS = new Set(["status", "health", "result", "state", "level", "severity", "verdict"])
const PERCENT_KEY_RE = /(percent|pct|rate|ratio|usage|utilization|score)$/i

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function isPrimitive(v: unknown): v is string | number | boolean | null {
  return v === null || typeof v === "string" || typeof v === "number" || typeof v === "boolean"
}

function statusBadgeVariant(val: string): "success" | "warning" | "danger" | "primary" | "outline" | null {
  const s = val.trim().toLowerCase()
  if (["pass", "passed", "ok", "healthy", "success", "normal", "active", "running", "ready", "up", "bound", "true"].includes(s)) {
    return "success"
  }
  if (["warning", "warn", "degraded", "partial", "pending"].includes(s)) {
    return "warning"
  }
  if (["critical", "error", "fail", "failed", "unhealthy", "down", "offline", "alert", "crashloopbackoff", "oomkilled"].includes(s)) {
    return "danger"
  }
  if (["skipped", "skip", "unknown", "inactive", "stopped", "disabled"].includes(s)) {
    return "outline"
  }
  return null
}

function formatScalarValue(v: unknown): string {
  if (v === null || v === undefined) return "—"
  if (typeof v === "boolean") return v ? "true" : "false"
  if (typeof v === "number") {
    if (!Number.isFinite(v)) return String(v)
    return Number.isInteger(v) ? v.toLocaleString("en-US") : Number(v.toFixed(3)).toString()
  }
  return String(v)
}

function stripFencedJsonFromMarkdown(md: string): string {
  // In the combined Visual tab, hide fenced json blocks that are already rendered
  // interactively by the dynamic JSON visualizer so the page stays crisp & non-redundant.
  return md.replace(/```(?:json|JSON)?\s*\n[\s\S]*?\n```/g, "").trim()
}

function downloadBlob(filename: string, content: string, mime: string) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function DynamicValueCell({ colKey, value }: { colKey: string; value: unknown }) {
  if (value === null || value === undefined || value === "") {
    return <span className="text-muted-foreground">—</span>
  }
  if (typeof value === "boolean") {
    return <Badge variant={value ? "success" : "outline"}>{value ? "true" : "false"}</Badge>
  }
  if (typeof value === "string") {
    const v = STATUS_KEYS.has(colKey.toLowerCase()) ? statusBadgeVariant(value) : null
    if (v) return <Badge variant={v}>{value}</Badge>
    return <span className="break-words text-xs text-foreground">{value}</span>
  }
  if (typeof value === "number") {
    return <span className="font-mono text-xs tabular-nums text-foreground">{formatScalarValue(value)}</span>
  }
  if (Array.isArray(value)) {
    if (value.length === 0) return <span className="text-muted-foreground">[]</span>
    if (value.every(isPrimitive)) {
      return (
        <div className="flex flex-wrap gap-1">
          {value.slice(0, 6).map((item, idx) => (
            <Badge key={idx} variant="outline">{formatScalarValue(item)}</Badge>
          ))}
          {value.length > 6 && <Badge variant="outline">+{value.length - 6}</Badge>}
        </div>
      )
    }
    return <span className="font-mono text-2xs text-muted-foreground">{value.length} 项</span>
  }
  return (
    <code className="block max-w-xs truncate rounded bg-muted/60 px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
      {JSON.stringify(value)}
    </code>
  )
}

function DynamicObjectTable({ label, rows }: { label?: string; rows: Record<string, unknown>[] }) {
  const columns = useMemo(() => {
    const keys: string[] = []
    const seen = new Set<string>()
    for (const row of rows.slice(0, 100)) {
      for (const k of Object.keys(row)) {
        if (!seen.has(k)) {
          seen.add(k)
          keys.push(k)
        }
      }
    }
    return keys.slice(0, 12)
  }, [rows])

  if (rows.length === 0 || columns.length === 0) return null

  return (
    <div className="space-y-1.5">
      {label && (
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs font-semibold text-foreground">{label}</span>
          <Badge variant="outline">{rows.length} 行</Badge>
        </div>
      )}
      <Table wrapperClassName="rounded-md border">
        <TableHeader className="bg-muted/30">
          <TableRow>
            {columns.map((col) => (
              <TableHead key={col} className="whitespace-nowrap font-mono text-2xs">
                {col}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.slice(0, 100).map((row, rIdx) => (
            <TableRow key={rIdx}>
              {columns.map((col) => (
                <TableCell key={col} className="align-top text-xs">
                  <DynamicValueCell colKey={col} value={row[col]} />
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {rows.length > 100 && (
        <p className="text-2xs text-muted-foreground">已展示前 100 行（共 {rows.length} 行），完整数据请切换「JSON 产物」查看或下载。</p>
      )}
    </div>
  )
}

function DynamicJsonNode({ data, depth = 0 }: { data: unknown; depth?: number }) {
  if (data === null || data === undefined) return null

  if (isPrimitive(data)) {
    return (
      <div className="rounded-md border bg-background/70 px-3 py-2 font-mono text-xs text-foreground">
        {formatScalarValue(data)}
      </div>
    )
  }

  if (Array.isArray(data)) {
    if (data.length === 0) {
      return <p className="font-mono text-xs text-muted-foreground">[]（空列表）</p>
    }
    if (data.every(isPlainObject)) {
      return <DynamicObjectTable rows={data} />
    }
    if (data.every(isPrimitive)) {
      return (
        <div className="flex flex-wrap gap-1.5">
          {data.map((item, i) => (
            <Badge key={i} variant="outline">{formatScalarValue(item)}</Badge>
          ))}
        </div>
      )
    }
    return (
      <pre className="max-h-64 overflow-auto rounded-md border bg-background p-2.5 font-mono text-2xs text-foreground">
        {JSON.stringify(data, null, 2)}
      </pre>
    )
  }

  if (!isPlainObject(data)) return null

  if (depth >= 3) {
    return (
      <pre className="max-h-60 overflow-auto rounded-md border bg-background p-2.5 font-mono text-2xs text-foreground">
        {JSON.stringify(data, null, 2)}
      </pre>
    )
  }

  const entries = Object.entries(data).filter(([, v]) => v !== undefined)
  let summaryText = ""
  let statusText = ""
  const scalars: Array<[string, string | number | boolean | null]> = []
  const objectArrays: Array<[string, Record<string, unknown>[]]> = []
  const primitiveArrays: Array<[string, Array<string | number | boolean | null>]> = []
  const nestedObjects: Array<[string, Record<string, unknown>]> = []
  const mixedArrays: Array<[string, unknown[]]> = []

  for (const [k, v] of entries) {
    const kl = k.toLowerCase()
    if (depth === 0 && !summaryText && SUMMARY_KEYS.has(kl) && typeof v === "string" && v.trim()) {
      summaryText = v.trim()
      continue
    }
    if (depth === 0 && !statusText && STATUS_KEYS.has(kl) && typeof v === "string" && v.trim()) {
      statusText = v.trim()
      scalars.push([k, v])
      continue
    }
    if (isPrimitive(v)) {
      scalars.push([k, v])
    } else if (Array.isArray(v)) {
      if (v.length === 0) {
        scalars.push([k, "0 项"])
      } else if (v.every(isPlainObject)) {
        objectArrays.push([k, v])
      } else if (v.every(isPrimitive)) {
        primitiveArrays.push([k, v])
      } else {
        mixedArrays.push([k, v])
      }
    } else if (isPlainObject(v)) {
      if (Object.keys(v).length > 0) {
        nestedObjects.push([k, v])
      }
    }
  }

  const statusTone = statusText ? statusBadgeVariant(statusText) : null

  return (
    <div className="space-y-3">
      {summaryText && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 px-3 py-2 text-xs">
          {statusText && <Badge variant={statusTone || "outline"}>{statusText}</Badge>}
          <span className="font-medium text-foreground">{summaryText}</span>
        </div>
      )}

      {scalars.length > 0 && (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
          {scalars.map(([k, v]) => {
            const isStatus = typeof v === "string" && STATUS_KEYS.has(k.toLowerCase())
            const badgeTone = isStatus ? statusBadgeVariant(v) : null
            const isPct =
              typeof v === "number" &&
              Number.isFinite(v) &&
              v >= 0 &&
              v <= 100 &&
              PERCENT_KEY_RE.test(k)
            const barColor =
              isPct && k.toLowerCase().includes("score")
                ? v >= 80
                  ? "bg-success"
                  : v >= 60
                    ? "bg-warning"
                    : "bg-destructive"
                : isPct
                  ? v >= 90
                    ? "bg-destructive"
                    : v >= 75
                      ? "bg-warning"
                      : "bg-primary"
                  : ""
            return (
              <div key={k} className="rounded-md border bg-background/70 px-2.5 py-2">
                <div className="truncate font-mono text-2xs text-muted-foreground" title={k}>
                  {k}
                </div>
                <div className="mt-1 flex items-center gap-1.5">
                  {badgeTone ? (
                    <Badge variant={badgeTone}>{v}</Badge>
                  ) : typeof v === "boolean" ? (
                    <Badge variant={v ? "success" : "outline"}>{v ? "true" : "false"}</Badge>
                  ) : (
                    <span className="truncate font-mono text-sm font-semibold tabular-nums text-foreground" title={formatScalarValue(v)}>
                      {formatScalarValue(v)}
                      {isPct && !k.toLowerCase().includes("score") ? "%" : ""}
                    </span>
                  )}
                </div>
                {isPct && (
                  <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                    <div className={`h-full rounded-full ${barColor}`} style={{ width: `${Math.max(2, Math.min(100, v))}%` }} />
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {objectArrays.map(([k, rows]) => (
        <DynamicObjectTable key={k} label={k} rows={rows} />
      ))}

      {primitiveArrays.map(([k, list]) => (
        <div key={k} className="space-y-1.5">
          <div className="flex items-center gap-2">
            <span className="font-mono text-xs font-semibold text-foreground">{k}</span>
            <Badge variant="outline">{list.length}</Badge>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {list.slice(0, 40).map((item, idx) => (
              <Badge key={idx} variant="outline">{formatScalarValue(item)}</Badge>
            ))}
            {list.length > 40 && <Badge variant="outline">+{list.length - 40}</Badge>}
          </div>
        </div>
      ))}

      {mixedArrays.map(([k, list]) => (
        <div key={k} className="space-y-1.5">
          <span className="font-mono text-xs font-semibold text-foreground">{k}</span>
          <pre className="max-h-56 overflow-auto rounded-md border bg-background p-2.5 font-mono text-2xs text-foreground">
            {JSON.stringify(list, null, 2)}
          </pre>
        </div>
      ))}

      {nestedObjects.map(([k, obj]) => (
        <div key={k} className="space-y-2 rounded-md border bg-muted/15 p-3">
          <div className="font-mono text-xs font-semibold text-foreground">{k}</div>
          <DynamicJsonNode data={obj} depth={depth + 1} />
        </div>
      ))}
    </div>
  )
}


function diffStatusVariant(status?: string): "success" | "warning" | "danger" | "outline" {
  const variant = statusBadgeVariant(status || "")
  return variant === "primary" ? "success" : variant || "outline"
}

function DiffCheckBadge({ status, label }: { status?: string; label: string }) {
  return <Badge variant={diffStatusVariant(status)}>{label}</Badge>
}

function RunDiffSection({ diff }: { diff: MonitorRunDiff | null }) {
  if (diff === null) {
    return (
      <div className="rounded-md border border-dashed bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
        首次运行，无对比基线
      </div>
    )
  }
  const summary = summarizeRunDiff(diff)
  const metrics = diff.metric_deltas || []
  const added = diff.added_checks || []
  const resolved = diff.resolved_checks || []
  const changed = diff.changed_checks || []
  return (
    <div className="space-y-2 rounded-md border bg-muted/20 px-3 py-2">
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        <span className="font-semibold text-foreground">与上次对比</span>
        <span className="font-mono text-2xs text-muted-foreground">
          #{diff.previous_run_id}
          {diff.previous_at_ms ? ` · ${new Date(diff.previous_at_ms).toLocaleString("zh-CN", { hour12: false })}` : ""}
        </span>
        {diff.previous_status && <Badge variant={diffStatusVariant(diff.previous_status)}>上次 {diff.previous_status}</Badge>}
        {diff.status_changed && <DiffCheckBadge status={diff.previous_status} label="状态变化" />}
        {!diffHasChanges(diff) && <Badge variant="outline">与上次一致</Badge>}
      </div>

      {metrics.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {metrics.map((m) => {
            const direction = deltaDirection(m.delta)
            const tone = direction === "up" ? "danger" : direction === "down" ? "success" : "outline"
            const arrow = direction === "up" ? "▲" : direction === "down" ? "▼" : "■"
            const pct = formatDeltaPercent(m.delta_pct)
            return (
              <span
                key={m.key}
                className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 font-mono text-2xs"
                title={`${m.key}: ${formatMetricValue(m.previous, m.unit)} → ${formatMetricValue(m.current, m.unit)}`}
              >
                <Badge variant={tone}>{arrow}</Badge>
                <span className="text-muted-foreground">{m.label || m.key}</span>
                <span className="tabular-nums text-foreground">
                  {formatMetricNumber(m.previous)} → {formatMetricValue(m.current, m.unit)}
                </span>
                <span className="tabular-nums text-muted-foreground">
                  （{formatSignedNumber(m.delta)}{pct ? ` / ${pct}` : " / 新增"}）
                </span>
              </span>
            )
          })}
        </div>
      )}

      {(added.length > 0 || resolved.length > 0 || changed.length > 0) && (
        <div className="space-y-1.5">
          {added.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <Badge variant="warning">新增 {added.length}</Badge>
              {added.map((c, idx) => (
                <Badge key={`a-${c.id || c.name || idx}`} variant={diffStatusVariant(c.status)}>
                  {c.name || c.id || "未命名检查项"}
                </Badge>
              ))}
            </div>
          )}
          {resolved.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <Badge variant="success">已恢复 {resolved.length}</Badge>
              {resolved.map((c, idx) => (
                <Badge key={`r-${c.id || c.name || idx}`} variant="outline">
                  {c.name || c.id || "未命名检查项"}
                </Badge>
              ))}
            </div>
          )}
          {changed.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <Badge variant="danger">状态变化 {changed.length}</Badge>
              {changed.map((c, idx) => (
                <span key={`c-${c.id || c.name || idx}`} className="inline-flex items-center gap-1 text-2xs text-muted-foreground">
                  <span className="text-foreground">{c.name || c.id || "未命名检查项"}</span>
                  <Badge variant={diffStatusVariant(c.previous_status)}>{c.previous_status}</Badge>
                  <span>→</span>
                  <Badge variant={diffStatusVariant(c.current_status)}>{c.current_status}</Badge>
                </span>
              ))}
            </div>
          )}
        </div>
      )}
      {summary.improved + summary.degraded > 0 && metrics.length === 0 && (
        <p className="text-2xs text-muted-foreground">
          指标上升 {summary.degraded} 项、下降 {summary.improved} 项
        </p>
      )}
    </div>
  )
}

type TrendState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; data: MonitorMetricSeriesResponse }

function TrendSection({
  session,
  skill,
  cluster,
  instance,
}: {
  session: Session
  skill: string
  cluster: string
  instance: string
}) {
  const [range, setRange] = useState<MonitorMetricRange>("24h")
  const [state, setState] = useState<TrendState>({ status: "loading" })
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    const ctrl = new AbortController()
    setState({ status: "loading" })
    fetchMonitorMetricSeries(session, { skill, cluster, instance, range }, ctrl.signal)
      .then((data) => setState({ status: "ready", data }))
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        setState({ status: "error", message: err instanceof Error ? err.message : "指标加载失败" })
      })
    return () => ctrl.abort()
  }, [session, skill, cluster, instance, range, revision])

  const series = state.status === "ready" ? pickTrendSeries(state.data.series) : null
  const geometry = series ? buildTrendGeometry(series.points, 100, 36, 4) : null

  return (
    <div className="space-y-2 rounded-md border bg-muted/20 px-3 py-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <span className="font-semibold text-foreground">历史趋势</span>
          <span className="font-mono text-2xs text-muted-foreground">{skill}</span>
        </div>
        <div className="flex items-center gap-1.5">
          <Select
            scale="sm"
            className="w-28"
            value={range}
            onChange={(e) => setRange(e.target.value as MonitorMetricRange)}
            aria-label="趋势时间范围"
          >
            {MONITOR_METRIC_RANGES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </Select>
          <Button size="sm" variant="ghost" onClick={() => setRevision((n) => n + 1)}>
            重试
          </Button>
        </div>
      </div>

      {state.status === "loading" && <p className="text-2xs text-muted-foreground">正在读取指标时序…</p>}
      {state.status === "error" && (
        <p className="text-2xs text-destructive">趋势加载失败：{state.message}（可点「重试」）</p>
      )}
      {state.status === "ready" && !series && (
        <p className="text-2xs text-muted-foreground">暂无多点指标时序；同一 Skill 连续执行两次后自动出现趋势。</p>
      )}
      {state.status === "ready" && series && geometry && (
        <div className="flex flex-wrap items-center gap-3">
          <div className="min-w-40 flex-1">
            <svg viewBox="0 0 100 36" preserveAspectRatio="none" className="h-12 w-full text-primary" role="img" aria-label={`${series.label || series.key} 趋势`}>
              <path d={geometry.areaPath} fill="currentColor" opacity={0.12} />
              <path d={geometry.path} fill="none" stroke="currentColor" strokeWidth={1.2} vectorEffect="non-scaling-stroke" />
            </svg>
          </div>
          <div className="space-y-0.5 text-2xs text-muted-foreground">
            <div className="font-mono text-xs text-foreground">{series.label || series.key}</div>
            <div>
              最新 <span className="font-mono text-foreground">{formatMetricValue(series.points[series.points.length - 1]?.value, series.unit)}</span>
              <span className="ml-2">区间 {formatMetricValue(geometry.min, series.unit)} ~ {formatMetricValue(geometry.max, series.unit)}</span>
            </div>
            <div>{series.points.length} 个采样点</div>
          </div>
        </div>
      )}
    </div>
  )
}

const CHART_TONE_BAR: Record<InspectionChartTone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-destructive",
  neutral: "bg-primary",
}

const CHART_TONE_STROKE: Record<InspectionChartTone, string> = {
  success: "stroke-success",
  warning: "stroke-warning",
  danger: "stroke-destructive",
  neutral: "stroke-primary",
}

const CHART_TONE_RANK: Record<InspectionChartTone, number> = { neutral: 0, success: 1, warning: 2, danger: 3 }

function worstChartTone(points: { tone: InspectionChartTone }[]): InspectionChartTone {
  return points.reduce<InspectionChartTone>(
    (worst, point) => (CHART_TONE_RANK[point.tone] > CHART_TONE_RANK[worst] ? point.tone : worst),
    "neutral",
  )
}

function ChartOverMaxBadge({ overMax }: { overMax: boolean }) {
  return overMax ? <span className="ml-1 text-destructive">超额</span> : null
}

function BarChartBody({ chart }: { chart: InspectionBarChart }) {
  return (
    <div className="space-y-1.5">
      {chart.series.map((point, index) => (
        <div
          key={`${point.label}-${index}`}
          className="flex items-center gap-2"
          data-chart-label={point.label}
          data-chart-tone={point.tone}
        >
          <span className="w-24 shrink-0 truncate text-2xs text-muted-foreground" title={point.label}>
            {point.label || "—"}
          </span>
          <div className="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-muted">
            <div
              className={`h-full rounded-full ${CHART_TONE_BAR[point.tone]}`}
              style={{ width: `${point.ratio * 100}%` }}
              data-chart-ratio={point.ratio.toFixed(4)}
            />
          </div>
          <span className="w-24 shrink-0 text-right text-2xs tabular-nums text-foreground">
            {formatChartValue(point.value, chart.unit)}
            <ChartOverMaxBadge overMax={point.overMax} />
          </span>
        </div>
      ))}
    </div>
  )
}

function GaugeChartBody({ chart }: { chart: InspectionGaugeChart }) {
  return (
    <div className="flex flex-wrap gap-4">
      {chart.arcs.map((arc, index) => (
        <div
          key={`${arc.label}-${index}`}
          className="text-center"
          data-chart-label={arc.label}
          data-chart-tone={arc.tone}
        >
          <svg
            viewBox="0 0 100 56"
            className="h-16 w-28"
            role="img"
            aria-label={`${arc.label || "仪表"} ${formatChartValue(arc.value, chart.unit)}`}
          >
            <path
              d={gaugeArcPath(50, 50, 40, 180, 360)}
              className="fill-none stroke-border"
              strokeWidth={8}
              strokeLinecap="round"
            />
            {arc.endAngle > arc.startAngle && (
              <path
                d={gaugeArcPath(50, 50, 40, arc.startAngle, arc.endAngle)}
                className={`fill-none ${CHART_TONE_STROKE[arc.tone]}`}
                strokeWidth={8}
                strokeLinecap="round"
                data-chart-ratio={arc.ratio.toFixed(4)}
              />
            )}
          </svg>
          <div className="truncate text-2xs text-muted-foreground">{arc.label || "—"}</div>
          <div className="text-2xs font-medium tabular-nums text-foreground">
            {formatChartValue(arc.value, chart.unit)}
            <ChartOverMaxBadge overMax={arc.overMax} />
          </div>
        </div>
      ))}
    </div>
  )
}

function LineChartBody({ chart }: { chart: InspectionLineChart }) {
  return (
    <div data-chart-points={chart.points.length}>
      <svg
        viewBox="0 0 100 40"
        preserveAspectRatio="none"
        className="h-16 w-full"
        role="img"
        aria-label={chart.title || "趋势折线"}
      >
        <polyline
          points={mapChartPointsToViewBox(chart, 100, 40)}
          className={`fill-none ${CHART_TONE_STROKE[worstChartTone(chart.points)]}`}
          strokeWidth={1.5}
          strokeLinecap="round"
          strokeLinejoin="round"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <div className="mt-1 flex gap-1 text-2xs text-muted-foreground">
        {chart.ticks.map((tick, index) => (
          <span key={`${tick}-${index}`} className="min-w-0 flex-1 truncate text-center">
            {tick}
          </span>
        ))}
      </div>
    </div>
  )
}

function InspectionChartCard({ chart }: { chart: InspectionChart }) {
  return (
    <div
      className="rounded-md border bg-background px-3.5 py-2.5"
      data-chart-kind={chart.kind}
      data-chart-title={chart.title}
    >
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-x-2 gap-y-1">
        <span className="text-xs font-semibold text-foreground">{chart.title || "图表"}</span>
        <span className="text-2xs text-muted-foreground">
          {chart.unit ? `单位 ${chart.unit}` : "无量纲"}
          {chart.maxSource === "series" ? ` · 上限按最大值 ${formatChartValue(chart.max, chart.unit)}` : ""}
        </span>
      </div>
      {chart.kind === "bar" && <BarChartBody chart={chart} />}
      {chart.kind === "gauge" && <GaugeChartBody chart={chart} />}
      {chart.kind === "line" && <LineChartBody chart={chart} />}
    </div>
  )
}

/** SKILL_FIRST_INSPECTION_SPEC.md §4.4 第 3 条：charts[] 存在时按 type 选通用原语渲染。 */
function InspectionChartsSection({ charts }: { charts: InspectionChart[] }) {
  if (charts.length === 0) return null
  return (
    <div className="space-y-2" data-inspection-charts data-chart-count={charts.length}>
      {charts.map((chart, index) => (
        <InspectionChartCard key={`${chart.kind}-${chart.title}-${index}`} chart={chart} />
      ))}
    </div>
  )
}

export function InspectionArtifactViewer({
  format,
  markdown,
  jsonData,
  rawOutput,
  filePrefix = "artifact",
  diff,
  session,
  trendSkill,
  trendCluster,
  trendInstance,
  artifactState,
  artifactTruncated,
}: {
  format?: string
  markdown?: string
  jsonData?: unknown
  rawOutput?: string
  filePrefix?: string
  /** 跨次对比结果；显式传 null 表示首次运行（渲染空态），不传则不渲染该区块。 */
  diff?: MonitorRunDiff | null
  /** 指标时序取数用的会话；与 trendSkill/trendCluster/trendInstance 同时存在才渲染趋势。 */
  session?: Session
  trendSkill?: string
  trendCluster?: string
  trendInstance?: string
  /** SKILL_FIRST_INSPECTION_SPEC.md §3.2：missing = artifact_dir 指向的产物文件已读不到，正文区换成提示。 */
  artifactState?: "present" | "missing"
  /** 仅 present 下的 256 KiB 截断标记；截断提示由正文自带的 "... [truncated]" 承担。 */
  artifactTruncated?: boolean
}) {
  const hasJson = jsonData !== undefined && jsonData !== null
  const hasMarkdown = Boolean(markdown && markdown.trim())
  const showTrend = Boolean(session && trendSkill && trendCluster && trendInstance)
  const artifactDisplay = describeArtifactState(artifactState, artifactTruncated)
  const [view, setView] = useState<ArtifactViewMode>("visual")
  const [copied, setCopied] = useState(false)

  const prettyJson = useMemo(() => (hasJson ? JSON.stringify(jsonData, null, 2) : ""), [hasJson, jsonData])
  const visualProse = useMemo(
    () => (hasMarkdown && hasJson ? stripFencedJsonFromMarkdown(markdown || "") : markdown || ""),
    [hasMarkdown, hasJson, markdown],
  )
  // charts[] 归一化是纯计算，跟着 jsonData 走；空结果不渲染区块。
  const charts = useMemo(() => normalizeInspectionCharts(jsonData), [jsonData])

  if (!hasJson && !hasMarkdown && !rawOutput && diff === undefined && !showTrend && !artifactDisplay.missing) {
    return null
  }

  const formatLabel =
    format === "markdown+json"
      ? "Markdown + JSON"
      : format === "json"
        ? "JSON"
        : format === "markdown"
          ? "Markdown"
          : format === "text"
            ? "文本"
            : hasJson && hasMarkdown
              ? "Markdown + JSON"
              : hasJson
                ? "JSON"
                : "Markdown"

  async function handleCopy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // ignore clipboard denial in headless browsers
    }
  }

  return (
    <div className="space-y-2.5 rounded-lg border bg-background/50 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b pb-2">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs font-semibold text-foreground">巡检产物</span>
          {artifactDisplay.missing ? (
            <Badge variant="outline">正文不可用</Badge>
          ) : (
            <Badge variant="primary">{formatLabel}</Badge>
          )}
        </div>
        {!artifactDisplay.missing && (
        <Inline gap={1} className="flex-wrap">
          <Button
            size="sm"
            variant={view === "visual" ? "primary" : "ghost"}
            onClick={() => setView("visual")}
          >
            可视化
          </Button>
          {hasMarkdown && (
            <Button
              size="sm"
              variant={view === "markdown" ? "primary" : "ghost"}
              onClick={() => setView("markdown")}
            >
              Markdown
            </Button>
          )}
          {hasJson && (
            <Button
              size="sm"
              variant={view === "json" ? "primary" : "ghost"}
              onClick={() => setView("json")}
            >
              JSON
            </Button>
          )}
          {hasJson && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => downloadBlob(`${filePrefix}.json`, prettyJson, "application/json")}
            >
              下载 JSON
            </Button>
          )}
          {hasJson && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                const html = renderFormalInspectionReportHtml({
                  title: trendSkill || filePrefix || "巡检评估报告",
                  skillSlug: trendSkill,
                  targetLabel: trendCluster && trendInstance ? `${trendCluster}/${trendInstance}` : (filePrefix || "target"),
                  artifactJson: jsonData,
                })
                downloadBlob(`${filePrefix || "inspection"}-report.html`, html, "text/html;charset=utf-8")
              }}
            >
              导出正式报告
            </Button>
          )}
          {hasMarkdown && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => downloadBlob(`${filePrefix}.md`, markdown || "", "text/markdown;charset=utf-8")}
            >
              下载 MD
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            onClick={() =>
              void handleCopy(
                view === "json" && hasJson
                  ? prettyJson
                  : hasMarkdown
                    ? markdown || ""
                    : prettyJson || rawOutput || "",
              )
            }
          >
            {copied ? "已复制" : "复制"}
          </Button>
        </Inline>
        )}
      </div>

      {view === "visual" && (
        <div className="space-y-3">
          {diff !== undefined && <RunDiffSection diff={diff} />}
          {showTrend && session && trendSkill && trendCluster && trendInstance && (
            <TrendSection
              session={session}
              skill={trendSkill}
              cluster={trendCluster}
              instance={trendInstance}
            />
          )}
          {artifactDisplay.missing ? (
            <>
              <div
                role="status"
                data-artifact-state="missing"
                className="rounded-md border border-warning/40 bg-warning/5 px-3.5 py-2.5 text-xs leading-relaxed text-foreground"
              >
                {artifactDisplay.notice}
              </div>
              {hasJson && (
                <div className="space-y-1.5">
                  <p className="text-2xs text-muted-foreground">
                    以下为巡检记录中的指标与检查项（原始产物正文已不可用）
                  </p>
                  <DynamicJsonNode data={jsonData} />
                </div>
              )}
            </>
          ) : (
            <>
              <InspectionChartsSection charts={charts} />
              {visualProse && (
                <div className="rounded-md border bg-background px-3.5 py-2.5 text-xs">
                  <Markdown content={visualProse} variant="compact" />
                </div>
              )}
              {hasJson && <DynamicJsonNode data={jsonData} />}
              {!visualProse && !hasJson && rawOutput && (
                <pre className="max-h-72 overflow-auto rounded-md border bg-background p-2.5 font-mono text-2xs text-foreground">
                  {rawOutput}
                </pre>
              )}
            </>
          )}
        </div>
      )}

      {view === "markdown" && hasMarkdown && (
        <div className="rounded-md border bg-background px-3.5 py-2.5 text-xs">
          <Markdown content={markdown || ""} variant="compact" />
        </div>
      )}

      {view === "json" && hasJson && (
        <pre className="max-h-96 overflow-auto rounded-md border bg-background p-3 font-mono text-2xs leading-relaxed text-foreground">
          {prettyJson}
        </pre>
      )}

      {!artifactDisplay.missing && rawOutput && rawOutput.trim() !== (markdown || "").trim() && rawOutput.trim() !== prettyJson.trim() && (
        <details className="text-xs">
          <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
            查看原始命令 / 模型输出
          </summary>
          <pre className="mt-2 max-h-60 overflow-auto rounded-md border bg-background p-2.5 font-mono text-2xs text-foreground">
            {rawOutput}
          </pre>
        </details>
      )}
    </div>
  )
}
