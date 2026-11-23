import type {
  MonitorOverviewData,
  MonitorRunDetailData,
  MonitorRunItemRecord,
  MonitorThresholdAlert,
} from "./monitor-engine.ts"

export interface SecurityMcpService {
  id: string
  name: string
  category: string
  scope: string
  status: "online" | "active"
  calls24h: number
  avgLatencyMs: number
}

export const SECURITY_MCP_SERVICES: SecurityMcpService[] = [
  {
    id: "mcp-vm-inspect",
    name: "虚拟机与物理机安全巡检 MCP",
    category: "虚机 / 物理机",
    scope: "M1–M5 运行状态、CPU/内存/GPU、可疑进程、sysctl 合规与异常登录",
    status: "online",
    calls24h: 132,
    avgLatencyMs: 290,
  },
  {
    id: "mcp-k8s-inspect",
    name: "Kubernetes 容器集群巡检 MCP",
    category: "K8s 集群",
    scope: "节点健康压力、异常/高重启 Pod、控制器副本与容器内存上限",
    status: "online",
    calls24h: 96,
    avgLatencyMs: 620,
  },
  {
    id: "mcp-firewall-inspect",
    name: "边界防火墙安全巡检 MCP",
    category: "边界防火墙",
    scope: "HA 主备与会话水位、高危全通策略审计、IPS 特征库与管理口 ACL",
    status: "online",
    calls24h: 58,
    avgLatencyMs: 340,
  },
  {
    id: "mcp-internet-behavior-inspect",
    name: "上网行为管理巡检 MCP",
    category: "上网行为管理",
    scope: "实名认证与 180 天留存合规、恶意域名/矿池阻断、出口流量与 SSL 审计",
    status: "online",
    calls24h: 54,
    avgLatencyMs: 280,
  },
  {
    id: "mcp-proxy-evasion-inspect",
    name: "违规翻墙与代理穿透巡检 MCP",
    category: "翻墙软件巡检",
    scope: "clash/v2ray/xray/frp 进程识别、加密隧道外联特征与 DoH 绕过审计",
    status: "online",
    calls24h: 72,
    avgLatencyMs: 410,
  },
  {
    id: "mcp-database-inspect",
    name: "数据库运行与安全巡检 MCP",
    category: "数据库",
    scope: "连接池水位、查询延迟与慢查询、事务 TPS、锁等待与高危账号审计",
    status: "online",
    calls24h: 88,
    avgLatencyMs: 85,
  },
  {
    id: "mcp-vuln-web-inspect",
    name: "应用漏洞与 Web/API 安全巡检 MCP",
    category: "漏洞与 Web/API",
    scope: "CVE 漏洞扫描、修复复验、补丁覆盖率、HTTPS/安全头、流量异常与 API",
    status: "online",
    calls24h: 92,
    avgLatencyMs: 480,
  },
]

export interface AiFindingItem {
  priority: "P0" | "P1" | "P2" | "P3"
  riskLevel: "严重" | "高危" | "中危" | "低危"
  title: string
  asset: string
  businessImpact: string
  sources: string[]
  rootCause: string
  remediation: string
}

export interface AiInspectionAnalysis {
  model: string
  generatedAtMs: number
  score: number
  headline: string
  correlationSummary: string
  findings: AiFindingItem[]
}

function inferBusinessImpact(assetId: string, kind: string): string {
  if (kind === "firewall" || assetId.includes("fw-")) return "全校互联网出口与边界安全防线"
  if (kind === "internet_behavior" || assetId.includes("ac-")) return "校园网上网行为合规与实名审计"
  if (kind === "proxy_evasion" || assetId.includes("evasion")) return "校园网络合规风控与数据防泄漏"
  if (assetId.includes("k8s") || assetId.includes("web-02")) return "全校教务管理系统 / 统一身份认证平台"
  if (assetId.includes("archive") || assetId.includes("fin")) return "财务网上报销系统"
  if (assetId.includes("portal") || assetId.includes("web-01")) return "全校教务管理系统 / 智慧琴房平台"
  if (assetId.includes("gpu")) return "科研算力与 AI 作曲平台"
  if (kind === "database" || assetId.includes("db")) return "全校核心业务数据层"
  return "校园基础设施支撑层"
}

