import assert from "node:assert/strict"
import test from "node:test"
import {
  bindCredentialToTarget,
  createCredential,
  listCredentials,
  promoteCredentialVersion,
  putCredentialPayload,
  revokeCredential,
  revokeCredentialBinding,
  verifyCredentialVersion,
} from "./credentials.ts"
import { gatewayProxyErrorBody } from "./server/gateway-errors.ts"

const session = { gateway: "https://gateway.example.test" }
const target = { cluster: "doops-zyj", instance: "test", key: "doops-zyj/test" }
const CANARY = "doops-synthetic-canary-k2-58e1"
const scope = { cluster: "doops-zyj", instance: "test", namespace: "agents-test", use: "opaqueSecret", workload: { kind: "", name: "" } }

// Every credential operation the page runs, so each one is held to the same
// error rules.
const OPERATIONS = [
  ["list", () => listCredentials(session, target, "managed")],
  ["create", () => createCredential(session, { name: "canary", scope: "personal", type: "opaque" })],
  ["put", () => putCredentialPayload(session, "cred_1", JSON.stringify({ data: { PASSWORD: CANARY } }))],
  ["bind", () => bindCredentialToTarget(session, "cred_1", scope)],
  ["verify", () => verifyCredentialVersion(session, "cred_1", { version_id: "credver_1", session_id: "verify-1", scope })],
  ["promote", () => promoteCredentialVersion(session, "cred_1", "credver_1")],
  ["unbind", () => revokeCredentialBinding(session, "cred_1", "credgrant_1")],
  ["revoke", () => revokeCredential(session, "cred_1")],
]

// The console proxy forwards a non-2xx Gateway body as {error: <text>, status}.
function mockFetch(t, status, body) {
  t.mock.method(globalThis, "fetch", async () =>
    new Response(typeof body === "string" ? body : JSON.stringify(body), { status }))
}

async function failure(t, run, status, body) {
  mockFetch(t, status, body)
  try {
    await run()
  } catch (err) {
    return err
  } finally {
    t.mock.restoreAll()
  }
  assert.fail("operation unexpectedly succeeded")
}

// Categories these endpoints return, taken from the Gateway sources.
const GATEWAY_CATEGORIES = [
  // credential_http.go: credentialErrorCategory, handler literals
  [404, "credential_not_found"],
  [404, "credential_version_not_found"],
  [400, "credential_revoked"],
  [403, "credential_forbidden"],
  [403, "credential_grant_denied"],
  [403, "credential_binding_target_mismatch"],
  [403, "credential_binding_scope_invalid"],
  [503, "credential_key_unavailable"],
  [400, "credential_payload_invalid"],
  [500, "credential_audit_unavailable"],
  [400, "credential_operation_failed"],
  [500, "credential_metadata_unavailable"],
  [409, "credential_version_not_staged"],
  [500, "credential_grants_unavailable"],
  [500, "credential_verifications_unavailable"],
  [409, "credential_verification_required"],
  [502, "credential_promotion_materialization_failed"],
  [502, "credential_promotion_outcome_unknown"],
  [502, "credential_grant_revocation_outcome_unknown"],
  [500, "credential_cleanup_context_unavailable"],
  [502, "credential_revocation_outcome_unknown"],
  // credential_scope.go / credential_store.go: grant create
  [400, "credential_scope_invalid"],
  [400, "credential_use_mismatch"],
  [400, "model_provider_already_bound"],
  // credential_runtime.go: verify
  [502, "credential_verification_outcome_unknown"],
  [500, "credential_verification_persist_failed"],
  [502, "verification_transport_failed"],
  [502, "target_offline"],
  [502, "execution_audit_unavailable"],
  [502, "credential_materialization_metadata_invalid"],
  [502, "credential_materialization_outcome_unknown"],
  [502, "credential_target_lock_changed"],
  [502, "credential_tool_failed"],
  // credential_materializer.go: Agent-side materialization
  [502, "credential_secret_get_failed"],
  [502, "credential_target_mutation_failed"],
  [502, "credential_secret_ownership_conflict"],
  [502, "credential_secret_ownership_mismatch"],
  [502, "credential_secret_verification_failed"],
  [502, "credential_secret_identity_invalid"],
  [502, "credential_secret_metadata_invalid"],
  [502, "credential_secret_restore_failed"],
  [502, "credential_secret_delete_failed"],
  [502, "credential_workload_get_failed"],
  [502, "credential_workload_invalid"],
  [502, "credential_workload_verification_failed"],
  [502, "credential_workload_restore_failed"],
  [502, "credential_key_mismatch"],
  [502, "credential_type_unsupported"],
  [502, "credential_request_invalid"],
  [502, "credential_cleanup_invalid"],
  [502, "credential_target_outcome_unknown"],
  [502, "credential_endpoint_invalid"],
  [502, "credential_endpoint_private"],
  [502, "credential_endpoint_unresolvable"],
  // model_provider.go: modelProvider verification
  [502, "model_probe_failed"],
  [502, "model_probe_unavailable"],
  [502, "model_probe_result_invalid"],
  [502, "model_settings_write_failed"],
]

