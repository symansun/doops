import assert from "node:assert/strict"
import test from "node:test"
import {
  createCredentialBundle,
  describeCredentialBundleItems,
  getCredentialBundle,
  validateCredentialBundleInput,
} from "./credentials.ts"

const session = { gateway: "https://gateway.example.test" }
const demo = { gateway: "demo", demo: true }
const CANARY = "doops-synthetic-canary-7f3a9c"
const input = { name: "release", scope: "personal", credentialIDs: ["cred_aaa", "cred_bbb"] }

function mockFetch(t, status, body) {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init = {}) => {
    calls.push({ url, init })
    return new Response(typeof body === "string" ? body : JSON.stringify(body), { status })
  })
  return calls
}

test("Given managed credentials, when a bundle is created, then only metadata is sent and returned", async (t) => {
  const calls = mockFetch(t, 200, {
    id: "credbundle_1", name: "release", scope: "personal", state: "active",
    items: [{ credential_id: "cred_aaa", payload: CANARY }, { credential_id: "cred_bbb" }],
    payload: CANARY,
  })
  const bundle = await createCredentialBundle(session, input)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, "/api/credential-bundles")
  assert.equal(calls[0].init.method, "POST")
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    name: "release", scope: "personal", items: [{ credential_id: "cred_aaa" }, { credential_id: "cred_bbb" }],
  })
  assert.deepEqual(bundle, {
    id: "credbundle_1", name: "release", scope: "personal", state: "active",
    items: [{ credential_id: "cred_aaa" }, { credential_id: "cred_bbb" }],
  })
  assert.ok(!JSON.stringify(bundle).includes(CANARY))
})

test("Given a bundle name, when it is viewed, then the name is sent as a query and items are listed", async (t) => {
  const calls = mockFetch(t, 200, { id: "credbundle_1", name: "re lease", scope: "platform", state: "active", items: [{ credential_id: "cred_aaa" }] })
  const bundle = await getCredentialBundle(session, "  re lease ")
  assert.equal(calls[0].url, "/api/credential-bundles?name=re+lease")
  assert.equal(calls[0].init.method, undefined)
  assert.equal(bundle.scope, "platform")
  assert.deepEqual(bundle.items, [{ credential_id: "cred_aaa" }])
})

const failures = [
  { name: "no permission", status: 403, error: "credential_forbidden", expect: /无权限/ },
  { name: "platform scope without admin", status: 403, error: "Forbidden", expect: /平台凭据组仅平台管理员/ },
  { name: "bundle not found", status: 404, error: "credential_bundle_not_found", expect: /未找到该凭据组/ },
  { name: "ambiguous name", status: 409, error: "credential_reference_ambiguous", expect: /同名/ },
  { name: "audit unavailable", status: 500, error: "credential_audit_unavailable", expect: /审计/ },
  { name: "key unavailable", status: 503, error: "credential_key_unavailable", expect: /密钥不可用/ },
  { name: "gateway unreachable", status: 502, error: "无法连接 gateway: connect ECONNREFUSED", expect: /无法连接 Gateway/ },
  { name: "no gateway session", status: 401, error: "未建立 Gateway 会话", expect: /未建立 Gateway 会话/ },
  { name: "invalid request", status: 400, error: "invalid credential request", expect: /Gateway 拒绝了凭据组请求/ },
  { name: "unexpected 5xx", status: 500, error: `boom ${CANARY}`, expect: /Gateway 暂时不可用（HTTP 500）$/ },
]

for (const failure of failures) {
  test(`Given the gateway reports ${failure.name}, when a bundle request fails, then a stable Chinese message is shown without echoing response text`, async (t) => {
    for (const run of [() => createCredentialBundle(session, input), () => getCredentialBundle(session, "release")]) {
      mockFetch(t, failure.status, { error: `${failure.error}`, payload: CANARY })
      await assert.rejects(run, (err) => {
        assert.match(err.message, failure.expect)
        assert.ok(!err.message.includes(CANARY))
        return true
      })
      t.mock.restoreAll()
    }
  })
}

// The Gateway answers a reused name with 400 credential_operation_failed (the
// UNIQUE(owner_id, name) violation is not given its own category). The console
// has already checked name, scope and duplicates, so it explains the likely cause.
test("Given a bundle name already used in that scope, when a bundle is created, then the duplicate name is named as the likely cause", async (t) => {
  mockFetch(t, 400, { error: "credential_operation_failed\n", status: 400 })
  await assert.rejects(createCredentialBundle(session, input), (err) => {
    assert.match(err.message, /同名/)
    assert.ok(err.message.endsWith("（credential_operation_failed）"), err.message)
    return true
  })
})

test("Given a bundle item that no longer exists, when a bundle is created, then the refusal names the missing credential as a cause", async (t) => {
  mockFetch(t, 500, { error: "credential_audit_unavailable\n", status: 500 })
  await assert.rejects(createCredentialBundle(session, input), (err) => {
    assert.match(err.message, /凭据不存在/)
    assert.ok(err.message.endsWith("（credential_audit_unavailable）"), err.message)
    return true
  })
})

