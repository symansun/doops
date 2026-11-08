import assert from "node:assert/strict"
import test from "node:test"
import { isDemoAllowed } from "./demo-policy.ts"

test("production never allows demo mode", () => {
  assert.equal(isDemoAllowed("production"), false)
})

test("non-production environments can explicitly use demo mode", () => {
  assert.equal(isDemoAllowed("development"), true)
  assert.equal(isDemoAllowed("test"), true)
})
