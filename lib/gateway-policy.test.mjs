import assert from "node:assert/strict"
import test from "node:test"
import { fileURLToPath } from "node:url"
import { sourceLoader } from "./test-source.mjs"

const load = sourceLoader(fileURLToPath(new URL("../", import.meta.url)))
const {
  GatewayPolicyError,
  allowedGatewayBase,
  setGatewaySession,
} = await load("lib/gateway-policy.ts")

test("production requires an explicit gateway allowlist", () => {
  assert.throws(
    () => allowedGatewayBase("https://doops.example.com", { NODE_ENV: "production" }),
    GatewayPolicyError,
  )
})

test("production accepts only an exact configured gateway origin", () => {
  const env = {
    NODE_ENV: "production",
    DOOPS_CONSOLE_ALLOWED_GATEWAYS: "https://doops.example.com,http://127.0.0.1:8080",
  }
  assert.equal(
    allowedGatewayBase("https://doops.example.com/", env),
    "https://doops.example.com",
  )
  assert.throws(
    () => allowedGatewayBase("https://other.example.com", env),
    GatewayPolicyError,
  )
})

test("production rejects non-loopback HTTP gateways", () => {
  const env = {
    NODE_ENV: "production",
    DOOPS_CONSOLE_ALLOWED_GATEWAYS: "http://doops.example.com",
  }
  assert.throws(
    () => allowedGatewayBase("http://doops.example.com", env),
    GatewayPolicyError,
  )
})

test("development without an allowlist permits loopback only", () => {
  assert.equal(
    allowedGatewayBase("127.0.0.1:8080", { NODE_ENV: "development" }),
    "http://127.0.0.1:8080",
  )
  assert.throws(
    () => allowedGatewayBase("http://169.254.169.254", { NODE_ENV: "development" }),
    GatewayPolicyError,
  )
})

test("gateway base rejects paths, credentials, and unsupported protocols", () => {
  const env = {
    NODE_ENV: "production",
    DOOPS_CONSOLE_ALLOWED_GATEWAYS: "https://doops.example.com",
  }
  for (const gateway of [
    "https://doops.example.com/private",
    "https://user:pass@doops.example.com",
    "file:///etc/passwd",
  ]) {
    assert.throws(() => allowedGatewayBase(gateway, env), GatewayPolicyError)
  }
})

test("session cookies do not outlive a known Gateway token expiry", () => {
  const headers = new Headers()
  setGatewaySession(headers, {
    gateway: "https://doops.example.com",
    token: "synthetic-token",
    expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
  })
  const cookies = headers.getSetCookie()
  assert.equal(cookies.length, 4)
  for (const cookie of cookies) {
    const maxAge = Number(cookie.match(/Max-Age=(\d+)/)?.[1])
    assert.ok(maxAge > 0 && maxAge <= 60 * 60)
  }
})

test("invalid expiry fails before any cookie is written", () => {
  const headers = new Headers()
  assert.throws(() => setGatewaySession(headers, {
    gateway: "https://doops.example.com", token: "synthetic-token", expiresAt: "invalid",
  }), /expiry is invalid/)
  assert.equal(headers.getSetCookie().length, 0)
})
