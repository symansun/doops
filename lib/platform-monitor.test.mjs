import assert from "node:assert/strict"
import test from "node:test"
import {
  describeAuditStatus,
  filterPlatformAgents,
  formatActionLabel,
  formatBytes,
  formatDurationMs,
  formatSuccessRate,
  formatTokens,
  parseAdmissionSnapshot,
  summarizeVersionConsistency,
} from "./platform-monitor.ts"

test("Given agent version counts, When summarized, Then version drift and unreported commits are flagged", () => {
  assert.deepEqual(summarizeVersionConsistency([]), {
    consistent: true,
    hasUnreported: false,
    headline: "当前无在线 Agent",
  })
  assert.deepEqual(summarizeVersionConsistency([{ release_commit: "0123456789abcdef0123456789abcdef01234567", count: 3 }]), {
    consistent: true,
    hasUnreported: false,
    headline: "版本一致 · 01234567",
  })
  const drift = summarizeVersionConsistency([
    { release_commit: "0123456789abcdef0123456789abcdef01234567", count: 2 },
    { release_commit: "", count: 1 },
  ])
  assert.equal(drift.consistent, false)
  assert.equal(drift.hasUnreported, true)
  assert.match(drift.headline, /2 个不同运行版本（含未上报）/)
})

test("Given platform monitor agent rows, When filtered by visibility and query, Then archived and history scopes behave deterministically", () => {
  const rows = [
    { cluster: "prod", instance: "web-01", key: "prod/web-01", activity: "online", status: "idle", busy: false, enrolled: true, release_commit: "abcdef12", remote: "10.0.0.1:5000", active_ops: 0, queued_ops: 0, sessions: ["console-alpha"], resources: [], window_connects: 1, window_disconnects: 0, window_calls: 5, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 1200 },
    { cluster: "prod", instance: "old-02", key: "prod/old-02", activity: "history", status: "offline", busy: false, enrolled: true, active_ops: 0, queued_ops: 0, sessions: [], resources: [], window_connects: 0, window_disconnects: 0, window_calls: 0, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 0 },
    { cluster: "prod", instance: "arch-03", key: "prod/arch-03", activity: "archived", status: "offline", busy: false, enrolled: true, active_ops: 0, queued_ops: 0, sessions: [], resources: [], window_connects: 0, window_disconnects: 0, window_calls: 0, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 0 },
  ]
  assert.deepEqual(filterPlatformAgents(rows, "active", "").map((r) => r.key), ["prod/web-01"])
  assert.deepEqual(filterPlatformAgents(rows, "with_history", "").map((r) => r.key), ["prod/web-01", "prod/old-02"])
  assert.deepEqual(filterPlatformAgents(rows, "all", "").map((r) => r.key), ["prod/web-01", "prod/old-02", "prod/arch-03"])
  assert.deepEqual(filterPlatformAgents(rows, "all", "console-alpha").map((r) => r.key), ["prod/web-01"])
})

test("Given status, admission, bytes and durations, When formatted, Then outcome_unknown stays warning and numbers are human-readable", () => {
  assert.deepEqual(describeAuditStatus("outcome_unknown"), { label: "结果未知", variant: "warning" })
  assert.equal(formatActionLabel("doops_agent_prompt"), "AI 助手")
  assert.equal(formatBytes(0), "0 B")
  assert.equal(formatBytes(2048), "2.00 KiB")
  assert.equal(formatTokens(0), "0")
  assert.equal(formatTokens(15_400), "15.4K")
  assert.equal(formatTokens(2_500_000), "2.50M")
  assert.equal(formatDurationMs(450), "450 ms")
  assert.equal(formatDurationMs(125_000), "2m 5s")
  assert.equal(formatSuccessRate({ success: 0, failed: 0 }), "—")
  assert.equal(formatSuccessRate({ success: 19, failed: 1 }), "95.0%")

  const snap = parseAdmissionSnapshot(JSON.stringify({ limit: 4, inUse: 2, available: 2, holders: [{}], queues: [] }))
  assert.ok(snap)
  assert.equal(snap.limit, 4)
  assert.equal(snap.holders, 1)
  assert.equal(parseAdmissionSnapshot("not json"), null)
})
