import assert from "node:assert/strict"
import test from "node:test"
import { loadSelection, persistsScope, saveSelection } from "./console-persistence.ts"
import { displayUserName, greeting, TOKEN_LOGIN_LABEL } from "./session-display.ts"

const gateway = "https://doops.l8ai.cn"

test("Given a token login without a username, When it is displayed, Then it shows 令牌登录", () => {
  for (const session of [{}, { username: "" }, { username: "   " }, { gateway, username: undefined }]) {
    assert.equal(displayUserName(session), TOKEN_LOGIN_LABEL, JSON.stringify(session))
    assert.equal(greeting(session), `你好，${TOKEN_LOGIN_LABEL}`, JSON.stringify(session))
  }
})

test("Given a session with a username, When it is displayed, Then the username itself is shown", () => {
  assert.equal(displayUserName({ username: "ops-zhang" }), "ops-zhang")
  assert.equal(greeting({ username: " ops-zhang " }), "你好，ops-zhang")
})

test("Given the demo session, When it is displayed, Then it stays 演示用户", () => {
  assert.equal(displayUserName({ username: "演示用户", demo: true }), "演示用户")
  assert.equal(displayUserName({ username: "", demo: true }), "演示用户")
  assert.equal(greeting({ demo: true }), "你好，演示用户")
})

// R4-2：显示回退不能变成持久化身份。
test("Given a token login, When the display fallback runs, Then nothing is persisted and the session keeps no placeholder", () => {
  const session = { gateway }
  assert.equal(displayUserName(session), TOKEN_LOGIN_LABEL)
  assert.deepEqual(session, { gateway }, "display must not write the placeholder into the session")
  assert.equal(persistsScope(session), false)

  const stored = new Map()
  const storage = {
    getItem: (key) => stored.get(key) ?? null,
    setItem: (key, value) => void stored.set(key, value),
    removeItem: (key) => void stored.delete(key),
  }
  saveSelection(storage, session, { targetKey: "doops-zyj/test", sessionId: "console-abc12345" })
  assert.equal(stored.size, 0, "a token login still writes no selection")
  assert.equal(loadSelection(storage, session), null)
  assert.ok(![...stored.keys()].some((key) => key.includes(TOKEN_LOGIN_LABEL)), "the placeholder is never a storage key")
})
