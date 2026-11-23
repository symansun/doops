import assert from "node:assert/strict"
import test from "node:test"
import {
  ARTIFACT_MISSING_NOTICE,
  DEFAULT_TREND_KEYS,
  buildMonitorMetricsPath,
  buildTrendGeometry,
  deltaDirection,
  diffHasChanges,
  formatDeltaPercent,
  formatMetricNumber,
  formatMetricValue,
  formatSignedNumber,
  pickTrendSeries,
  describeArtifactState,
  summarizeRunDiff,
} from "./monitor-metrics.ts"

const series = (key, values, unit = "%") => ({
  key,
  unit,
  label: key,
  points: values.map((value, i) => ({ at_ms: 1_700_000_000_000 + i * 60_000, value, run_id: "run_" + i })),
})

test("buildMonitorMetricsPath encodes skill and cluster/instance target", () => {
  const path = buildMonitorMetricsPath("inspect-vm-host", "prod-cn", "web/01", "24h")
  const qs = new URLSearchParams(path.slice(path.indexOf("?") + 1))
  assert.equal(path.startsWith("metrics?"), true)
  assert.equal(qs.get("skill"), "inspect-vm-host")
  assert.equal(qs.get("target"), "prod-cn/web/01")
  assert.equal(qs.get("range"), "24h")
})

test("pickTrendSeries prefers standard keys with enough points", () => {
  const picked = pickTrendSeries([series("z_custom", [1, 2, 3]), series("cpu_percent", [10, 12])])
  assert.equal(picked?.key, "cpu_percent")
  // 标准键只有 1 个点时退回点数最多的序列，避免画出无意义的单点趋势。
  const sparse = pickTrendSeries([series("cpu_percent", [10]), series("disk_percent", [1, 2, 3, 4])])
  assert.equal(sparse?.key, "disk_percent")
  assert.equal(pickTrendSeries([]), null)
  assert.equal(pickTrendSeries(null), null)
  assert.equal(DEFAULT_TREND_KEYS.includes("cpu_percent"), true)
})

test("buildTrendGeometry maps values into the viewport and keeps order", () => {
  const geometry = buildTrendGeometry(series("cpu_percent", [10, 20, 15]).points, 100, 40, 4)
  assert.equal(geometry.points.length, 3)
  assert.equal(geometry.min, 10)
  assert.equal(geometry.max, 20)
  const xs = geometry.points.map((p) => p.x)
  assert.deepEqual(xs, [...xs].sort((a, b) => a - b))
  for (const p of geometry.points) {
    assert.ok(p.x >= 4 && p.x <= 96, `x ${p.x} out of range`)
    assert.ok(p.y >= 4 && p.y <= 36, `y ${p.y} out of range`)
  }
  // 最大值应贴顶（y=padding），最小值贴底。
  assert.equal(geometry.points[1].y, 4)
  assert.equal(geometry.points[0].y, 36)
  assert.equal(geometry.path.split(" ").length, 3)
  assert.ok(geometry.areaPath.endsWith("Z"))
})

test("buildTrendGeometry degrades safely for flat, single and empty series", () => {
  const flat = buildTrendGeometry(series("cpu_percent", [7, 7, 7]).points, 100, 40, 4)
  assert.equal(new Set(flat.points.map((p) => p.y)).size, 1)
  const single = buildTrendGeometry(series("cpu_percent", [7]).points, 100, 40, 4)
  assert.equal(single.points.length, 1)
  assert.equal(buildTrendGeometry([], 100, 40, 4).path, "")
  assert.equal(buildTrendGeometry([{ at_ms: 1, value: Number.NaN }], 100, 40, 4).path, "")
})

test("metric formatting keeps units and avoids fake precision", () => {
  assert.equal(formatMetricNumber(12.3456), "12.35")
  assert.equal(formatMetricNumber(1234), "1,234")
  assert.equal(formatMetricValue(12.34, "%"), "12.34%")
  assert.equal(formatMetricValue(0.5, "核"), "0.5 核")
  assert.equal(formatMetricValue(null, "%"), "—")
  assert.equal(formatDeltaPercent(12.34), "+12.3%")
  assert.equal(formatDeltaPercent(-3), "-3%")
  assert.equal(formatDeltaPercent(null), null)
  assert.equal(formatSignedNumber(2.5), "+2.5")
  assert.equal(formatSignedNumber(-2.5), "-2.5")
  assert.equal(formatSignedNumber(0), "0")
})

test("deltaDirection is epsilon-stable", () => {
  assert.equal(deltaDirection(1), "up")
  assert.equal(deltaDirection(-1), "down")
  assert.equal(deltaDirection(1e-12), "flat")
  assert.equal(deltaDirection(null), "flat")
})

test("summarizeRunDiff and diffHasChanges describe the three diff states", () => {
  const diff = {
    previous_run_id: "run_prev",
    status_changed: false,
    metric_deltas: [
      { key: "cpu_percent", previous: 10, current: 15, delta: 5, delta_pct: 50 },
      { key: "errors", previous: 4, current: 2, delta: -2, delta_pct: -50 },
    ],
    added_checks: [{ id: "load", status: "pass" }],
    resolved_checks: [{ id: "tls", status: "pass" }, { id: "mem", status: "pass" }],
    changed_checks: [{ id: "disk", previous_status: "pass", current_status: "critical" }],
  }
  const summary = summarizeRunDiff(diff)
  assert.deepEqual(summary, { added: 1, resolved: 2, changed: 1, improved: 1, degraded: 1 })
  assert.equal(diffHasChanges(diff), true)
  assert.equal(diffHasChanges({ previous_run_id: "r", metric_deltas: [], added_checks: [], resolved_checks: [], changed_checks: [], status_changed: false }), false)
  assert.equal(diffHasChanges(null), false)
  assert.deepEqual(summarizeRunDiff(null), { added: 0, resolved: 0, changed: 0, improved: 0, degraded: 0 })
})

test("describeArtifactState maps missing, truncated and legacy-absent to the body decision", () => {
  // missing：正文区必须换成保留策略提示，且不把截断状态提升进来。
  const missing = describeArtifactState("missing", true)
  assert.equal(missing.missing, true)
  assert.equal(missing.showBody, false)
  assert.equal(missing.truncated, false)
  assert.equal(missing.notice, ARTIFACT_MISSING_NOTICE)
  assert.ok(missing.notice.includes("保留策略清理"), "提示要说明是保留策略清理")
  assert.ok(missing.notice.includes("默认保留 30 天"))
  assert.ok(missing.notice.includes("90 天"), "提示要说明指标趋势仍保留 90 天")
  assert.ok(missing.notice.includes("不代表本次采集失败"), "提示不能让用户误以为采集失败")

  // present + truncated：截断是 present 的子状态，正文照常渲染；
  // 截断提示由后端写进正文的 "... [truncated]"（或 JSON 信封）承担，不替换正文。
  const truncated = describeArtifactState("present", true)
  assert.equal(truncated.missing, false)
  assert.equal(truncated.showBody, true)
  assert.equal(truncated.notice, null)
  assert.equal(truncated.truncated, true)
  const truncatedBody = "巡检正文 ... [truncated]"
  assert.equal(truncated.showBody && truncatedBody.includes("[truncated]"), true)

  // 字段缺省（老响应）必须与 present 完全一致。
  const legacy = describeArtifactState(undefined, undefined)
  assert.deepEqual(legacy, describeArtifactState("present", false))
  assert.equal(legacy.missing, false)
  assert.equal(legacy.showBody, true)
  assert.equal(legacy.notice, null)
  assert.equal(legacy.truncated, false)
})
