"use client"

import { useMemo, useState } from "react"
import { Markdown } from "./markdown"
import {
  Badge,
  Button,
  Inline,
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

export function InspectionArtifactViewer({
  format,
  markdown,
  jsonData,
  rawOutput,
  filePrefix = "artifact",
}: {
  format?: string
  markdown?: string
  jsonData?: unknown
  rawOutput?: string
  filePrefix?: string
}) {
  const hasJson = jsonData !== undefined && jsonData !== null
  const hasMarkdown = Boolean(markdown && markdown.trim())
  const [view, setView] = useState<ArtifactViewMode>("visual")
  const [copied, setCopied] = useState(false)

  const prettyJson = useMemo(() => (hasJson ? JSON.stringify(jsonData, null, 2) : ""), [hasJson, jsonData])
  const visualProse = useMemo(
    () => (hasMarkdown && hasJson ? stripFencedJsonFromMarkdown(markdown || "") : markdown || ""),
    [hasMarkdown, hasJson, markdown],
  )

  if (!hasJson && !hasMarkdown && !rawOutput) {
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
          <Badge variant="primary">{formatLabel}</Badge>
        </div>
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
      </div>

      {view === "visual" && (
        <div className="space-y-3">
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

      {rawOutput && rawOutput.trim() !== (markdown || "").trim() && rawOutput.trim() !== prettyJson.trim() && (
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
