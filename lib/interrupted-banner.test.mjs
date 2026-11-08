import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { CommandJournal, restoreSelection } from "./console-persistence.ts"
import { interruptedBannerNotices, interruptedBannerView } from "./interrupted-banner.ts"

const scope = { gateway: "https://gateway.example.com", username: "ops-zhang" }
const NOW = new Date("2026-11-08T00:00:00.000Z")
const record = {
  id: "cmd_1",
  targetKey: "doops-zyj/zyj",
  sessionId: "console-abc12345",
  summary: "sleep …",
  startedAt: NOW.toISOString(),
  source: "terminal",
}

function memoryStorage() {
  const data = new Map()
  return {
    data,
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: (key) => data.delete(key),
  }
}

function source(file) {
  return readFileSync(new URL(file, import.meta.url), "utf8")
}

// K25：刷新恢复的第一处 /api/targets 可能返回空，restored.selected 为 null；
// 提示仍然必须生成，否则记录被消费后用户什么都看不到。
test("Given a reload left a command, When the target list is empty or the target is restored, Then the unknown-outcome notice is produced either way", () => {
  for (const [name, restored] of [
    ["first target list is empty", { selected: null, sessionId: null }],
    ["target and session restored", { selected: { key: "doops-zyj/zyj" }, sessionId: "console-abc12345" }],
  ]) {
    const notices = interruptedBannerNotices([record], restored)
    assert.equal(notices.length, 1, name)
    assert.match(notices[0], /结果未知/, name)
    assert.match(notices[0], /请先核对/, name)
    assert.match(notices[0], /sleep …/, name)
  }
  assert.deepEqual(interruptedBannerNotices([], { selected: null, sessionId: null }), [])
})

test("Given a record drained from a reload, When the banner view is built, Then it is an alert with a dismiss label and stays until dismissed", () => {
  assert.equal(interruptedBannerView([]), null)
  const notices = interruptedBannerNotices([record], { selected: null, sessionId: null })
  const view = interruptedBannerView(notices)
  assert.equal(view.role, "alert")
  assert.deepEqual(view.notices, notices)
  assert.ok(view.dismissLabel, "the banner needs a dismiss label")
  // 可多次渲染：再次构建得到同样的内容，调用方不重建就不会丢提示。
  assert.deepEqual(interruptedBannerView(view.notices), view)
})

test("Given a command left in sessionStorage, When the page reloads, Then the notice outlives the consumed record", () => {
  const storage = memoryStorage()
  const before = new CommandJournal(() => storage, () => NOW)
  before.begin(scope, { targetKey: record.targetKey, sessionId: record.sessionId, command: "sleep 20", source: "terminal" })

  // 刷新：新页面取出记录（存储里的副本被移除），提示留在页面状态里。
  const records = new CommandJournal(() => storage, () => NOW).takeInterrupted(scope)
  assert.equal(records.length, 1)
  const notices = interruptedBannerNotices(records, { selected: null, sessionId: null })
  assert.equal(notices.length, 1)
  assert.equal(storage.data.size, 0, "the record is drained once")

  // 记录被消费不等于提示被丢弃：横幅仍拿得到同样的文本，直到用户关闭。
  assert.equal(interruptedBannerView(notices).notices.length, 1)
})

// 结构断言：提示必须在 shell 顶层、终端标签页分支之前渲染，且终端面板不再持有它，
// 否则 K25 的「只有打开终端页才可见」回归会重现。
test("Given the console shell, When a reload leaves a command unfinished, Then the banner renders at the top level and the terminal panel no longer owns the notices", () => {
  const shell = source("../components/console-shell.tsx")
  const terminal = source("../components/terminal-panel.tsx")
  const bannerAt = shell.indexOf("interruptedBannerView(interruptedNotices)")
  const terminalTabAt = shell.indexOf('tab === "terminal"')
  assert.ok(bannerAt > -1, "console-shell renders the interrupted banner")
  assert.ok(terminalTabAt > -1, "console-shell renders the terminal tab")
  assert.ok(bannerAt < terminalTabAt, "the banner renders before the terminal tab branch, so it does not depend on the tab")
  assert.doesNotMatch(shell, /notices=\{interruptedNotices\}/)
  assert.doesNotMatch(terminal, /notices\?: readonly string\[\]/)
  assert.doesNotMatch(terminal, /onNoticesShown/)
})

// K25 已通过的一半：密码登录刷新后目标与 session 仍然恢复。
test("Given a saved selection, When the page reloads, Then the target and session are still restored", () => {
  const zyj = { cluster: "doops-zyj", instance: "zyj", key: "doops-zyj/zyj" }
  const other = { cluster: "doops-zyj", instance: "test", key: "doops-zyj/test" }
  const restored = restoreSelection({ targetKey: "doops-zyj/zyj", sessionId: "console-abc12345" }, [other, zyj])
  assert.equal(restored.selected.key, "doops-zyj/zyj")
  assert.equal(restored.sessionId, "console-abc12345")
  assert.equal(restored.notice, "")
})