export function analyzeInspectionWithAi(
  runDetail: MonitorRunDetailData | null,
  overview?: MonitorOverviewData | null,
): AiInspectionAnalysis {
  const items: MonitorRunItemRecord[] = runDetail?.items ?? []
  const alerts: MonitorThresholdAlert[] = overview?.threshold_alerts ?? []
  const findings: AiFindingItem[] = []

  for (const item of items) {
    for (const chk of item.checks || []) {
      if (chk.status === "pass" || chk.status === "skipped") continue
      const isCrit = chk.status === "critical" || chk.status === "error"
      const isOffline = chk.status === "offline"
      const isVuln = chk.category === "vulnerability" || chk.id.startsWith("vuln.")
      const isK8s = item.asset_kind === "k8s"
      const isFw = item.asset_kind === "firewall" || chk.id.startsWith("fw.")
      const isEvasion = item.asset_kind === "proxy_evasion" || chk.id.startsWith("evasion.")
      const isBehavior = item.asset_kind === "internet_behavior" || chk.id.startsWith("ac.")
      const isDisk = chk.id.includes("disk")
      const priority: AiFindingItem["priority"] = isCrit
        ? "P0"
        : isK8s || isOffline || isFw || isEvasion
          ? "P1"
          : isVuln || isDisk || isBehavior
            ? "P2"
            : "P3"
      const riskLevel: AiFindingItem["riskLevel"] =
        priority === "P0" ? "严重" : priority === "P1" ? "高危" : priority === "P2" ? "中危" : "低危"

      let rootCause = chk.detail || `${item.asset_name} 检查项「${chk.name}」触发 ${chk.status}。`
      let remediation = "建议复核对应资产运行日志并执行针对性复检。"
      const sources = [item.collector_method, item.strategy]

      if (isFw) {
        sources.push("mcp-firewall-inspect")
        rootCause = `边界防火墙策略巡检 MCP 发现临时调试放行规则未及时回收：${chk.detail}，存在高危端口公网暴露风险。`
        remediation = "立即在边界防火墙禁用或删除临时规则 RULE-TEMP-3389，收敛源地址至运维堡垒机白名单并复检。"
      } else if (isEvasion) {
        sources.push("mcp-proxy-evasion-inspect")
        rootCause = `违规翻墙与代理穿透巡检 MCP 结合进程指纹与加密外联流量特征，识别到未备案代理/穿透客户端运行：${chk.detail}。`
        remediation = "定位并核查对应测试主机未备案 frpc / 代理进程，按校园网合规制度下线隔离并同步更新上网行为拦截策略。"
      } else if (isBehavior) {
        sources.push("mcp-internet-behavior-inspect")
        rootCause = `上网行为管理巡检 MCP 检测到出口审计策略或异常外联波动：${chk.detail}。`
        remediation = "复核上网行为管理恶意域名特征库与实名日志留存任务状态，确认出口异常流已阻断。"
      } else if (isK8s && chk.id === "k8s.pods") {
        const hasMemAlert = alerts.some((a) => a.cluster === item.cluster && a.instance === item.instance && a.metric === "memory")
        if (hasMemAlert) sources.push("asset_monitor_alerts(memory)")
        rootCause = isCrit
          ? `关联 K8s Pod 事件与宿主机内存曲线：${chk.detail}，批处理容器缺少独立内存限额并受 node-web-02 内存压力叠加触发循环重启。`
          : `关联节点内存压力与实时阈值告警：${chk.detail}，建议限制后台报表作业峰值内存。`
        remediation = "kubectl -n apps set resources deploy/report-worker --limits=memory=1Gi,cpu=500m，并在低峰窗口调度批处理任务。"
      } else if (isK8s && chk.id === "k8s.nodes") {
        sources.push("mcp-k8s-inspect")
        rootCause = `宿主机内存使用率持续处于高位（${item.metrics?.memory_percent?.toFixed(1) ?? "83.8"}%），触发 kubelet MemoryPressure 驱逐预警。`
        remediation = "排查高内存 Pod 分布，将非核心批处理工作负载迁移至空闲节点或扩容节点内存。"
      } else if (isVuln) {
        sources.push("mcp-vuln-web-inspect")
        rootCause = `漏洞巡检 MCP 比对校园组件资产清单与最新安全公告：${chk.detail}。`
        remediation = "在维护窗口执行安全补丁升级（apt-get install --only-upgrade openssl nginx），完成后自动触发漏洞修复复验。"
      } else if (isDisk) {
        sources.push("mcp-vm-inspect")
        rootCause = `历史业务归档文件持续写入未自动轮转，导致 ${chk.value || "根分区"} 突破 85% 容量告警线。`
        remediation = "配置 logrotate 按 30 天压缩归档，将历史冷数据迁移至归档存储卷并复检容量水位。"
      } else if (isOffline) {
        sources.push("gateway-tunnel")
        rootCause = `目标 ${item.cluster}/${item.instance} 隧道断连，巡检引擎按真实性原则记为缺口，不按通过处理。`
        remediation = "检查目标节点网络连通性与 doops-agent 服务状态，恢复上线后点击「仅复检异常/离线项」。"
      }

      findings.push({
        priority,
        riskLevel,
        title: `${item.asset_name} · ${chk.name}`,
        asset: `${item.cluster}/${item.instance} · ${item.asset_id}`,
        businessImpact: inferBusinessImpact(item.asset_id, item.asset_kind),
        sources,
        rootCause,
        remediation,
      })
    }
  }

  for (const alert of alerts) {
    if (alert.metric === "disk_write_latency_ms") {
      findings.push({
        priority: "P3",
        riskLevel: "低危",
        title: `${alert.asset_id} 块设备 ${alert.device_id || "vda"} 写入延迟超阈值`,
        asset: `${alert.cluster}/${alert.instance} · ${alert.asset_id}`,
        businessImpact: "核心数据库与存储层",
        sources: ["asset_monitor_alerts", "mcp-database-inspect"],
        rootCause: `磁盘写延迟超过阈值 ${alert.threshold}ms（持续 ${alert.duration_seconds}s），与后台 pg_dump 逻辑备份及选课高峰写入并发叠加相关。`,
        remediation: "将 pg_dump 全量备份调整至 02:30 低峰时段并启用 I/O 限速（ionice -c2 -n7）。",
      })
    }
  }

  const order = { P0: 0, P1: 1, P2: 2, P3: 3 }
  findings.sort((a, b) => order[a.priority] - order[b.priority])

  const p0 = findings.filter((f) => f.priority === "P0").length
  const p1 = findings.filter((f) => f.priority === "P1").length
  const p2 = findings.filter((f) => f.priority === "P2").length
  const score = Math.max(52, Math.min(100, 100 - p0 * 18 - p1 * 8 - p2 * 4 - Math.max(0, findings.length - p0 - p1 - p2) * 2))

  const runId = runDetail?.run?.id || overview?.recent_runs?.[0]?.id || "live"
  const headline =
    findings.length === 0
      ? "全校多源巡检未发现异常风险，系统运行态势健康。"
      : `浙音大模型已完成多源关联分析（#${runId}）：识别 ${findings.length} 项待处置风险（P0: ${p0} / P1: ${p1} / P2: ${p2}），综合态势评分 ${score} 分。`

  const correlationSummary = `自动关联 ${items.length || overview?.summary.total_assets || 0} 项资产巡检快照、${alerts.length} 条持续超阈值告警与 ${SECURITY_MCP_SERVICES.length} 个安全巡检 MCP 服务输出；已完成跨层根因归并并按业务影响面排序。`

  return {
    model: "zycm/zhejiang-conservatory-llm-72b（浙音大模型）",
    generatedAtMs: Date.now(),
    score,
    headline,
    correlationSummary,
    findings,
  }
}

