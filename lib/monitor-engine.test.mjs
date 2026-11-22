import assert from "node:assert/strict"
import test from "node:test"
import {
  buildRunReportCSV,
  describeMonitorStatus,
  formatBytesCompact,
  formatIntervalLabel,
  formatPercent,
  formatRateBytes,
} from "./monitor-engine.ts"

test("Given monitor engine statuses, When described, Then labels and badge variants map cleanly", () => {
  assert.deepEqual(describeMonitorStatus("pass"), { label: "正常", variant: "success" })
  assert.deepEqual(describeMonitorStatus("warning"), { label: "告警", variant: "warning" })
  assert.deepEqual(describeMonitorStatus("partial"), { label: "部分完成", variant: "warning" })
  assert.deepEqual(describeMonitorStatus("critical"), { label: "严重", variant: "danger" })
  assert.deepEqual(describeMonitorStatus("offline"), { label: "离线", variant: "default" })
  assert.deepEqual(describeMonitorStatus("skipped"), { label: "跳过", variant: "outline" })
})

test("Given intervals, bytes, rates and percentages, When formatted, Then human-readable values are deterministic", () => {
  assert.equal(formatIntervalLabel(300), "每 5 分钟")
  assert.equal(formatIntervalLabel(3600), "每 1 小时")
  assert.equal(formatIntervalLabel(86400), "每天")
  assert.equal(formatIntervalLabel(604800), "每周")
  assert.equal(formatBytesCompact(0), "0 B")
  assert.equal(formatBytesCompact(2048), "2.00 KiB")
  assert.equal(formatRateBytes(1024), "1.00 KiB/s")
  assert.equal(formatPercent(42.34), "42.3%")
  assert.equal(formatPercent(null), "—")
})

test("Given a run detail payload, When exported as CSV, Then BOM, check rows and offline rows are serialized safely", () => {
  const csv = buildRunReportCSV({
    run: {
      id: "run_1",
      trigger_kind: "scheduled",
      status: "partial",
      total_targets: 2,
      total_assets: 2,
      passed_checks: 1,
      warning_checks: 0,
      failed_checks: 1,
      skipped_checks: 0,
      offline_targets: 1,
      metrics_written: 10,
      started_at_ms: 1,
      finished_at_ms: 2,
      duration_ms: 1,
    },
    strategies: [],
    items: [
      {
        id: 1,
        run_id: "run_1",
        cluster: "prod",
        instance: "web-01",
        asset_id: "srv-1",
        asset_name: "Web 01",
        asset_kind: "vm",
        strategy: "vm_host",
        collector_method: "agent_local",
        status: "pass",
        started_at_ms: 1,
        finished_at_ms: 2,
        duration_ms: 1,
        metrics: {},
        checks: [{ id: "host.uptime", name: "运行与重启", category: "availability", status: "pass", value: "3600s", threshold: ">=300s", detail: 'ok "quoted"' }],
      },
      {
        id: 2,
        run_id: "run_1",
        cluster: "prod",
        instance: "off-02",
        asset_id: "off-02",
        asset_name: "Off 02",
        asset_kind: "vm",
        strategy: "vm_host",
        collector_method: "agent_local",
        status: "offline",
        reason: "target_offline",
        started_at_ms: 1,
        finished_at_ms: 2,
        duration_ms: 1,
        metrics: {},
        checks: [],
      },
    ],
  })
  assert.ok(csv.startsWith("\uFEFFrun_id,"))
  assert.ok(csv.includes('"host.uptime"'))
  assert.ok(csv.includes('"ok ""quoted"""'))
  assert.ok(csv.includes('"target_offline"'))
})
