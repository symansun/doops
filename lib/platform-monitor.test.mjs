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
  isAgentVersionDrifted,
  parseAdmissionSnapshot,
  resolveDriftedOnlineCount,
  resolveOfflineTotal,
  summarizeVersionConsistency,
} from "./platform-monitor.ts"

test("Given agent version counts, When summarized, Then gateway baseline drift and legacy fallbacks are accurate", () => {
  const gatewayCommit = "b56892220123456789abcdef0123456789abcdef"
  const oldCommitA = "74fe24910123456789abcdef0123456789abcdef"
  const oldCommitB = "574d168a0123456789abcdef0123456789abcdef"

  assert.deepEqual(summarizeVersionConsistency([]), {
    consistent: true,
    hasUnreported: false,
    headline: "当前无在线 Agent",
    driftedOnline: 0,
  })
  assert.deepEqual(summarizeVersionConsistency([{ release_commit: oldCommitA, count: 3 }]), {
    consistent: true,
    hasUnreported: false,
    headline: "版本一致 · 74fe2491",
    driftedOnline: 0,
  })
  const legacyDrift = summarizeVersionConsistency([
    { release_commit: oldCommitA, count: 2 },
    { release_commit: "", count: 1 },
  ])
  assert.equal(legacyDrift.consistent, false)
  assert.equal(legacyDrift.hasUnreported, true)
  assert.equal(legacyDrift.driftedOnline, 1)
  assert.match(legacyDrift.headline, /2 个不同运行版本（含未上报）/)

  // Gateway baseline with drifted online agents (including all-old single version).
  const baselineDrift = summarizeVersionConsistency(
    [
      { release_commit: oldCommitA, count: 6 },
      { release_commit: oldCommitB, count: 3 },
      { release_commit: "", count: 1 },
    ],
    { gatewayReleaseCommit: gatewayCommit, driftedOnline: 10 },
  )
  assert.deepEqual(baselineDrift, {
    consistent: false,
    hasUnreported: true,
    headline: "基线 b5689222 · 10 台在线 Agent 版本漂移",
    baselineCommit: gatewayCommit,
    driftedOnline: 10,
  })

  // Gateway baseline aligned with all online agents.
  const baselineAligned = summarizeVersionConsistency(
    [{ release_commit: gatewayCommit, count: 4 }],
    { gatewayReleaseCommit: gatewayCommit, driftedOnline: 0 },
  )
  assert.deepEqual(baselineAligned, {
    consistent: true,
    hasUnreported: false,
    headline: "基线 b5689222 · 版本一致",
    baselineCommit: gatewayCommit,
    driftedOnline: 0,
  })

  // Backward-compatible offline_total and drifted_online fallback derivation.
  assert.equal(resolveOfflineTotal({ recent_offline: 2, history: 3 }), 5)
  assert.equal(resolveOfflineTotal({ recent_offline: 2, history: 3, offline_total: 5 }), 5)
  assert.equal(
    resolveDriftedOnlineCount({
      gateway_release_commit: gatewayCommit,
      versions: [
        { release_commit: gatewayCommit, count: 2 },
        { release_commit: oldCommitA, count: 3 },
      ],
    }),
    3,
  )
})

test("Given platform monitor agent rows, When filtered by visibility, quickFilter, and query, Then drifted and offline scopes behave deterministically", () => {
  const gatewayCommit = "b56892220123456789abcdef0123456789abcdef"
  const oldCommit = "74fe24910123456789abcdef0123456789abcdef"
  const rows = [
    { cluster: "prod", instance: "web-01", key: "prod/web-01", activity: "online", status: "idle", busy: false, enrolled: true, release_commit: gatewayCommit, version_drifted: false, remote: "10.0.0.1:5000", active_ops: 0, queued_ops: 0, sessions: ["console-alpha"], resources: [], window_connects: 1, window_disconnects: 0, window_calls: 5, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 1200 },
    { cluster: "prod", instance: "web-02", key: "prod/web-02", activity: "online", status: "idle", busy: false, enrolled: true, release_commit: oldCommit, version_drifted: true, remote: "10.0.0.2:5000", active_ops: 0, queued_ops: 0, sessions: [], resources: [], window_connects: 1, window_disconnects: 0, window_calls: 2, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 0 },
    { cluster: "prod", instance: "off-recent", key: "prod/off-recent", activity: "recent_offline", status: "offline", busy: false, enrolled: true, release_commit: oldCommit, active_ops: 0, queued_ops: 0, sessions: [], resources: [], window_connects: 0, window_disconnects: 1, window_calls: 0, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 0 },
    { cluster: "prod", instance: "old-02", key: "prod/old-02", activity: "history", status: "offline", busy: false, enrolled: true, active_ops: 0, queued_ops: 0, sessions: [], resources: [], window_connects: 0, window_disconnects: 0, window_calls: 0, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 0 },
    { cluster: "prod", instance: "arch-03", key: "prod/arch-03", activity: "archived", status: "offline", busy: false, enrolled: true, active_ops: 0, queued_ops: 0, sessions: [], resources: [], window_connects: 0, window_disconnects: 0, window_calls: 0, window_failed: 0, window_bytes_in: 0, window_bytes_out: 0, window_tokens: 0 },
  ]
  const summary = {
    gateway_release_commit: gatewayCommit,
    versions: [
      { release_commit: gatewayCommit, count: 1 },
      { release_commit: oldCommit, count: 1 },
    ],
  }

  assert.deepEqual(filterPlatformAgents(rows, "active", "").map((r) => r.key), ["prod/web-01", "prod/web-02", "prod/off-recent"])
  assert.deepEqual(filterPlatformAgents(rows, "with_history", "").map((r) => r.key), ["prod/web-01", "prod/web-02", "prod/off-recent", "prod/old-02"])
  assert.deepEqual(filterPlatformAgents(rows, "all", "").map((r) => r.key), ["prod/web-01", "prod/web-02", "prod/off-recent", "prod/old-02", "prod/arch-03"])
  assert.deepEqual(filterPlatformAgents(rows, "all", "console-alpha").map((r) => r.key), ["prod/web-01"])

  // Quick filter: drifted returns only drifted online nodes.
  assert.deepEqual(
    filterPlatformAgents(rows, "active", "", { quickFilter: "drifted", summary }).map((r) => r.key),
    ["prod/web-02"],
  )
  // Quick filter: offline returns all known non-archived offline nodes (recent_offline + history), excluding archived.
  assert.deepEqual(
    filterPlatformAgents(rows, "active", "", { quickFilter: "offline", summary }).map((r) => r.key),
    ["prod/off-recent", "prod/old-02"],
  )
  // Legacy Gateway fallback when version_drifted is omitted on agent rows.
  const legacyRows = rows.map(({ version_drifted: _omit, ...rest }) => rest)
  assert.equal(isAgentVersionDrifted(legacyRows[0], summary), false)
  assert.equal(isAgentVersionDrifted(legacyRows[1], summary), true)
  assert.equal(isAgentVersionDrifted(legacyRows[2], summary), false)
  assert.deepEqual(
    filterPlatformAgents(legacyRows, "active", "", { quickFilter: "drifted", summary }).map((r) => r.key),
    ["prod/web-02"],
  )
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
