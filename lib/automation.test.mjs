import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import {
  backgroundTaskTabBadge,
  describeBackgroundTaskStatus,
  describeScheduleScopeForTarget,
  filterBackgroundTasks,
  filterRunsForTargetFallback,
  formatTaskDuration,
  isBackgroundTaskTerminal,
  matchesTaskStatusFilter,
  scheduleAppliesToTarget,
  scheduleExecutionKinds,
  scheduleIsInspectionPolicy,
  summarizeBackgroundTasks,
  validateAutomationScheduleInput,
} from "./automation.ts"
import {
  buildExplainTaskPrompt,
  composeBackgroundTaskCommand,
  parseAutomationCommand,
  pickHighlightEnvVars,
  summarizeTaskOutput,
} from "./automation-command.ts"

test("Given Gateway detached task statuses, When described, Then error maps to Chinese danger 失败 and running/unknown follow semantic colors", () => {
  assert.deepEqual(describeBackgroundTaskStatus("running"), {
    label: "运行中",
    variant: "primary",
    terminal: false,
  })
  assert.deepEqual(describeBackgroundTaskStatus("success"), {
    label: "成功",
    variant: "success",
    terminal: true,
  })
  assert.deepEqual(describeBackgroundTaskStatus("error"), {
    label: "失败",
    variant: "danger",
    terminal: true,
  })
  assert.deepEqual(describeBackgroundTaskStatus("failed"), {
    label: "失败",
    variant: "danger",
    terminal: true,
  })
  assert.deepEqual(describeBackgroundTaskStatus("timeout"), {
    label: "超时",
    variant: "danger",
    terminal: true,
  })
  assert.deepEqual(describeBackgroundTaskStatus("canceled"), {
    label: "已停止",
    variant: "default",
    terminal: true,
  })
  assert.deepEqual(describeBackgroundTaskStatus("outcome_unknown"), {
    label: "结果未知",
    variant: "warning",
    terminal: true,
  })
  assert.equal(isBackgroundTaskTerminal("running"), false)
  assert.equal(isBackgroundTaskTerminal("error"), true)
  assert.equal(isBackgroundTaskTerminal("outcome_unknown"), true)
})

test("Given a list of background tasks, When summarized and filtered, Then error/failed/timeout group under failed and search matches id, command, session, and task_id", () => {
  const tasks = [
    { audit_id: 929221, command_summary: "cd /root/ws/exam-lt && VUS=5 ./run.sh", session: "console-a", status: "success", task_id: "task-1" },
    { audit_id: 928319, command_summary: "cd /root/ws/exam-lt && VUS=1000 ./run.sh", session: "console-a", status: "error", task_id: "task-2" },
    { audit_id: 928201, command_summary: "sleep 3600", session: "console-b", status: "running", task_id: "task-3" },
    { audit_id: 928100, command_summary: "tar -czf out.tgz /var/log", session: "console-c", status: "timeout", task_id: "task-4" },
    { audit_id: 928050, command_summary: "pg_dump db", session: "console-d", status: "outcome_unknown", task_id: "task-5" },
    { audit_id: 928000, command_summary: "tail -f /var/log/syslog", session: "console-e", status: "canceled", task_id: "task-6" },
  ]

  assert.deepEqual(summarizeBackgroundTasks(tasks), {
    total: 6,
    running: 1,
    success: 1,
    failed: 2,
    canceled: 1,
    unknown: 1,
  })

  assert.equal(matchesTaskStatusFilter("error", "failed"), true)
  assert.equal(matchesTaskStatusFilter("timeout", "failed"), true)
  assert.equal(matchesTaskStatusFilter("success", "failed"), false)

  assert.deepEqual(
    filterBackgroundTasks(tasks, { statusFilter: "failed", query: "" }).map((t) => t.audit_id),
    [928319, 928100],
  )
  assert.deepEqual(
    filterBackgroundTasks(tasks, { statusFilter: "all", query: "#928319" }).map((t) => t.audit_id),
    [928319],
  )
  assert.deepEqual(
    filterBackgroundTasks(tasks, { statusFilter: "all", query: "vus=1000" }).map((t) => t.audit_id),
    [928319],
  )
  assert.deepEqual(
    filterBackgroundTasks(tasks, { statusFilter: "all", query: "task-5" }).map((t) => t.audit_id),
    [928050],
  )
  assert.deepEqual(
    filterBackgroundTasks(tasks, { statusFilter: "all", query: "console-e" }).map((t) => t.audit_id),
    [928000],
  )
})

