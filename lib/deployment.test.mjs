import assert from "node:assert/strict"
import test from "node:test"
import { DEPLOYMENT_EXTENSION, agentErrorText, deploymentAvailability, deploymentExtensionCommand, deploymentFormControls, deploymentRequest } from "./deployment.ts"

const target = { cluster: "test", instance: "node", key: "test/node" }
const input = { services: "api", release: "app", namespace: "apps", chart: "https://charts.example.test/app.tgz", values: "", repository: "registry.example.test/app", selector: "digest", version: "sha256:" + "a".repeat(64), commit: "b".repeat(40), ci: "https://cnb.cool/example/app/-/build/logs/verified", reason: "fix resource cleanup", acceptance: "Final Deployment generation/spec/status, actual imageID and health pass", credentials: "registry-reader" }

test("authorized deployment uses explicit target and only credential names", () => {
  const request = deploymentRequest(target, input)
  assert.equal(request.operation, "apply")
  assert.deepEqual(request.credential_refs, ["registry-reader"])
  assert.match(request.instruction, /test\/node/)
  assert.match(request.instruction, /sha256:/)
  assert.doesNotMatch(request.instruction, /\/root\/ws|doops push|kubeconfig/)
})

test("incomplete or mutable release inputs are blocked before RPC", () => {
  for (const invalid of [{ version: "bad tag" }, { commit: "short" }, { chart: "deploy/helm/app" }, { chart: "oci://registry.example.test/charts/app" }, { chart: "https://secret:payload@charts.example.test/app" }, { namespace: "" }, { credentials: "../secret" }]) {
    assert.throws(() => deploymentRequest(target, { ...input, ...invalid }))
  }
})

test("business-owned release tags remain valid version inputs", () => {
  const request = deploymentRequest(target, { ...input, selector: "tag", version: "v2.3.1" })
  assert.match(request.instruction, /registry\.example\.test\/app:v2\.3\.1/)
})

test("console form maps to the confirmed doops.operations/apply extension command", () => {
  const command = deploymentExtensionCommand(target, input, true)
  assert.deepEqual({ namespace: command.namespace, semanticType: command.semanticType, schemaVersion: command.schemaVersion }, DEPLOYMENT_EXTENSION)
  assert.deepEqual(DEPLOYMENT_EXTENSION, { namespace: "doops.operations", semanticType: "apply", schemaVersion: "1" })
  assert.deepEqual(Object.keys(command.payload).sort(), ["confirmed", "input"])
  assert.equal(command.payload.confirmed, true)
  assert.deepEqual(command.payload.input, input)
  assert.notEqual(command.payload.input, input, "the payload must be a snapshot, not the live form state")
  assert.equal(command.payload.input.credentials, "registry-reader")
})

test("unconfirmed console submissions are rejected before any command is built", () => {
  for (const confirmed of [false, undefined, "true", 1]) {
    assert.throws(() => deploymentExtensionCommand(target, input, confirmed), /变更授权/)
  }
})

test("console command keeps every existing field check", () => {
  for (const [invalid, message] of [
    [{ version: "latest", selector: "digest" }, /digest/], [{ commit: "b".repeat(39) }, /40 位/], [{ chart: "http://charts.example.test/app.tgz" }, /HTTPS/],
    [{ release: "App" }, /Kubernetes/], [{ credentials: "registry-reader,../secret" }, /资源名/], [{ ci: "" }, /完整填写/],
    [{ repository: "registry.example.test/app:v1/" }, /镜像仓库/],
  ]) {
    assert.throws(() => deploymentExtensionCommand(target, { ...input, ...invalid }, true), message)
  }
})

