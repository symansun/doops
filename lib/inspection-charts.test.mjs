import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import {
  GAUGE_SWEEP_DEGREES,
  formatChartValue,
  gaugeArcPath,
  inspectionChartTone,
  mapChartPointsToViewBox,
  normalizeInspectionCharts,
} from "./inspection-charts.ts"

const here = dirname(fileURLToPath(import.meta.url))
const repoRoot = join(here, "..")

function readFixture() {
  return JSON.parse(readFileSync(join(here, "testdata", "inspection-charts-vm-host.json"), "utf8"))
}

function readViewerSource() {
  return readFileSync(join(repoRoot, "components", "inspection-artifact-viewer.tsx"), "utf8")
}

// ---------- 真实产物：inspect-vm-host 在容器里真跑一次截下的 charts ----------

test("Given the real inspect-vm-host artifact, When normalized, Then it becomes one 100% bar with three series", () => {
  const fixture = readFixture()
  const charts = normalizeInspectionCharts(fixture)
  assert.equal(charts.length, 1)
  const chart = charts[0]
  assert.equal(chart.kind, "bar")
  assert.equal(chart.title, "主机核心资源水位")
  assert.equal(chart.unit, "%")
  assert.equal(chart.max, 100)
  assert.equal(chart.maxSource, "declared")
  assert.deepEqual(
    chart.series.map((point) => point.label),
    ["CPU", "内存", "磁盘 (/p)"],
  )
  assert.deepEqual(
    chart.series.map((point) => point.tone),
    ["success", "success", "warning"],
  )
  for (const point of chart.series) {
    assert.equal(point.overMax, false)
    assert.ok(point.ratio >= 0 && point.ratio <= 1)
  }
})

test("Given the real artifact, Then chart values equal the metrics of that same run (no fixture drift)", () => {
  const fixture = readFixture()
  const chart = normalizeInspectionCharts(fixture)[0]
  const metricByKey = new Map(fixture.metrics.map((metric) => [metric.key, metric]))
  assert.equal(chart.series[0].value, metricByKey.get("cpu_percent").value)
  assert.equal(chart.series[1].value, metricByKey.get("memory_percent").value)
  assert.equal(chart.series[2].value, metricByKey.get("disk_percent").value)
  assert.equal(chart.series[2].status, metricByKey.get("disk_percent").status)
})

// ---------- bar ----------

test("Given a bar value above max, Then the ratio saturates at 1 and the series is flagged overMax", () => {
  const [chart] = normalizeInspectionCharts([
    { type: "bar", max: 50, series: [{ label: "a", value: 80 }, { label: "b", value: 50 }] },
  ])
  assert.equal(chart.series[0].ratio, 1)
  assert.equal(chart.series[0].overMax, true)
  assert.equal(chart.series[1].ratio, 1)
  assert.equal(chart.series[1].overMax, false)
})

test("Given a bar without max, Then max falls back to the largest series value", () => {
  const [chart] = normalizeInspectionCharts([{ type: "bar", series: [{ label: "a", value: 4 }, { label: "b", value: 16 }] }])
  assert.equal(chart.max, 16)
  assert.equal(chart.maxSource, "series")
  assert.deepEqual(chart.series.map((point) => point.ratio), [0.25, 1])
})

test("Given an unusable max, Then it is treated as missing instead of dividing by zero", () => {
  for (const max of [0, -5, "100", null, Number.NaN]) {
    const [chart] = normalizeInspectionCharts([{ type: "bar", max, series: [{ label: "a", value: 0 }] }])
    assert.equal(chart.maxSource, "series")
    assert.equal(chart.max, 1)
    assert.equal(chart.series[0].ratio, 0)
  }
})

// ---------- gauge ----------

test("Given a gauge, Then each series becomes a 180° arc scaled by its ratio", () => {
  const [chart] = normalizeInspectionCharts([
    { type: "gauge", title: "水位", unit: "%", max: 100, series: [{ label: "CPU", value: 25, status: "pass" }, { label: "磁盘", value: 130, status: "critical" }] },
  ])
  assert.equal(chart.kind, "gauge")
  assert.equal(chart.arcs.length, 2)
  assert.equal(chart.arcs[0].startAngle, 180)
  assert.equal(chart.arcs[0].endAngle, 180 + 0.25 * GAUGE_SWEEP_DEGREES)
  assert.equal(chart.arcs[1].ratio, 1)
  assert.equal(chart.arcs[1].overMax, true)
  assert.equal(chart.arcs[1].endAngle, 180 + GAUGE_SWEEP_DEGREES)
  assert.equal(chart.arcs[1].tone, "danger")
})

// ---------- line ----------