test("Given start and finish timestamps, When formatting task duration, Then sub-second, seconds, minutes, hours, and live elapsed are readable", () => {
  const base = Date.parse("2026-11-26T10:00:00Z")
  assert.equal(formatTaskDuration("2026-11-26T10:00:00Z", "2026-11-26T10:00:00.400Z"), "<1s")
  assert.equal(formatTaskDuration("2026-11-26T10:00:00Z", "2026-11-26T10:00:42Z"), "42s")
  assert.equal(formatTaskDuration("2026-11-26T10:00:00Z", "2026-11-26T10:03:15Z"), "3m 15s")
  assert.equal(formatTaskDuration("2026-11-26T10:00:00Z", "2026-11-26T11:05:00Z"), "1h 05m")
  assert.equal(formatTaskDuration("2026-11-26T10:00:00Z", "2026-11-27T12:00:00Z"), "1d 2h")
  assert.equal(formatTaskDuration("2026-11-26T10:00:00Z", undefined, base + 90_000), "1m 30s")
  assert.equal(formatTaskDuration(undefined, undefined), "—")
  assert.equal(formatTaskDuration("invalid-date", undefined), "—")
})

test("Given monitor schedules and a selected target, When checking scope and execution badges, Then target matching and descriptions stay accurate", () => {
  const target = { cluster: "prod-cn", instance: "web-01" }
  const allOnline = { scope_mode: "all_online", targets: [], tags: [], command: "", prompt: "检查状态", skills: ["inspect-vm-host"] }
  const onlyThis = { scope_mode: "targets", targets: [{ cluster: "prod-cn", instance: "web-01" }], tags: [], command: "nginx -t", prompt: "", skills: [] }
  const multiWithThis = {
    scope_mode: "targets",
    targets: [
      { cluster: "prod-cn", instance: "web-01" },
      { cluster: "prod-cn", instance: "web-02" },
    ],
    tags: [],
    command: "df -h",
    prompt: "汇总磁盘",
    skills: ["inspect-vm-host", "inspect-app-service"],
  }
  const otherTarget = { scope_mode: "targets", targets: [{ cluster: "staging", instance: "db-01" }], tags: [], command: "pg_isready", prompt: "", skills: [] }
  const tagged = { scope_mode: "tags", targets: [], tags: ["env=prod"], command: "uptime", prompt: "", skills: [] }

  assert.equal(scheduleAppliesToTarget(allOnline, target), true)
  assert.equal(scheduleAppliesToTarget(onlyThis, target), true)
  assert.equal(scheduleAppliesToTarget(multiWithThis, target), true)
  assert.equal(scheduleAppliesToTarget(otherTarget, target), false)
  assert.equal(scheduleAppliesToTarget(tagged, target), false)
  assert.equal(scheduleAppliesToTarget(otherTarget, null), true)

  assert.equal(describeScheduleScopeForTarget(allOnline, target), "全部在线目标")
  assert.equal(describeScheduleScopeForTarget(onlyThis, target), "仅当前目标")
  assert.equal(describeScheduleScopeForTarget(multiWithThis, target), "指定目标 (2)")
  assert.equal(describeScheduleScopeForTarget(tagged, target), "按标签 (1)")

  assert.deepEqual(scheduleExecutionKinds(onlyThis), ["Shell 命令"])
  assert.deepEqual(scheduleExecutionKinds(allOnline), ["Skill: inspect-vm-host", "AI 提示词"])
  assert.deepEqual(scheduleExecutionKinds(multiWithThis), ["Shell 命令", "Skill (2)", "AI 提示词"])
})