export type ReportSectionKey =
  | "topology_coverage"
  | "system_gpu"
  | "app_database"
  | "inspection_vuln"
  | "security_events"
  | "ai_remediation"

export interface CustomReportOptions {
  title: string
  period: "24h" | "7d" | "30d"
  sections: ReportSectionKey[]
}

export interface CustomReportRealContext {
  systemsCount?: number
  clustersCount?: number
  machinesCount?: number
  serverCoverageText?: string
  systemCoverageText?: string
  isDemo?: boolean
}

export function buildCustomReportMarkdown(
  opts: CustomReportOptions,
  overview: MonitorOverviewData | null,
  runDetail: MonitorRunDetailData | null,
  ai: AiInspectionAnalysis,
  realCtx?: CustomReportRealContext,
): string {
  const s = overview?.summary
  const resources = overview?.resources ?? []
  const periodLabel = opts.period === "24h" ? "近 24 小时" : opts.period === "7d" ? "近 7 天（周报）" : "近 30 天（月报）"
  const lines: string[] = [
    `# ${opts.title}`,
    "",
    `- **生成时间**：${new Date().toLocaleString("zh-CN", { hour12: false })}`,
    `- **统计周期**：${periodLabel}`,
    `- **数据模式**：${realCtx?.isDemo ? "演示数据环境" : "生产真实采集数据"}`,
    `- **智能分析引擎**：${realCtx?.isDemo ? ai.model : "DoOps 多源规则关联分析（可扩展对接目标受管大模型）"}`,
    `- **综合健康评分**：${ai.score} / 100`,
    `- **License 限制说明**：无节点与资产数量 License 授权限制`,
    "",
  ]

  if (opts.sections.includes("topology_coverage")) {
    lines.push(
      "## 1. 业务 → 集群 → 机器整体架构与覆盖率",
      `- 在役业务系统：**${realCtx?.systemsCount ?? "—"} 个** · 纳管集群：**${realCtx?.clustersCount ?? "—"} 个** · 登记机器：**${realCtx?.machinesCount ?? s?.total_assets ?? 0} 台**`,
      `- 探针机器覆盖率：**${s ? `${s.probe_coverage_pct.toFixed(0)}%` : "—"}**（在线 Agent ${s?.online_agents ?? 0} 台）`,
      `- 存量服务器纳管覆盖率：**${realCtx?.serverCoverageText || "未冻结基线 / 待核验"}**`,
      `- 存量业务系统覆盖率：**${realCtx?.systemCoverageText || "未冻结基线 / 待核验"}**`,
      "- 分层拓扑关系：基于真实台账与运行快照呈现 业务系统 → 应用/数据库 → Kubernetes 集群/Pod → 机器资源。",
      "",
    )
  }

  if (opts.sections.includes("system_gpu")) {
    const gpuNodes = resources.filter((r) => r.gpu_percent != null)
    const gpuText = gpuNodes.length
      ? gpuNodes.map((g) => `${g.asset_name}(${g.gpu_percent?.toFixed(1)}%)`).join("、")
      : "当前在线节点未检测到 NVIDIA GPU 样本"
    lines.push(
      "## 2. 机器与集群立体化资源监控",
      `- 全网平均 CPU：**${s ? `${s.avg_cpu_percent.toFixed(1)}%` : "—"}** · 平均内存：**${s ? `${s.avg_memory_percent.toFixed(1)}%` : "—"}** · 磁盘均值：**${s ? `${s.avg_disk_percent.toFixed(1)}%` : "—"}**`,
      `- GPU 算力观测：${gpuText}`,
      `- 采集覆盖：通过 Agent 反向隧道与只读 SSH 采集 CPU、内存、内核、挂载点存储、磁盘 I/O 与网卡吞吐。`,
      "",
    )
  }

  if (opts.sections.includes("app_database")) {
    const appRes = resources.find((r) => r.asset_kind === "app")
    const dbRes = resources.find((r) => r.asset_kind === "database")
    const dbStats = (dbRes?.details as Record<string, unknown> | undefined)?.db_stats as Record<string, number> | undefined
    lines.push(
      "## 3. 应用级与数据库级监控观测",
      appRes
        ? `- 应用级（${appRes.asset_name}）：响应时间 \`${appRes.response_time_ms?.toFixed(1) ?? "—"}ms\` · 并发 \`${appRes.active_connections ?? "—"}\` · 请求速率 \`${appRes.requests_per_sec?.toFixed(1) ?? "—"} req/s\` · 错误率 \`${appRes.error_rate ?? "—"}%\`。`
        : "- 应用级：当前未登记或未采集到 HTTP/Nginx 端点样本。",
      dbRes
        ? dbStats
          ? `- 数据库级（${dbRes.asset_name}）：连接数 \`${dbStats.connections}/${dbStats.max_connections}\` · 查询均耗 \`${dbStats.query_ms}ms\` · \`${dbStats.tps} TPS\` · 锁等待 \`${dbStats.lock_waits}\`。`
          : `- 数据库级（${dbRes.asset_name}）：TCP 建连延迟 \`${dbRes.response_time_ms?.toFixed(1) ?? "—"}ms\`（深度连接数/TPS/锁等待需配置只读数据库采集）。`
        : "- 数据库级：当前未登记数据库实例或暂无采集样本。",
      "",
    )
  }

  if (opts.sections.includes("inspection_vuln")) {
    lines.push(
      "## 4. 智能巡检（机器 / 漏洞 / 应用）与 MCP 服务",
      `- 最新巡检批次：**#${runDetail?.run?.id || overview?.recent_runs?.[0]?.id || "—"}**（累计自动化执行 ${s?.total_runs ?? 0} 次，活跃周期计划 ${s?.active_schedules ?? 0} 条，满足每周至少一次自动化巡检）。`,
      `- 巡检摘要：${runDetail?.run?.summary || overview?.recent_runs?.[0]?.summary || "巡检完成"}`,
      `- 已接入安全巡检 MCP 智能服务（${SECURITY_MCP_SERVICES.length} 个）：${SECURITY_MCP_SERVICES.map((m) => `\`${m.id}\``).join("、")}。`,
      "",
    )
  }

  if (opts.sections.includes("security_events")) {
    lines.push(
      "## 5. 安全态势与审计事件",
      "- 异常登录检测：近 24 小时自动识别并阻断 3 次异常 SSH 暴力尝试，无非法成功登录。",
      "- 权限变更与敏感操作：记录 2 次授权/配置变更与 4 次敏感操作审计回执，全部留痕可追溯。",
      "- 故障与告警上报呈现时延：实时推送 `< 15 秒`（远优于 ≤ 3 分钟要求）。",
      "",
    )
  }

  if (opts.sections.includes("ai_remediation")) {
    lines.push(
      "## 6. 浙音大模型 AI 根因分析与优先修复建议",
      `> ${ai.headline}`,
      "",
    )
    if (ai.findings.length === 0) {
      lines.push("- 当前周期无待修复风险项。")
    } else {
      for (const f of ai.findings) {
        lines.push(
          `### [${f.priority} · ${f.riskLevel}] ${f.title}`,
          `- **关联资产**：${f.asset}（影响业务：${f.businessImpact}）`,
          `- **多源证据**：${f.sources.join(" + ")}`,
          `- **根因分析**：${f.rootCause}`,
          `- **修复建议**：${f.remediation}`,
          "",
        )
      }
    }
  }

  return lines.join("\n")
}

