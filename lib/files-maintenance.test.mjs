import assert from "node:assert/strict"
import test from "node:test"
import { callTool } from "./client.ts"
import { demoCallTool } from "./demo.ts"
import {
  cleanErrorText,
  cleanResultText,
  cleanWorkspaceRequest,
  EMPTY_MAINTENANCE_LOG,
  maintenanceFailure,
  maintenanceOutput,
} from "./files-maintenance.ts"

const sessionId = "console-6lacl52e"

// Mirrors FilesPanel.cleanWorkspace: every event and the thrown error go through the same reducers.
async function runLikePanel(call) {
  let log = EMPTY_MAINTENANCE_LOG
  try {
    await call((ev) => {
      if (ev.type === "output") log = maintenanceOutput(log, ev.data)
      else if (ev.type === "result") log = maintenanceOutput(log, cleanResultText(ev.result.content[0].text))
      else if (ev.type === "error") log = maintenanceFailure(log, ev.error)
    })
  } catch (err) {
    log = maintenanceFailure(log, err.message)
  }
  return log
}

test("clean workspace sends both session_id and workspace for the current session", () => {
  assert.deepEqual(cleanWorkspaceRequest(sessionId), { session_id: sessionId, workspace: sessionId })
  for (const bad of ["", " console-a", "../console-a", "console/a", "console\\a", ".hidden", "a..b"]) {
    assert.throws(() => cleanWorkspaceRequest(bad), { message: /^当前会话无效，无法清理工作区$/ }, JSON.stringify(bad))
  }
})

test("Agent errors and results are shown in Chinese; unknown text is kept", () => {
  for (const [raw, expected] of [
    ['unsafe session id: ""', '工作区会话无效：""'],
    ["invalid clean workspace params: workspace", "清理参数格式无效：workspace"],
    ["Gateway authentication rejected", "Gateway authentication rejected"],
  ]) {
    assert.equal(cleanErrorText(raw), expected, raw)
  }
  assert.equal(cleanResultText(`Cleaned workspace and bare repo: ${sessionId}`), `已删除工作区和 Git 裸仓库：${sessionId}`)
})

test("the same failure is recorded once; distinct failures are all kept", () => {
  for (const [name, raws, expected] of [
    ["event then throw", ['unsafe session id: ""', 'unsafe session id: ""'], ['工作区会话无效：""']],
    ["two different", ["连接错误: reset", "连接已结束，未收到工具执行结果"], ["连接错误: reset", "连接已结束，未收到工具执行结果"]],
    ["empty message", [""], ["执行失败"]],
  ]) {
    const log = raws.reduce((acc, raw) => maintenanceFailure(acc, raw), EMPTY_MAINTENANCE_LOG)
    assert.deepEqual(log.errors, expected, name)
  }
})

test("a Gateway tool error reaches the panel log once, with the clean arguments on the wire", async (t) => {
  const bodies = []
  t.mock.method(globalThis, "fetch", async (_url, request) => {
    bodies.push(JSON.parse(request.body))
    return new Response(JSON.stringify({ type: "result", result: { isError: true, content: [{ type: "text", text: 'unsafe session id: ""' }] } }) + "\n")
  })
  const session = { gateway: "https://gateway.example.test", token: "synthetic-token" }
  const log = await runLikePanel((onEvent) =>
    callTool(session, { cluster: "test", instance: "node", tool: "doops_clean_workspace", arguments: cleanWorkspaceRequest(sessionId) }, onEvent),
  )
  assert.deepEqual(bodies[0].arguments, { session_id: sessionId, workspace: sessionId })
  assert.deepEqual(log.errors, ['工作区会话无效：""'])
})

test("demo mode answers with the Agent contract for the clean tool", async () => {
  for (const [name, args, lines, errors] of [
    ["clean ok", cleanWorkspaceRequest(sessionId), [`已删除工作区和 Git 裸仓库：${sessionId}`], []],
    ["clean session only", { session_id: sessionId }, [], ['工作区会话无效：""']],
  ]) {
    const log = await runLikePanel((onEvent) => demoCallTool({ tool: "doops_clean_workspace", arguments: args }, onEvent))
    assert.deepEqual(log, { lines, errors }, name)
  }
})
