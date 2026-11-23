import type {
  MonitorOverviewData,
  MonitorRunDetailData,
  MonitorRunItemRecord,
  MonitorThresholdAlert,
} from "./monitor-engine.ts"

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

/**
 * Summarizes real non-passing inspection checks and open threshold alerts from
 * the Gateway without fabricating metrics, business names, or model output.
 */
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
      const isK8s = item.asset_kind === "k8s"
      const isDisk = chk.id.includes("disk")
      const priority: AiFindingItem["priority"] = isCrit
        ? "P0"
        : isOffline || isK8s
          ? "P1"
          : isDisk
            ? "P2"
            : "P3"
      const riskLevel: AiFindingItem["riskLevel"] =
        priority === "P0" ? "严重" : priority === "P1" ? "高危" : priority === "P2" ? "中危" : "低危"

      const rootCause =
        chk.detail ||
        `${item.asset_name || item.asset_id} 检查项「${chk.name}」返回 ${chk.status}${chk.value ? `（${chk.value}）` : ""}。`
      let remediation = "查看该次巡检产物与目标实时状态，确认并处理异常后执行复检。"
      if (isOffline) {
        remediation = "检查目标节点网络连通性与 doops-agent 运行状态，上线后重新执行巡检。"
      } else if (isK8s) {
        remediation = "核查目标是否运行 Kubernetes 集群或具备可用 kubeconfig；非集群主机无需启用 K8s 巡检 Skill。"
      }

      findings.push({
        priority,
        riskLevel,
        title: `${item.asset_name || item.asset_id} · ${chk.name}`,
        asset: `${item.cluster}/${item.instance} · ${item.asset_id}`,
        businessImpact: `${item.cluster}/${item.instance} (${item.asset_kind})`,
        sources: [item.collector_method, item.strategy].filter(Boolean),
        rootCause,
        remediation,
      })
    }
  }

  for (const alert of alerts) {
    findings.push({
      priority: "P2",
      riskLevel: "中危",
      title: `${alert.asset_id} 指标 ${alert.metric}${alert.device_id ? ` (${alert.device_id})` : ""} 超出阈值`,
      asset: `${alert.cluster}/${alert.instance} · ${alert.asset_id}`,
      businessImpact: `${alert.cluster}/${alert.instance}`,
      sources: ["asset_monitor_alerts"],
      rootCause: `实时指标 ${alert.metric} 超出阈值 ${alert.threshold}（已持续 ${alert.duration_seconds}s）。`,
      remediation: "前往「机器与集群资源」或目标终端排查资源占用进程并确认负载恢复。",
    })
  }

  const order = { P0: 0, P1: 1, P2: 2, P3: 3 }
  findings.sort((a, b) => order[a.priority] - order[b.priority])

  const p0 = findings.filter((f) => f.priority === "P0").length
  const p1 = findings.filter((f) => f.priority === "P1").length
  const p2 = findings.filter((f) => f.priority === "P2").length
  const score = Math.max(
    0,
    Math.min(100, 100 - p0 * 18 - p1 * 8 - p2 * 4 - Math.max(0, findings.length - p0 - p1 - p2) * 2),
  )

  const runId = runDetail?.run?.id || overview?.recent_runs?.[0]?.id || "live"
  const headline =
    findings.length === 0
      ? `最近巡检（#${runId}）未检测到异常项。`
      : `巡检汇总（#${runId}）：发现 ${findings.length} 项异常或告警（P0: ${p0} / P1: ${p1} / P2: ${p2}）。`

  const correlationSummary = `基于 Gateway 返回的 ${items.length || overview?.summary.total_assets || 0} 条真实巡检记录与 ${alerts.length} 条实时阈值告警自动归并。`

  return {
    model: "真实巡检规则汇总",
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
}

