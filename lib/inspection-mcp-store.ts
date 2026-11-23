"use client"

export interface InspectionMcpService {
  id: string
  name: string
  category: string
  strategy: string
  description: string
  endpoint: string
  tools: string[]
  checks: string[]
  enabled: boolean
  builtin: boolean
  calls24h: number
  avgLatencyMs: number
  lastStatus: "pass" | "warning" | "critical" | "idle"
  lastRunAtMs?: number
}

const STORAGE_KEY = "doops.inspection.mcp.v1"

export const DEFAULT_INSPECTION_MCP_SERVICES: InspectionMcpService[] = [
  {
    id: "mcp-vm-inspect",
    name: "虚拟机与物理机安全巡检 MCP",
    category: "虚机 / 物理机",
    strategy: "vm_host",
    description: "覆盖虚拟机、双卡 A100 GPU 物理机与无 Agent SSH 主机的运行状态、资源水位、可疑进程、配置合规及异常登录检测。",
    endpoint: "mcp://agent-hub/vm-host-inspect",
    tools: ["mcp.vm.check_uptime_resource", "mcp.vm.scan_processes", "mcp.vm.audit_sysctl_auth"],
    checks: ["运行状态 (M1)", "CPU/内存/GPU/I/O (M2)", "可疑进程 (M3)", "配置合规 (M4)", "异常登录与提权 (M5)"],
    enabled: true,
    builtin: true,
    calls24h: 132,
    avgLatencyMs: 290,
    lastStatus: "pass",
  },
  {
    id: "mcp-k8s-inspect",
    name: "Kubernetes 容器集群巡检 MCP",
    category: "K8s 集群",
    strategy: "k8s_cluster",
    description: "巡检 K8s 节点健康与资源压力、异常/高重启 Pod、控制器副本一致性、容器内存限额与 Warning 安全事件。",
    endpoint: "mcp://agent-hub/k8s-cluster-inspect",
    tools: ["mcp.k8s.check_nodes", "mcp.k8s.scan_workloads", "mcp.k8s.check_container_limits"],
    checks: ["节点 Ready/压力", "异常 Pod 监测", "Deployment/StatefulSet 副本", "容器内存上限"],
    enabled: true,
    builtin: true,
    calls24h: 96,
    avgLatencyMs: 620,
    lastStatus: "warning",
  },
  {
    id: "mcp-firewall-inspect",
    name: "边界防火墙安全巡检 MCP",
    category: "边界防火墙",
    strategy: "firewall",
    description: "核查校园出口防火墙 HA 主备状态、并发会话水位、高危全通策略 (any->any/3389/22)、IPS/防病毒特征库版本及审计日志外发。",
    endpoint: "mcp://sec-gateway/firewall-inspect",
    tools: ["mcp.fw.check_ha_session", "mcp.fw.audit_highrisk_policies", "mcp.fw.check_ips_signatures"],
    checks: ["HA 主备与会话水位", "高危放行策略审计", "IPS/AV 特征库版本", "管理口 ACL 与日志外发"],
    enabled: true,
    builtin: true,
    calls24h: 58,
    avgLatencyMs: 340,
    lastStatus: "warning",
  },
  {
    id: "mcp-internet-behavior-inspect",
    name: "上网行为管理巡检 MCP",
    category: "上网行为管理",
    strategy: "internet_behavior",
    description: "巡检上网行为审计设备实名认证联动、日志留存周期合规（≥180天）、违规站点/矿池拦截策略、出口带宽与 SSL 审计引擎状态。",
    endpoint: "mcp://sec-gateway/internet-behavior-inspect",
    tools: ["mcp.ac.check_log_retention", "mcp.ac.audit_url_blocking", "mcp.ac.check_egress_traffic"],
    checks: ["实名认证与 180 天留存", "恶意域名/矿池阻断", "出口带宽与异常外联", "SSL 解密与审计引擎"],
    enabled: true,
    builtin: true,
    calls24h: 54,
    avgLatencyMs: 280,
    lastStatus: "pass",
  },
  {
    id: "mcp-proxy-evasion-inspect",
    name: "违规翻墙与代理穿透巡检 MCP",
    category: "翻墙软件巡检",
    strategy: "proxy_evasion",
    description: "识别主机与校园网终端违规代理/翻墙软件（clash/v2ray/xray/sing-box/shadowsocks）及未备案内网穿透隧道（frp/ngrok）、DoH 绕过行为。",
    endpoint: "mcp://sec-gateway/proxy-evasion-inspect",
    tools: ["mcp.evasion.scan_proxy_processes", "mcp.evasion.detect_encrypted_tunnels", "mcp.evasion.audit_doh_bypass"],
    checks: ["代理/翻墙进程特征", "未备案反向穿透隧道", "加密隧道外联特征", "DNS-over-HTTPS 绕过审计"],
    enabled: true,
    builtin: true,
    calls24h: 72,
    avgLatencyMs: 410,
    lastStatus: "warning",
  },
  {
    id: "mcp-database-inspect",
    name: "数据库运行与安全巡检 MCP",
    category: "数据库",
    strategy: "database_service",
    description: "持续检测 PostgreSQL / MySQL 连通性、连接池水位、查询延迟与慢查询、事务处理 TPS、锁等待及高危账号权限。",
    endpoint: "mcp://agent-hub/database-inspect",
    tools: ["mcp.db.check_connections_perf", "mcp.db.check_tps_locks", "mcp.db.audit_privileges"],
    checks: ["连接池水位 (A2)", "查询性能与慢查询", "事务处理 TPS", "锁等待与死锁", "账号与审计留存"],
    enabled: true,
    builtin: true,
    calls24h: 88,
    avgLatencyMs: 85,
    lastStatus: "pass",
  },
  {
    id: "mcp-vuln-web-inspect",
    name: "应用漏洞与 Web/API 安全巡检 MCP",
    category: "漏洞与 Web/API",
    strategy: "app_service",
    description: "覆盖应用 CVE 漏洞扫描、历史漏洞修复验证、补丁基线核查，以及 Web 可用性、流量异常、TLS/HSTS 安全头与核心 API 接口巡检。",
    endpoint: "mcp://agent-hub/vuln-web-inspect",
    tools: ["mcp.vuln.scan_cves_patches", "mcp.vuln.verify_fixes", "mcp.web.probe_tls_api_traffic"],
    checks: ["应用漏洞检测 (CVE)", "漏洞修复验证", "补丁管理与更新", "应用可用性与流量异常", "Web 安全与 API 接口"],
    enabled: true,
    builtin: true,
    calls24h: 92,
    avgLatencyMs: 480,
    lastStatus: "warning",
  },
]

