// Shared pure models and helpers for Agent Monitor & Skill-First metric trends.

const LOWER_SNAKE_CODE = /^[a-z][a-z0-9_]{0,63}$/

function extractMonitorErrorCode(body?: { code?: unknown; error?: unknown } | null): string {
  if (typeof body?.code === "string" && LOWER_SNAKE_CODE.test(body.code.trim())) {
    return body.code.trim()
  }
  if (typeof body?.error === "string") {
    const raw = body.error.trim()
    if (LOWER_SNAKE_CODE.test(raw)) return raw
    if (raw.startsWith("{")) {
      try {
        const parsed = JSON.parse(raw) as { code?: unknown }
        if (typeof parsed?.code === "string" && LOWER_SNAKE_CODE.test(parsed.code.trim())) {
          return parsed.code.trim()
        }
      } catch {
        // Ignore malformed JSON string
      }
    }
  }
  return ""
}

/**
 * Maps `/api/assets/monitor` HTTP errors to accurate, operator-friendly Chinese
 * diagnostics without falsely blaming an online Agent when Gateway storage fails.
 */
export function formatAssetMonitorError(
  status: number,
  body?: { code?: unknown; error?: unknown; message?: unknown } | null,
): string {
  const code = extractMonitorErrorCode(body)
  const message = typeof body?.message === "string" ? body.message.trim() : ""
  const rawError = typeof body?.error === "string" ? body.error.trim() : ""

  if (status === 401) return "登录已失效，请重新登录。"
  if (status === 403) return "无权访问该监控来源。"
  if (status === 404) return "监控对象或序列不存在。"
  if (status === 422 && code === "monitor_window_too_dense") {
    return "当前时间窗口内采样点过多，请缩短时间范围后重试。"
  }
  if (status === 503) {
    if (code === "target_offline") {
      return "目标 Agent 离线，无法执行实时采集。"
    }
    if (code === "container_runtime_unavailable") {
      return "目标节点未检测到可用的容器运行时（Docker / containerd）。"
    }
    if (code === "asset_monitor_unavailable") {
      return "监控数据暂不可用，请检查 Gateway 监控存储或查看服务日志。"
    }
    if (message && message !== code) {
      return message
    }
    return code
      ? `监控服务暂不可用（${code}），请稍后重试或查看 Gateway 服务日志。`
      : "监控服务暂不可用，请稍后重试或查看 Gateway 服务日志。"
  }
  if (message && message !== code) return message
  if (rawError && !rawError.startsWith("{")) return rawError
  return code || `请求失败（HTTP ${status}）`
}

export const MONITOR_KINDS: Record<string, string> = {
  machine: "机器 / 虚机",
  container: "容器",
  os: "操作系统",
  kernel: "内核",
  process: "进程",
  app: "应用 / Pod",
  database: "数据库",
}

export const MONITOR_METRICS: Record<string, string> = {
  cpu: "CPU",
  memory: "内存",
  memory_limit: "内存上限",
  gpu: "GPU",
  network: "网络吞吐",
  requests: "请求速率",
  response_time_ms: "应用响应时间",
  active_connections: "并发连接数",
  error_rate: "应用错误率",
  storage_total: "存储总量",
  storage: "存储已用",
  storage_available: "存储可用",
  disk_read_iops: "磁盘读 IOPS",
  disk_write_iops: "磁盘写 IOPS",
  disk_read_bytes_per_second: "磁盘读吞吐",
  disk_write_bytes_per_second: "磁盘写吞吐",
  disk_read_latency_ms: "磁盘平均读延迟",
  disk_write_latency_ms: "磁盘平均写延迟",
  db_tps: "数据库事务处理 (TPS)",
  db_lock_waits: "数据库锁等待",
}