test("Given schedule form inputs, When validated, Then missing name, missing target, empty target list, or empty actions are rejected", () => {
  assert.ok(validateAutomationScheduleInput({ name: "  ", command: "uptime", prompt: "", skills: [], scopeMode: "targets", hasTarget: true }))
  assert.ok(validateAutomationScheduleInput({ name: "检查", command: "uptime", prompt: "", skills: [], scopeMode: "targets", hasTarget: false }))
  assert.ok(validateAutomationScheduleInput({ name: "检查", command: "uptime", prompt: "", skills: [], scopeMode: "targets", hasTarget: true, selectedTargetsCount: 0 }))
  assert.ok(validateAutomationScheduleInput({ name: "检查", command: "  ", prompt: "", skills: [], scopeMode: "targets", hasTarget: true }))
  assert.equal(
    validateAutomationScheduleInput({ name: "Nginx 每日检测", command: "nginx -t", prompt: "", skills: [], scopeMode: "targets", hasTarget: false, selectedTargetsCount: 2 }),
    null,
  )
})

test("Given schedules and runs, When classifying inspection policy vs shell task and applying legacy target fallback, Then classification and fallback stay deterministic", () => {
  assert.equal(scheduleIsInspectionPolicy({ skills: ["inspect-vm-host"], prompt: "", command: "" }), true)
  assert.equal(scheduleIsInspectionPolicy({ skills: [], prompt: "检查端口与日志", command: "" }), true)
  assert.equal(scheduleIsInspectionPolicy({ skills: [], prompt: "", command: "nginx -t" }), false)
  assert.equal(scheduleIsInspectionPolicy({ skills: [], prompt: "校验配置", command: "nginx -t" }), false)

  const target = { cluster: "prod-cn", instance: "web-01" }
  const schedules = [
    { id: "sched_mine", scope_mode: "targets", targets: [target] },
    { id: "sched_other", scope_mode: "targets", targets: [{ cluster: "prod-cn", instance: "web-02" }] },
    { id: "sched_all", scope_mode: "all_online", targets: [] },
  ]
  const runs = [
    { id: "run_1", schedule_id: "sched_mine", trigger_kind: "scheduled", status: "success", total_targets: 1, total_assets: 1, passed_checks: 1, warning_checks: 0, failed_checks: 0, skipped_checks: 0, offline_targets: 0, metrics_written: 0, started_at_ms: 400, finished_at_ms: 410, duration_ms: 10 },
    { id: "run_2", schedule_id: "sched_other", trigger_kind: "scheduled", status: "success", total_targets: 1, total_assets: 1, passed_checks: 1, warning_checks: 0, failed_checks: 0, skipped_checks: 0, offline_targets: 0, metrics_written: 0, started_at_ms: 300, finished_at_ms: 310, duration_ms: 10 },
    { id: "run_3", schedule_id: "sched_all", trigger_kind: "scheduled", status: "success", total_targets: 2, total_assets: 2, passed_checks: 2, warning_checks: 0, failed_checks: 0, skipped_checks: 0, offline_targets: 0, metrics_written: 0, started_at_ms: 200, finished_at_ms: 210, duration_ms: 10 },
    { id: "run_4", schedule_id: "sched_deleted", trigger_kind: "scheduled", status: "success", total_targets: 1, total_assets: 1, passed_checks: 1, warning_checks: 0, failed_checks: 0, skipped_checks: 0, offline_targets: 0, metrics_written: 0, started_at_ms: 100, finished_at_ms: 110, duration_ms: 10 },
  ]

  // Legacy Gateway (targetFiltered omitted/false): filters out schedules explicitly scoped to other targets.
  assert.deepEqual(
    filterRunsForTargetFallback(runs, schedules, target, false).map((r) => r.id),
    ["run_1", "run_3", "run_4"],
  )
  // Upgraded Gateway (targetFiltered=true): trusts exact server-side monitor_run_items filter even if schedule scope changed later.
  assert.deepEqual(
    filterRunsForTargetFallback(runs, schedules, target, true).map((r) => r.id),
    ["run_1", "run_2", "run_3", "run_4"],
  )
})

