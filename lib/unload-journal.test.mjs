import assert from "node:assert/strict"
import test from "node:test"
import { CommandJournal, UNLOAD_WARNING } from "./console-persistence.ts"
import { installJournalUnloadHandlers } from "./unload-journal.ts"

const scope = { gateway: "https://doops.l8ai.cn", username: "ops-zhang" }
const start = { targetKey: "doops-zyj/zyj", sessionId: "console-abc12345", command: "sleep 20; echo qa-k25-done", source: "terminal" }
const NOW = new Date("2026-11-08T01:00:00.000Z")

function memoryStorage() {
  const data = new Map()
  return {
    data,
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: (key) => data.delete(key),
  }
}

function journal(storage) {
  return new CommandJournal(() => storage, () => NOW)
}

function fakeTarget() {
  const listeners = new Map()
  return {
    addEventListener(type, listener) {
      listeners.set(type, [...(listeners.get(type) ?? []), listener])
    },
    removeEventListener(type, listener) {
      listeners.set(type, (listeners.get(type) ?? []).filter((item) => item !== listener))
    },
    count(type) {
      return (listeners.get(type) ?? []).length
    },
    dispatch(type) {
      const event = { prevented: false, preventDefault() { this.prevented = true } }
      for (const listener of listeners.get(type) ?? []) listener(event)
      return event
    },
  }
}

// 可控定时器：不依赖真实 500ms。
function fakeTimers() {
  const pending = []
  let nextHandle = 1
  return {
    pending,
    set(callback, delayMs) {
      const handle = nextHandle++
      pending.push({ handle, callback, delayMs })
      return handle
    },
    clear(handle) {
      const index = pending.findIndex((item) => item.handle === handle)
      if (index >= 0) pending.splice(index, 1)
    },
    flush() {
      for (const item of pending.splice(0)) item.callback()
    },
  }
}

// K25：Chrome 真实顺序是 beforeunload → 被中断的 RPC 流 end() → pagehide。
// unloading 必须在 beforeunload 就置位，否则 end() 会把记录删掉，横幅不会出现。
test("Given a running command, When Chrome reloads in its real order, Then the record survives beforeunload and end", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const target = fakeTarget()
  const timers = fakeTimers()
  const uninstall = installJournalUnloadHandlers(target, commands, { timers })
  const id = commands.begin(scope, start)

  const event = target.dispatch("beforeunload")
  assert.equal(event.prevented, true, "the warning still blocks the reload")
  assert.equal(event.returnValue, UNLOAD_WARNING)
  assert.equal(commands.unloading, true, "beforeunload must set the flag before the aborted stream ends")

  commands.end(scope, id)
  target.dispatch("pagehide")
  timers.flush("pagehide cancelled the cancel-reload reset")
  assert.equal(commands.unloading, true, "a real navigation stays unloading")

  const nextPage = journal(storage)
  assert.deepEqual(nextPage.takeInterrupted(scope).map((record) => record.id), [id])
  uninstall()
})

test("Given a running command, When the user cancels the reload, Then the delayed reset keeps the page out of unloading and a normal end removes the record", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const target = fakeTarget()
  const timers = fakeTimers()
  const uninstall = installJournalUnloadHandlers(target, commands, { timers })
  const id = commands.begin(scope, start)

  target.dispatch("beforeunload")
  assert.equal(commands.unloading, true)
  assert.equal(timers.pending.length, 1, "a cancel-reload reset is scheduled")
  timers.flush()
  assert.equal(commands.unloading, false, "no pagehide means the page is still there")

  commands.end(scope, id)
  assert.deepEqual(commands.takeInterrupted(scope), [], "a command that ended normally is not reported")
  assert.equal(storage.data.size, 0)
  uninstall()
})

test("Given a reload then a bfcache restore, When pageshow fires, Then the flag is reset", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const target = fakeTarget()
  const timers = fakeTimers()
  const uninstall = installJournalUnloadHandlers(target, commands, { timers })
  const id = commands.begin(scope, start)

  target.dispatch("beforeunload")
  target.dispatch("pagehide")
  assert.equal(commands.unloading, true)
  target.dispatch("pageshow")
  assert.equal(commands.unloading, false)

  commands.end(scope, id)
  assert.deepEqual(commands.takeInterrupted(scope), [])
  uninstall()
})

test("Given no running command, When beforeunload fires, Then there is no warning, no flag and no reset", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const target = fakeTarget()
  const timers = fakeTimers()
  const uninstall = installJournalUnloadHandlers(target, commands, { timers })

  const event = target.dispatch("beforeunload")
  assert.equal(event.prevented, false)
  assert.equal(event.returnValue, undefined)
  assert.equal(commands.unloading, false)
  assert.equal(timers.pending.length, 0)
  uninstall()
})

test("Given installed handlers, When they are uninstalled, Then later events are ignored and the pending reset is cancelled", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const target = fakeTarget()
  const timers = fakeTimers()
  const uninstall = installJournalUnloadHandlers(target, commands, { timers, resetDelayMs: 25 })
  const id = commands.begin(scope, start)

  target.dispatch("beforeunload")
  assert.equal(timers.pending[0]?.delayMs, 25, "the caller's reset delay is used")
  uninstall()
  for (const type of ["beforeunload", "pagehide", "pageshow"]) {
    assert.equal(target.count(type), 0, `${type} listener is removed`)
  }
  assert.equal(timers.pending.length, 0, "the pending reset is cancelled on uninstall")

  commands.setUnloading(false)
  target.dispatch("beforeunload")
  assert.equal(commands.unloading, false, "uninstalled handlers no longer act")
  commands.end(scope, id)
})