test("Given a line chart, Then points are evenly spaced in 0..100 and the polyline stays inside the box", () => {
  const [chart] = normalizeInspectionCharts([
    { type: "line", unit: "ms", series: [{ label: "t1", value: 10 }, { label: "t2", value: 20 }, { label: "t3", value: 5 }] },
  ])
  assert.equal(chart.kind, "line")
  assert.equal(chart.max, 20)
  assert.deepEqual(chart.points.map((point) => point.x), [0, 50, 100])
  assert.deepEqual(chart.points.map((point) => point.y), [50, 0, 75])
  assert.deepEqual(chart.ticks, ["t1", "t2", "t3"])
  assert.equal(chart.polyline, "0,50 50,0 100,75")
  assert.equal(mapChartPointsToViewBox(chart, 200, 40), "0,20 100,0 200,30")
  for (const point of chart.points) {
    assert.ok(point.x >= 0 && point.x <= 100 && point.y >= 0 && point.y <= 100)
  }
})

test("Given a single-point line, Then the point is centered instead of producing NaN", () => {
  const [chart] = normalizeInspectionCharts([{ type: "line", max: 10, series: [{ label: "only", value: 5 }] }])
  assert.equal(chart.points.length, 1)
  assert.equal(chart.points[0].x, 50)
  assert.equal(chart.points[0].y, 50)
  assert.equal(chart.polyline, "50,50")
})

// ---------- 降级与容错 ----------

test("Given non-numeric series values, Then they are skipped rather than drawn as zero", () => {
  const [chart] = normalizeInspectionCharts([
    { type: "bar", max: 10, series: [{ label: "good", value: 5 }, { label: "text", value: "5" }, { label: "null", value: null }, { label: "nan", value: Number.NaN }] },
  ])
  assert.equal(chart.series.length, 1)
  assert.equal(chart.series[0].label, "good")
  for (const point of chart.series) assert.ok(Number.isFinite(point.value))
})

test("Given an unknown type or empty series, Then it is skipped without throwing", () => {
  assert.deepEqual(normalizeInspectionCharts([{ type: "pie", series: [{ label: "a", value: 1 }] }]), [])
  assert.deepEqual(normalizeInspectionCharts([{ type: "bar", series: [] }]), [])
  assert.deepEqual(normalizeInspectionCharts([{ type: "bar", series: [{ label: "a", value: "x" }] }]), [])
  assert.deepEqual(normalizeInspectionCharts([{ type: "bar" }, { type: 42 }, {}, null, "bar"]), [])
  assert.deepEqual(normalizeInspectionCharts({}), [])
  assert.deepEqual(normalizeInspectionCharts(undefined), [])
})

test("Given statuses, Then the colour comes from status only and unknown statuses stay neutral", () => {
  assert.equal(inspectionChartTone("pass"), "success")
  assert.equal(inspectionChartTone("WARNING"), "warning")
  assert.equal(inspectionChartTone("critical"), "danger")
  assert.equal(inspectionChartTone("unknown"), "neutral")
  assert.equal(inspectionChartTone(undefined), "neutral")
  const [chart] = normalizeInspectionCharts([
    { type: "bar", max: 10, series: [{ label: "low", value: 1, status: "critical" }, { label: "high", value: 9 }] },
  ])
  assert.equal(chart.series[0].tone, "danger")
  assert.equal(chart.series[1].tone, "neutral")
})

test("Given a gauge arc, Then the path is empty at ratio 0 and sweeps clockwise through the top", () => {
  assert.equal(gaugeArcPath(50, 50, 40, 180, 180), "")
  const quarter = gaugeArcPath(50, 50, 40, 180, 225)
  assert.match(quarter, /^M 10 50 A 40 40 0 0 1 /)
  const full = gaugeArcPath(50, 50, 40, 180, 360)
  assert.match(full, /^M 10 50 A 40 40 0 0 1 90 50$/)
})

test("Given chart numbers, Then labels keep one decimal at most and integers stay clean", () => {
  assert.equal(formatChartValue(90.22, "%"), "90.22%")
  assert.equal(formatChartValue(100, "%"), "100%")
  assert.equal(formatChartValue(54.6), "54.6")
  assert.equal(formatChartValue(Number.NaN, "%"), "—")
})

// ---------- 源码接线锁 ----------

test("Given the viewer source, Then it renders charts between the trend strip and the JSON tree", () => {
  const source = readViewerSource()
  assert.match(source, /from "@\/lib\/inspection-charts"/, "viewer 必须从 lib/inspection-charts 引入图表原语")
  assert.match(source, /normalizeInspectionCharts\(/, "viewer 必须用 normalizeInspectionCharts 归一化 charts[]")
  assert.match(source, /data-inspection-charts/, "图表区必须有稳定的 data 属性，便于浏览器实测定位")
  const chartsAt = source.indexOf("<InspectionChartsSection")
  const trendAt = source.indexOf("<TrendSection")
  // 正文树在 missing 与 present 两个分支各出现一次，图表区插在 present 分支里那棵之前。
  const jsonAt = source.lastIndexOf("<DynamicJsonNode data={jsonData} />")
  assert.ok(chartsAt > 0, "viewer 必须渲染 <InspectionChartsSection")
  assert.ok(trendAt > 0 && trendAt < chartsAt, "图表区必须在趋势条之后")
  assert.ok(jsonAt > 0 && chartsAt < jsonAt, "图表区必须在 DynamicJsonNode 之前")
})