test("Given complex shell commands from background tasks, When parsed, Then readable Chinese titles, workdir, steps, env vars, and diff highlights are extracted", () => {
  const cmd1 =
    "cd /root/ws/exam-lt-20261123 && BASE_URL=https://exam.edu.example EXAM_ID=5btGZIS4onw EXAM_CODE=LT1276 SUBJECT_ID=5btGapJVRui VUS=5 T0_AT=0 HOLD_SECONDS=90 LOGIN_SPREAD_SECONDS=30 k6 run exam.js"
  const cmd2 =
    "cd /root/ws/exam-lt-20261123 && BASE_URL=https://exam.edu.example EXAM_ID=5btGZIS4onw EXAM_CODE=LT1276 SUBJECT_ID=5btGapJVRui VUS=1000 START_AT=1795695178 LOGIN_SPREAD_SECONDS..."

  const p1 = parseAutomationCommand(cmd1)
  const p2 = parseAutomationCommand(cmd2)

  assert.equal(p1.title, "运行 k6 压测（exam.js）")
  assert.equal(p1.category, "压测脚本")
  assert.equal(p1.workdir, "/root/ws/exam-lt-20261123")
  assert.equal(p1.workdirName, "exam-lt-20261123")
  assert.equal(p1.envVars.length, 8)
  assert.equal(p1.maybeTruncated, false)

  assert.equal(p2.title, "在 exam-lt-20261123 中执行压测任务")
  assert.equal(p2.category, "压测脚本")
  assert.equal(p2.maybeTruncated, true)
  const startAt = p2.envVars.find((ev) => ev.key === "START_AT")
  assert.ok(startAt?.hint, "epoch START_AT should provide a human-readable time hint")

  // Cross-task diff highlights prioritize differing keys (VUS, START_AT / T0_AT / HOLD_SECONDS) over shared constants.
  const h1 = pickHighlightEnvVars([p1, p2], p1, 3)
  assert.deepEqual(
    h1.highlights.map((ev) => ev.key),
    ["VUS", "T0_AT", "HOLD_SECONDS"],
  )
  assert.equal(h1.remainingCount, 5)

  // Explicit `# <title>` comment takes precedence and masks sensitive env vars.
  const named = parseAutomationCommand("# 每日 Nginx 校验\ncd /etc/nginx && API_TOKEN=super-secret-value certbot renew --dry-run && nginx -t")
  assert.equal(named.customTitle, "每日 Nginx 校验")
  assert.equal(named.title, "每日 Nginx 校验")
  assert.equal(named.workdir, "/etc/nginx")
  assert.equal(named.envVars[0].masked, true)
  assert.equal(named.envVars[0].displayValue, "••••••")

  // Common ops commands get clean Chinese summaries without a custom title.
  assert.equal(parseAutomationCommand("certbot renew --dry-run --quiet && nginx -t").title, "演练检查 TLS 证书续期 → 校验 Nginx 配置")
  assert.equal(parseAutomationCommand("pg_dump campus_core -Fc -f /tmp/campus_core.dump").title, "备份 PostgreSQL 数据库 campus_core")
  assert.equal(parseAutomationCommand("tar -czf /var/backups/nginx-access.tgz /var/log/nginx").title, "归档压缩 nginx-access.tgz")
  assert.equal(parseAutomationCommand("nerdctl -n k8s.io image pull harbor.edu.example/apps/edu-portal:2.4.1").title, "拉取容器镜像 apps/edu-portal:2.4.1")
})