// Only id, name, scope, state, timestamps and credential_id survive, and each
// must have the Gateway's shape; a response outside it is rejected as a whole
// so a partly trusted list is never shown.
test("Given a bundle response outside the metadata shape, when it is viewed, then it is rejected without echoing it", async (t) => {
  const valid = { id: "credbundle_1a2b", name: "release", scope: "personal", state: "active", items: [{ credential_id: "cred_aaa" }] }
  for (const [name, body] of [
    ["id carrying text", { ...valid, id: `credbundle_1 ${CANARY}` }],
    ["id without prefix", { ...valid, id: "1a2b" }],
    ["unknown scope", { ...valid, scope: "project" }],
    ["state carrying text", { ...valid, state: CANARY }],
    ["item carrying a value", { ...valid, items: [{ credential_id: CANARY }] }],
    ["item without id", { ...valid, items: [{}] }],
    ["items not a list", { ...valid, items: { credential_id: "cred_aaa" } }],
    ["array body", [valid]],
  ]) {
    await t.test(name, async (t) => {
      mockFetch(t, 200, body)
      await assert.rejects(getCredentialBundle(session, "release"), (err) => {
        assert.match(err.message, /凭据组响应格式无效/)
        assert.ok(!err.message.includes(CANARY))
        return true
      })
    })
  }
})

test("Given a bundle with an unparseable timestamp, when it is viewed, then the timestamp is dropped", async (t) => {
  mockFetch(t, 200, { id: "credbundle_1", name: "release", scope: "personal", state: "active", created_at: CANARY, items: [] })
  const bundle = await getCredentialBundle(session, "release")
  assert.deepEqual(bundle, { id: "credbundle_1", name: "release", scope: "personal", state: "active", items: [] })
})

// A bundle is static: a credential revoked after the bundle was created stays
// in it. The console shows each item's current state from the managed list.
test("Given a bundle that references revoked or unknown credentials, when it is shown, then each item states its current status", () => {
  const bundle = {
    id: "credbundle_1", name: "release", scope: "personal", state: "active",
    items: [{ credential_id: "cred_aaa" }, { credential_id: "cred_bbb" }, { credential_id: "cred_ccc" }, { credential_id: "cred_ddd" }],
  }
  const managed = [
    { id: "cred_aaa", name: "registry", scope: "personal", type: "registry", state: "active" },
    { id: "cred_bbb", name: "database", scope: "personal", type: "opaque", state: "revoked" },
    { id: "cred_ccc", name: "helm", scope: "personal", type: "helmRepository", state: "revoking" },
  ]
  assert.deepEqual(describeCredentialBundleItems(bundle, managed), [
    { credential_id: "cred_aaa", name: "registry", status: "active", label: "有效" },
    { credential_id: "cred_bbb", name: "database", status: "revoked", label: "已吊销" },
    { credential_id: "cred_ccc", name: "helm", status: "revoking", label: "吊销中" },
    { credential_id: "cred_ddd", status: "unknown", label: "不在你可管理的凭据中" },
  ])
  assert.deepEqual(describeCredentialBundleItems(bundle, null).map((item) => item.status), ["unchecked", "unchecked", "unchecked", "unchecked"])
})

test("Given a non-JSON gateway response, when a bundle is created, then the raw text is not shown", async (t) => {
  mockFetch(t, 502, `<html>${CANARY}</html>`)
  await assert.rejects(createCredentialBundle(session, input), (err) => !err.message.includes(CANARY) && /无效响应/.test(err.message))
})

test("Given a success response without id, when a bundle is created, then it fails closed", async (t) => {
  mockFetch(t, 200, { name: "release" })
  await assert.rejects(createCredentialBundle(session, input), /缺少 id/)
})

const invalidInputs = [
  { name: "empty name", input: { ...input, name: "  " }, expect: /名称/ },
  { name: "no credentials", input: { ...input, credentialIDs: [] }, expect: /至少选择一个凭据/ },
  { name: "blank credential id", input: { ...input, credentialIDs: [" "] }, expect: /至少选择一个凭据/ },
  { name: "duplicate credential", input: { ...input, credentialIDs: ["cred_aaa", " cred_aaa"] }, expect: /重复/ },
  { name: "unknown scope", input: { ...input, scope: "project" }, expect: /归属/ },
]

for (const item of invalidInputs) {
  test(`Given ${item.name}, when a bundle is created, then validation fails before any request`, async (t) => {
    const calls = mockFetch(t, 200, {})
    assert.match(validateCredentialBundleInput(item.input), item.expect)
    await assert.rejects(createCredentialBundle(session, item.input), item.expect)
    assert.equal(calls.length, 0)
  })
}

test("Given a blank name, when a bundle is viewed, then no request is sent", async (t) => {
  const calls = mockFetch(t, 200, {})
  await assert.rejects(getCredentialBundle(session, " "), /名称/)
  assert.equal(calls.length, 0)
})

test("Given demo mode, when bundles are used, then writes are refused and views use local metadata", async (t) => {
  const calls = mockFetch(t, 200, {})
  await assert.rejects(createCredentialBundle(demo, input), /演示模式/)
  const bundle = await getCredentialBundle(demo, "release-demo")
  assert.equal(bundle.items[0].credential_id, "cred_demo_registry")
  await assert.rejects(getCredentialBundle(demo, "other"), /未找到/)
  assert.equal(calls.length, 0)
})
