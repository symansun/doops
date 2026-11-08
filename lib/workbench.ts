import { callTool, extractText, type Session, type Target, type ToolCancel } from "./client"

export interface ToolText {
  output: string
  result: string
}

export async function runTargetTool(
  session: Session,
  target: Pick<Target, "cluster" | "instance">,
  sessionId: string,
  tool: string,
  args: Record<string, unknown>,
  options?: { signal?: AbortSignal; onCancelReady?: (cancel: ToolCancel) => void },
): Promise<ToolText> {
  let output = ""
  let result = ""
  try {
    await callTool(
      session,
      {
        cluster: target.cluster,
        instance: target.instance,
        tool,
        arguments: { session_id: sessionId, ...args },
        signal: options?.signal,
        onCancelReady: options?.onCancelReady,
      },
      (event) => {
        if (event.type === "output" && event.data) output += event.data
        if (event.type === "result") result += extractText(event.result)
      },
    )
  } catch (err) {
    // A failed tool streams its reason (kubectl stderr, the command's output)
    // before the failure; the failure text alone is only "exit status 1".
    if (isAbortError(err) || !output.trim()) throw err
    throw new Error(`${output.trim()}\n${err instanceof Error ? err.message : String(err)}`)
  }
  return { output, result }
}

const COMPLETION = new Set([
  "Operation complete.",
  "Kubernetes operation complete.",
  "exit code: 0",
])

export function toolBody(text: ToolText): string {
  const result = text.result.trim()
  if (result.startsWith("{") || result.startsWith("[")) return result
  if (text.output.trim()) return text.output
  if (COMPLETION.has(result)) return ""
  return text.result
}

