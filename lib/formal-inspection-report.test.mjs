import assert from "node:assert/strict"
import test from "node:test"
import { escapeHtml, renderFormalInspectionReportHtml } from "./formal-inspection-report.ts"

test("renderFormalInspectionReportHtml produces self-contained escaped HTML with score, findings, and coverage gaps", () => {
  const html = renderFormalInspectionReportHtml({
    title: "外部暴露面扫描 <script>alert(1)</script>",
    skillSlug: "attack-surface-scan",
    targetLabel: "prod/web-01",
    runId: "run_123",
    collectedAt: "2026-11-28T10:00:00Z",
    summary: "发现 1 项安全头缺口",
    artifactJson: {
      metrics: [{ key: "surface_urls_scanned", label: "扫描站点数", value: 3, unit: "个" }],
      findings: [
        { id: "SURF-HDR-001", severity: "medium", root_cause: "missing-csp-header", title: "缺少 CSP", asset: "https://app.example.com/", evidence: "GET / HTTP 200", recommendation: "配置 CSP" },
      ],
      coverage_gaps: [
        { module: "nuclei-allowlist", reason: "nuclei 未安装", impact: "未执行 CVE 模板" },
      ],
      resources: [
        { url: "https://app.example.com/", status_code: 200, missing_headers: 1, status: "warning" },
      ],
    },
    checks: [{ name: "安全头基线", status: "warning", value: "缺 1 项", detail: "CSP 缺失" }],
  })

  assert.ok(html.startsWith("<!doctype html>"))
  assert.ok(!html.includes("<script>alert(1)</script>"), "raw script tag must be HTML-escaped")
  assert.ok(html.includes("&lt;script&gt;alert(1)&lt;/script&gt;"))
  assert.ok(html.includes("健康评级 A") || html.includes("健康评级 B"))
  assert.ok(html.includes("覆盖缺口与证据边界"))
  assert.ok(html.includes("nuclei-allowlist"))
  assert.ok(html.includes("@media print"))
  assert.equal(escapeHtml(`"a"&'b'`), "&quot;a&quot;&amp;&#39;b&#39;")
})
