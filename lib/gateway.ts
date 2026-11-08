// 共享的 gateway 协议类型与工具函数

export interface Target {
  cluster: string
  instance: string
  key: string
  remote?: string
  connected_at?: string
  last_seen?: string
  busy?: boolean
  status?: string
  active_ops?: number
  queued_ops?: number
  busy_reason?: string
  resources?: string[]
  sessions?: string[]
}

export interface AuditEvent {
  id: number
  user_id?: string
  token_id?: string
  cluster?: string
  instance?: string
  credential_id?: string
  action?: string
  session?: string
  command_summary?: string
  status?: string
  error?: string
  tail?: string
  bytes_in?: number
  bytes_out?: number
  started_at?: string
  ended_at?: string
}

export interface AuditSummary {
  total: number
  running: number
  success: number
  failed: number
  unknown: number
  bytes_in: number
  bytes_out: number
  status?: Record<string, number>
}

export type AuditStatusKind = "success" | "running" | "unknown" | "failed" | "other"

// outcome_unknown is not a confirmed failure: the mutation may still have run.
export function auditStatusKind(status?: string): AuditStatusKind {
  switch (status) {
    case "success":
    case "ok":
    case "completed":
      return "success"
    case "running":
    case "executing":
      return "running"
    case "outcome_unknown":
      return "unknown"
    case "error":
    case "failed":
    case "forbidden":
    case "blocked":
      return "failed"
    default:
      return "other"
  }
}

export function auditStatusLabel(status?: string): string {
  switch (auditStatusKind(status)) {
    case "success":
      return "成功"
    case "running":
      return "进行中"
    case "unknown":
      return "结果未知"
    case "failed":
      if (status === "forbidden") return "拒绝"
      if (status === "blocked") return "阻断"
      return "失败"
    default:
      if (status === "canceled") return "已取消"
      if (status === "offline") return "离线"
      if (status === "busy") return "忙碌"
      if (status === "rate_limited") return "限流"
      return status || "未知"
  }
}

export type AuditScope = "target" | "session" | "self" | "global"

export const AUDIT_ACTIONS = [
  "exec",
  "ask",
  "read",
  "write",
  "push",
  "pull",
  "info",
  "check",
  "clean",
  "proxy",
  "agent:upgrade",
  "targets:list",
  "credential:create",
  "credential:metadata",
  "credential:grant",
  "credential:use",
  "credential:verify",
  "credential:rotate",
  "credential:revoke",
  "credential:audit",
  "admin",
  "edge:admin",
  "edge:view",
] as const

export const AUDIT_STATUSES = [
  "running",
  "executing",
  "outcome_unknown",
  "success",
  "error",
  "forbidden",
  "blocked",
  "canceled",
  "offline",
  "busy",
  "rate_limited",
] as const

// gateway 目标动作工具名
export const TOOLS = {
  shell: "doops_shell",
  prompt: "doops_agent_prompt",
  fileRead: "doops_file_read",
  fileWrite: "doops_file_write",
  workspaceRead: "doops_workspace_read",
  nodeInfo: "doops_node_info",
  k8s: "doops_k8s",
  databaseInventory: "doops_database_inventory",
  checkDeployment: "doops_check_deployment",
  cleanWorkspace: "doops_clean_workspace",
} as const

// 把 http(s):// 规整成 ws(s):// 基地址，去掉尾部斜杠
export function toWsBase(gateway: string): string {
  const trimmed = gateway.trim().replace(/\/+$/, "")
  if (trimmed.startsWith("https://")) return "wss://" + trimmed.slice("https://".length)
  if (trimmed.startsWith("http://")) return "ws://" + trimmed.slice("http://".length)
  if (trimmed.startsWith("wss://") || trimmed.startsWith("ws://")) return trimmed
  return "ws://" + trimmed
}

// 规整 http 基地址
export function toHttpBase(gateway: string): string {
  const trimmed = gateway.trim().replace(/\/+$/, "")
  if (trimmed.startsWith("ws://")) return "http://" + trimmed.slice("ws://".length)
  if (trimmed.startsWith("wss://")) return "https://" + trimmed.slice("wss://".length)
  if (trimmed.startsWith("http://") || trimmed.startsWith("https://")) return trimmed
  return "http://" + trimmed
}

export function resolveDefaultGateway(savedGateway: string | null, currentOrigin: string): string {
  const saved = savedGateway?.trim()
  if (!saved) return currentOrigin

  try {
    const current = new URL(currentOrigin)
    const candidate = new URL(saved.includes("://") ? saved : `http://${saved}`)
    if (
      current.protocol === "https:" &&
      candidate.protocol !== "https:" &&
      candidate.protocol !== "wss:"
    ) {
      return current.origin
    }
  } catch {
    return currentOrigin
  }

  return saved
}

// 生成一个随机 session id
export function randomSession(prefix = "console"): string {
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`
}