export const MONITOR_UNITS: Record<string, string> = {
  cpu: "cores",
  memory: "bytes",
  memory_limit: "bytes",
  gpu: "%",
  network: "bytes/s",
  requests: "req/s",
  response_time_ms: "ms",
  active_connections: "connections",
  error_rate: "%",
  storage_total: "bytes",
  storage: "bytes",
  storage_available: "bytes",
  disk_read_iops: "ops/s",
  disk_write_iops: "ops/s",
  disk_read_bytes_per_second: "bytes/s",
  disk_write_bytes_per_second: "bytes/s",
  disk_read_latency_ms: "ms",
  disk_write_latency_ms: "ms",
  db_tps: "tx/s",
  db_lock_waits: "waits",
}

export const MONITOR_STATES: Record<string, string> = {
  missing: "缺数据",
  not_applicable: "不适用",
  open: "异常",
  recovered: "恢复",
  interrupted: "采集中断",
  reconfigured: "规则已更新",
}

export type AgentMonitorRange = "1h" | "6h" | "24h" | "7d"
export type SkillTrendRange = AgentMonitorRange | "30d"

export const MONITOR_RANGE_MS: Record<AgentMonitorRange, number> = {
  "1h": 3600000,
  "6h": 21600000,
  "24h": 86400000,
  "7d": 604800000,
}

export const SKILL_TREND_RANGES: readonly SkillTrendRange[] = ["1h", "6h", "24h", "7d", "30d"]

export const CORE_TARGET_INSPECTION_SKILLS = [
  "inspect-vm-host",
  "inspect-container-runtime",
  "inspect-app-web",
  "inspect-k8s-cluster",
] as const

export const SKILL_TREND_LABELS: Record<string, string> = {
  "inspect-vm-host": "虚机 / 物理机巡检",
  "inspect-container-runtime": "容器运行时巡检",
  "inspect-k8s-cluster": "K8s 集群巡检",
  "inspect-app-web": "应用 / Web 巡检",
  "inspect-database": "数据库巡检",
  "inspect-ssh-host": "SSH 主机巡检",
  "inspect-crypto-mining": "挖矿行为安全巡检",
  "inspect-package-vuln": "软件包与漏洞基线巡检",
  "inspect-proxy-evasion": "代理与穿透检测巡检",
}

export const SKILL_CATEGORY_LABELS: Record<string, string> = {
  inspection: "基础巡检",
  security: "安全巡检",
  ops: "运维巡检",
}

export interface TrendSkillOption {
  slug: string
  label: string
  category: string
}

export interface MonitorAssetRef {
  type: string
  asset_id: string
  namespace: string
  origin: string
}

export function monitorAssetKey(a: MonitorAssetRef): string {
  return JSON.stringify([a.type, a.asset_id, a.namespace, a.origin])
}

export function monitorSourceName(origin: string): string {
  switch (origin) {
    case "k8s_top":
      return "Agent · K8s Pod / 节点"
    case "agent_host":
      return "Agent · 虚拟机/主机"
    case "container_stats":
      return "Agent · 容器运行时"
    case "app_probe":
      return "Agent · 应用探测"
    default:
      return "可信 exporter"
  }
}

export function humanBytesHint(unit: string, value: number | null): string {
  if (value === null || !Number.isFinite(value) || value < 1024) return ""
  const normalized = unit === "B" ? "bytes" : unit === "B/s" ? "bytes/s" : unit
  const isRate = normalized === "bytes/s"
  if (normalized !== "bytes" && !isRate) return ""
  const suffixes = ["B", "KiB", "MiB", "GiB", "TiB"]
  let v = value
  let idx = 0
  while (v >= 1024 && idx < suffixes.length - 1) {
    v /= 1024
    idx++
  }
  return `${v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)} ${suffixes[idx]}${isRate ? "/s" : ""}`
}