// K12: the Agent state decides only whether a release can be submitted. The fields stay editable so the
// release can be drafted and checked locally on a target that cannot run it yet.
test("agent availability blocks only submission and names the next step", () => {
  const idle = { connection: "disconnected", supportsApply: true, running: false }
  for (const [name, state, expected] of [
    ["no model provider", { ...idle, error: "[unavailable] Gateway 还没有可下发到该节点的模型" },
      { kind: "no_model", title: "该目标还没有配置 DoAgent 模型，暂时不能发布", detail: /联系平台管理员为该目标绑定 modelProvider 凭据/, actions: ["open_credentials", "reconnect"] }],
    ["model not connected", { ...idle, error: "[unavailable] DoAgent 模型连接不可用" },
      { kind: "model_unavailable", title: /模型当前不可用/, detail: /modelProvider 凭据/, actions: ["open_credentials", "reconnect"] }],
    ["model provider delivery category", { ...idle, error: "[unavailable] model_provider_ambiguous" },
      { kind: "model_unavailable", title: /模型当前不可用/, detail: /model_provider_ambiguous/, actions: ["open_credentials", "reconnect"] }],
    ["agent without ACP events", { ...idle, error: "[failed_precondition] 目标 Agent 不支持结构化 ACP 事件协议，必须升级 Agent" },
      { kind: "upgrade_agent", title: /升级|版本过旧/, detail: /部署 → 升级 Agent/, actions: ["upgrade_agent"] }],
    ["no grant on the target", { ...idle, error: "[permission_denied] forbidden: ask on doops-zyj/zyj-test" },
      { kind: "forbidden", title: /没有该目标的使用权限/, detail: /管理 → 权限/, actions: [] }],
    ["Gateway rejected the token", { ...idle, error: "[permission_denied] gateway_auth_failed" },
      { kind: "signed_out", title: /登录已失效/, detail: /重新登录/, actions: [] }],
    ["console lost the login", { ...idle, error: "[unauthenticated] 缺少登录 token" },
      { kind: "signed_out", title: /登录已失效/, detail: /重新登录/, actions: [] }],
    ["target offline", { ...idle, error: "[unavailable] target offline: doops-zyj/zyj-test" },
      { kind: "offline", title: /离线/, detail: /重新连接/, actions: ["reconnect"] }],
    ["same session still applying", { ...idle, error: "[unavailable] target busy: doops-zyj/zyj-test" },
      { kind: "busy", title: /目标正忙/, detail: /审计/, actions: ["reconnect"] }],
    ["queue timeout", { ...idle, connection: "reconnecting", error: "[unavailable] target queue timeout: doops-zyj/zyj-test" },
      { kind: "busy", title: /目标正忙/, detail: /审计/, actions: ["reconnect"] }],
    ["gateway proxy timeout", { ...idle, error: "[deadline_exceeded] 智能体请求失败 (504)：错误响应无效" },
      { kind: "unavailable", title: /暂时连不上目标 Agent/, detail: /504/, actions: ["reconnect"] }],
    ["dropped after a failed release", { ...idle, error: "[doops_operation_failed] helm_preflight_failed: invalid ownership metadata" },
      { kind: "unavailable", title: /暂时连不上目标 Agent/, detail: /^连接已断开/, actions: ["reconnect"] }],
    ["first connection", { ...idle, connection: "connecting", error: null },
      { kind: "connecting", title: /正在连接/, detail: /先填写和校验/, actions: [] }],
    ["connected session without the apply extension", { ...idle, connection: "connected", error: null, supportsApply: false },
      { kind: "unavailable", title: /不支持发布/, detail: /升级/, actions: ["upgrade_agent"] }],
  ]) {
    const availability = deploymentAvailability(state)
    assert.equal(availability.fieldsEditable, true, `${name}: fields must stay editable`)
    assert.equal(availability.canSubmit, false, `${name}: submission must be blocked`)
    const notice = availability.notice
    assert.ok(notice, `${name}: an inline notice is required`)
    assert.equal(notice.kind, expected.kind, name)
    assert.match(notice.title, expected.title instanceof RegExp ? expected.title : new RegExp(`^${expected.title}$`), name)
    assert.match(notice.detail, expected.detail, name)
    assert.deepEqual(notice.actions, expected.actions, name)
    for (const text of [notice.title, notice.detail, availability.blockedReason]) {
      assert.doesNotMatch(text, /\[[a-z_]+\]/, `${name}: no protocol code prefix in user text`)
    }
  }
})

test("a connected session submits unless a release is already running", () => {
  const ready = deploymentAvailability({ connection: "connected", error: null, supportsApply: true, running: false })
  assert.deepEqual(ready, { fieldsEditable: true, canSubmit: true })
  // A connected snapshot's error is the last release's failure, not an availability problem.
  const afterFailure = deploymentAvailability({ connection: "connected", error: "[doops_operation_failed] helm_preflight_failed: x", supportsApply: true, running: false })
  assert.equal(afterFailure.canSubmit, true)
  const running = deploymentAvailability({ connection: "connected", error: null, supportsApply: true, running: true })
  assert.equal(running.canSubmit, false)
  assert.equal(running.fieldsEditable, true)
  assert.match(running.blockedReason, /正在执行/)
  assert.equal(running.notice, undefined)
})

test("form controls: only an in-flight submission locks the fields", () => {
  for (const [name, state, expected] of [
    ["no model, valid and confirmed", { canSubmit: false, valid: true, submitting: false, confirmed: true }, { fieldsDisabled: false, confirmDisabled: true, submitDisabled: true }],
    ["no model, invalid draft", { canSubmit: false, valid: false, submitting: false, confirmed: false }, { fieldsDisabled: false, confirmDisabled: true, submitDisabled: true }],
    ["ready but not confirmed", { canSubmit: true, valid: true, submitting: false, confirmed: false }, { fieldsDisabled: false, confirmDisabled: false, submitDisabled: true }],
    ["ready but invalid", { canSubmit: true, valid: false, submitting: false, confirmed: true }, { fieldsDisabled: false, confirmDisabled: true, submitDisabled: true }],
    ["ready and confirmed", { canSubmit: true, valid: true, submitting: false, confirmed: true }, { fieldsDisabled: false, confirmDisabled: false, submitDisabled: false }],
    ["submitting", { canSubmit: true, valid: true, submitting: true, confirmed: true }, { fieldsDisabled: true, confirmDisabled: true, submitDisabled: true }],
  ]) assert.deepEqual(deploymentFormControls(state), expected, name)
})

test("agent error text drops protocol code prefixes", () => {
  for (const [raw, text] of [
    ["[unavailable] Gateway 还没有可下发到该节点的模型", "Gateway 还没有可下发到该节点的模型"],
    ["[doops_operation_failed] [aborted] 当前会话仍有运行中的任务", "当前会话仍有运行中的任务"],
    ["plain", "plain"], [null, ""], [undefined, ""],
  ]) assert.equal(agentErrorText(raw), text)
})
