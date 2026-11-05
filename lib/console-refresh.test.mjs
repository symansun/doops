import assert from "node:assert/strict"
import test from "node:test"
import {
  errorAfterRefresh,
  mapWithConcurrency,
  reconcileSelected,
  reconcileTargets,
  sameTarget,
  targetIdentity,
} from "./console-refresh.ts"

const base = {
  cluster: "doops-zyj", instance: "test", key: "doops-zyj/test", remote: "10.0.0.8:41234",
  connected_at: "2026-11-05T01:00:00Z", last_seen: "2026-11-05T01:10:00Z", busy: false, status: "idle",
  active_ops: 0, queued_ops: 0, resources: [], sessions: ["s1"],
}
const other = { ...base, instance: "prod", key: "doops-zyj/prod" }

test("Given a poll with the same target content, when it is reconciled, then the list and selected object are kept", () => {
  const previous = [base, other]
  const next = [structuredClone(base), structuredClone(other)]
  const targets = reconcileTargets(previous, next)
  assert.equal(targets, previous)
  assert.equal(reconcileSelected(base, targets), base)
})

test("Given a poll where the target changes, when it is reconciled, then only the changed target is replaced", () => {
  for (const [name, change] of [
    ["heartbeat", { last_seen: "2026-11-05T01:10:10Z" }],
    ["busy", { busy: true, busy_reason: "active_operation" }],
    ["sessions", { sessions: ["s1", "s2"] }],
    ["resources", { resources: ["helm:apps/web"] }],
    ["status", { status: "active", active_ops: 1 }],
  ]) {
    const next = [{ ...base, ...change }, structuredClone(other)]
    const targets = reconcileTargets([base, other], next)
    assert.notEqual(targets[0], base, name)
    assert.deepEqual(targets[0], next[0], name)
    assert.equal(targets[1], other, name)
    const selected = reconcileSelected(base, targets)
    assert.equal(selected, targets[0], name)
    assert.equal(targetIdentity(selected), targetIdentity(base), name)
  }
})

test("Given the selected target disappears or nothing is selected, when reconciled, then the first target is chosen", () => {
  assert.equal(reconcileSelected(base, [other]), other)
  assert.equal(reconcileSelected(null, [other, base]), other)
  assert.equal(reconcileSelected(base, []), null)
})

test("Given two targets, when compared, then sameTarget looks at every field the console shows", () => {
  assert.ok(sameTarget(base, structuredClone(base)))
  assert.ok(!sameTarget(base, { ...base, remote: "10.0.0.9:1" }))
  assert.ok(!sameTarget(base, { ...base, queued_ops: 2 }))
  assert.ok(!sameTarget(base, { ...base, sessions: undefined }))
  assert.ok(sameTarget({ ...base, resources: undefined }, { ...base, resources: [] }))
})

test("Given an operation error on screen, when a refresh runs, then only a user refresh clears it", () => {
  for (const [trigger, want] of [["background", "credential_secret_get_failed 的中文说明"], ["user", ""]]) {
    assert.equal(errorAfterRefresh("credential_secret_get_failed 的中文说明", trigger), want, trigger)
  }
})

test("Given many requests, when mapped with a limit, then at most that many run at once and order is kept", async () => {
  let running = 0
  let peak = 0
  const results = await mapWithConcurrency(Array.from({ length: 23 }, (_, index) => index), 4, async (item) => {
    running++
    peak = Math.max(peak, running)
    await new Promise((resolve) => setTimeout(resolve, item % 3))
    running--
    return item * 2
  })
  assert.equal(peak, 4)
  assert.deepEqual(results, Array.from({ length: 23 }, (_, index) => index * 2))
  assert.deepEqual(await mapWithConcurrency([], 4, async () => 1), [])
})

test("Given an aborted load, when mapped, then no further request starts and the abort is reported", async () => {
  const controller = new AbortController()
  const started = []
  const run = mapWithConcurrency(Array.from({ length: 10 }, (_, index) => index), 2, async (item) => {
    started.push(item)
    if (item === 1) controller.abort(new DOMException("view changed", "AbortError"))
    await new Promise((resolve) => setTimeout(resolve, 1))
    return item
  }, controller.signal)
  await assert.rejects(run, (err) => err.name === "AbortError")
  assert.ok(started.length <= 3, `started ${started.length}`)
})

test("Given one failing request, when mapped, then the failure is reported and no new request starts", async () => {
  const started = []
  await assert.rejects(mapWithConcurrency(Array.from({ length: 10 }, (_, index) => index), 2, async (item) => {
    started.push(item)
    if (item === 0) throw new Error("credential_metadata_unavailable")
    await new Promise((resolve) => setTimeout(resolve, 5))
    return item
  }), /credential_metadata_unavailable/)
  assert.ok(started.length <= 2, `started ${started.length}`)
})