export function visibleMetricsForAsset(
  asset: Pick<MonitorAssetRef, "type" | "origin">,
  presentMetrics: Iterable<string>,
): string[] {
  const present = new Set(presentMetrics)
  if (asset.type === "container") {
    const base = ["cpu", "memory", "memory_limit"]
    for (const m of Object.keys(MONITOR_METRICS)) if (present.has(m) && !base.includes(m)) base.push(m)
    return base
  }
  if (asset.type === "kernel" || asset.type === "os" || asset.type === "process") {
    const base = ["cpu", "memory"]
    for (const m of Object.keys(MONITOR_METRICS)) if (present.has(m) && !base.includes(m)) base.push(m)
    return base
  }
  if (asset.type === "database") {
    const base = ["active_connections", "response_time_ms", "requests", "db_tps", "db_lock_waits"]
    const list = base.filter((m) => present.has(m))
    return list.length ? list : ["active_connections", "response_time_ms", "requests"]
  }
  if (asset.type === "app" && asset.origin === "app_probe") {
    const base = ["response_time_ms", "error_rate", "active_connections", "requests"]
    for (const m of Object.keys(MONITOR_METRICS)) if (present.has(m) && !base.includes(m)) base.push(m)
    return base
  }
  if (asset.type === "app" && asset.origin === "k8s_top") {
    const base = ["cpu", "memory"]
    for (const m of Object.keys(MONITOR_METRICS)) if (present.has(m) && !base.includes(m)) base.push(m)
    return base
  }
  return Object.keys(MONITOR_METRICS).filter((m) => {
    if (
      (m.startsWith("disk_") ||
        m.startsWith("db_") ||
        m === "memory_limit" ||
        m === "response_time_ms" ||
        m === "active_connections" ||
        m === "error_rate") &&
      !present.has(m)
    ) {
      return false
    }
    return true
  })
}

export function monitorDeviceLabel(metric: string, id?: string): string {
  if (metric.startsWith("disk_")) return id ? `块设备 ${id}` : ""
  if (metric === "network") return id ? `网卡 ${id}` : ""
  if (metric === "storage" || metric === "storage_total" || metric === "storage_available") {
    return id ? `挂载点 ${id}` : ""
  }
  return id ? `设备 ${id}` : ""
}

/**
 * Resolves the explicit Skill-First skill slugs for one-click inspection on a
 * target. Prevents sending an empty `skills` array to `POST /v1/monitor/runs`
 * (which Skill-First rejects as an empty run after retiring legacy strategies).
 */
export function resolveTargetInspectionSkills(
  target: { kubernetes_available?: boolean },
  availableSkills?: ReadonlyArray<{ slug: string; enabled?: boolean }>,
): string[] {
  const candidates = CORE_TARGET_INSPECTION_SKILLS.filter(
    (slug) => slug !== "inspect-k8s-cluster" || target.kubernetes_available !== false,
  )
  if (!availableSkills || availableSkills.length === 0) {
    return [...candidates]
  }
  const enabled = new Set(
    availableSkills
      .filter((s) => s.enabled !== false)
      .map((s) => s.slug.trim().toLowerCase()),
  )
  return candidates.filter((slug) => enabled.has(slug))
}

/**
 * Resolves explicit Skill-First skill slugs for fleet-wide one-click collection
 * across online targets in Resource Monitoring -> 全网资源总览.
 */
export function resolveFleetInspectionSkills(
  targets: ReadonlyArray<{ kubernetes_available?: boolean }>,
  availableSkills?: ReadonlyArray<{ slug: string; enabled?: boolean }>,
): string[] {
  const anyK8s = targets.length === 0 || targets.some((t) => t.kubernetes_available !== false)
  return resolveTargetInspectionSkills({ kubernetes_available: anyK8s }, availableSkills)
}

export interface MonitorAssetFocus {
  type?: string
  assetId?: string
  namespace?: string
  origin?: string
  metric?: string
}

/**
 * Finds the best matching asset in an AgentMonitor catalog for a drill-down
 * focus (e.g., clicking a threshold alert row or resource drawer).
 */
export function matchMonitorAssetFocus(
  assets: ReadonlyArray<MonitorAssetRef>,
  focus?: MonitorAssetFocus | null,
): MonitorAssetRef | null {
  if (!focus || assets.length === 0) return null
  const exact = assets.find(
    (a) =>
      (!focus.type || a.type === focus.type) &&
      (!focus.assetId || a.asset_id === focus.assetId) &&
      (focus.namespace === undefined || a.namespace === focus.namespace) &&
      (!focus.origin || a.origin === focus.origin),
  )
  if (exact) return exact
  if (focus.assetId) {
    const byId = assets.find((a) => a.asset_id === focus.assetId && (!focus.type || a.type === focus.type))
    if (byId) return byId
  }
  if (focus.type) {
    const byType = assets.find((a) => a.type === focus.type)
    if (byType) return byType
  }
  return null
}

