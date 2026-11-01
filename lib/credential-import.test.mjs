import assert from "node:assert/strict"
import test from "node:test"
import {
  buildSecretImportRequest,
  describeSecretImportError,
  importCredentialFromSecret,
} from "./credentials.ts"

const session = { gateway: "https://gateway.example.test" }
const target = { cluster: "test", instance: "node", key: "test/node" }
const CANARY = "doops-canary-7f3a9c-never-display"
const input = { namespace: "apps", secretName: "app-database", keys: "DB_URL, DB_PASSWORD", keyMap: "DB_URL=SPRING_DATASOURCE_URL" }

function mockFetch(t, status, body) {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response(typeof body === "string" ? body : JSON.stringify(body), { status })
  })
  return calls
}

async function importWith(t, status, body) {
  const request = buildSecretImportRequest(target, "credential-import-test", "opaque", input)
  const calls = mockFetch(t, status, body)
  try {
    return { calls, version: await importCredentialFromSecret(session, "cred_1", request) }
  } catch (err) {
    return { calls, message: describeSecretImportError(err) }
  }
}

test("Given an owner with target access, When importing, Then only references are sent and metadata is returned", async (t) => {
  const { calls, version } = await importWith(t, 200, {
    id: "credver_1", credential_id: "cred_1", state: "staged", payload_digest: "sha256:abc",
    payload: { data: { DB_PASSWORD: CANARY } },
  })
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, "/api/credentials/cred_1/import-secret")
  assert.equal(calls[0].init.method, "POST")
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    session_id: "credential-import-test", cluster: "test", instance: "node", namespace: "apps",
    secret_name: "app-database", keys: ["DB_URL", "DB_PASSWORD"], key_map: { DB_URL: "SPRING_DATASOURCE_URL" },
  })
  assert.deepEqual(version, { id: "credver_1", credential_id: "cred_1", payload_digest: "sha256:abc", state: "staged" })
  assert.doesNotMatch(JSON.stringify(version), new RegExp(CANARY))
})

test("Given failures from the Gateway, When importing, Then the stable category is shown", async (t) => {
  for (const [status, category, text] of [
    [403, "credential_import_forbidden", /无权导入/],
    [503, "target_offline", /目标离线/],
    [502, "credential_import_source_unavailable", /找不到该 Secret/],
    [502, "credential_import_key_missing", /缺少所选键/],
    [500, "credential_audit_unavailable", /审计服务不可用/],
    [503, "credential_key_unavailable", /加密密钥不可用/],
    [409, "credential_revoked", /已吊销/],
    [502, "credential_import_some_new_category", /导入失败/],
  ]) {
    await t.test(category, async (t) => {
      const { message } = await importWith(t, status, { error: `${category}\n`, status })
      assert.match(message, text)
      assert.match(message, new RegExp(`（${category}）$`))
    })
  }
})

test("Given an upstream body carrying a value, When importing fails, Then the value is never displayed", async (t) => {
  for (const body of [{ error: `password=${CANARY}` }, `<html>${CANARY}</html>`, { error: `credential_import_failed ${CANARY}` }]) {
    const { message } = await importWith(t, 502, body)
    assert.doesNotMatch(message, new RegExp(CANARY))
    assert.match(message, /credential_import_failed|无效响应/)
  }
})

test("Given a success response that does not describe a staged version, When importing, Then it is rejected", async (t) => {
  const { message } = await importWith(t, 200, { id: "credver_1", credential_id: "cred_other", state: "active", payload: CANARY })
  assert.match(message, /credential_import_result_invalid/)
  assert.doesNotMatch(message, new RegExp(CANARY))
})

test("Given invalid input, When building the request, Then it fails before any network call", async (t) => {
  const calls = mockFetch(t, 200, {})
  for (const [type, override, category] of [
    ["registry", {}, "credential_import_type_invalid"],
    ["opaque", { namespace: "Apps" }, "credential_import_request_invalid"],
    ["opaque", { secretName: "../secret" }, "credential_import_request_invalid"],
    ["opaque", { keys: "" }, "credential_import_keys_invalid"],
    ["opaque", { keys: "A,A" }, "credential_import_keys_invalid"],
    ["opaque", { keys: "A B" }, "credential_import_keys_invalid"],
    ["opaque", { keyMap: "OTHER=X" }, "credential_import_key_map_invalid"],
    ["opaque", { keyMap: "DB_URL" }, "credential_import_key_map_invalid"],
    ["opaque", { keyMap: "DB_URL=X,DB_PASSWORD=X" }, "credential_import_key_map_invalid"],
    ["opaque", { keyMap: "DB_URL=DB_PASSWORD" }, "credential_import_key_map_invalid"],
    ["modelProvider", { keys: "a,b", keyMap: "" }, "credential_import_format_invalid"],
  ]) {
    assert.throws(
      () => buildSecretImportRequest(target, "s", type, { ...input, ...override }),
      (err) => err.category === category && describeSecretImportError(err).endsWith(`（${category}）`),
      `${type} ${JSON.stringify(override)}`,
    )
  }
  assert.equal(calls.length, 0)
})

test("Given a modelProvider credential, When building the request, Then the doagent_settings format is used", () => {
  const request = buildSecretImportRequest(target, "s", "modelProvider", { namespace: "apps", secretName: "doagent", keys: "settings.json", keyMap: "" })
  assert.equal(request.format, "doagent_settings")
  assert.deepEqual(request.keys, ["settings.json"])
  assert.equal(request.key_map, undefined)
})

test("Given demo mode, When importing, Then no request is sent and a synthetic staged version returns", async (t) => {
  const calls = mockFetch(t, 500, {})
  const request = buildSecretImportRequest(target, "s", "opaque", input)
  const version = await importCredentialFromSecret({ ...session, demo: true }, "cred_demo_database", request)
  assert.equal(calls.length, 0)
  assert.equal(version.state, "staged")
  assert.equal(version.credential_id, "cred_demo_database")
})
