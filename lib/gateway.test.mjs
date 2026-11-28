import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import publicAgentTools from "../backend/agent/internal/server/testdata/public-agent-tools.json" with { type: "json" }
import {
  TOOLS,
  auditQueryParams,
  formatAuditPaginationSummary,
  resolveDefaultGateway,
  sliceAuditPage,
  AUDIT_ACTIONS,
  AUDIT_STATUSES,
} from "./gateway.ts"

test("every Web tool name is in the Gateway's public Agent tool contract", () => {
  for (const [alias, name] of Object.entries(TOOLS)) {
    assert.ok(publicAgentTools.includes(name), `TOOLS.${alias}: ${name} is not a public Agent tool`)
  }
})

test("local console defaults to the Gateway port and migrates the former default", () => {
  for (const origin of ["http://127.0.0.1:3000", "http://localhost:3000", "http://[::1]:3000"]) {
    for (const saved of [null, origin, "not a URL"]) {
      assert.equal(resolveDefaultGateway(saved, origin), "http://127.0.0.1:42222")
    }
    assert.equal(resolveDefaultGateway("http://localhost:42223", origin), "http://localhost:42223")
  }
  assert.equal(resolveDefaultGateway(null, "https://ops.example.com"), "https://ops.example.com")
})

test("production HTTPS console migrates saved HTTP gateway to its own origin", () => {
  assert.equal(
    resolveDefaultGateway("http://203.0.113.10:42222", "https://doops.l8ai.cn"),
    "https://doops.l8ai.cn",
  )
  assert.equal(
    resolveDefaultGateway("203.0.113.10:42222", "https://doops.l8ai.cn"),
    "https://doops.l8ai.cn",
  )
})

test("audit supports operation cursor and time filters without hiding terminal observations", () => {
  const input = {
    scope: "operation", target: { cluster: "test", instance: "node" }, sessionId: "console-test",
    credentialId: "", action: "agent:upgrade", status: "", limit: 50, correlationId: " operation-42 ",
    beforeId: 100, since: "2026-11-10T00:00:00Z", until: "2026-11-11T00:00:00Z", hideUpgradeObserve: true,
  }
  assert.deepEqual(auditQueryParams(input), { ok: true, params: {
    limit: "50", correlation_id: "operation-42",
    before_id: "100", since: "2026-11-10T00:00:00.000Z", until: "2026-11-11T00:00:00.000Z",
    hide_upgrade_observe: "true", action: "agent:upgrade",
  } })
  assert.equal(auditQueryParams({ ...input, hideUpgradeObserve: false }).params.hide_upgrade_observe, "false")
  for (const change of [{ limit: 201 }, { beforeId: -1 }, { since: "bad date" }, { until: input.since }, { correlationId: "" }]) {
    assert.equal(auditQueryParams({ ...input, ...change }).ok, false)
  }
  for (const status of ["accepted", "observing", "success", "error", "busy", "outcome_unknown"]) assert.ok(AUDIT_STATUSES.includes(status))
  assert.ok(AUDIT_ACTIONS.includes("audit:purge"))
})

test("valid saved HTTPS gateway remains selected", () => {
  assert.equal(
    resolveDefaultGateway("https://ops.example.com", "https://doops.l8ai.cn"),
    "https://ops.example.com",
  )
})

test("valid saved WSS gateway remains selected on HTTPS console", () => {
  assert.equal(
    resolveDefaultGateway("wss://ops.example.com", "https://doops.l8ai.cn"),
    "wss://ops.example.com",
  )
})

test("invalid saved gateway falls back to the current origin", () => {
  assert.equal(resolveDefaultGateway("not a URL", "https://doops.l8ai.cn"), "https://doops.l8ai.cn")
})