/**
 * Resolves all enabled Skills selectable in Agent Monitor → Skill 巡检趋势.
 * Places core target monitoring Skills first (respecting target capabilities),
 * followed by remaining enabled Skills across all categories (including
 * `security` such as `inspect-crypto-mining` and custom Skills).
 */
export function resolveTrendSkillOptions(
  target: { kubernetes_available?: boolean },
  availableSkills?: ReadonlyArray<{ slug: string; name?: string; category?: string; enabled?: boolean }>,
): TrendSkillOption[] {
  const core = resolveTargetInspectionSkills(target, availableSkills)
  const bySlug = new Map<string, { slug: string; name?: string; category?: string; enabled?: boolean }>()
  for (const s of availableSkills ?? []) {
    const slug = s.slug.trim().toLowerCase()
    if (slug) bySlug.set(slug, s)
  }
  const extras: string[] = []
  for (const s of availableSkills ?? []) {
    const slug = s.slug.trim().toLowerCase()
    if (!slug || s.enabled === false) continue
    if (slug === "inspect-k8s-cluster" && target.kubernetes_available === false) continue
    if (!core.includes(slug) && !extras.includes(slug)) {
      extras.push(slug)
    }
  }
  return [...core, ...extras].map((slug) => {
    const rec = bySlug.get(slug)
    return {
      slug,
      label: rec?.name?.trim() || SKILL_TREND_LABELS[slug] || slug,
      category: rec?.category?.trim() || "inspection",
    }
  })
}

export interface SparklinePointInput {
  at_ms: number
  value: number | null
  status?: string
}

export interface SparklineGeometry {
  polylines: string[]
  dots: Array<{ x: number; y: number }>
  hasData: boolean
}

/**
 * Computes SVG polyline segments and isolated dots inside a 400x80 viewBox,
 * splitting segments whenever a sample is non-ok or the gap exceeds maxGapMs.
 * Shared by both `/api/assets/monitor` and `/api/monitor/metrics` curves.
 */
export function buildSparklineGeometry(
  points: readonly SparklinePointInput[],
  fromMs: number,
  toMs: number,
  maxGapMs?: number,
): SparklineGeometry {
  const validValues = points
    .filter((p) => (p.status === undefined || p.status === "ok") && p.value !== null && Number.isFinite(p.value))
    .map((p) => p.value as number)
  if (validValues.length === 0) {
    return { polylines: [], dots: [], hasData: false }
  }
  const lo = Math.min(...validValues)
  const hi = Math.max(...validValues)
  const span = Math.max(1, toMs - fromMs)
  const x = (at: number) => 4 + ((at - fromMs) / span) * 392
  const y = (v: number) => (hi === lo ? 40 : 72 - ((v - lo) / (hi - lo)) * 64)

  const segments: Array<Array<{ x: number; y: number }>> = []
  let current: Array<{ x: number; y: number }> = []
  let prevAt = -1
  for (const p of points) {
    const ok = (p.status === undefined || p.status === "ok") && p.value !== null && Number.isFinite(p.value)
    if (!ok) {
      if (current.length) segments.push(current)
      current = []
      prevAt = -1
      continue
    }
    if (prevAt >= 0 && maxGapMs && maxGapMs > 0 && p.at_ms - prevAt > maxGapMs) {
      if (current.length) segments.push(current)
      current = []
    }
    current.push({ x: x(p.at_ms), y: y(p.value as number) })
    prevAt = p.at_ms
  }
  if (current.length) segments.push(current)

  const polylines = segments
    .filter((seg) => seg.length > 1)
    .map((seg) => seg.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" "))
  const dots = segments.filter((seg) => seg.length === 1).map((seg) => seg[0])
  return { polylines, dots, hasData: true }
}
