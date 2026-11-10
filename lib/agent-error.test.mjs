import assert from "node:assert/strict"
import test from "node:test"
import { AGENT_MODEL_MISSING_HINT, agentErrorMessage, classifyAgentAdmission, createVisibilityScopedRuntime } from "./agent-error.ts"

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

// One classifier for every page: a page never parses protocol text itself, and the retry policy is
// part of that classifier rather than something each page guesses.
test("the admission classifier maps every wire category and its retry policy", () => {
  const cases = [
    { name: "queued, no transport prefix", raw: "doagent_prompt_queued", want: { kind: "queued", retryable: true, needsMutationRecheck: false } },
    { name: "queued after a transport prefix", raw: "[unavailable] doagent_prompt_queued", want: { kind: "queued", retryable: true, needsMutationRecheck: false } },
    { name: "queued as the CLI relays it", raw: "remote error: doagent_prompt_queued", want: { kind: "queued", retryable: true, needsMutationRecheck: false } },
    // An older Gateway wraps a pre-admission answer in the unknown outcome. That fail-closed
    // mutation gate wins over the inner token: never retry, always recheck the target.
    { name: "unknown wrapper beats queued", raw: "-32008 execution_outcome_unknown: remote error: doagent_prompt_queued", want: { kind: "unknown", retryable: false, needsMutationRecheck: true } },
    { name: "unknown wrapper beats queued without the RPC code", raw: "execution_outcome_unknown: remote error: doagent_prompt_queued", want: { kind: "unknown", retryable: false, needsMutationRecheck: true } },
    { name: "queue cleanup was never confirmed", raw: "doagent_prompt_queue_cleanup_unknown", want: { kind: "queue_cleanup_unknown", retryable: false, needsMutationRecheck: true } },
    { name: "queue cleanup relayed by the CLI", raw: "remote error: doagent_prompt_queue_cleanup_unknown: queue entry may still run", want: { kind: "queue_cleanup_unknown", retryable: false, needsMutationRecheck: true } },
    { name: "unknown wrapper beats cleanup", raw: "execution_outcome_unknown: doagent_prompt_queue_cleanup_unknown", want: { kind: "unknown", retryable: false, needsMutationRecheck: true } },
    { name: "empty prompt", raw: "doagent_prompt_no_prompt", want: { kind: "no_prompt", retryable: false, needsMutationRecheck: false } },
    { name: "empty prompt rejected before admission", raw: "-32602 doagent_prompt_no_prompt", want: { kind: "no_prompt", retryable: false, needsMutationRecheck: false } },
    { name: "pre-admission rejection", raw: "doagent_prompt_rejected", want: { kind: "rejected", retryable: false, needsMutationRecheck: false } },
    { name: "rejection relayed as a remote error", raw: "remote error: doagent_prompt_rejected", want: { kind: "rejected", retryable: false, needsMutationRecheck: false } },
    { name: "outcome unknown", raw: "execution_outcome_unknown", want: { kind: "unknown", retryable: false, needsMutationRecheck: true } },
    { name: "missing prompt turn id", raw: "-32008 execution_outcome_unknown: prompt_turn_id_missing", want: { kind: "unknown", retryable: false, needsMutationRecheck: true } },
    { name: "apply outcome unknown from the receipt code", raw: "Gateway 已断开，远端终态未确认", code: "doops_apply_outcome_unknown", want: { kind: "unknown", retryable: false, needsMutationRecheck: true } },
    { name: "Gateway busy answer is not an admission category", raw: "target busy: dev/node", want: { kind: "other", retryable: false, needsMutationRecheck: false } },
    { name: "Chinese legacy wording is not a wire category", raw: "缺少 DoAgent turn_finished 终态，执行结果未确认", want: { kind: "other", retryable: false, needsMutationRecheck: false } },
    { name: "another failure", raw: "doagent_execution_failed", want: { kind: "other", retryable: false, needsMutationRecheck: false } },
    { name: "a longer token does not match a shorter category", raw: "doagent_prompt_queued_extra", want: { kind: "other", retryable: false, needsMutationRecheck: false } },
    { name: "empty", raw: "", want: { kind: "other", retryable: false, needsMutationRecheck: false } },
    { name: "null", raw: null, want: { kind: "other", retryable: false, needsMutationRecheck: false } },
  ]
  for (const c of cases) {
    assert.deepEqual(classifyAgentAdmission(c.raw, c.code), c.want, c.name)
  }
  // The two policies a page must never confuse: queued may retry (new session), unknown may not.
  assert.equal(classifyAgentAdmission("doagent_prompt_queued").retryable, true)
  assert.equal(classifyAgentAdmission("execution_outcome_unknown: remote error: doagent_prompt_queued").retryable, false)
  assert.equal(classifyAgentAdmission("execution_outcome_unknown: remote error: doagent_prompt_queued").needsMutationRecheck, true)
  // The exact wire categories the Gateway emits (backend/agent/internal/server/doagent_prompt_admission.go).
  for (const category of ["doagent_prompt_queued", "doagent_prompt_queue_cleanup_unknown", "doagent_prompt_no_prompt", "doagent_prompt_rejected"]) {
    assert.notEqual(classifyAgentAdmission(category).kind, "other", category)
  }
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
