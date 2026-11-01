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
const DIGEST = "0123456789abcdef".repeat(4)
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
    id: "credver_1", credential_id: "cred_1", state: "staged", payload_digest: DIGEST,
    created_at: "2026-11-01T04:35:29.34481Z", created_by: "usr_1",
    payload: { data: { DB_PASSWORD: CANARY } },
  })
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, "/api/credentials/cred_1/import-secret")
  assert.equal(calls[0].init.method, "POST")
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    session_id: "credential-import-test", cluster: "test", instance: "node", namespace: "apps",
    secret_name: "app-database", keys: ["DB_URL", "DB_PASSWORD"], key_map: { DB_URL: "SPRING_DATASOURCE_URL" },
  })
  assert.deepEqual(version, {
    id: "credver_1", credential_id: "cred_1", payload_digest: DIGEST, state: "staged", created_at: "2026-11-01T04:35:29.34481Z",
  })
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
    [404, "credential_not_found", /凭据不存在/],
    [504, "credential_import_timeout", /导入超时/],
    [502, "credential_import_agent_unsupported", /升级 Agent/],
    [409, "target_busy", /目标忙/],
    [400, "credential_import_request_invalid", /导入请求不合法/],
    [502, "credential_import_some_new_category", /导入失败/],
  ]) {
    await t.test(category, async (t) => {
      const { message } = await importWith(t, status, { error: `${category}\n`, status })
      assert.match(message, text)
      assert.match(message, new RegExp(`（${category}）$`))
    })
  }
})

// The console proxy wraps a non-2xx Gateway body as {error: <text>, status}.
// Only stable categories and the proxy's own fixed messages are recognised;
// everything else is reported by HTTP status without the response text.
test("Given a failure that is not a bare category, When importing, Then it is classified without echoing the body", async (t) => {
  const gatewayAuth = JSON.stringify({ error: "unauthorized", code: "gateway_auth_failed", message: `token ${CANARY}` })
  for (const [name, status, body, text, suffix] of [
    ["expired Gateway login", 401, { error: gatewayAuth, status: 401 }, /重新登录/, "（gateway_auth_failed）"],
    ["no console session", 401, { error: "未建立 Gateway 会话" }, /未建立 Gateway 会话/, "（HTTP 401）"],
    ["gateway unreachable", 502, { error: `无法连接 gateway: connect ECONNREFUSED ${CANARY}` }, /无法连接 Gateway/, "（HTTP 502）"],
    ["lock wait canceled", 409, { error: "operation canceled\n", status: 409 }, /操作已取消/, "（operation_canceled）"],
    ["console policy refusal", 403, { error: "该 gateway 未获控制台授权" }, /拒绝/, "（HTTP 403）"],
    ["free text with a value", 502, { error: `password=${CANARY}` }, /无法识别/, "（HTTP 502）"],
    ["category followed by a value", 502, { error: `credential_import_failed ${CANARY}` }, /无法识别/, "（HTTP 502）"],
    ["html body", 502, `<html>${CANARY}</html>`, /无法识别/, "（HTTP 502）"],
    ["empty 500", 500, "", /无法识别/, "（HTTP 500）"],
  ]) {
    await t.test(name, async (t) => {
      const { message } = await importWith(t, status, body)
      assert.match(message, text)
      assert.ok(message.endsWith(suffix), message)
      assert.doesNotMatch(message, new RegExp(CANARY))
      assert.doesNotMatch(message, /doops login|unauthorized/)
    })
  }
})

// A 2xx answer is shown only if it is exactly a new staged version of the
// requested credential. Anything else is discarded under a console-side
// category, because the Gateway may already have stored a version.
test("Given a success response that is not new staged version metadata, When importing, Then it is rejected", async (t) => {
  const valid = { id: "credver_1", credential_id: "cred_1", state: "staged", payload_digest: DIGEST }
  for (const [name, body] of [
    ["other credential", { ...valid, credential_id: "cred_other" }],
    ["active state", { ...valid, state: "active" }],
    ["id carrying text", { ...valid, id: `credver_1 ${CANARY}` }],
    ["id without prefix", { ...valid, id: "1" }],
    ["digest carrying a value", { ...valid, payload_digest: CANARY }],
    ["missing digest", { ...valid, payload_digest: undefined }],
    ["array body", [valid]],
    ["non-JSON body", `ok ${CANARY}`],
  ]) {
    await t.test(name, async (t) => {
      const { message, version } = await importWith(t, 200, body)
      assert.equal(version, undefined)
      assert.match(message, /请刷新/)
      assert.ok(message.endsWith("（credential_import_response_invalid）"), message)
      assert.doesNotMatch(message, new RegExp(CANARY))
    })
  }
})

test("Given a staged version with an unparseable timestamp, When importing, Then the timestamp is dropped", async (t) => {
  const { version } = await importWith(t, 200, {
    id: "credver_1", credential_id: "cred_1", state: "staged", payload_digest: `sha256:${DIGEST}`, created_at: CANARY,
  })
  assert.deepEqual(version, { id: "credver_1", credential_id: "cred_1", payload_digest: `sha256:${DIGEST}`, state: "staged" })
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
    // The CLI and Kubernetes Secret keys allow only [A-Za-z0-9._-]; the
    // Gateway's metadata token also allows ":" and "/", which cannot materialize.
    ["opaque", { keyMap: "DB_URL=spring/url" }, "credential_import_key_map_invalid"],
    ["opaque", { keyMap: "DB_URL=spring:url" }, "credential_import_key_map_invalid"],
    // A namespace is a DNS label: no dots, at most 63 characters.
    ["opaque", { namespace: "apps.prod" }, "credential_import_request_invalid"],
    ["opaque", { namespace: "a".repeat(64) }, "credential_import_request_invalid"],
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
