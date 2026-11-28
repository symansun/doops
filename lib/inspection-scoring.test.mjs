import assert from "node:assert/strict"
import test from "node:test"
import { computeInspectionScore, normalizeCoverageGaps, normalizeInspectionFindings } from "./inspection-scoring.ts"

test("Given repeated same-root-cause findings, When computing score, Then penalty is bounded by root-cause cap instead of collapsing to zero", () => {
  const repeatedCsp = Array.from({ length: 15 }, (_, idx) => ({
    id: `SURF-HDR-${idx + 1}`,
    severity: "medium",
    root_cause: "missing-csp-header",
    title: `缺少 CSP (${idx + 1})`,
    asset: `https://s${idx + 1}.example.com/`,
    evidence: "GET / HTTP 200; header content-security-policy absent",
  }))
  const res = computeInspectionScore({ findings: repeatedCsp, coverage_gaps: [] })
  assert.equal(res.model, "doops.root-cause-capped.v1")
  assert.equal(res.deductions.length, 1)
  assert.equal(res.deductions[0].penalty, 14, "missing-csp-header must be capped at 14")
  assert.equal(res.deductions[0].capped, true)
  assert.equal(res.score, 86)
  assert.equal(res.grade, "B")
})

test("Given coverage gaps and info login-surface findings, When computing score, Then zero false penalty is applied and quality audit flags high score with gaps", () => {
  const payload = {
    findings: [
      { id: "SURF-AUTH-01", severity: "info", root_cause: "login-surface", title: "公开登录页", asset: "https://app.example.com", evidence: "HTTP 200" },
    ],
    coverage_gaps: [
      { module: "nuclei-allowlist", reason: "nuclei not installed", impact: "CVE templates skipped" },
    ],
  }
  const gaps = normalizeCoverageGaps(payload)
  assert.equal(gaps.length, 1)
  const score = computeInspectionScore(payload)
  assert.equal(score.score, 100)
  assert.equal(score.deductions.length, 0)
  assert.equal(score.coverage_gap_count, 1)
})

test("Given confirmed outage, non-prod environment cap, and secret leak in evidence, Then caps and quality audit gate trigger accurately", () => {
  const outage = computeInspectionScore({
    findings: [{ id: "DOWN-1", severity: "high", root_cause: "confirmed-outage", title: "站点宕机", asset: "https://down.example.com", evidence: "connection refused" }],
  })
  assert.ok(outage.score <= 65, `confirmed outage must keep score <= 65, got ${outage.score}`)

  const nonProd = computeInspectionScore(
    { findings: [{ id: "EXP-1", severity: "critical", root_cause: "dotenv-exposure", title: ".env exposed", asset: "https://dev.example.com/.env", evidence: "HTTP 200" }] },
    { environment: "staging" },
  )
  assert.equal(nonProd.deductions[0].penalty, 12, "non-prod penalty must be capped at 12")

  const leaked = computeInspectionScore({
    findings: [{ id: "LEAK-1", severity: "critical", root_cause: "dotenv-exposure", title: "leak", asset: "x", evidence: "password=CANARY_LEAKED_VALUE_999" }],
  })
  assert.equal(leaked.quality_audit.status, "warning")
  assert.ok(leaked.quality_audit.warnings.some((w) => w.includes("LEAK-1")))
  assert.equal(normalizeInspectionFindings({ findings: "not-an-array" }).length, 0)
})
