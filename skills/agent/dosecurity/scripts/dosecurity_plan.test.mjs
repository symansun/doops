import assert from "node:assert/strict"
import test from "node:test"
import { createHash } from "node:crypto"
import { buildScanPlan, evaluateSensitiveExposure, normalizeAssets } from "./dosecurity_plan.mjs"
import { validateScanArtifactContract } from "./dosecurity_contract.mjs"

test("buildScanPlan enforces domain scope, caps budget, and rejects embedded credentials", () => {
  const plan = buildScanPlan({
    rootUrl: "https://app.example.com",
    rootDomain: "example.com",
    authorizedBy: "sec-lead",
    requestBudget: { maxRequests: 9999 },
  })
  assert.equal(plan.mode, "low-speed-readonly")
  assert.equal(plan.requestBudget.maxRequests, 200)
  assert.ok(plan.forbiddenCapabilities.includes("bruteforce"))

  assert.throws(() => buildScanPlan({ rootUrl: "https://other.invalid", rootDomain: "example.com", authorizedBy: "sec" }), /outside authorized/)
  assert.throws(() => buildScanPlan({ rootUrl: "https://user:canary@app.example.com", rootDomain: "example.com", authorizedBy: "sec" }), /embedded credentials/)
})

test("normalizeAssets drops plaintext passwords and secret metadata keys", () => {
  const normalized = normalizeAssets([
    {
      name: "portal",
      kind: "website",
      endpoint: "https://app.example.com",
      credential: { username: "auditor", password: "CANARY_PASSWORD_NEVER_STORE", secretRef: "k8s://sec/auditor" },
      metadata: { env: "prod", apiKey: "CANARY_API_KEY", token: "CANARY_TOKEN" },
    },
  ])
  const serialized = JSON.stringify(normalized)
  assert.ok(!serialized.includes("CANARY_PASSWORD"))
  assert.ok(!serialized.includes("CANARY_API_KEY"))
  assert.ok(!serialized.includes("CANARY_TOKEN"))
  assert.equal(normalized[0].credentialRefs[0].secretRef, "k8s://sec/auditor")
})

test("evaluateSensitiveExposure blocks non-2xx, SPA fallback false positives, and never echoes .env secrets", () => {
  assert.equal(evaluateSensitiveExposure({ status: 404, path: "/.env", bodySample: "DB_PASS=CANARY_SECRET" }), null)

  const spaHtml = "<!doctype html><html><body>APP_ENV=1</body></html>"
  const spaHash = createHash("sha256").update(spaHtml).digest("hex").slice(0, 16)
  assert.equal(evaluateSensitiveExposure({ status: 200, path: "/.env", bodySample: spaHtml, contentType: "text/html", rootBodyHash: spaHash }), null)

  const hit = evaluateSensitiveExposure({ status: 200, path: "/.env", bodySample: "DB_PASSWORD=CANARY_DOTENV_SECRET\n", contentType: "text/plain" })
  assert.ok(hit)
  assert.equal(hit.severity, "critical")
  assert.ok(!JSON.stringify(hit).includes("CANARY_DOTENV_SECRET"), "secret body must never appear in finding evidence")
})

test("validateScanArtifactContract enforces finding schema and blocks secret leaks in artifacts", () => {
  const good = validateScanArtifactContract({
    status: "warning",
    summary: "scanned 1 site",
    checks: [{ id: "surface.1", status: "warning" }],
    findings: [
      {
        id: "SURF-001",
        severity: "medium",
        priority: "P2",
        root_cause: "missing-csp-header",
        title: "缺少 CSP 响应头",
        evidence: "GET / HTTP 200; header content-security-policy absent",
      },
    ],
    coverage_gaps: [
      { module: "nuclei-allowlist", reason: "nuclei 未安装", impact: "仅执行轻量暴露面基线" },
    ],
  })
  assert.equal(good.valid, true, good.errors.join("; "))

  const leaked = validateScanArtifactContract({
    status: "critical",
    summary: "exposed dotenv",
    checks: [],
    findings: [
      {
        id: "SURF-002",
        severity: "critical",
        priority: "P1",
        root_cause: "dotenv-exposure",
        title: ".env 暴露",
        evidence: "DB_PASSWORD=SYNTHETIC_LEAK_CANARY_123",
      },
    ],
  })
  assert.equal(leaked.valid, false)
  assert.ok(leaked.errors.some((e) => e.includes("forbidden inline secret")))
})