let cachedServices: InspectionMcpService[] | null = null
const listeners = new Set<() => void>()

function emitChange() {
  for (const fn of listeners) fn()
}

function cloneDefaults(): InspectionMcpService[] {
  return DEFAULT_INSPECTION_MCP_SERVICES.map((s) => ({ ...s, tools: [...s.tools], checks: [...s.checks] }))
}

function saveToStorage(list: InspectionMcpService[]) {
  if (typeof window === "undefined") return
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(list))
  } catch {
    // ignore storage errors in private mode
  }
}

export function getInspectionMcpServices(): InspectionMcpService[] {
  if (cachedServices) return cachedServices
  if (typeof window !== "undefined") {
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY)
      if (raw) {
        const parsed = JSON.parse(raw) as InspectionMcpService[]
        if (Array.isArray(parsed) && parsed.length >= 5) {
          cachedServices = parsed
          return cachedServices
        }
      }
    } catch {
      // fall through to defaults
    }
  }
  cachedServices = cloneDefaults()
  return cachedServices
}

export function subscribeInspectionMcpServices(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function setInspectionMcpEnabled(id: string, enabled: boolean): InspectionMcpService[] {
  const next = getInspectionMcpServices().map((svc) =>
    svc.id === id ? { ...svc, enabled } : svc,
  )
  cachedServices = next
  saveToStorage(next)
  emitChange()
  return next
}

export function setAllInspectionMcpEnabled(enabled: boolean): InspectionMcpService[] {
  const next = getInspectionMcpServices().map((svc) => ({ ...svc, enabled }))
  cachedServices = next
  saveToStorage(next)
  emitChange()
  return next
}

export function registerCustomInspectionMcp(input: {
  id: string
  name: string
  category?: string
  endpoint?: string
  description?: string
  checks?: string[]
}): InspectionMcpService[] {
  const current = getInspectionMcpServices()
  const cleanId = (input.id ?? "").trim().toLowerCase().replace(/[^a-z0-9_-]/g, "-") || `mcp-custom-${Date.now()}`
  const existing = current.find((s) => s.id === cleanId)
  const checks = Array.isArray(input.checks) ? input.checks.map((c) => c.trim()).filter(Boolean) : []
  const item: InspectionMcpService = {
    id: cleanId,
    name: (input.name ?? "").trim(),
    category: (input.category ?? "").trim() || "自定义巡检",
    strategy: "custom_mcp",
    description: (input.description ?? "").trim() || "通过标准 MCP 协议接入智能体平台的自定义安全巡检服务。",
    endpoint: (input.endpoint ?? "").trim() || `mcp://custom-hub/${cleanId}`,
    tools: [`${cleanId.replace(/-/g, ".")}.run_inspect`],
    checks: checks.length ? checks : ["连通性核查", "基线配置检查", "异常事件审计"],
    enabled: true,
    builtin: false,
    calls24h: 1,
    avgLatencyMs: 220,
    lastStatus: "pass",
    lastRunAtMs: Date.now(),
  }
  const next = existing
    ? current.map((s) => (s.id === cleanId ? item : s))
    : [...current, item]
  cachedServices = next
  saveToStorage(next)
  emitChange()
  return next
}

export function removeCustomInspectionMcp(id: string): InspectionMcpService[] {
  const next = getInspectionMcpServices().filter((s) => s.builtin || s.id !== id)
  cachedServices = next
  saveToStorage(next)
  emitChange()
  return next
}

export function recordInspectionMcpRun(mcpIds: string[], statusMap?: Record<string, "pass" | "warning" | "critical">) {
  const now = Date.now()
  const idSet = new Set(mcpIds)
  const next = getInspectionMcpServices().map((svc) => {
    if (!idSet.has(svc.id)) return svc
    return {
      ...svc,
      calls24h: svc.calls24h + 1,
      lastRunAtMs: now,
      lastStatus: statusMap?.[svc.id] ?? svc.lastStatus,
    }
  })
  cachedServices = next
  saveToStorage(next)
  emitChange()
}

export function mapMcpServicesToStrategies(services: InspectionMcpService[]): string[] {
  const allowed = new Set(["vm_host", "ssh_host", "k8s_cluster", "container_runtime", "app_service", "database_service"])
  const out = new Set<string>()
  for (const svc of services) {
    if (!svc.enabled) continue
    if (allowed.has(svc.strategy)) out.add(svc.strategy)
    if (svc.id === "mcp-vm-inspect") out.add("ssh_host")
    if (svc.id === "mcp-k8s-inspect") out.add("container_runtime")
  }
  if (out.size === 0) out.add("vm_host")
  return [...out]
}
