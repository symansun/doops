import { callTool, extractText, type Session, type Target, type ToolCancel } from "./client.ts"
import { TOOLS } from "./gateway.ts"

export interface GovernanceOptions {
  session: Session
  target: Pick<Target, "cluster" | "instance">
  sessionId: string
  engine: string
  database: string
  credential: { id: string; name: string }
  signal?: AbortSignal
  onCancelReady?: (cancel: ToolCancel) => void
}

export interface ColumnIR {
  name: string
  type: string
  nullable: boolean
  default?: string
  extra?: string
  comment?: string
}

export interface IndexIR {
  name: string
  unique: boolean
  primary?: boolean
  columns: string[]
  type?: string
}

export interface ForeignKeyIR {
  name: string
  columns: string[]
  referenced_table: string
  referenced_columns: string[]
  on_update?: string
  on_delete?: string
}

export interface TableIR {
  name: string
  kind: string
  engine?: string
  charset?: string
  estimated_rows?: number
  comment?: string
  columns: ColumnIR[]
  indexes: IndexIR[]
  foreign_keys: ForeignKeyIR[]
}

export interface SchemaIR {
  engine: string
  database: string
  schema_hash: string
  table_count: number
  tables: TableIR[]
}

export interface SchemaInspectReport {
  mode: "inspect"
  credential: string
  schema: SchemaIR
}

export interface VersionSummary {
  version: number
  operation_id: string
  status: string
  committed: boolean
  implicit_commits: number
}

export interface VersionsReport {
  mode: "versions"
  engine: string
  database: string
  credential: string
  versions: VersionSummary[]
  snapshots: number[]
}

export interface SchemaChange {
  kind: string
  table: string
  object?: string
  before?: string
  after?: string
  destructive?: boolean
  ddl?: string
}

export interface SchemaDiff {
  engine: string
  from_database: string
  to_database: string
  from_hash: string
  to_hash: string
  identical: boolean
  has_destructive?: boolean
  changes: SchemaChange[]
  suggested_ddl: string[]
  possible_renames: string[]
}

export interface SchemaDiffReport {
  mode: "diff"
  from: string
  to: string
  credential: string
  diff: SchemaDiff
}

export interface StatementRecord {
  seq: number
  timestamp: string
  session_id?: string
  operation_id: string
  version?: number
  engine: string
  database: string
  credential?: string
  release_tag?: string
  mode: string
  kind: string
  segment_index?: number
  atomic?: boolean
  statement: string
  stmt_hash: string
  affected_rows?: number
  duration_ms: number
  status: string
  error?: string
}

export interface HistoryReport {
  mode: "history"
  engine: string
  database: string
  credential: string
  records: StatementRecord[]
}

export interface MigrationRevision {
  version: number
  description: string
  type: string
  checksum: string
  release_tag?: string
  applied: number
  total: number
  status: string
  error?: string
  applied_at: string
  duration_ms: number
}

export interface MigrationStatusReport {
  mode: "status"
  engine: string
  database: string
  credential: string
  status: {
    current_version: number
    applied: MigrationRevision[]
    pending: Array<{ version: number; version_tag: string; file_name: string; description: string }>
    dirty?: MigrationRevision | null
  }
}

export interface PlannedSegment {
  index: number
  kind: string
  atomic: boolean
  statements: Array<{ index?: number; kind?: string; text?: string }>
}

export interface PolicyFinding {
  rule_id: string
  severity: "off" | "warn" | "error" | string
  statement_index: number
  message: string
  overridden?: boolean
}

export interface LintReport {
  mode: "lint"
  engine: string
  database: string
  statements: number
  segments: PlannedSegment[]
  policy: {
    passed: boolean
    errors: number
    warnings: number
    findings: PolicyFinding[]
    overrides?: string[]
  }
}

const DIFF_REF_PATTERN = /^(?:live|latest|v[1-9]\d*)$/

export function supportsSchemaInspect(engine: string): boolean {
  return engine === "mysql" || engine === "postgres"
}

export function isValidDiffRef(ref: string): boolean {
  return DIFF_REF_PATTERN.test(ref.trim().toLowerCase())
}