test("Given each Gateway category, When a credential operation fails, Then a Chinese reason and next step keep the category", async (t) => {
  for (const [status, category] of GATEWAY_CATEGORIES) {
    for (const [name, run] of OPERATIONS) {
      const err = await failure(t, run, status, { error: `${category}\n`, status })
      assert.ok(err.message.endsWith(`（${category}）`), `${name} ${category}: ${err.message}`)
      assert.match(err.message, /[一-龥]/, `${name} ${category}`)
      assert.match(err.message, /请/, `${name} ${category} has no next step: ${err.message}`)
      assert.doesNotMatch(err.message, /^凭据操作失败/, `${name} ${category} fell back to the generic text`)
      assert.equal(err.category, category)
      assert.equal(err.status, status)
    }
  }
})

test("Given the categories testers hit online, When shown, Then the next step names what to check", async (t) => {
  for (const [category, expect] of [
    ["credential_secret_get_failed", /namespace.*RBAC/],
    ["credential_verification_required", /验证暂存/],
    ["credential_grant_revocation_outcome_unknown", /解除/],
    ["credential_promotion_outcome_unknown", /核对/],
    ["model_provider_already_bound", /解除/],
  ]) {
    const err = await failure(t, OPERATIONS[5][1], 409, { error: category })
    assert.match(err.message, expect, category)
  }
})

test("Given a Gateway phrase or console message that is not a category, When shown, Then it is classified by a fixed text", async (t) => {
  const gatewayAuth = JSON.stringify({ error: "unauthorized", code: "gateway_auth_expired", message: `token ${CANARY}`, next_action: "Run doops login" })
  for (const [name, status, body, expect, suffix] of [
    ["Gateway Forbidden", 403, { error: "Forbidden\n", status: 403 }, /owner 或平台管理员/, "（Forbidden）"],
    ["lock wait canceled", 409, { error: "operation canceled\n", status: 409 }, /已取消/, "（operation_canceled）"],
    ["hidden credential", 404, { error: "404 page not found\n", status: 404 }, /刷新凭据列表/, "（HTTP 404）"],
    ["payload not JSON", 400, { error: "credential payload must be valid JSON\n", status: 400 }, /合法 JSON/, "（HTTP 400）"],
    ["request decode", 400, { error: "invalid credential request\n", status: 400 }, /版本一致/, "（HTTP 400）"],
    ["verify request", 400, { error: "invalid credential verify request\n", status: 400 }, /版本一致/, "（HTTP 400）"],
    ["list failure", 500, { error: "failed to list managed credentials\n", status: 500 }, /列出凭据/, "（HTTP 500）"],
    ["expired login", 401, { error: gatewayAuth, status: 401 }, /重新登录/, "（gateway_auth_expired）"],
    ["no console session", 401, { error: "未建立 Gateway 会话" }, /重新连接/, "（HTTP 401）"],
    ["gateway unreachable", 502, { error: `无法连接 gateway: connect ECONNREFUSED ${CANARY}` }, /无法连接 Gateway/, "（HTTP 502）"],
    ["console policy refusal", 403, { error: "该 gateway 未获控制台授权" }, /控制台授权/, "（HTTP 403）"],
  ]) {
    const err = await failure(t, OPERATIONS[4][1], status, body)
    assert.match(err.message, expect, name)
    assert.ok(err.message.endsWith(suffix), `${name}: ${err.message}`)
    assert.doesNotMatch(err.message, new RegExp(CANARY), name)
    assert.doesNotMatch(err.message, /doops login|unauthorized|ECONNREFUSED/, name)
  }
})

test("Given an unknown category, When shown, Then a generic reason keeps the category", async (t) => {
  const err = await failure(t, OPERATIONS[4][1], 502, { error: "credential_future_failure", status: 502 })
  assert.equal(err.message, "凭据操作失败。请刷新后核对凭据状态（credential_future_failure）")
  assert.equal(err.category, "credential_future_failure")
})

