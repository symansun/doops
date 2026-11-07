import assert from "node:assert/strict"
import test from "node:test"
import { resolveDefaultGateway } from "./gateway.ts"

test("production HTTPS console migrates saved HTTP gateway to its own origin", () => {
  assert.equal(
    resolveDefaultGateway("http://192.0.2.10:42222", "https://gateway.example.com"),
    "https://gateway.example.com",
  )
  assert.equal(
    resolveDefaultGateway("192.0.2.10:42222", "https://gateway.example.com"),
    "https://gateway.example.com",
  )
})

test("valid saved HTTPS gateway remains selected", () => {
  assert.equal(
    resolveDefaultGateway("https://ops.example.com", "https://gateway.example.com"),
    "https://ops.example.com",
  )
})

test("valid saved WSS gateway remains selected on HTTPS console", () => {
  assert.equal(
    resolveDefaultGateway("wss://ops.example.com", "https://gateway.example.com"),
    "wss://ops.example.com",
  )
})

test("invalid saved gateway falls back to the current origin", () => {
  assert.equal(resolveDefaultGateway("not a URL", "https://gateway.example.com"), "https://gateway.example.com")
})