export function parseJSONDocument(text: string): unknown | null {
  const trimmed = text.trim()
  const start = trimmed.search(/[[{]/)
  if (start < 0) return null
  try {
    return JSON.parse(trimmed.slice(start))
  } catch {
    return null
  }
}

export interface DataTable {
  columns: string[]
  rows: string[][]
  note?: string
}

export interface DatabaseAsset {
  database_asset_id: string
  cluster?: string
  instance?: string
  namespace: string
  service: string
  engine: string
  host?: string
  port?: number
  database_name?: string
  status: string
  source?: string
}

export interface DatabaseInventory {
  status?: string
  cluster?: string
  instance?: string
  candidates: DatabaseAsset[]
}

export function parseDatabaseInventory(text: string): DatabaseInventory | null {
  const parsed = parseJSONDocument(text)
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null
  const record = parsed as { candidates?: unknown; status?: string; cluster?: string; instance?: string }
  if (!Array.isArray(record.candidates)) return null
  const candidates: DatabaseAsset[] = []
  for (const item of record.candidates) {
    if (!item || typeof item !== "object") continue
    const row = item as Partial<DatabaseAsset>
    if (!row.database_asset_id || !row.namespace || !row.service || !row.engine) continue
    candidates.push({
      database_asset_id: row.database_asset_id,
      cluster: row.cluster,
      instance: row.instance,
      namespace: row.namespace,
      service: row.service,
      engine: row.engine,
      host: row.host,
      port: typeof row.port === "number" ? row.port : undefined,
      database_name: row.database_name,
      status: row.status || "candidate",
      source: row.source,
    })
  }
  return {
    status: record.status,
    cluster: record.cluster,
    instance: record.instance,
    candidates,
  }
}

export interface SqlTable {
  columns: string[]
  rows: string[][]
  truncated: boolean
  statement?: string
  engine?: string
  database?: string
  credential?: string
}

export function parseSqlReport(text: string): SqlTable | null {
  const parsed = parseJSONDocument(text)
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null
  const report = parsed as {
    engine?: string
    database?: string
    credential?: string
    result?: { statements?: Array<{ columns?: string[]; rows?: unknown[]; truncated?: boolean; statement?: string }> }
  }
  const statement = report.result?.statements?.[0]
  if (!statement) return null
  const columns = Array.isArray(statement.columns) ? statement.columns.map(String) : []
  const rows = Array.isArray(statement.rows)
    ? statement.rows.map((row) => (Array.isArray(row) ? row.map((cell) => (cell == null ? "" : String(cell))) : []))
    : []
  return {
    columns,
    rows,
    truncated: statement.truncated === true,
    statement: statement.statement,
    engine: report.engine,
    database: report.database,
    credential: report.credential,
  }
}

type K8sObject = {
  kind?: string
  metadata?: { name?: string; namespace?: string; labels?: Record<string, string> }
  spec?: { replicas?: number; containers?: unknown[]; nodeName?: string }
  status?: {
    phase?: string
    readyReplicas?: number
    availableReplicas?: number
    containerStatuses?: Array<{ ready?: boolean }>
    conditions?: Array<{ type?: string; status?: string }>
    addresses?: Array<{ type?: string; address?: string }>
    nodeInfo?: { kubeletVersion?: string }
  }
  specNodeName?: string
}

function asObjects(value: unknown): K8sObject[] | null {
  if (Array.isArray(value)) return value as K8sObject[]
  if (!value || typeof value !== "object") return null
  const record = value as { items?: unknown; kind?: string }
  if (Array.isArray(record.items)) return record.items as K8sObject[]
  if (record.kind) return [value as K8sObject]
  return null
}

function nodeRoles(labels: Record<string, string> | undefined): string {
  if (!labels) return ""
  const roles = Object.keys(labels)
    .filter((key) => key.startsWith("node-role.kubernetes.io/"))
    .map((key) => key.slice("node-role.kubernetes.io/".length))
  if (labels["kubernetes.io/role"]) roles.push(labels["kubernetes.io/role"])
  return roles.filter(Boolean).join(",") || "—"
}

export function helmReleaseTable(text: string): DataTable | null {
  const parsed = parseJSONDocument(text)
  if (!Array.isArray(parsed) || parsed.length === 0) return null
  const releases = parsed.filter((item): item is { name?: string; namespace?: string; revision?: number; status?: string; chart?: string } => {
    return !!item && typeof item === "object" && "name" in item && "chart" in item
  })
  if (releases.length !== parsed.length) return null
  return {
    columns: ["命名空间", "发布", "修订", "状态", "Chart"],
    rows: releases.map((item) => [
      item.namespace || "—",
      item.name || "—",
      item.revision == null ? "—" : String(item.revision),
      item.status || "—",
      item.chart || "—",
    ]),
  }
}

export function k8sTable(text: string): DataTable | null {
  const objects = asObjects(parseJSONDocument(text))
  if (!objects || objects.length === 0) return null
  const kind = (objects[0].kind || "").toLowerCase()
  if (kind === "node" || objects.some((item) => item.status?.nodeInfo || item.status?.addresses)) {
    return {
      columns: ["名称", "状态", "角色", "内网 IP", "Kubelet"],
      rows: objects.map((item) => {
        const ready = item.status?.conditions?.find((condition) => condition.type === "Ready")
        const ip = item.status?.addresses?.find((address) => address.type === "InternalIP")?.address || "—"
        return [
          item.metadata?.name || "—",
          ready?.status === "True" ? "Ready" : ready?.status || "Unknown",
          nodeRoles(item.metadata?.labels),
          ip,
          item.status?.nodeInfo?.kubeletVersion || "—",
        ]
      }),
    }
  }
  if (kind === "pod" || objects.some((item) => item.status?.phase || item.status?.containerStatuses)) {
    return {
      columns: ["命名空间", "名称", "状态", "就绪", "节点"],
      rows: objects.map((item) => {
        const statuses = item.status?.containerStatuses || []
        const ready = statuses.filter((status) => status.ready).length
        const total = statuses.length || item.spec?.containers?.length || 0
        const node = item.spec?.nodeName || "—"
        return [
          item.metadata?.namespace || "—",
          item.metadata?.name || "—",
          item.status?.phase || "—",
          total ? `${ready}/${total}` : "—",
          node,
        ]
      }),
    }
  }
  if (objects.some((item) => typeof item.status?.readyReplicas === "number" || typeof item.spec?.replicas === "number")) {
    return {
      columns: ["命名空间", "名称", "期望", "就绪", "可用"],
      rows: objects.map((item) => [
        item.metadata?.namespace || "—",
        item.metadata?.name || "—",
        item.spec?.replicas == null ? "—" : String(item.spec.replicas),
        item.status?.readyReplicas == null ? "—" : String(item.status.readyReplicas),
        item.status?.availableReplicas == null ? "—" : String(item.status.availableReplicas),
      ]),
    }
  }
  if (objects.every((item) => item.metadata?.name)) {
    return {
      columns: ["命名空间", "名称"],
      rows: objects.map((item) => [item.metadata?.namespace || "—", item.metadata?.name || "—"]),
    }
  }
  return null
}

export function hostSections(text: string): Array<{ title: string; body: string }> {
  const parts = text.split(/^---\s*(.+?)\s*---$/m)
  if (parts.length < 3) return text.trim() ? [{ title: "输出", body: text.trim() }] : []
  const sections: Array<{ title: string; body: string }> = []
  for (let index = 1; index < parts.length; index += 2) {
    const body = (parts[index + 1] || "").trim()
    if (body) sections.push({ title: parts[index].trim(), body })
  }
  return sections
}

export function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === "AbortError"
}

export { formatReadyList, labelSelectorString, parseAlignedColumns } from "./workbench-parse"