async function callGovernanceAction(
  opts: GovernanceOptions,
  action: "inspect" | "versions" | "diff" | "history" | "status" | "lint",
  extraArgs: Record<string, unknown>,
  instruction: string,
): Promise<Record<string, unknown>> {
  if (!opts.database.trim() || !opts.credential.id.trim() || !opts.credential.name.trim()) {
    throw new Error("数据库治理操作需要选定数据库与凭据")
  }
  let text = ""
  await callTool(
    opts.session,
    {
      cluster: opts.target.cluster,
      instance: opts.target.instance,
      tool: TOOLS.prompt,
      signal: opts.signal,
      onCancelReady: opts.onCancelReady,
      arguments: {
        session_id: opts.sessionId,
        operation: "ask",
        skill: "dosql",
        credential_mode: "auto",
        sql_credential: opts.credential.id,
        sql_database: opts.database,
        sql_action: action,
        instruction,
        ...extraArgs,
      },
    },
    (event) => {
      if (event.type === "result") text = extractText(event.result)
    },
  )
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error("DoSql 治理接口返回了无效 JSON")
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("DoSql 治理接口返回格式无效")
  }
  const obj = parsed as Record<string, unknown>
  if (obj.mode !== action) {
    throw new Error(`DoSql 治理回执模式不匹配（预期 ${action}，实际 ${String(obj.mode ?? "")}）`)
  }
  if (typeof obj.credential === "string" && obj.credential !== opts.credential.name) {
    throw new Error("DoSql 治理回执凭据与所选凭据不匹配")
  }
  return obj
}

export async function inspectDatabaseSchema(opts: GovernanceOptions & { tables?: string[] }): Promise<SchemaInspectReport> {
  if (!supportsSchemaInspect(opts.engine)) {
    throw new Error(`引擎 ${opts.engine} 暂不支持 Schema 结构采集（目前支持 MySQL 与 PostgreSQL）`)
  }
  const obj = await callGovernanceAction(
    opts,
    "inspect",
    opts.tables?.length ? { sql_tables: opts.tables } : {},
    "DoSql 只读 Schema 结构采集",
  )
  const schema = obj.schema as Partial<SchemaIR> | undefined
  if (!schema || typeof schema !== "object" || typeof schema.schema_hash !== "string" || !schema.schema_hash.trim() || schema.database !== opts.database) {
    throw new Error("Schema 采集回执缺少校验哈希或与目标库不匹配")
  }
  const tables: TableIR[] = Array.isArray(schema.tables)
    ? schema.tables.map((t) => ({
        name: String(t.name ?? ""),
        kind: String(t.kind ?? "TABLE"),
        engine: t.engine,
        charset: t.charset,
        estimated_rows: typeof t.estimated_rows === "number" ? t.estimated_rows : undefined,
        comment: t.comment,
        columns: Array.isArray(t.columns) ? t.columns : [],
        indexes: Array.isArray(t.indexes) ? t.indexes : [],
        foreign_keys: Array.isArray(t.foreign_keys) ? t.foreign_keys : [],
      }))
    : []
  return {
    mode: "inspect",
    credential: String(obj.credential ?? opts.credential.name),
    schema: {
      engine: String(schema.engine ?? opts.engine),
      database: schema.database,
      schema_hash: schema.schema_hash,
      table_count: typeof schema.table_count === "number" ? schema.table_count : tables.length,
      tables,
    },
  }
}

function normalizeVersionRow(raw: Record<string, unknown>): VersionSummary {
  const version = Number(raw.version ?? raw.Version ?? 0)
  const implicit = Number(raw.implicit_commits ?? raw.ImplicitCommits ?? 0)
  return {
    version: Number.isFinite(version) ? version : 0,
    operation_id: String(raw.operation_id ?? raw.OperationID ?? ""),
    status: String(raw.status ?? raw.Status ?? "unknown"),
    committed: Boolean(raw.committed ?? raw.Committed ?? false),
    implicit_commits: Number.isFinite(implicit) ? implicit : 0,
  }
}

export async function listDatabaseVersions(opts: GovernanceOptions): Promise<VersionsReport> {
  const obj = await callGovernanceAction(opts, "versions", {}, "DoSql 只读版本日志查询")
  if (typeof obj.database === "string" && obj.database !== opts.database) {
    throw new Error("版本日志回执与所选数据库不匹配")
  }
  const rawVersions = Array.isArray(obj.versions) ? obj.versions : []
  const rawSnapshots = Array.isArray(obj.snapshots) ? obj.snapshots : []
  const snapshots = rawSnapshots
    .map((v) => Number(v))
    .filter((v) => Number.isSafeInteger(v) && v > 0)
    .sort((a, b) => a - b)
  return {
    mode: "versions",
    engine: String(obj.engine ?? opts.engine),
    database: String(obj.database ?? opts.database),
    credential: String(obj.credential ?? opts.credential.name),
    versions: rawVersions
      .filter((row): row is Record<string, unknown> => !!row && typeof row === "object")
      .map(normalizeVersionRow)
      .sort((a, b) => b.version - a.version),
    snapshots,
  }
}

