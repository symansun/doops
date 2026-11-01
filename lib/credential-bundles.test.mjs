import assert from "node:assert/strict"
import test from "node:test"
import { createCredentialBundle, getCredentialBundle, validateCredentialBundleInput } from "./credentials.ts"

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
  { name: "unexpected 5xx", status: 500, error: `boom ${CANARY}`, expect: /Gateway 暂时不可用 \(500\)/ },
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
