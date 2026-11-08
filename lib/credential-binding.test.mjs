import assert from "node:assert/strict"
import test from "node:test"
import { bindCredentialToTarget, buildBindingScope, parseRequiredKeys } from "./credentials.ts"

const target = { cluster: "doops-zyj", instance: "test", key: "doops-zyj/test" }
const session = { gateway: "https://gateway.example.test" }

// Same rules as `credential grant/configure --required-keys`: optional, comma
// separated Kubernetes Secret keys, deduplicated and sorted.
test("Given required keys input, When parsed, Then keys are trimmed, deduplicated and sorted", () => {
  for (const [raw, want] of [
    ["", []],
    ["   ", []],
    ["DB_URL", ["DB_URL"]],
    [" DB_USER , DB_URL,DB_URL ", ["DB_URL", "DB_USER"]],
    ["tls.crt,.dockerconfigjson,a-b_c", [".dockerconfigjson", "a-b_c", "tls.crt"]],
  ]) {
    assert.deepEqual(parseRequiredKeys(raw), want, JSON.stringify(raw))
  }
})

test("Given required keys that are not Secret key names, When parsed, Then they are rejected", () => {
  for (const raw of ["A,,B", "A,", "A B", "spring/url", "spring:url", "密码", "x".repeat(254)]) {
    assert.throws(() => parseRequiredKeys(raw), /required keys|键名/, JSON.stringify(raw))
  }
})

test("Given the binding form, When the scope is built, Then required keys are sent only for opaqueSecret", () => {
  for (const [name, input, want] of [
    ["opaqueSecret with keys", { use: "opaqueSecret", namespace: " agents-test ", requiredKeys: "B,A" }, { required_keys: ["A", "B"] }],
    ["opaqueSecret without keys", { use: "opaqueSecret", namespace: "agents-test", requiredKeys: "" }, {}],
    ["database ignores keys", { use: "database", namespace: "agents-test", requiredKeys: "A" }, {}],
    ["imagePull ignores keys", { use: "imagePull", namespace: "agents-test", requiredKeys: "A" }, {}],
  ]) {
    assert.deepEqual(buildBindingScope(target, input), {
      cluster: "doops-zyj", instance: "test", namespace: "agents-test", use: input.use,
      workload: { kind: "", name: "" }, ...want,
    }, name)
  }
})

test("Given an empty namespace or invalid keys, When the scope is built, Then nothing is sent", async (t) => {
  const fetchMock = t.mock.method(globalThis, "fetch", async () => Response.json({}))
  for (const input of [
    { use: "opaqueSecret", namespace: "  ", requiredKeys: "" },
    { use: "opaqueSecret", namespace: "agents-test", requiredKeys: "A,,B" },
  ]) {
    assert.throws(() => buildBindingScope(target, input))
  }
  assert.equal(fetchMock.mock.callCount(), 0)
})

test("Given a scope with required keys, When bound, Then the grant body carries them", async (t) => {
  const fetchMock = t.mock.method(globalThis, "fetch", async () =>
    Response.json({ id: "credgrant_1", credential_id: "cred_1", state: "active", scope: {} }))
  const scope = buildBindingScope(target, { use: "opaqueSecret", namespace: "agents-test", requiredKeys: "DB_URL,DB_PASSWORD" })
  await bindCredentialToTarget(session, "cred_1", scope)
  const [url, init] = fetchMock.mock.calls[0].arguments
  assert.equal(url, "/api/credentials/cred_1/grants")
  assert.deepEqual(JSON.parse(init.body).scope.required_keys, ["DB_PASSWORD", "DB_URL"])
})