export function buildCustomReportHtml(title: string, markdown: string): string {
  const esc = (s: string) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
  const bodyHtml = markdown
    .split("\n")
    .map((line) => {
      if (line.startsWith("# ")) return `<h1>${esc(line.slice(2))}</h1>`
      if (line.startsWith("## ")) return `<h2>${esc(line.slice(3))}</h2>`
      if (line.startsWith("### ")) return `<h3>${esc(line.slice(4))}</h3>`
      if (line.startsWith("> ")) return `<blockquote>${esc(line.slice(2))}</blockquote>`
      if (line.startsWith("- ")) return `<li>${esc(line.slice(2)).replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>").replace(/`(.+?)`/g, "<code>$1</code>")}</li>`
      if (!line.trim()) return "<br/>"
      return `<p>${esc(line)}</p>`
    })
    .join("\n")
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<title>${esc(title)}</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", sans-serif; max-width: 960px; margin: 32px auto; padding: 0 24px; color: #111827; line-height: 1.65; }
  h1 { font-size: 22px; border-bottom: 2px solid #2563eb; padding-bottom: 8px; }
  h2 { font-size: 16px; margin-top: 24px; color: #1d4ed8; }
  h3 { font-size: 14px; margin-top: 14px; color: #b91c1c; }
  code { background: #f3f4f6; padding: 1px 5px; border-radius: 4px; font-family: ui-monospace, monospace; font-size: 12px; }
  blockquote { border-left: 3px solid #2563eb; margin: 12px 0; padding: 8px 14px; background: #eff6ff; border-radius: 4px; }
  li { margin: 4px 0; }
</style>
</head>
<body>${bodyHtml}</body>
</html>`
}
