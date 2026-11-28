// Pure-function Root-Cause Capped Scoring Engine distilled from DoSecurity's
// scoring_model.py for DoOps Skill-First artifacts & L2+ capability plugins.

export type FindingSeverity = "critical" | "high" | "medium" | "low" | "info"

export interface InspectionFinding {
  id: string
  severity: FindingSeverity
  confidence?: string
  root_cause: string
  title: string
  asset: string
  evidence: string
  recommendation: string
  priority: "P1" | "P2" | "P3" | "P4"
  environment?: string
}

export interface InspectionCoverageGap {
  module: string
  reason: string
  impact: string
}

export interface RootCauseDeduction {
  root_cause: string
  label: string
  severity: FindingSeverity
  count: number
  penalty: number
  capped: boolean
}

export interface ReportQualityAudit {
  status: "pass" | "warning"
  checks: string[]
  warnings: string[]
}

export interface InspectionScoreSummary {
  model: "doops.root-cause-capped.v1"
  score: number
  grade: "A" | "B" | "C" | "D" | "E"
  severity_counts: Record<FindingSeverity, number>
  deductions: RootCauseDeduction[]
  coverage_gap_count: number
  quality_audit: ReportQualityAudit
}

interface RootCauseRule {
  label: string
  base: number
  increment: number
  cap: number
}

const ROOT_CAUSE_RULES: Record<string, RootCauseRule> = {
  "confirmed-outage": { label: "已确认核心可用性故障", base: 35, increment: 8, cap: 55 },
  "dotenv-exposure": { label: ".env 敏感配置文件暴露", base: 30, increment: 10, cap: 50 },
  "git-config-exposure": { label: ".git 元数据目录暴露", base: 28, increment: 8, cap: 45 },
  "spring-actuator-exposure": { label: "Actuator 敏感端点暴露", base: 26, increment: 8, cap: 42 },
  "swagger-ui-exposure": { label: "API 文档界面公开暴露", base: 14, increment: 4, cap: 24 },
  "openapi-docs-exposure": { label: "OpenAPI 定义文件公开暴露", base: 14, increment: 4, cap: 24 },
  "apache-status-exposure": { label: "服务状态页公开暴露", base: 14, increment: 4, cap: 24 },
  "dns-open-recursion": { label: "DNS 公网开放递归风险", base: 24, increment: 6, cap: 36 },
  "dns-axfr-exposed": { label: "DNS AXFR 区域传送暴露", base: 24, increment: 6, cap: 36 },
  "dns-resolution-failed": { label: "DNS 解析失败或超时", base: 20, increment: 5, cap: 35 },
  "dns-high-latency": { label: "DNS 解析延迟偏高", base: 6, increment: 2, cap: 12 },
  "bind-config-invalid": { label: "BIND 主配置语法异常", base: 25, increment: 5, cap: 35 },
  "bind-zone-invalid": { label: "BIND Zone 语法异常", base: 20, increment: 5, cap: 32 },
  "unbound-config-invalid": { label: "Unbound 递归配置异常", base: 18, increment: 4, cap: 26 },
  "mail-pod-unready": { label: "邮件服务 Pod 未就绪", base: 28, increment: 8, cap: 45 },
  "mail-pvc-unbound": { label: "邮件存储 PVC 未绑定", base: 24, increment: 6, cap: 36 },
  "mail-port-unreachable": { label: "SMTP/IMAPS 端口不可达", base: 12, increment: 4, cap: 24 },
  "missing-mail-mx": { label: "邮件域缺少 MX 记录", base: 18, increment: 4, cap: 28 },
  "missing-mail-spf": { label: "邮件域缺少 SPF 记录", base: 8, increment: 2, cap: 14 },
  "missing-mail-dmarc": { label: "邮件域缺少 DMARC 记录", base: 8, increment: 2, cap: 14 },
  "missing-mail-dkim": { label: "邮件域未检出 DKIM 记录", base: 5, increment: 2, cap: 10 },
  "missing-csp-header": { label: "缺少 Content-Security-Policy 头", base: 8, increment: 2, cap: 14 },
  "missing-hsts-header": { label: "HTTPS 缺少 HSTS 响应头", base: 8, increment: 2, cap: 14 },
  "missing-security-header": { label: "缺少浏览器辅助安全响应头", base: 4, increment: 1, cap: 8 },
  "server-version-banner": { label: "响应头暴露精确版本号", base: 3, increment: 1, cap: 6 },
  "tls-expiry": { label: "TLS 证书有效期风险", base: 10, increment: 4, cap: 22 },
  "login-surface": { label: "公开认证/登录入口提示", base: 0, increment: 0, cap: 0 },
  "coverage-gap": { label: "巡检覆盖缺口（不扣分）", base: 0, increment: 0, cap: 0 },
  "automation-session-boundary": { label: "自动化会话边界（不扣分）", base: 0, increment: 0, cap: 0 },
  "mining": { label: "疑似挖矿木马特征", base: 35, increment: 10, cap: 55 },
  "proxy": { label: "违规代理或内网穿透", base: 25, increment: 6, cap: 40 },
  "ssh": { label: "SSH 加固基线缺口", base: 8, increment: 3, cap: 16 },
}

