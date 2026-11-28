import type { Session } from "./client.ts"
import { consoleFetch } from "./network-error.ts"
import type { MonitorTargetRef } from "./monitor-engine.ts"

export interface MCPToolMeta {
  name: string
  description?: string
  input_schema?: Record<string, unknown>
}

export interface PlatformMCPServerRecord {
  slug: string
  name: string
  description: string
  scope: "system" | "custom"
  transport: "stdio" | "http" | "sse"
  command?: string
  args?: string[]
  url?: string
  env_keys?: string[]
  header_keys?: string[]
  enabled: boolean
  auto_start: boolean
  tool_timeout_secs: number
  last_probe_status?: string
  last_probe_error?: string
  last_probe_at_ms?: number
  last_latency_ms?: number
  tools: MCPToolMeta[]
  owner_id: string
  owner_name: string
  created_at_ms: number
  updated_at_ms: number
  can_edit: boolean
  can_delete: boolean
}

export interface MCPInspectServerResult {
  name: string
  builtin: boolean
  transport: string
  command?: string
  args?: string[]
  url?: string
  auto_start: boolean
  tool_timeout_secs?: number
  status: "ok" | "error" | "configured" | string
  error?: string
  latency_ms?: number
  server_info?: { name?: string; version?: string }
  tools: MCPToolMeta[]
  env_keys?: string[]
  header_keys?: string[]
}

export interface MCPSyncTargetResult {
  cluster: string
  instance: string
  status: "ok" | "offline" | "error"
  synced_count: number
  total_servers: number
  probe_results?: MCPInspectServerResult[]
  error?: string
}

async function mcpRequest<T>(
  session: Session,
  path: string,
  body?: Record<string, unknown>,
  signal?: AbortSignal,
): Promise<T> {
  if (session.demo) {
    throw new Error("MCP 管理需要连接真实 Gateway 会话")
  }
  const url = !path ? "/api/mcp-servers" : path.startsWith("?") ? `/api/mcp-servers${path}` : `/api/mcp-servers/${path}`
  const res = await consoleFetch(url, {
    method: body ? "POST" : "GET",
    cache: "no-store",
    signal,
    ...(body ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {}),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(data.message || data.error || `MCP 接口返回 HTTP ${res.status}`)
  }
  return data as T
}

export function fetchPlatformMCPServers(
  session: Session,
  opts: { target?: string; probe?: boolean } = {},
  signal?: AbortSignal,
): Promise<{
  servers: PlatformMCPServerRecord[]
  agent_servers?: MCPInspectServerResult[]
  agent_error?: string
}> {
  const qs = new URLSearchParams()
  if (opts.target) qs.set("target", opts.target)
  if (opts.probe) qs.set("probe", "1")
  const suffix = qs.toString() ? `?${qs.toString()}` : ""
  return mcpRequest(session, suffix, undefined, signal)
}

export function upsertPlatformMCPServer(
  session: Session,
  payload: {
    slug: string
    name?: string
    description?: string
    transport: "stdio" | "http" | "sse"
    command?: string
    args?: string[]
    url?: string
    env?: Record<string, string>
    headers?: Record<string, string>
    enabled?: boolean
    auto_start?: boolean
    tool_timeout_secs?: number
  },
): Promise<{ server: PlatformMCPServerRecord }> {
  return mcpRequest(session, "", { action: "upsert", ...payload })
}

export function togglePlatformMCPServer(
  session: Session,
  slug: string,
  enabled: boolean,
): Promise<{ server: PlatformMCPServerRecord }> {
  return mcpRequest(session, "", { action: "toggle", slug, enabled })
}

export function deletePlatformMCPServer(session: Session, slug: string): Promise<{ deleted: string }> {
  return mcpRequest(session, "", { action: "delete", slug })
}

export function syncMCPServersToAgents(
  session: Session,
  targets?: MonitorTargetRef[],
): Promise<{ results: MCPSyncTargetResult[] }> {
  return mcpRequest(session, "", { action: "sync", ...(targets ? { targets } : {}) })
}

export function probeMCPServerOnTarget(
  session: Session,
  opts: { slug?: string; target?: MonitorTargetRef } = {},
): Promise<{
  target: MonitorTargetRef
  probe_results: MCPInspectServerResult[]
  servers: PlatformMCPServerRecord[]
}> {
  return mcpRequest(session, "", {
    action: "probe",
    ...(opts.slug ? { slug: opts.slug } : {}),
    ...(opts.target ? { target: opts.target } : {}),
  })
}
