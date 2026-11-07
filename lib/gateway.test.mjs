import assert from "node:assert/strict"
import test from "node:test"
import { resolveDefaultGateway } from "./gateway.ts"

test("production HTTPS console migrates saved HTTP gateway to its own origin", () => {
  assert.equal(
    resolveDefaultGateway("http://106.54.197.139:42222", "https://doops.l8ai.cn"),
    "https://doops.l8ai.cn",
  )
  assert.equal(
    resolveDefaultGateway("106.54.197.139:42222", "https://doops.l8ai.cn"),
    "https://doops.l8ai.cn",
  )
})

test("valid saved HTTPS gateway remains selected", () => {
  assert.equal(
    resolveDefaultGateway("https://ops.example.com", "https://doops.l8ai.cn"),
    "https://ops.example.com",
  )
})

test("valid saved WSS gateway remains selected on HTTPS console", () => {
  assert.equal(
    resolveDefaultGateway("wss://ops.example.com", "https://doops.l8ai.cn"),
    "wss://ops.example.com",
  )
})

test("invalid saved gateway falls back to the current origin", () => {
  assert.equal(resolveDefaultGateway("not a URL", "https://doops.l8ai.cn"), "https://doops.l8ai.cn")
})
