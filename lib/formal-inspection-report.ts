import {
  computeInspectionScore,
  normalizeCoverageGaps,
  normalizeInspectionFindings,
  type FindingSeverity,
} from "./inspection-scoring.ts"

export interface FormalReportInput {
  title: string
  subtitle?: string
  skillSlug?: string
  targetLabel: string
  runId?: string
  collectedAt?: string
  status?: string
  summary?: string
  metrics?: Array<{ key: string; label?: string; value: number | string; unit?: string; status?: string }>
  checks?: Array<{ id?: string; name: string; status: string; value?: string; threshold?: string; detail?: string }>
  artifactJson?: unknown
  environment?: string
}

export function escapeHtml(raw: unknown): string {
  return String(raw ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;")
}

const SEV_LABELS: Record<FindingSeverity, string> = {
  critical: "严重",
  high: "高危",
  medium: "中危",
  low: "低危",
  info: "信息",
}

function statusPillClass(raw?: string): string {
  const s = String(raw ?? "").toLowerCase()
  if (["pass", "passed", "ok", "healthy", "success"].includes(s)) return "pill-ok"
  if (["warning", "warn", "degraded", "partial", "medium", "low"].includes(s)) return "pill-warn"
  if (["critical", "high", "error", "fail", "failed"].includes(s)) return "pill-crit"
  return "pill-muted"
}

function extractResources(artifactJson: unknown): Record<string, unknown>[] {
  if (!artifactJson || typeof artifactJson !== "object" || Array.isArray(artifactJson)) return []
  const raw = (artifactJson as Record<string, unknown>).resources
  if (!Array.isArray(raw)) return []
  return raw.filter((r): r is Record<string, unknown> => Boolean(r && typeof r === "object" && !Array.isArray(r)))
}

function extractMetrics(input: FormalReportInput): Array<{ key: string; label: string; value: string; unit: string; status: string }> {
  const src = input.metrics && input.metrics.length > 0
    ? input.metrics
    : (input.artifactJson && typeof input.artifactJson === "object" && !Array.isArray(input.artifactJson) && Array.isArray((input.artifactJson as Record<string, unknown>).metrics)
      ? ((input.artifactJson as Record<string, unknown>).metrics as Array<Record<string, unknown>>)
      : [])
  return src.slice(0, 12).map((m) => ({
    key: String(m.key ?? ""),
    label: String(m.label ?? m.key ?? ""),
    value: String(m.value ?? "—"),
    unit: String(m.unit ?? ""),
    status: String(m.status ?? "pass"),
  }))
}

export function renderFormalInspectionReportHtml(input: FormalReportInput): string {
  const scoreSummary = computeInspectionScore(input.artifactJson, { environment: input.environment })
  const findings = normalizeInspectionFindings(input.artifactJson)
  const gaps = normalizeCoverageGaps(input.artifactJson)
  const resources = extractResources(input.artifactJson)
  const metrics = extractMetrics(input)
  const checks = input.checks ?? []
  const generatedAt = input.collectedAt || new Date().toISOString()

  const gradeTone =
    scoreSummary.grade === "A" || scoreSummary.grade === "B"
      ? "score-ok"
      : scoreSummary.grade === "C"
        ? "score-warn"
        : "score-crit"

  const kpiHtml = metrics.length > 0
    ? `<section class="card"><h2>关键观测指标</h2><div class="kpi-grid">${metrics.map((m) => `
      <div class="kpi-box">
        <div class="kpi-label">${escapeHtml(m.label)}</div>
        <div class="kpi-value">${escapeHtml(m.value)}<span class="kpi-unit">${escapeHtml(m.unit)}</span></div>
      </div>`).join("")}</div></section>`
    : ""

  const deductionsHtml = scoreSummary.deductions.length > 0
    ? `<section class="card"><h2>根因归并扣分明细（封顶模型）</h2>
      <table><thead><tr><th>根因分类</th><th>级别</th><th>命中次数</th><th>扣分</th><th>封顶保护</th></tr></thead><tbody>
      ${scoreSummary.deductions.map((d) => `<tr>
        <td><strong>${escapeHtml(d.label)}</strong><div class="mono muted">${escapeHtml(d.root_cause)}</div></td>
        <td><span class="pill ${statusPillClass(d.severity)}">${escapeHtml(SEV_LABELS[d.severity])}</span></td>
        <td class="mono">${d.count}</td>
        <td class="mono">-${d.penalty}</td>
        <td>${d.capped ? "已封顶" : "未触发"}</td>
      </tr>`).join("")}</tbody></table></section>`
    : ""

  const findingsHtml = findings.length > 0
    ? `<section class="card"><h2>风险发现与整改优先级（共 ${findings.length} 项）</h2>
      <table><thead><tr><th>编号</th><th>优先级</th><th>级别</th><th>问题与资产</th><th>证据摘要</th><th>整改建议</th></tr></thead><tbody>
      ${findings.map((f) => `<tr>
        <td class="mono">${escapeHtml(f.id)}</td>
        <td class="mono">${escapeHtml(f.priority)}</td>
        <td><span class="pill ${statusPillClass(f.severity)}">${escapeHtml(SEV_LABELS[f.severity])}</span></td>
        <td><strong>${escapeHtml(f.title)}</strong><div class="mono muted">${escapeHtml(f.asset)}</div></td>
        <td class="mono small">${escapeHtml(f.evidence)}</td>
        <td>${escapeHtml(f.recommendation)}</td>
      </tr>`).join("")}</tbody></table></section>`
    : `<section class="card"><h2>风险发现</h2><p class="muted">本次巡检在已覆盖模块中未检出结构化风险发现。</p></section>`

  const gapsHtml = gaps.length > 0
    ? `<section class="card gap-card"><h2>覆盖缺口与证据边界（共 ${gaps.length} 项，不视作健康）</h2>
      <ul>${gaps.map((g) => `<li><strong>${escapeHtml(g.module)}</strong>：${escapeHtml(g.reason)} — <span class="muted">${escapeHtml(g.impact)}</span></li>`).join("")}</ul></section>`
    : ""

  let resourcesHtml = ""
  if (resources.length > 0) {
    const cols = Object.keys(resources[0]).slice(0, 7)
    resourcesHtml = `<section class="card"><h2>纳管资产与探测明细（共 ${resources.length} 项）</h2>
      <table><thead><tr>${cols.map((c) => `<th>${escapeHtml(c)}</th>`).join("")}</tr></thead><tbody>
      ${resources.slice(0, 60).map((row) => `<tr>${cols.map((c) => `<td class="mono small">${escapeHtml(row[c])}</td>`).join("")}</tr>`).join("")}
      </tbody></table></section>`
  }

  const checksHtml = checks.length > 0
    ? `<section class="card"><details open><summary><strong>技术检查明细附录（共 ${checks.length} 项）</strong></summary>
      <table class="mt"><thead><tr><th>状态</th><th>检查项</th><th>实测值</th><th>阈值</th><th>详情</th></tr></thead><tbody>
      ${checks.map((c) => `<tr>
        <td><span class="pill ${statusPillClass(c.status)}">${escapeHtml(c.status)}</span></td>
        <td>${escapeHtml(c.name)}</td>
        <td class="mono">${escapeHtml(c.value || "—")}</td>
        <td class="mono muted">${escapeHtml(c.threshold || "—")}</td>
        <td class="small">${escapeHtml(c.detail || "—")}</td>
      </tr>`).join("")}</tbody></table></details></section>`
    : ""

  const auditBanner = scoreSummary.quality_audit.warnings.length > 0
    ? `<div class="audit-warn"><strong>质量审计提示：</strong>${scoreSummary.quality_audit.warnings.map((w) => escapeHtml(w)).join("；")}</div>`
    : `<div class="audit-pass"><strong>质量审计通过：</strong>${scoreSummary.quality_audit.checks.map((c) => escapeHtml(c)).join(" · ")}</div>`

  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>${escapeHtml(input.title)} — DoOps 正式评估报告</title>
<style>
  :root { --bg:#f8fafc; --card:#ffffff; --fg:#0f172a; --muted:#475569; --line:#e2e8f0; --ok:#15803d; --warn:#b45309; --crit:#b91c1c; --brand:#1d4ed8; }
  * { box-sizing: border-box; }
  body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", sans-serif; background: var(--bg); color: var(--fg); line-height: 1.55; }
  .wrap { max-width: 1080px; margin: 0 auto; padding: 28px 24px 48px; }
  .cover { background: linear-gradient(135deg, #0f172a 0%, #1e3a8a 100%); color: #fff; border-radius: 14px; padding: 28px 32px; display: flex; justify-content: space-between; align-items: center; gap: 24px; margin-bottom: 20px; }
  .cover h1 { margin: 0 0 8px; font-size: 24px; }
  .cover p { margin: 0 0 12px; color: #cbd5e1; font-size: 13px; }
  .meta-row { display: flex; flex-wrap: wrap; gap: 14px; font-size: 12px; color: #e2e8f0; }
  .score-box { min-width: 140px; text-align: center; background: rgba(255,255,255,0.12); border: 1px solid rgba(255,255,255,0.22); border-radius: 12px; padding: 14px 18px; }
  .score-num { font-size: 38px; font-weight: 700; line-height: 1.1; }
  .score-grade { font-size: 13px; margin-top: 4px; opacity: 0.92; }
  .score-ok .score-num { color: #86efac; }
  .score-warn .score-num { color: #fde68a; }
  .score-crit .score-num { color: #fca5a5; }
  .card { background: var(--card); border: 1px solid var(--line); border-radius: 12px; padding: 18px 20px; margin-bottom: 16px; break-inside: avoid; }
  .gap-card { border-left: 4px solid var(--warn); background: #fffbeb; }
  h2 { margin: 0 0 12px; font-size: 15px; color: var(--fg); }
  .kpi-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(155px, 1fr)); gap: 10px; }
  .kpi-box { border: 1px solid var(--line); border-radius: 8px; padding: 10px 12px; background: #f8fafc; }
  .kpi-label { font-size: 11px; color: var(--muted); }
  .kpi-value { font-size: 20px; font-weight: 700; margin-top: 4px; font-variant-numeric: tabular-nums; }
  .kpi-unit { font-size: 11px; font-weight: 400; color: var(--muted); margin-left: 3px; }
  table { width: 100%; border-collapse: collapse; font-size: 12px; }
  th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
  th { background: #f1f5f9; font-weight: 600; color: var(--muted); }
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .small { font-size: 11px; word-break: break-word; }
  .muted { color: var(--muted); font-size: 11px; }
  .mt { margin-top: 10px; }
  .pill { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
  .pill-ok { background: #dcfce7; color: var(--ok); }
  .pill-warn { background: #fef3c7; color: var(--warn); }
  .pill-crit { background: #fee2e2; color: var(--crit); }
  .pill-muted { background: #e2e8f0; color: var(--muted); }
  .audit-pass { background: #f0fdf4; border: 1px solid #bbf7d0; color: var(--ok); border-radius: 8px; padding: 10px 14px; font-size: 12px; margin-bottom: 16px; }
  .audit-warn { background: #fffbeb; border: 1px solid #fde68a; color: var(--warn); border-radius: 8px; padding: 10px 14px; font-size: 12px; margin-bottom: 16px; }
  footer { text-align: center; font-size: 11px; color: var(--muted); margin-top: 24px; }
  @media print {
    body { background: #fff; }
    .wrap { max-width: none; padding: 0; }
    .cover { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
    .card { break-inside: avoid; }
  }
</style>
</head>
<body>
<div class="wrap">
  <header class="cover">
    <div>
      <h1>${escapeHtml(input.title)}</h1>
      <p>${escapeHtml(input.summary || input.subtitle || "基于 DoOps Skill-First 实时采集证据生成的正式管理评估报告")}</p>
      <div class="meta-row">
        <span>目标：${escapeHtml(input.targetLabel)}</span>
        ${input.skillSlug ? `<span>能力插件：${escapeHtml(input.skillSlug)}</span>` : ""}
        ${input.runId ? `<span>运行批次：#${escapeHtml(input.runId)}</span>` : ""}
        <span>生成时间：${escapeHtml(generatedAt)}</span>
      </div>
    </div>
    <div class="score-box ${gradeTone}">
      <div class="score-num">${scoreSummary.score}</div>
      <div class="score-grade">健康评级 ${scoreSummary.grade} · 根因封顶</div>
    </div>
  </header>

  ${auditBanner}
  ${kpiHtml}
  ${deductionsHtml}
  ${findingsHtml}
  ${gapsHtml}
  ${resourcesHtml}
  ${checksHtml}

  <footer>由 DoOps 平台正式报告交付引擎生成（评分模型 ${escapeHtml(scoreSummary.model)} · 零外部脚本依赖 · 支持浏览器打印导出 PDF）</footer>
</div>
</body>
</html>`
}