const SEVERITY_FALLBACK: Record<FindingSeverity, RootCauseRule> = {
  critical: { label: "严重安全/可用性风险", base: 28, increment: 6, cap: 45 },
  high: { label: "高危风险发现", base: 18, increment: 4, cap: 30 },
  medium: { label: "中危基线风险", base: 9, increment: 2, cap: 15 },
  low: { label: "低危基线建议", base: 4, increment: 1, cap: 8 },
  info: { label: "信息级治理提示", base: 0, increment: 0, cap: 0 },
}

const NON_PROD_ENVS = new Set(["dev", "development", "test", "testing", "qa", "uat", "stage", "staging", "sandbox", "local"])
const SECRET_LEAK_RE = /-----BEGIN [A-Z ]+ PRIVATE KEY-----|AKIA[0-9A-Z]{16}|(?:password|secret|token|api_key)\s*[:=]\s*['"]?[A-Za-z0-9._+/=-]{8,}/i

function normalizeSeverity(raw: unknown): FindingSeverity {
  const s = String(raw ?? "").trim().toLowerCase()
  if (s === "critical" || s === "fatal") return "critical"
  if (s === "high" || s === "error") return "high"
  if (s === "medium" || s === "warning" || s === "warn") return "medium"
  if (s === "low") return "low"
  return "info"
}

function defaultPriority(sev: FindingSeverity): "P1" | "P2" | "P3" | "P4" {
  if (sev === "critical" || sev === "high") return "P1"
  if (sev === "medium") return "P2"
  if (sev === "low") return "P3"
  return "P4"
}

function extractTlsDays(evidence: string): number | null {
  const m = /tls_expiry_days\s*[:=]\s*(-?\d+(?:\.\d+)?)/i.exec(evidence)
  if (!m) return null
  const val = Number(m[1])
  return Number.isFinite(val) ? val : null
}

export function normalizeInspectionFindings(artifactJson: unknown): InspectionFinding[] {
  if (!artifactJson || typeof artifactJson !== "object" || Array.isArray(artifactJson)) return []
  const raw = (artifactJson as Record<string, unknown>).findings
  if (!Array.isArray(raw)) return []
  const out: InspectionFinding[] = []
  raw.forEach((entry, idx) => {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) return
    const obj = entry as Record<string, unknown>
    const severity = normalizeSeverity(obj.severity)
    const rootCause = String(obj.root_cause ?? obj.category ?? obj.type ?? "review-needed")
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9._-]+/g, "-") || "review-needed"
    const prioRaw = String(obj.priority ?? "").trim().toUpperCase()
    const priority = prioRaw === "P1" || prioRaw === "P2" || prioRaw === "P3" || prioRaw === "P4"
      ? prioRaw
      : defaultPriority(severity)
    out.push({
      id: String(obj.id ?? `FND-${String(idx + 1).padStart(3, "0")}`),
      severity,
      confidence: typeof obj.confidence === "string" ? obj.confidence : undefined,
      root_cause: rootCause,
      title: String(obj.title ?? obj.reason ?? obj.name ?? rootCause),
      asset: String(obj.asset ?? obj.comm ?? obj.target ?? "—"),
      evidence: String(obj.evidence ?? obj.detail ?? obj.reason ?? "—"),
      recommendation: String(obj.recommendation ?? "按安全与运维基线复核并制定受控加固计划。"),
      priority,
      environment: typeof obj.environment === "string" ? obj.environment : undefined,
    })
  })
  return out
}

export function normalizeCoverageGaps(artifactJson: unknown): InspectionCoverageGap[] {
  if (!artifactJson || typeof artifactJson !== "object" || Array.isArray(artifactJson)) return []
  const raw = (artifactJson as Record<string, unknown>).coverage_gaps
  if (!Array.isArray(raw)) return []
  const out: InspectionCoverageGap[] = []
  for (const entry of raw) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue
    const obj = entry as Record<string, unknown>
    const mod = String(obj.module ?? obj.name ?? "coverage").trim()
    const reason = String(obj.reason ?? obj.detail ?? "").trim()
    const impact = String(obj.impact ?? "").trim()
    if (!mod && !reason) continue
    out.push({ module: mod || "coverage", reason: reason || "未覆盖", impact: impact || "该项结果不代表健康" })
  }
  return out
}