test("Given structured composer inputs, task output, and explain prompt, When composed or summarized, Then workdir escapes safely and secrets stay redacted", () => {
  const composed = composeBackgroundTaskCommand({
    title: "压测 1000 并发",
    workdir: "/root/ws/exam's dir",
    command: "VUS=1000 k6 run exam.js",
  })
  assert.equal(composed, "# 压测 1000 并发\ncd '/root/ws/exam'\\''s dir' && VUS=1000 k6 run exam.js")

  const outSummary = summarizeTaskOutput(
    "Task submitted.\nTaskID: task-e304\nPID: 17902\n[bench] upstream returned 502 Bad Gateway (threshold exceeded)\nTask failed.\nExitCode: 1\n",
    "error",
  )
  assert.equal(outSummary.exitCode, 1)
  assert.equal(outSummary.pid, "17902")
  assert.equal(outSummary.headline, "[bench] upstream returned 502 Bad Gateway (threshold exceeded)")

  const parsed = parseAutomationCommand("API_TOKEN=canary-secret-123 curl https://api.example/health")
  const prompt = buildExplainTaskPrompt(
    {
      audit_id: 99,
      status: "error",
      cluster: "prod-cn",
      instance: "web-01",
      command_summary: "API_TOKEN=canary-secret-123 curl https://api.example/health",
      output: "curl: (7) Failed to connect",
    },
    parsed,
    { exitCode: 7, headline: "curl: (7) Failed to connect" },
  )
  assert.ok(!prompt.includes("canary-secret-123"), "AI explain prompt must redact secret env values")
  assert.ok(prompt.includes("API_TOKEN=REDACTED"))
})

test("Given merged Automation workbench and background task badge helper, When inspected, Then Tab 3 badge only surfaces active running commands and duplicate wrappers/jargon stay removed (wiring lock)", () => {
  // Tab badge for 「一次性后台执行」 must NOT show a misleading "(1)" for already-finished historical tasks.
  assert.equal(backgroundTaskTabBadge(null), null)
  assert.equal(backgroundTaskTabBadge({ running: 0 }), null)
  assert.deepEqual(backgroundTaskTabBadge({ running: 2 }), { label: "运行中 2", variant: "primary" })

  const bgSrc = readFileSync(new URL("../components/background-tasks-panel.tsx", import.meta.url), "utf8")
  const autoSrc = readFileSync(new URL("../components/automation-panel.tsx", import.meta.url), "utf8")
  const workspaceSrc = readFileSync(new URL("../components/monitor-task-workspace.tsx", import.meta.url), "utf8")
  assert.ok(bgSrc.includes("describeBackgroundTaskStatus"), "background-tasks-panel must decode status via describeBackgroundTaskStatus")
  assert.ok(bgSrc.includes("parseAutomationCommand"), "background-tasks-panel must parse commands into human-readable summaries")
  assert.ok(bgSrc.includes("submitBackgroundTask"), "background-tasks-panel must allow direct task submission")
  assert.ok(autoSrc.includes('aria-label="自动化"'), "automation-panel section must be labeled 自动化")
  assert.ok(autoSrc.includes("backgroundTaskTabBadge"), "automation-panel must use backgroundTaskTabBadge for 一次性后台执行")
  assert.ok(autoSrc.includes("一次性后台执行"), "automation-panel must clearly label Tab 3 as 一次性后台执行")
  assert.ok(autoSrc.includes("执行记录"), "automation-panel must expose dedicated 执行记录 tab")
  assert.ok(!autoSrc.includes("配置巡检策略"), "automation-panel must not expose duplicate 配置巡检策略 button")
  assert.ok(workspaceSrc.includes("onlyConfiguredSchedules: true"), "定时任务 tab must only render configured schedules without mixed-in ad-hoc duplicates")
  assert.ok(!workspaceSrc.includes("批次"), "monitor-task-workspace must not use opaque 批次 jargon")
  assert.ok(workspaceSrc.includes("scheduleAppliesToTarget"), "monitor-task-workspace must filter schedules via scheduleAppliesToTarget")
  assert.ok(workspaceSrc.includes("filterRunsForTargetFallback"), "monitor-task-workspace must support legacy Gateway target fallback")
  assert.ok(workspaceSrc.includes("MonitorScheduleEditor"), "monitor-task-workspace must use shared MonitorScheduleEditor")
  assert.ok(workspaceSrc.includes("MonitorRunDetailView"), "monitor-task-workspace must render MonitorRunDetailView in place")
})
