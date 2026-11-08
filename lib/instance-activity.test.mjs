import assert from "node:assert/strict"
import test from "node:test"
import {
  ACTIVE_INSTANCE_WINDOW_DAYS,
  OFFLINE_INSTANCE_RETENTION_DAYS,
  compareInstances,
  gatewayTime,
  groupInstances,
  instanceActivity,
  instanceClusterOptions,
  offlineLabel,
  summarizeInstanceScope,
} from "./instance-activity.ts"

const DAY = 24 * 60 * 60 * 1000
const NOW = Date.parse("2026-11-01T12:00:00Z")
const ago = (ms) => new Date(NOW - ms).toISOString()
const ZERO_TIME = "0001-01-01T00:00:00Z"

function instance(cluster, name, status, lastSeen) {
  return { cluster, instance: name, status, busy: false, active_ops: 0, queued_ops: 0, last_seen: lastSeen }
}

test("instance windows match the gateway retention contract", () => {
  assert.equal(ACTIVE_INSTANCE_WINDOW_DAYS, 7)
  assert.equal(OFFLINE_INSTANCE_RETENTION_DAYS, 90)
})

test("gateway times treat empty, zero and malformed values as missing", () => {
  for (const [value, expected] of [
    [undefined, null],
    ["", null],
    [ZERO_TIME, null],
    ["not-a-time", null],
    ["2026-11-01T12:00:00Z", NOW],
  ]) {
    assert.equal(gatewayTime(value), expected, String(value))
  }
})

test("instance activity uses a 7-day offline window, inclusive", () => {
  for (const [name, row, activity, label] of [
    ["online without heartbeat", instance("c", "a", "online", ZERO_TIME), "online", ""],
    ["offline 1 hour", instance("c", "a", "offline", ago(3600_000)), "recent", "离线不到 1 天"],
    ["offline 6 days", instance("c", "a", "offline", ago(6 * DAY)), "recent", "离线 6 天"],
    ["offline exactly 7 days", instance("c", "a", "offline", ago(7 * DAY)), "recent", "离线 7 天"],
    ["offline 7 days + 1 ms", instance("c", "a", "offline", ago(7 * DAY + 1)), "history", "离线 7 天"],
    ["offline 100 days", instance("c", "a", "offline", ago(100 * DAY)), "history", "离线 100 天"],
    ["offline without last_seen", instance("c", "a", "offline", undefined), "history", "离线时间未知"],
    ["offline with zero last_seen", instance("c", "a", "offline", ZERO_TIME), "history", "离线时间未知"],
    ["heartbeat slightly in the future", instance("c", "a", "offline", ago(-5000)), "recent", "离线不到 1 天"],
  ]) {
    assert.equal(instanceActivity(row, NOW), activity, name)
    assert.equal(offlineLabel(row, NOW), label, name)
  }
})

test("instances sort online first, then by last_seen descending, missing heartbeats last", () => {
  const rows = [
    instance("b", "missing", "offline", undefined),
    instance("a", "old", "offline", ago(40 * DAY)),
    instance("z", "online-old", "online", ago(2 * DAY)),
    instance("a", "fresh", "offline", ago(1 * DAY)),
    instance("a", "online-new", "online", ago(0)),
    instance("a", "missing", "offline", ZERO_TIME),
  ]
  assert.deepEqual(
    [...rows].sort(compareInstances).map((row) => `${row.cluster}/${row.instance}`),
    ["a/online-new", "z/online-old", "a/fresh", "a/old", "a/missing", "b/missing"],
  )
})

test("instances split into active and history groups, optionally filtered by cluster", () => {
  const rows = [
    instance("prod", "web-01", "online", ago(0)),
    instance("prod", "web-02", "offline", ago(3 * DAY)),
    instance("prod", "web-03", "offline", ago(30 * DAY)),
    instance("proxy-a-20260913", "p1", "offline", ago(50 * DAY)),
    instance("probe", "x", "offline", undefined),
  ]
  for (const [cluster, active, history] of [
    ["", ["web-01", "web-02"], ["web-03", "p1", "x"]],
    ["prod", ["web-01", "web-02"], ["web-03"]],
    ["proxy-a-20260913", [], ["p1"]],
    ["missing", [], []],
  ]) {
    const groups = groupInstances(rows, NOW, cluster)
    assert.deepEqual(groups.active.map((row) => row.instance), active, `cluster=${cluster}`)
    assert.deepEqual(groups.history.map((row) => row.instance), history, `cluster=${cluster}`)
  }
  assert.deepEqual(summarizeInstanceScope(rows, NOW), { active: 2, online: 1, history: 3, archived: 0 })
  assert.deepEqual(summarizeInstanceScope([], NOW), { active: 0, online: 0, history: 0, archived: 0 })
  assert.deepEqual(instanceClusterOptions(rows), [
    { name: "probe", online: 0, total: 1 },
    { name: "prod", online: 1, total: 3 },
    { name: "proxy-a-20260913", online: 0, total: 1 },
  ])
})


test("archived instances remain discoverable without counting as active or historical", () => {
  const archived = { ...instance("prod", "old", "offline", ago(30 * DAY)), archived: true }
  assert.equal(instanceActivity(archived, NOW), "archived")
  assert.deepEqual(groupInstances([archived], NOW), { active: [], history: [], archived: [archived] })
  assert.deepEqual(summarizeInstanceScope([archived], NOW), { active: 0, online: 0, history: 0, archived: 1 })
})