export async function diffDatabaseSchema(opts: GovernanceOptions & { from: string; to: string; tables?: string[] }): Promise<SchemaDiffReport> {
  if (!supportsSchemaInspect(opts.engine)) {
    throw new Error(`引擎 ${opts.engine} 暂不支持结构漂移对比`)
  }
  const from = opts.from.trim().toLowerCase()
  const to = opts.to.trim().toLowerCase()
  if (!isValidDiffRef(from) || !isValidDiffRef(to)) {
    throw new Error("对比版本引用无效，请使用 live、latest 或 v<版本号>")
  }
  const obj = await callGovernanceAction(
    opts,
    "diff",
    { sql_diff_from: from, sql_diff_to: to, ...(opts.tables?.length ? { sql_tables: opts.tables } : {}) },
    `DoSql 只读结构漂移对比 ${from} -> ${to}`,
  )
  const rawDiff = obj.diff as Partial<SchemaDiff> | undefined
  if (!rawDiff || typeof rawDiff !== "object" || typeof rawDiff.identical !== "boolean") {
    throw new Error("结构漂移回执格式无效")
  }
  return {
    mode: "diff",
    from: String(obj.from ?? from),
    to: String(obj.to ?? to),
    credential: String(obj.credential ?? opts.credential.name),
    diff: {
      engine: String(rawDiff.engine ?? opts.engine),
      from_database: String(rawDiff.from_database ?? opts.database),
      to_database: String(rawDiff.to_database ?? opts.database),
      from_hash: String(rawDiff.from_hash ?? ""),
      to_hash: String(rawDiff.to_hash ?? ""),
      identical: rawDiff.identical,
      has_destructive: Boolean(rawDiff.has_destructive),
      changes: Array.isArray(rawDiff.changes) ? rawDiff.changes : [],
      suggested_ddl: Array.isArray(rawDiff.suggested_ddl) ? rawDiff.suggested_ddl : [],
      possible_renames: Array.isArray(rawDiff.possible_renames) ? rawDiff.possible_renames : [],
    },
  }
}

export async function listDatabaseHistory(opts: GovernanceOptions & { allSessions?: boolean }): Promise<HistoryReport> {
  const allSessions = opts.allSessions ?? true
  const obj = await callGovernanceAction(
    opts,
    "history",
    { sql_all_sessions: allSessions },
    "DoSql 只读 SQL 变更流水查询",
  )
  if (typeof obj.database === "string" && obj.database !== opts.database) {
    throw new Error("变更流水回执与所选数据库不匹配")
  }
  const records = Array.isArray(obj.records) ? (obj.records as StatementRecord[]) : []
  return {
    mode: "history",
    engine: String(obj.engine ?? opts.engine),
    database: String(obj.database ?? opts.database),
    credential: String(obj.credential ?? opts.credential.name),
    records,
  }
}

export async function getMigrationStatus(opts: GovernanceOptions): Promise<MigrationStatusReport> {
  const obj = await callGovernanceAction(opts, "status", {}, "DoSql 只读迁移状态查询")
  if (typeof obj.database === "string" && obj.database !== opts.database) {
    throw new Error("迁移状态回执与所选数据库不匹配")
  }
  const rawStatus = obj.status as Partial<MigrationStatusReport["status"]> | undefined
  if (!rawStatus || typeof rawStatus !== "object" || typeof rawStatus.current_version !== "number") {
    throw new Error("迁移状态回执格式无效")
  }
  return {
    mode: "status",
    engine: String(obj.engine ?? opts.engine),
    database: String(obj.database ?? opts.database),
    credential: String(obj.credential ?? opts.credential.name),
    status: {
      current_version: rawStatus.current_version,
      applied: Array.isArray(rawStatus.applied) ? rawStatus.applied : [],
      pending: Array.isArray(rawStatus.pending) ? rawStatus.pending : [],
      dirty: rawStatus.dirty ?? null,
    },
  }
}

export async function lintDatabaseSql(opts: GovernanceOptions & { sql: string }): Promise<LintReport> {
  if (!opts.sql.trim()) {
    throw new Error("请先输入待检查的 SQL 语句")
  }
  const obj = await callGovernanceAction(opts, "lint", { sql: opts.sql }, "DoSql 只读 SQL 门禁预检")
  const rawPolicy = obj.policy as Partial<LintReport["policy"]> | undefined
  if (!rawPolicy || typeof rawPolicy !== "object" || typeof rawPolicy.passed !== "boolean") {
    throw new Error("SQL 门禁预检回执格式无效")
  }
  return {
    mode: "lint",
    engine: String(obj.engine ?? opts.engine),
    database: String(obj.database ?? opts.database),
    statements: typeof obj.statements === "number" ? obj.statements : 0,
    segments: Array.isArray(obj.segments) ? (obj.segments as PlannedSegment[]) : [],
    policy: {
      passed: rawPolicy.passed,
      errors: typeof rawPolicy.errors === "number" ? rawPolicy.errors : 0,
      warnings: typeof rawPolicy.warnings === "number" ? rawPolicy.warnings : 0,
      findings: Array.isArray(rawPolicy.findings) ? rawPolicy.findings : [],
      overrides: Array.isArray(rawPolicy.overrides) ? rawPolicy.overrides : undefined,
    },
  }
}

