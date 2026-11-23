import assert from "node:assert/strict"
import test from "node:test"
import { AGENT_BUSY_TEXT, agentBusyView, agentTurnRunning } from "./agent-busy-banner.ts"

const running = { status: "running", items: [], interactions: [] }
const idle = { status: "idle", items: [{ kind: "message", role: "assistant", status: "complete" }], interactions: [] }

test("运行中的回合在其他标签页给出停止入口", () => {
  assert.deepEqual(agentBusyView(running, "terminal"), {
    text: AGENT_BUSY_TEXT,
    stopLabel: "停止 AI 任务",
    openLabel: "查看",
  })
})

test("停在 AI 助手时不重复提示：输入框自带停止按钮", () => {
  assert.equal(agentBusyView(running, "ask"), null)
})

test("待处理的用户消息也算运行中", () => {
  const pending = { status: "ready", items: [{ kind: "message", role: "user", status: "pending" }], interactions: [] }
  assert.equal(agentTurnRunning(pending), true)
  assert.ok(agentBusyView(pending, "overview"))
})

test("等待用户处理的交互也会挡住其他页面", () => {
  const needsInput = {
    status: "ready",
    items: [],
    interactions: [{ status: "pending" }, { status: "complete" }],
  }
  assert.equal(agentTurnRunning(needsInput), true)
  assert.equal(agentTurnRunning({ ...needsInput, interactions: [{ status: "running" }] }), true)
})

test("空闲会话不显示提示", () => {
  assert.equal(agentTurnRunning(idle), false)
  assert.equal(agentBusyView(idle, "terminal"), null)
  assert.equal(agentBusyView(null, "terminal"), null)
  assert.equal(agentBusyView(undefined, "overview"), null)
})