// K8 把资源吊销清理失败的类别从 credential_cleanup_failed 换成
// credential_revocation_outcome_unknown；旧 Gateway 仍可能返回旧类别，中文映射要保留。
test("Given the older Gateway cleanup-failure category, When shown, Then its Chinese reason is kept for compatibility", async (t) => {
  const err = await failure(t, OPERATIONS[7][1], 502, { error: "credential_cleanup_failed" })
  assert.ok(err.message.endsWith("（credential_cleanup_failed）"), err.message)
  assert.match(err.message, /清理失败.*再次吊销/)
  assert.equal(err.category, "credential_cleanup_failed")
})

// Resource revocation answers "category: cluster/instance, cluster/instance"
// (credential_http.go). The body goes through the console proxy first, as on the page.
test("Given a revocation body that names targets, When shown, Then resource and grant revocation both keep the category and list the targets", async (t) => {
  // 解除绑定（unbind）和吊销凭据（revoke）都走同一个 request 封装，两处调用点都要带出目标。
  const revokes = [OPERATIONS[6][1], OPERATIONS[7][1]]
  for (const [name, status, raw, category, reason, targets] of [
    ["target offline", 503, "target_offline: doops-zyj/test\n", "target_offline", /Agent 离线/, "doops-zyj/test"],
    ["target busy", 409, "target_busy: dev/local\n", "target_busy", /等待当前操作结束/, "dev/local"],
    ["outcome unknown on two targets", 502, "credential_revocation_outcome_unknown: dev/local, dev/renamed\n",
      "credential_revocation_outcome_unknown", /清理结果未知.*再次吊销/, "dev/local、dev/renamed"],
  ]) {
    const proxied = gatewayProxyErrorBody(status, raw, "凭据请求")
    assert.equal(proxied.error, category, name)
    for (const revoke of revokes) {
      const err = await failure(t, revoke, status, proxied)
      assert.match(err.message, reason, name)
      assert.ok(err.message.endsWith(`。未清理的目标：${targets}（${category}）`), `${name}: ${err.message}`)
      assert.equal(err.category, category, name)
      assert.equal(err.status, status, name)
    }
  }
})

test("Given a revocation body whose target part is not an identifier, When shown, Then it falls back to the status text", async (t) => {
  const revoke = OPERATIONS[7][1]
  for (const [name, status, raw] of [
    ["key=value after a target", 503, `target_offline: dev/local, password=${CANARY}`],
    ["sentence after the category", 502, `credential_revocation_outcome_unknown: dev/local could not reach ${CANARY}`],
    ["markup as a target", 409, `target_busy: <b>${CANARY}</b>/x`],
    ["trailing separator", 503, `target_offline: dev/${CANARY}, `],
    ["semicolon list", 502, `credential_revocation_outcome_unknown: dev/local; dev/${CANARY}`],
  ]) {
    const proxied = gatewayProxyErrorBody(status, raw, "凭据请求")
    assert.equal(proxied.targets, undefined, name)
    assert.doesNotMatch(JSON.stringify(proxied), new RegExp(CANARY), name)
    const err = await failure(t, revoke, status, proxied)
    assert.equal(err.message, `无法识别的 Gateway 响应。请刷新后核对凭据状态（HTTP ${status}）`, name)
    assert.equal(err.category, "", name)
  }
})

test("Given targets that are not identifiers in the proxy body, When shown, Then only the category reason is shown", async (t) => {
  const err = await failure(t, OPERATIONS[7][1], 503, { error: "target_offline", status: 503, targets: [`dev/local`, `x ${CANARY}`] })
  assert.equal(err.message, "目标 Agent 离线。请确认 Agent 已连接后重试（target_offline）")
})

test("Given a failure body that could carry a value, When shown, Then the body never reaches the message", async (t) => {
  for (const [name, status, body] of [
    ["free text", 502, { error: `password=${CANARY}` }],
    ["category followed by text", 502, { error: `credential_secret_get_failed ${CANARY}` }],
    ["upper-case token", 400, { error: CANARY.toUpperCase() }],
    ["html body", 502, `<html>${CANARY}</html>`],
    ["plain text body", 500, `panic: ${CANARY}`],
    ["empty body", 500, ""],
    ["JSON without error", 500, { detail: CANARY }],
    ["array body", 502, [CANARY]],
  ]) {
    for (const [operation, run] of OPERATIONS) {
      const err = await failure(t, run, status, body)
      assert.equal(err.message, `无法识别的 Gateway 响应。请刷新后核对凭据状态（HTTP ${status}）`, `${operation} ${name}`)
      assert.equal(err.category, "")
    }
  }
})

test("Given a success response that is not JSON, When an operation runs, Then it fails without echoing the body", async (t) => {
  for (const [operation, run] of OPERATIONS) {
    const err = await failure(t, run, 200, `ok ${CANARY}`)
    assert.equal(err.message, "凭据服务返回了无效响应。请刷新后核对凭据状态（HTTP 200）", operation)
  }
})