export function summarizeSchema(schema: SchemaIR): {
  baseTableCount: number
  viewCount: number
  columnCount: number
  indexCount: number
  foreignKeyCount: number
} {
  let baseTableCount = 0
  let viewCount = 0
  let columnCount = 0
  let indexCount = 0
  let foreignKeyCount = 0
  for (const table of schema.tables) {
    if (table.kind.toUpperCase() === "VIEW") viewCount++
    else baseTableCount++
    columnCount += table.columns.length
    indexCount += table.indexes.length
    foreignKeyCount += table.foreign_keys.length
  }
  return { baseTableCount, viewCount, columnCount, indexCount, foreignKeyCount }
}

export function buildDiffRefOptions(snapshots: number[]): Array<{ value: string; label: string }> {
  const sortedDesc = [...new Set(snapshots)].filter((n) => Number.isSafeInteger(n) && n > 0).sort((a, b) => b - a)
  const options: Array<{ value: string; label: string }> = [{ value: "live", label: "live（当前线上库）" }]
  if (sortedDesc.length > 0) {
    options.push({ value: "latest", label: `latest（最新快照 v${sortedDesc[0]}）` })
    for (const ver of sortedDesc) {
      options.push({ value: `v${ver}`, label: `v${ver} 快照` })
    }
  }
  return options
}

export function filterHistoryRecords(
  records: StatementRecord[],
  filter: { mode?: string; status?: string; query?: string },
): StatementRecord[] {
  const mode = (filter.mode ?? "").trim().toLowerCase()
  const status = (filter.status ?? "").trim().toLowerCase()
  const q = (filter.query ?? "").trim().toLowerCase()
  return [...records]
    .sort((a, b) => (b.seq - a.seq) || b.timestamp.localeCompare(a.timestamp))
    .filter((rec) => {
      if (mode && rec.mode.toLowerCase() !== mode) return false
      if (status && rec.status.toLowerCase() !== status) return false
      if (q) {
        const hay = [rec.statement, rec.operation_id, rec.release_tag ?? "", rec.error ?? "", rec.kind, rec.session_id ?? ""].join(" ").toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
}

export function describeDiffChangeKind(kind: string): { label: string; tone: "success" | "warning" | "danger" | "default" } {
  switch (kind) {
    case "add_table": return { label: "新增表", tone: "success" }
    case "drop_table": return { label: "删除表", tone: "danger" }
    case "add_column": return { label: "新增列", tone: "success" }
    case "drop_column": return { label: "删除列", tone: "danger" }
    case "modify_column": return { label: "修改列", tone: "warning" }
    case "add_index": return { label: "新增索引", tone: "success" }
    case "drop_index": return { label: "删除索引", tone: "warning" }
    case "add_foreign_key": return { label: "新增外键", tone: "success" }
    case "drop_foreign_key": return { label: "删除外键", tone: "warning" }
    default: return { label: kind || "变更", tone: "default" }
  }
}

export function describeSegmentKind(kind: string): { label: string; variant: "primary" | "warning" | "default" } {
  switch (kind) {
    case "transaction": return { label: "事务段", variant: "primary" }
    case "implicit_commit": return { label: "隐式提交 DDL", variant: "warning" }
    case "non_transactional": return { label: "非事务段", variant: "warning" }
    case "session": return { label: "会话设置", variant: "default" }
    default: return { label: kind || "未知段", variant: "default" }
  }
}

export function describeGovernanceError(message: string): string {
  if (message.includes("dosql_action_unsupported")) {
    return `当前目标 Agent 版本暂不支持该治理能力，请先升级 Agent（原始错误：${message}）`
  }
  if (message.includes("dosql_diff_source_unavailable")) {
    return `未找到所选基线快照，请先执行一次数据库变更以生成 SchemaIR 快照（${message}）`
  }
  if (message.includes("dosql_database_not_bound")) {
    return `当前凭据未绑定该数据库，请切换对应数据库的凭据（${message}）`
  }
  return message
}
