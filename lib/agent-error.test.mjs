import assert from "node:assert/strict"
import test from "node:test"
import { AGENT_MODEL_MISSING_HINT, agentErrorMessage, createVisibilityScopedRuntime } from "./agent-error.ts"

test("agentErrorMessage drops the transport code and explains a missing model", () => {
  const cases = [
    { name: "missing model with connect prefix", raw: "[unavailable] Gateway 还没有可下发到该节点的模型", want: AGENT_MODEL_MISSING_HINT },
    { name: "missing model without prefix", raw: "Gateway 还没有可下发到该节点的模型", want: AGENT_MODEL_MISSING_HINT },
    { name: "other prefixed error keeps its text", raw: "[failed_precondition] 目标 Agent 不支持结构化 ACP 事件协议，必须升级 Agent", want: "目标 Agent 不支持结构化 ACP 事件协议，必须升级 Agent" },
    { name: "model connection down", raw: "[unavailable] DoAgent 模型连接不可用", want: "DoAgent 模型连接不可用" },
    { name: "plain message unchanged", raw: "智能体请求失败 (500)", want: "智能体请求失败 (500)" },
    { name: "bracket inside text is kept", raw: "请求 [abc] 失败", want: "请求 [abc] 失败" },
    { name: "prefix only", raw: "[unavailable]", want: "智能体连接失败" },
    { name: "null", raw: null, want: null },
    { name: "empty", raw: "  ", want: null },
  ]
  for (const c of cases) {
    assert.equal(agentErrorMessage(c.raw), c.want, c.name)
  }
  // DoAgent models come only from the Gateway-materialized modelProvider credential,
  // so the hint points at credentials, never at the config center's settings.json writer.
  assert.match(AGENT_MODEL_MISSING_HINT, /该目标还没有绑定 DoAgent 模型/)
  assert.match(AGENT_MODEL_MISSING_HINT, /「凭据」.*绑定并激活 modelProvider 凭据/)
  assert.doesNotMatch(AGENT_MODEL_MISSING_HINT, /配置中心/)
  assert.doesNotMatch(AGENT_MODEL_MISSING_HINT, /\[unavailable\]/)
})

function fakeRuntime(initial) {
  const listeners = new Set()
  let snapshot = initial
  return {
    calls: [],
    getSnapshot() { return snapshot },
    subscribe(_sessionId, listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    open(sessionId) { this.calls.push(["open", sessionId]); return Promise.resolve() },
    close(sessionId) { this.calls.push(["close", sessionId]) },
    set(next) {
      snapshot = next
      for (const listener of [...listeners]) listener()
    },
    listenerCount() { return listeners.size },
  }
}

test("a hidden assistant does not report its connection error", () => {
  const failed = { sessionId: "s", connection: "disconnected", status: "launching", error: AGENT_MODEL_MISSING_HINT }
  const healthy = { sessionId: "s", connection: "connected", status: "idle", error: null }
  const cases = [
    { name: "hidden failed session shows no error", visible: false, snapshot: failed, want: null },
    { name: "visible failed session shows the error", visible: true, snapshot: failed, want: AGENT_MODEL_MISSING_HINT },
    { name: "hidden healthy session unchanged", visible: false, snapshot: healthy, want: null },
    { name: "visible healthy session unchanged", visible: true, snapshot: healthy, want: null },
  ]
  for (const c of cases) {
    const source = fakeRuntime(c.snapshot)
    const { runtime } = createVisibilityScopedRuntime(source, c.visible)
    const got = runtime.getSnapshot("s")
    assert.equal(got.error, c.want, c.name)
    assert.equal(got.connection, c.snapshot.connection, c.name)
    if (!c.snapshot.error || c.visible) assert.equal(got, c.snapshot, `${c.name}: passes the source snapshot through`)
  }
})

test("visibility changes notify subscribers and keep snapshots stable", () => {
  const failed = { sessionId: "s", connection: "disconnected", error: "该目标还没有绑定模型" }
  const source = fakeRuntime(failed)
  const scoped = createVisibilityScopedRuntime(source, false)
  let notified = 0
  const unsubscribe = scoped.runtime.subscribe("s", () => { notified += 1 })

  const first = scoped.runtime.getSnapshot("s")
  assert.equal(first.error, null)
  assert.equal(scoped.runtime.getSnapshot("s"), first, "same object until the source changes")

  scoped.setVisible(false)
  assert.equal(notified, 0, "no-op visibility change does not notify")
  scoped.setVisible(true)
  assert.equal(notified, 1)
  assert.equal(scoped.runtime.getSnapshot("s").error, "该目标还没有绑定模型")

  scoped.setVisible(false)
  assert.equal(notified, 2)
  source.set({ ...failed, error: "另一条错误" })
  assert.equal(notified, 3, "source updates still reach subscribers")
  assert.equal(scoped.runtime.getSnapshot("s").error, null)

  unsubscribe()
  assert.equal(source.listenerCount(), 0)
  scoped.setVisible(true)
  assert.equal(notified, 3, "unsubscribed listener is not called")
})

test("the scoped runtime forwards lifecycle calls with stable bound methods", async () => {
  const source = fakeRuntime({ sessionId: "s", connection: "connecting", error: null })
  const { runtime } = createVisibilityScopedRuntime(source, false)
  assert.equal(runtime.open, runtime.open, "bound method identity is stable")
  await runtime.open("s")
  runtime.close("s")
  assert.deepEqual(source.calls, [["open", "s"], ["close", "s"]])
})