export function computeInspectionScore(
  artifactJson: unknown,
  options: { environment?: string } = {},
): InspectionScoreSummary {
  const findings = normalizeInspectionFindings(artifactJson)
  const gaps = normalizeCoverageGaps(artifactJson)
  const env = String(options.environment ?? "").trim().toLowerCase()
  const isNonProdGlobal = NON_PROD_ENVS.has(env)

  const severityCounts: Record<FindingSeverity, number> = {
    critical: 0,
    high: 0,
    medium: 0,
    low: 0,
    info: 0,
  }
  const grouped = new Map<string, InspectionFinding[]>()
  for (const f of findings) {
    severityCounts[f.severity] += 1
    const arr = grouped.get(f.root_cause) ?? []
    arr.push(f)
    grouped.set(f.root_cause, arr)
  }

  const sevRank: Record<FindingSeverity, number> = { critical: 4, high: 3, medium: 2, low: 1, info: 0 }
  const deductions: RootCauseDeduction[] = []
  let totalPenalty = 0
  let hasConfirmedOutage = false

  for (const [rcKey, list] of grouped.entries()) {
    let topSev: FindingSeverity = "info"
    for (const item of list) {
      if (sevRank[item.severity] > sevRank[topSev]) topSev = item.severity
    }
    if (rcKey === "confirmed-outage") hasConfirmedOutage = true

    const rule = ROOT_CAUSE_RULES[rcKey] ?? SEVERITY_FALLBACK[topSev]
    let penalty = 0
    let capped = false

    if (rule.cap > 0) {
      if (rcKey === "tls-expiry") {
        let maxTls = 0
        for (const item of list) {
          const days = extractTlsDays(item.evidence)
          const p = days === null ? 10 : days < 0 ? 22 : days <= 7 ? 12 : days <= 30 ? 5 : 3
          if (p > maxTls) maxTls = p
        }
        const rawP = maxTls + Math.max(0, list.length - 1) * rule.increment
        penalty = Math.min(rawP, rule.cap)
        capped = rawP > rule.cap
      } else {
        const rawP = rule.base + Math.max(0, list.length - 1) * rule.increment
        penalty = Math.min(rawP, rule.cap)
        capped = rawP > rule.cap
      }
      const allNonProd = isNonProdGlobal || list.every((item) => item.environment && NON_PROD_ENVS.has(item.environment.toLowerCase()))
      if (allNonProd && penalty > 12) {
        penalty = 12
        capped = true
      }
    }

    totalPenalty += penalty
    if (penalty > 0) {
      deductions.push({
        root_cause: rcKey,
        label: rule.label,
        severity: topSev,
        count: list.length,
        penalty,
        capped,
      })
    }
  }

  deductions.sort((a, b) => b.penalty - a.penalty || a.root_cause.localeCompare(b.root_cause))
  let score = Math.max(0, Math.min(100, Math.round(100 - totalPenalty)))
  if (hasConfirmedOutage && score > 65) {
    score = 65
  }

  const grade: "A" | "B" | "C" | "D" | "E" =
    score >= 90 ? "A" : score >= 80 ? "B" : score >= 70 ? "C" : score >= 60 ? "D" : "E"

  // Pre-export quality audit gate
  const auditChecks = ["root_cause_capped_scoring_applied", "coverage_gaps_separated"]
  const warnings: string[] = []
  for (const f of findings) {
    if (SECRET_LEAK_RE.test(f.evidence) || SECRET_LEAK_RE.test(f.title)) {
      warnings.push(`发现 ${f.id} 证据字段疑似包含敏感凭据片段，请在对外交付前人工复核脱敏`)
    }
  }
  if (gaps.length > 0 && score >= 95 && findings.length === 0) {
    warnings.push(`存在 ${gaps.length} 项覆盖缺口，高分仅代表已覆盖模块的当前状态，不代表未覆盖项无风险`)
  }
  if (warnings.length === 0) {
    auditChecks.push("no_plaintext_secret_pattern", "score_reasonableness_ok")
  }

  return {
    model: "doops.root-cause-capped.v1",
    score,
    grade,
    severity_counts: severityCounts,
    deductions,
    coverage_gap_count: gaps.length,
    quality_audit: {
      status: warnings.length > 0 ? "warning" : "pass",
      checks: auditChecks,
      warnings,
    },
  }
}
