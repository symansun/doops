import assert from "node:assert/strict"
import test from "node:test"
import {
  consoleScopeKey,
  loadSelection,
  restoreSelection,
  saveSelection,
  validSessionId,
} from "./console-persistence.ts"

function memoryStorage() {
  const data = new Map()
  return {
    data,
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: (key) => data.delete(key),
  }
}

const scope = { gateway: "https://doops.l8ai.cn", username: "ops-zhang" }
const zyj = { cluster: "doops-zyj", instance: "zyj", key: "doops-zyj/zyj" }
const test1 = { cluster: "doops-zyj", instance: "test", key: "doops-zyj/test" }

test("Given a saved selection, When targets load, Then the saved target and session are restored", () => {
  const storage = memoryStorage()
  saveSelection(storage, scope, { targetKey: zyj.key, sessionId: "console-abc12345" })
  const restored = restoreSelection(loadSelection(storage, scope), [test1, zyj])
  assert.equal(restored.selected, zyj)
  assert.equal(restored.sessionId, "console-abc12345")
  assert.equal(restored.notice, "")
})

test("Given the saved target is gone, When targets load, Then the first target is chosen with a new session and one notice", () => {
  for (const [name, targets, selected, notice] of [
    ["another target remains", [test1], test1, /doops-zyj\/zyj.*不在可用目标列表中.*已切换到 doops-zyj\/test/],
    ["no target remains", [], null, /doops-zyj\/zyj.*当前没有可用目标/],
  ]) {
    const storage = memoryStorage()
    saveSelection(storage, scope, { targetKey: zyj.key, sessionId: "console-abc12345" })
    const restored = restoreSelection(loadSelection(storage, scope), targets)
    assert.equal(restored.selected, selected, name)
    assert.equal(restored.sessionId, null, name)
    assert.match(restored.notice, notice, name)
  }
})

test("Given nothing saved, When targets load, Then the first target is chosen without a notice", () => {
  const restored = restoreSelection(loadSelection(memoryStorage(), scope), [test1, zyj])
  assert.deepEqual(restored, { selected: test1, sessionId: null, notice: "" })
})

test("Given another Gateway or user, When the selection is loaded, Then the other scope's selection is not used", () => {
  const storage = memoryStorage()
  saveSelection(storage, scope, { targetKey: zyj.key, sessionId: "console-abc12345" })
  for (const other of [
    { gateway: "https://other.example.test", username: "ops-zhang" },
    { gateway: scope.gateway, username: "ops-li" },
    { gateway: scope.gateway },
  ]) {
    assert.equal(loadSelection(storage, other), null, JSON.stringify(other))
  }
  assert.notEqual(consoleScopeKey({ gateway: "a|b", username: "c" }), consoleScopeKey({ gateway: "a", username: "b|c" }))
})

test("Given a stored value that is malformed or unsafe, When it is loaded, Then it is ignored", () => {
  for (const raw of [
    "not json",
    "[]",
    JSON.stringify({ targetKey: zyj.key }),
    JSON.stringify({ targetKey: zyj.key, sessionId: "../other" }),
    JSON.stringify({ targetKey: zyj.key, sessionId: "a/b" }),
    JSON.stringify({ targetKey: "", sessionId: "console-1" }),
    JSON.stringify({ targetKey: zyj.key, sessionId: 42 }),
  ]) {
    const storage = memoryStorage()
    storage.setItem(`doops.console.v1.selection:${consoleScopeKey(scope)}`, raw)
    assert.equal(loadSelection(storage, scope), null, raw)
  }
  assert.equal(loadSelection(null, scope), null)
})

test("Given a session id, When validated, Then only Gateway-safe ids pass", () => {
  for (const [value, want] of [
    ["console-abc12345", true],
    ["workload-x.y_z", true],
    ["", false],
    [" console", false],
    ["..", false],
    ["a/b", false],
    ["a\\b", false],
    ["a\nb", false],
    ["x".repeat(129), false],
  ]) {
    assert.equal(validSessionId(value), want, JSON.stringify(value))
  }
})

test("Given storage that throws, When the selection is saved or loaded, Then the console keeps working", () => {
  const broken = {
    getItem: () => { throw new Error("denied") },
    setItem: () => { throw new Error("quota") },
    removeItem: () => { throw new Error("denied") },
  }
  saveSelection(broken, scope, { targetKey: zyj.key, sessionId: "console-1" })
  assert.equal(loadSelection(broken, scope), null)
})

test("Given a selection, When it is saved, Then only the target key and session id are stored", () => {
  const storage = memoryStorage()
  saveSelection(storage, scope, { targetKey: zyj.key, sessionId: "console-abc12345", token: "synthetic-token" })
  const [value] = [...storage.data.values()]
  assert.deepEqual(JSON.parse(value), { targetKey: zyj.key, sessionId: "console-abc12345" })
})