export function buildCustomReportMarkdown(
  opts: CustomReportOptions,
  overview: MonitorOverviewData | null,
  runDetail: MonitorRunDetailData | null,
  analysis: AiInspectionAnalysis,
  realCtx?: CustomReportRealContext,
): string {
  const s = overview?.summary
  const resources = overview?.resources ?? []
  const alerts = overview?.threshold_alerts ?? []
  const periodLabel = opts.period === "24h" ? "近 24 小时" : opts.period === "7d" ? "近 7 天" : "近 30 天"
  const lines: string[] = [
    `# ${opts.title}`,
    "",
    `- **生成时间**：${new Date().toLocaleString("zh-CN", { hour12: false })}`,
    `- **统计周期**：${periodLabel}`,
    `- **数据来源**：DoOps Gateway 真实采集与巡检产物`,
    `- **健康评分**：${analysis.score} / 100`,
    "",
  ]

  if (opts.sections.includes("topology_coverage")) {
    lines.push(
      "## 1. 业务 → 集群 → 机器架构与覆盖率",
      `- 在役业务系统：**${realCtx?.systemsCount ?? 0} 个** · 纳管集群：**${realCtx?.clustersCount ?? 0} 个** · 登记机器：**${realCtx?.machinesCount ?? s?.total_assets ?? 0} 台**`,
      `- 在线 Agent：**${s?.online_agents ?? 0} 台** · 已采样资产：**${s?.covered_assets ?? 0} 项**`,
      `- 存量服务器纳管覆盖率：**${realCtx?.serverCoverageText || "未配置基线"}**`,
      `- 存量业务系统覆盖率：**${realCtx?.systemCoverageText || "未配置基线"}**`,
      "",
    )
  }

  if (opts.sections.includes("system_gpu")) {
    const gpuNodes = resources.filter((r) => r.gpu_percent != null)
    const gpuText = gpuNodes.length
      ? gpuNodes.map((g) => `${g.asset_name}(${g.gpu_percent?.toFixed(1)}%)`).join("、")
      : "当前在线节点未检测到 GPU 样本"
    lines.push(
      "## 2. 机器与集群资源水位",
      `- 平均 CPU：**${s && resources.length > 0 ? `${s.avg_cpu_percent.toFixed(1)}%` : "—"}** · 平均内存：**${s && resources.length > 0 ? `${s.avg_memory_percent.toFixed(1)}%` : "—"}** · 平均磁盘：**${s && resources.length > 0 ? `${s.avg_disk_percent.toFixed(1)}%` : "—"}**`,
      `- GPU 观测：${gpuText}`,
      "",
    )
  }

  if (opts.sections.includes("app_database")) {
    const appRes = resources.find((r) => r.asset_kind === "app")
    const dbRes = resources.find((r) => r.asset_kind === "database")
    lines.push(
      "## 3. 应用与数据库观测",
      appRes
        ? `- 应用（${appRes.asset_name}）：响应时间 \`${appRes.response_time_ms?.toFixed(1) ?? "—"}ms\` · 错误率 \`${appRes.error_rate ?? "—"}%\`。`
        : "- 应用：当前暂无已采样的网站/应用端点。",
      dbRes
        ? `- 数据库（${dbRes.asset_name}）：TCP 建连延迟 \`${dbRes.response_time_ms?.toFixed(1) ?? "—"}ms\`。`
        : "- 数据库：当前暂无已采样的数据库实例。",
      "",
    )
  }

  if (opts.sections.includes("inspection_vuln")) {
    lines.push(
      "## 4. 巡检执行与产物概览",
      `- 最新巡检批次：**#${runDetail?.run?.id || overview?.recent_runs?.[0]?.id || "—"}**（累计执行 ${s?.total_runs ?? 0} 次，启用定时计划 ${s?.active_schedules ?? 0} 条）`,
      `- 执行摘要：${runDetail?.run?.summary || overview?.recent_runs?.[0]?.summary || "暂无执行记录"}`,
      "",
    )
  }

  if (opts.sections.includes("security_events")) {
    lines.push("## 5. 实时阈值告警记录")
    if (alerts.length === 0) {
      lines.push("- 当前周期内无未恢复的实时阈值告警。", "")
    } else {
      for (const a of alerts) {
        lines.push(`- \`${a.cluster}/${a.instance}\` · **${a.asset_id}** 指标 \`${a.metric}\` 超出阈值 ${a.threshold}（持续 ${a.duration_seconds}s）`)
      }
      lines.push("")
    }
  }

  if (opts.sections.includes("ai_remediation")) {
    lines.push("## 6. 异常项与处置建议", `> ${analysis.headline}`, "")
    if (analysis.findings.length === 0) {
      lines.push("- 当前无待处置异常项。")
    } else {
      for (const f of analysis.findings) {
        lines.push(
          `### [${f.priority} · ${f.riskLevel}] ${f.title}`,
          `- **目标资产**：${f.asset}`,
          `- **事实描述**：${f.rootCause}`,
          `- **建议动作**：${f.remediation}`,
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