test("audit query params match each scope and filter", () => {
  const credential = "cred_0123456789abcdef01234567"
  const base = {
    scope: "target",
    target: { cluster: "prod", instance: "node-1" },
    sessionId: "console-a1b2",
    credentialId: "",
    action: "",
    status: "",
    limit: 50,
  }
  const cases = [
    { name: "target", input: {}, want: { ok: true, params: { limit: "50", cluster: "prod", instance: "node-1" } } },
    {
      name: "target with action and status", input: { action: "credential:verify", status: "error" },
      want: { ok: true, params: { limit: "50", cluster: "prod", instance: "node-1", action: "credential:verify", status: "error" } },
    },
    {
      name: "session", input: { scope: "session" },
      want: { ok: true, params: { limit: "50", cluster: "prod", instance: "node-1", session: "console-a1b2" } },
    },
    { name: "self", input: { scope: "self", action: "ask" }, want: { ok: true, params: { limit: "50", user_id: "self", action: "ask" } } },
    { name: "terminal single-shot ai", input: { action: "terminal:ai", status: "success" }, want: { ok: true, params: { limit: "50", cluster: "prod", instance: "node-1", action: "terminal:ai", status: "success" } } },
    { name: "global", input: { scope: "global", status: "blocked" }, want: { ok: true, params: { limit: "50", view: "global", status: "blocked" } } },
    {
      name: "credential sends only the session, never the target",
      input: { scope: "credential", credentialId: credential },
      want: { ok: true, params: { limit: "50", session: credential } },
    },
    {
      name: "credential with action, trimmed",
      input: { scope: "credential", credentialId: `  ${credential} `, action: "credential:rotate" },
      want: { ok: true, params: { limit: "50", session: credential, action: "credential:rotate" } },
    },
    {
      name: "credential id ignored outside the credential scope",
      input: { scope: "target", credentialId: credential },
      want: { ok: true, params: { limit: "50", cluster: "prod", instance: "node-1" } },
    },
    {
      name: "credential without an id",
      input: { scope: "credential", credentialId: "  " },
      want: { ok: false, reason: "missing", message: "填写凭据 ID（cred_ 开头）后查询该凭据的审计记录" },
    },
    ...["cred_123", "CRED_0123456789ABCDEF01234567", "credver_0123456789abcdef01234567", "console-a1b2"].map((id) => ({
      name: `malformed credential id ${id}`,
      input: { scope: "credential", credentialId: id },
      want: { ok: false, reason: "invalid", message: "凭据 ID 格式应为 cred_ 加 24 位小写十六进制，可在凭据页复制" },
    })),
  ]
  for (const c of cases) assert.deepEqual(auditQueryParams({ ...base, ...c.input }), c.want, c.name)
})

test("Given audit pages with limit+1 probe, When slicing and formatting summary, Then users can distinguish end-of-list from more pages cleanly", () => {
  const short = sliceAuditPage(Array.from({ length: 20 }, (_, i) => i + 1), 50)
  assert.equal(short.items.length, 20)
  assert.equal(short.hasMore, false)

  const exact = sliceAuditPage(Array.from({ length: 50 }, (_, i) => i + 1), 50)
  assert.equal(exact.items.length, 50)
  assert.equal(exact.hasMore, false)

  const overflow = sliceAuditPage(Array.from({ length: 51 }, (_, i) => i + 1), 50)
  assert.equal(overflow.items.length, 50)
  assert.equal(overflow.hasMore, true)

  assert.equal(
    formatAuditPaginationSummary({ loaded: 0, pageSize: 50, hasMore: false, loading: true, loadingMore: false }),
    "每页 50 条，正在查询…",
  )
  assert.equal(
    formatAuditPaginationSummary({ loaded: 0, pageSize: 50, hasMore: false, loading: false, loadingMore: false }),
    "每页 50 条，当前筛选条件无匹配记录。",
  )
  assert.equal(
    formatAuditPaginationSummary({ loaded: 20, pageSize: 50, hasMore: false, loading: false, loadingMore: false }),
    "每页 50 条，已加载全部 20 条匹配记录（无更多数据）。",
  )
  assert.match(
    formatAuditPaginationSummary({ loaded: 50, pageSize: 50, hasMore: true, loading: false, loadingMore: false }),
    /还有更早记录/,
  )
  assert.match(
    formatAuditPaginationSummary({ loaded: 50, pageSize: 50, hasMore: true, loading: true, loadingMore: true }),
    /正在加载更早记录/,
  )

  const panelSrc = readFileSync(new URL("../components/audit-panel.tsx", import.meta.url), "utf8")
  assert.match(panelSrc, /limit:\s*PAGE_SIZE \+ 1/, "AuditPanel must query limit+1 to detect hasMore accurately")
  assert.match(panelSrc, /sliceAuditPage\(/, "AuditPanel must slice extra probe row via sliceAuditPage")
  assert.match(panelSrc, /formatAuditPaginationSummary\(/, "AuditPanel must render explicit pagination state via formatAuditPaginationSummary")
})
