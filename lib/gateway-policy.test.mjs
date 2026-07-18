import assert from "node:assert/strict"
import test from "node:test"
import {
  GatewayPolicyError,
  allowedGatewayBase,
} from "./gateway-policy.ts"

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
