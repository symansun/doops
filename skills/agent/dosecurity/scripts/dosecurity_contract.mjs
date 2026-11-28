/**
 * Sanitized DoSecurity Contract & Schema Validator (zero external dependencies).
 * Enforces machine-verifiable finding, coverage_gap, scan artifact, and
 * quality_audit schemas plus recursive zero-secret-leak checks.
 */

const ALLOWED_SEVERITIES = new Set(["critical", "high", "medium", "low", "info"])
const ALLOWED_PRIORITIES = new Set(["P1", "P2", "P3", "P4"])
const ALLOWED_STATUSES = new Set(["pass", "warning", "critical", "error"])

const SENSITIVE_KEY_PATTERN =
  /(^|\.)(password|plainpassword|passwordhash|token|cookie|privatekey|apikey|accesskeysecret|authorizationheader|bearertoken)$/i
const INLINE_SECRET_VALUE_PATTERNS = [
  /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/,
  /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/,
  /\bgh[pousr]_[A-Za-z0-9]{20,255}\b/,
  /\b(?:sk|rk)_live_[A-Za-z0-9]{16,255}\b/,
  /(?<!\w)(?:DB_PASSWORD|ALIYUN_ACCESS_KEY_SECRET|JWT_SECRET|OPENAI_API_KEY)\s*=\s*\S+/i,
]

export function collectSensitiveLeakIssues(value, path = "") {
  const issues = []
  if (value === null || value === undefined) return issues
  if (typeof value === "string") {
    for (const pattern of INLINE_SECRET_VALUE_PATTERNS) {
      if (pattern.test(value)) {
        issues.push(`${path || "value"} matches forbidden inline secret pattern`)
        break
      }
    }
    return issues
  }
  if (Array.isArray(value)) {
    value.forEach((item, idx) => {
      issues.push(...collectSensitiveLeakIssues(item, `${path}[${idx}]`))
    })
    return issues
  }
  if (typeof value === "object") {
    for (const [key, nested] of Object.entries(value)) {
      const nextPath = path ? `${path}.${key}` : key
      if (SENSITIVE_KEY_PATTERN.test(key)) {
        issues.push(`${nextPath} uses forbidden sensitive key name`)
      }
      issues.push(...collectSensitiveLeakIssues(nested, nextPath))
    }
  }
  return issues
}

export function validateFindingContract(finding, index = 0) {
  const errors = []
  const prefix = `findings[${index}]`
  if (!finding || typeof finding !== "object" || Array.isArray(finding)) {
    return [`${prefix} must be an object`]
  }
  if (typeof finding.id !== "string" || !finding.id.trim()) errors.push(`${prefix}.id is required`)
  if (!ALLOWED_SEVERITIES.has(String(finding.severity ?? "").toLowerCase())) {
    errors.push(`${prefix}.severity must be one of critical|high|medium|low|info`)
  }
  if (finding.priority !== undefined && !ALLOWED_PRIORITIES.has(String(finding.priority))) {
    errors.push(`${prefix}.priority must be one of P1|P2|P3|P4`)
  }
  if (typeof finding.root_cause !== "string" || !/^[a-z0-9][a-z0-9._-]{1,63}$/.test(finding.root_cause)) {
    errors.push(`${prefix}.root_cause must be a normalized kebab/dot identifier`)
  }
  if (typeof finding.title !== "string" || !finding.title.trim()) errors.push(`${prefix}.title is required`)
  if (typeof finding.evidence !== "string" || !finding.evidence.trim()) errors.push(`${prefix}.evidence is required`)
  errors.push(...collectSensitiveLeakIssues(finding, prefix))
  return errors
}

export function validateCoverageGapContract(gap, index = 0) {
  const errors = []
  const prefix = `coverage_gaps[${index}]`
  if (!gap || typeof gap !== "object" || Array.isArray(gap)) {
    return [`${prefix} must be an object`]
  }
  if (typeof gap.module !== "string" || !gap.module.trim()) errors.push(`${prefix}.module is required`)
  if (typeof gap.reason !== "string" || !gap.reason.trim()) errors.push(`${prefix}.reason is required`)
  if (typeof gap.impact !== "string" || !gap.impact.trim()) errors.push(`${prefix}.impact is required`)
  return errors
}

export function validateScanArtifactContract(artifact) {
  const errors = []
  if (!artifact || typeof artifact !== "object" || Array.isArray(artifact)) {
    return { valid: false, errors: ["artifact must be a JSON object"] }
  }
  if (!ALLOWED_STATUSES.has(String(artifact.status ?? "").toLowerCase())) {
    errors.push("status must be one of pass|warning|critical|error")
  }
  if (typeof artifact.summary !== "string" || !artifact.summary.trim()) {
    errors.push("summary is required")
  }
  if (!Array.isArray(artifact.checks)) {
    errors.push("checks must be an array")
  }
  if (artifact.findings !== undefined) {
    if (!Array.isArray(artifact.findings)) {
      errors.push("findings must be an array when present")
    } else {
      artifact.findings.forEach((f, i) => errors.push(...validateFindingContract(f, i)))
    }
  }
  if (artifact.coverage_gaps !== undefined) {
    if (!Array.isArray(artifact.coverage_gaps)) {
      errors.push("coverage_gaps must be an array when present")
    } else {
      artifact.coverage_gaps.forEach((g, i) => errors.push(...validateCoverageGapContract(g, i)))
    }
  }
  errors.push(...collectSensitiveLeakIssues(artifact, "artifact"))
  return { valid: errors.length === 0, errors }
}
