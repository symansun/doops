import assert from "node:assert/strict"
import test from "node:test"
import {
  DEPLOYMENT_EXTENSION, agentErrorText, deploymentAvailability, deploymentExtensionCommand, deploymentFormControls,
  deploymentDraftText, deploymentNewSessionWarning, deploymentOutcome, deploymentRequest, deploymentStopFailure,
  deploymentSubmitError, parseDeploymentDraft, singleFlight,
} from "./deployment.ts"
import { AGENT_MODEL_MISSING_HINT, agentErrorMessage } from "./agent-error.ts"
import { gatewayUpgradeRejection } from "./server/gateway-upgrade.ts"

const target = { cluster: "test", instance: "node", key: "test/node" }
const input = { services: "api", release: "app", namespace: "apps", chart: "https://charts.example.test/app.tgz", values: "", repository: "registry.example.test/app", selector: "digest", version: "sha256:" + "a".repeat(64), commit: "b".repeat(40), ci: "https://github.com/example/app/actions/runs/verified", reason: "fix resource cleanup", acceptance: "Final Deployment generation/spec/status, actual imageID and health pass", credentials: "registry-reader" }

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
    ["Gateway rejected the token", { ...idle, error: "[permission_denied] gateway_auth_expired" },
      { kind: "signed_out", title: /登录已失效/, detail: /重新登录/, actions: [] }],
    ["console lost the login", { ...idle, error: "[unauthenticated] 缺少登录 token" },
      { kind: "signed_out", title: /登录已失效/, detail: /重新登录/, actions: [] }],
    ["target offline", { ...idle, error: "[unavailable] target offline: doops-zyj/zyj-test" },
      { kind: "offline", title: /离线/, detail: /重新连接/, actions: ["reconnect"] }],
    // The console answers a refused /v1/rpc upgrade with these messages (gateway-upgrade.ts) and the HTTP status.
    ["login expired at connect", { ...idle, error: `[unauthenticated] ${gatewayUpgradeRejection(401).message}` },
      { kind: "signed_out", title: /登录已失效/, detail: /重新登录/, actions: [] }],
    ["no grant at connect", { ...idle, error: `[permission_denied] ${gatewayUpgradeRejection(403).message}` },
      { kind: "forbidden", title: /没有该目标的使用权限/, detail: /管理 → 权限/, actions: [] }],
    ["target missing at connect", { ...idle, error: `[not_found] ${gatewayUpgradeRejection(404).message}` },
      { kind: "offline", title: /离线/, detail: /重新连接/, actions: ["reconnect"] }],
    ["other upgrade refusal", { ...idle, error: `[unavailable] ${gatewayUpgradeRejection(502).message}` },
      { kind: "unavailable", title: /暂时连不上目标 Agent/, detail: /HTTP 502/, actions: ["reconnect"] }],
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
    // The page reads the console runtime's snapshot, whose error agentErrorMessage has already rewritten
    // (agent-ui-runtime.ts): no code prefix, and the missing-model text replaced by AGENT_MODEL_MISSING_HINT.
    if (state.error) {
      const shown = deploymentAvailability({ ...state, error: agentErrorMessage(state.error) }).notice
      assert.equal(shown?.kind, expected.kind, `${name}: same kind after agentErrorMessage`)
      assert.deepEqual(shown?.actions, expected.actions, `${name}: same actions after agentErrorMessage`)
    }
  }
  const missing = deploymentAvailability({ ...idle, error: AGENT_MODEL_MISSING_HINT }).notice
  assert.equal(missing.kind, "no_model", "the rewritten missing-model hint itself")
  assert.equal(missing.title, "该目标还没有配置 DoAgent 模型，暂时不能发布")
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

// The Agent stages the Chart and values itself (helm_artifact.go managedHelmURL, helm_tool.go fetchHelmValues) and
// the Gateway refuses instructions carrying credential material (credential_scope.go). Inputs they would refuse are
// stopped here, before the release takes the target's mutation lock, and secret-looking text never enters the prompt.
test("release inputs the Agent or Gateway would refuse are stopped before submission", () => {
  for (const [name, invalid, message] of [
    ["chart that is not a .tgz package", { chart: "https://charts.example.test/app" }, /\.tgz/],
    ["chart with a query string", { chart: "https://charts.example.test/app.tgz?token=abc" }, /查询参数/],
    ["chart with a fragment", { chart: "https://charts.example.test/app.tgz#v2" }, /片段/],
    ["chart with percent-encoding", { chart: "https://charts.example.test/my%20app.tgz" }, /百分号/],
    ["chart with a space", { chart: "https://charts.example.test/my app.tgz" }, /空格/],
    ["values URL carrying a token", { values: "https://values.example.test/prod.yaml?token=abc" }, /Values.*凭据/],
    ["second values URL carrying a signature", { values: "https://values.example.test/prod.yaml https://values.example.test/extra.yaml?sig=abc" }, /Values.*凭据/],
    ["CI URL carrying a token", { ci: "https://github.com/example/app/actions/runs/verified?access_token=abc" }, /CI.*凭据/],
    ["GitHub token in the change reason", { reason: `rotate ${"ghp_" + "a".repeat(36)}` }, /凭据内容/],
    ["bearer header in the acceptance", { acceptance: "curl -H 'Authorization: Bearer abc' returns 200" }, /凭据内容/],
    ["basic header in the reason", { reason: "Authorization: Basic dXNlcjpwYXNz" }, /凭据内容/],
    ["private key in the service scope", { services: "-----BEGIN OPENSSH PRIVATE KEY----- x" }, /凭据内容/],
    // The Gateway may refuse "basic " and "bearer " in the instruction; it skips the check when no credential is
    // resolved (credential_runtime.go:166, 190-192), so the message says "may" and how to rewrite the text.
    // It never claims the Gateway always refuses.
    ["basic auth wording the Gateway may refuse", { reason: "修复 basic auth 问题" }, /可能被 Gateway 拒绝.*不应写进发布说明.*改写/],
    ["bearer wording the Gateway may refuse", { acceptance: "返回 401 并要求 Bearer token" }, /可能被 Gateway 拒绝.*不应写进发布说明.*改写/],
    // A URL written in free text is checked like a URL field: no user info, no token-like query keys.
    ["URL in the acceptance carrying a token", { acceptance: "GET https://app.example.com/x?access_token=abc 返回 200" }, /地址.*凭据内容/],
    ["URL in the reason carrying user info", { reason: "从 https://user:pass@git.example.test/x 拉取" }, /地址.*凭据内容/],
    // R6-1: a connection string of any scheme with a password stays out of the prompt, wherever it sits in the text.
    ["postgres connection string with a password", { acceptance: "postgres://app:S3cretPw@db.apps:5432/app 迁移到新库" }, /地址.*凭据内容/],
    ["redis connection string with a password", { reason: "redis://:S3cretPw@cache:6379/0 改为哨兵模式" }, /地址.*凭据内容/],
    ["mongodb connection string with a password", { acceptance: "mongodb://root:S3cretPw@mongo:27017/admin 可连接" }, /地址.*凭据内容/],
    ["connection string after Chinese text", { reason: "从 postgres://app:S3cretPw@db.apps:5432/app 迁移到新库" }, /地址.*凭据内容/],
    ["connection string in the service scope", { services: "api（mysql://root:S3cretPw@db:3306/app）" }, /地址.*凭据内容/],
    // R6-2: token-like query keys in a URL inside free text are still stopped.
    ...[["page_token", "abc"], ["access_token", "abc"], ["api_key", "abc"], ["gitlab_pat", "abc"], ["pat", "abc"], ["sig", "abc"], ["password", "abc"]]
      .map(([key, value]) => [`URL in free text with ?${key}=`, { acceptance: `GET https://api.example.test/v1/items?${key}=${value} 返回 200` }, /地址.*凭据内容/]),
    // The URL fields keep the Gateway's rules (helm_tool.go fetchHelmValues), where data and a pat suffix are refused.
    ["values URL with ?data=", { values: "https://values.example.test/prod.yaml?data=1" }, /Values.*凭据/],
    ["values URL with ?compat=", { values: "https://values.example.test/prod.yaml?compat=true" }, /Values.*凭据/],
    // tunnel_hub.go validateExplicitCredentialNames refuses a repeated name.
    ["credential named twice", { credentials: "registry-reader, registry-reader" }, /重复/],
    // agent-session.ts refuses a form payload over 64 KB with a generic protocol error.
    ["form over the 64 KB command limit", { acceptance: "x".repeat(70_000) }, /过长/],
    // Helm refuses release names over 53 characters at preflight.
    ["release name over Helm's limit", { release: "a".repeat(54) }, /53/],
    ["namespace over the DNS label limit", { namespace: "a".repeat(64) }, /63/],
  ]) {
    assert.throws(() => deploymentRequest(target, { ...input, ...invalid }), message, name)
  }
  for (const reason of ["修复 basic auth 问题", "返回 401 并要求 Bearer token"]) {
    assert.throws(() => deploymentRequest(target, { ...input, reason }), (error) => !/Gateway 会/.test(error.message), reason)
  }
  for (const [name, valid] of [
    ["values URL with a plain query", { values: "https://gitlab.example.test/api/v4/projects/1/repository/files/values.yaml/raw?ref=main" }],
    ["chart on an IP and port", { chart: "https://10.0.0.5:8443/charts/app-1.2.0.tgz" }],
    ["text that names a credential concept without a value", { reason: "修复 token 刷新问题" }],
    // R4-6: a half-width "?" in free text is not a URL query; the Gateway accepts these (credential_scope.go:193).
    ["question then a word ending in password", { reason: "Does login work? Verify with the test password" }],
    ["path with a question mark then token", { acceptance: "登录接口 /api/login?返回新的 token" }],
    ["question then a word ending in pat", { reason: "回滚后兼容吗? compat" }],
    ["question then signature", { services: "接口改名? 旧字段 signature" }],
    ["URL without a query", { acceptance: "验收：GET https://app.example.com/healthz 返回 200" }],
    ["URL with a plain query", { acceptance: "GET https://app.example.com/search?q=release 返回 200" }],
    ["JDBC address without a password", { acceptance: "jdbc:postgresql://db:5432/app 可连接" }],
    ["MySQL address without a password", { acceptance: "jdbc:mysql://db:3306/app 可连接" }],
    ["postgres address with only a user", { reason: "postgres://db.apps:5432/app 迁移到新库" }],
    // R6-2: the Gateway does not check URLs inside the instruction, so ordinary query keys in free text pass.
    ["URL in free text with ?compat=", { acceptance: "GET https://api.example.test/v1/items?compat=true 返回 200" }],
    ["URL in free text with ?data=", { acceptance: "GET https://api.example.test/v1/items?data=1 返回 200" }],
    ["URL in free text with ?stringData=", { reason: "回调 https://hooks.example.test/x?stringData=on 不再报错" }],
    ["URL in free text with a dashboard query", { acceptance: "https://grafana.example.test/d/app?orgId=1&from=now-1h 无告警" }],
  ]) assert.doesNotThrow(() => deploymentRequest(target, { ...input, ...valid }), name)
})

test("only a Gateway grant refusal is reported as a missing grant", () => {
  const idle = { connection: "disconnected", supportsApply: true, running: false }
  for (const [name, error, kind] of [
    ["grant refusal", "[permission_denied] forbidden: ask on doops-zyj/zyj-test", "forbidden"],
    // HandleClientRPC answers a user without a grant with 403 before the WebSocket upgrade, and a bad token with 401.
    ["grant refusal at connect", `[permission_denied] ${gatewayUpgradeRejection(403).message}`, "forbidden"],
    ["token refusal at connect", `[unauthenticated] ${gatewayUpgradeRejection(401).message}`, "signed_out"],
    ["console gateway allowlist", "[permission_denied] 该 gateway 未获控制台授权", "console_policy"],
    ["production gateway over http", "[permission_denied] 生产 gateway 必须使用 https", "console_policy"],
    ["internal-only tool", "[permission_denied] credential control-plane tools are internal only", "unavailable"],
  ]) {
    assert.equal(deploymentAvailability({ ...idle, error }).notice.kind, kind, name)
    assert.equal(deploymentAvailability({ ...idle, error: agentErrorMessage(error) }).notice.kind, kind, `${name} after agentErrorMessage`)
  }
})

// After a release ends, the session error is the release's own result. Each category the Gateway, Agent or Helm tool
// returns (next.go, helm_release.go, NATURAL_LANGUAGE_DEPLOYMENT.md) says whether anything changed and what to do next.
test("release outcome says whether the target changed and what to do next", () => {
  const failed = (message, code = "doops_operation_failed") => ({ status: "failed", error: `[${code}] ${message}` })
  for (const [name, state, expected] of [
    ["not started", { status: "idle", error: null }, { kind: "idle", title: /填写并确认/ }],
    // The SDK reports "launching" before it has any session snapshot (emptySession); nothing was submitted yet.
    ["no snapshot yet", { status: "launching", error: "[unavailable] connect ECONNREFUSED 127.0.0.1:18080" }, { kind: "idle", title: /填写并确认/ }],
    ["running", { status: "running", error: null }, { kind: "running", title: /执行中/ }],
    ["finished", { status: "completed", error: null }, { kind: "succeeded", detail: /验收条件/ }],
    ["connection cut before the terminal result", failed("Gateway 已断开，远端终态未确认", "doops_apply_outcome_unknown"),
      { kind: "unknown", title: /结果未确认/, detail: /核对 release.*不会自动重试/ }],
    ["Agent could not confirm the outcome", failed("execution_outcome_unknown: cancellation not confirmed"), { kind: "unknown", title: /结果未确认/ }],
    ["missing turn_finished", failed("缺少 DoAgent turn_finished 终态，执行结果未确认"), { kind: "unknown", title: /结果未确认/ }],
    // A refused admission is not a release result: queued proves nothing ran, and the older
    // Gateway's unknown wrapper around the same token stays the fail-closed mutation gate.
    ["queued admission at the target", failed("remote error: doagent_prompt_queued"),
      { kind: "failed", title: /本次发布没有执行/, detail: /没有空闲的执行容量.*不需要核对 Helm 或 Deployment/ }],
    ["unknown wrapper around a queued admission", failed("execution_outcome_unknown: remote error: doagent_prompt_queued"),
      { kind: "unknown", title: /结果未确认/, detail: /不会自动重试/ }],
    ["queue cleanup was never confirmed", failed("doagent_prompt_queue_cleanup_unknown"),
      { kind: "unknown", title: /结果未确认/, detail: /审计.*队列.*Kubernetes/ }],
    ["empty prompt was refused", failed("doagent_prompt_no_prompt"),
      { kind: "failed", title: /发布任务内容为空/, detail: /没有执行/ }],
    ["pre-admission rejection keeps its existing failure category", failed("doagent_prompt_rejected"),
      { kind: "failed", title: /^发布失败$/, detail: /doagent_prompt_rejected/ }],
    ["stopped", failed("operation canceled"), { kind: "cancelled", title: /已停止/, detail: /不会自动回滚/ }],
    ["several Helm credentials bound", failed("credential_reference_ambiguous"),
      { kind: "failed", title: /多个 Helm 仓库凭据/, detail: /凭据资源名.*没有执行/, actions: ["open_credentials"] }],
    ["Chart repository needs a credential", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_chart_requires_credential: helm_artifact_http_401"),
      { kind: "failed", title: /Chart 仓库需要凭据/, detail: /helmPull/, actions: ["open_credentials"] }],
    ["credential never promoted", failed("credential_version_not_found"), { kind: "failed", title: /没有激活的版本/, actions: ["open_credentials"] }],
    ["named credential not usable", failed("credential_grant_denied"), { kind: "failed", title: /凭据在该目标上不可用/, detail: /凭据资源名/ }],
    ["same session busy", failed("session has an active operation; wait for terminal evidence: op-0123"), { kind: "failed", title: /目标正忙/, detail: /没有执行/ }],
    ["target queue timeout", failed("target queue timeout: doops-zyj/zyj-test"), { kind: "failed", title: /目标正忙/ }],
    ["grant removed", failed("forbidden: exec on doops-zyj/zyj-test"), { kind: "failed", title: /没有该目标的使用权限/, detail: /没有执行/ }],
    ["target went offline", failed("target offline: doops-zyj/zyj-test"), { kind: "failed", title: /离线/, detail: /没有执行/ }],
    ["objects owned by nobody", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_preflight_failed: Unable to continue with install: Service \"app\" exists and cannot be imported into the current release: invalid ownership metadata"),
      { kind: "failed", title: /已有同名对象/, detail: /接管.*没有被修改/ }],
    ["preflight", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_preflight_failed: values don't meet the schema"), { kind: "failed", title: /预检失败/, detail: /没有被修改/ }],
    ["Chart download", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_artifact_http_404"), { kind: "failed", title: /Chart 或 values/, detail: /没有被修改/ }],
    ["values download", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_values_unavailable"), { kind: "failed", title: /Chart 或 values/ }],
    ["Helm command failed", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_mutation_failed: UPGRADE FAILED: timed out"), { kind: "failed", title: /Helm 变更失败/, detail: /helm-history/ }],
    ["cluster does not match", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_verification_failed: Deployment apps/app 1/2 ready"), { kind: "failed", title: /校验未通过/, detail: /Kubernetes/ }],
    ["call ran out of time", failed("doagent_apply_tool_failed: mcp_doops_helm: helm_interrupted: revision 7 pending-upgrade"), { kind: "failed", title: /超时/, detail: /revision 7.*核对/ }],
    ["no trusted evidence", failed("doagent_apply_tool_evidence_missing"), { kind: "failed", title: /可信的变更证据/ }],
    ["anything else", failed("doagent_execution_failed"), { kind: "failed", title: /^发布失败$/, detail: /doagent_execution_failed/ }],
  ]) {
    const outcome = deploymentOutcome(state)
    assert.equal(outcome.kind, expected.kind, name)
    assert.equal(deploymentOutcome({ ...state, error: agentErrorMessage(state.error) }).kind, expected.kind, `${name} after agentErrorMessage`)
    if (expected.title) assert.match(outcome.title, expected.title, name)
    if (expected.detail) assert.match(outcome.detail, expected.detail, name)
    assert.deepEqual(outcome.actions, expected.actions || [], name)
    for (const text of [outcome.title, outcome.detail]) assert.doesNotMatch(text, /\[[a-z_]+\]/, `${name}: no protocol code prefix`)
  }
  // A Helm error can carry a whole stderr; the summary stays readable and the trace keeps the full text.
  const long = deploymentOutcome(failed(`doagent_execution_failed: ${"x".repeat(300_000)}`))
  assert.ok(long.detail.length <= 600, `detail is ${long.detail.length} characters`)
  assert.match(long.detail, /…/)
  const unreachable = deploymentAvailability({ connection: "disconnected", error: `[unavailable] ${"y".repeat(300_000)}`, supportsApply: true, running: false })
  assert.ok(unreachable.notice.detail.length <= 600, `notice detail is ${unreachable.notice.detail.length} characters`)
})

test("a refused submission explains whether the release started", () => {
  for (const [name, raw, expected] of [
    ["previous release running", "[aborted] 当前会话仍有运行中的任务", /上一次发布还在执行/],
    ["replay of an unconfirmed command", "[aborted] 该命令结果未确认，禁止重放", /结果未确认.*核对/],
    ["session held by an unconfirmed stop", "[aborted] 远端结果未确认，不能复用此会话；请先核对远端状态", /结果未确认.*核对/],
    ["session moved on", "[failed_precondition] command_position_mismatch", /重新勾选/],
    ["SDK not ready", "agent_workbench_session_not_ready", /连接未就绪/],
    ["SDK connection closed", "agent_workbench_connection_closed", /连接未就绪/],
    ["no response", "[unavailable] Failed to fetch", /可能已经开始.*不要重复提交/],
    ["proxy timeout without a body", "[deadline_exceeded] 智能体请求失败 (504)：错误响应无效", /可能已经开始/],
    ["target offline", "[unavailable] target offline: doops-zyj/zyj-test", /离线.*没有执行/],
    ["grant refusal", "[permission_denied] forbidden: ask on doops-zyj/zyj-test", /没有该目标的使用权限.*没有执行/],
    ["no model", "[unavailable] Gateway 还没有可下发到该节点的模型", /没有配置 DoAgent 模型.*没有执行/],
    // Admission categories the Gateway answers with before any turn exists (doagent_prompt_admission.go).
    ["queued admission", "[doops_operation_failed] remote error: doagent_prompt_queued", /容量忙.*没有执行.*新发布/],
    ["unknown wrapper around a queued admission", "[doops_operation_failed] execution_outcome_unknown: remote error: doagent_prompt_queued", /没有收到确认.*核对 Helm\/Deployment/],
    ["queue cleanup was never confirmed", "[doops_operation_failed] doagent_prompt_queue_cleanup_unknown", /清理结果未确认.*审计/],
    ["empty prompt", "[failed_precondition] doagent_prompt_no_prompt", /任务内容为空.*没有提交/],
    ["server-side field check", "[invalid_argument] Chart 必须为无凭据的 HTTPS 地址", /^Chart 必须为无凭据的 HTTPS 地址$/],
  ]) {
    const text = deploymentSubmitError(raw)
    assert.match(text, expected, name)
    assert.doesNotMatch(text, /\[[a-z_]+\]|agent_workbench_/, `${name}: no protocol code in user text`)
  }
})

// The Gateway serializes mutations within one session only (tunnel_hub.go exclusiveLockKey). A new release session after
// an unconfirmed result could run beside a release that is still going, so the operator confirms first.
test("starting a new release session after an unconfirmed result asks for confirmation", () => {
  for (const [kind, warned] of [["unknown", true], ["idle", false], ["succeeded", false], ["failed", false], ["cancelled", false], ["running", false]]) {
    const warning = deploymentNewSessionWarning(kind)
    if (warned) assert.match(warning, /结果未确认.*核对/, kind)
    else assert.equal(warning, undefined, kind)
  }
})

test("a stop that did not take effect says what happened to the release", () => {
  for (const [name, raw, expected] of [
    ["stop requested, no terminal evidence", "已请求停止，远端终态未确认", /没有确认终态.*仍在执行.*核对/],
    ["release already finished", "任务已结束，取消未生效", /已经结束.*以执行过程/],
    ["nothing running", "[aborted] 当前没有运行中的任务", /已经结束/],
    ["unconfirmed session", "[aborted] 远端结果未确认，不能复用此会话；请先核对远端状态", /结果未确认.*核对/],
    ["wrong turn", "[aborted] 中断目标不是当前任务", /不是当前发布/],
    ["other", "[unavailable] Failed to fetch", /^停止请求失败：Failed to fetch/],
  ]) {
    const text = deploymentStopFailure(raw)
    assert.match(text, expected, name)
    assert.doesNotMatch(text, /\[[a-z_]+\]/, name)
  }
})

// A fast double click submits twice before React re-renders the disabled button; the second call must be dropped.
test("a second submission while one is in flight is dropped", async () => {
  let release
  const calls = []
  const submit = singleFlight(async (value) => { calls.push(value); await new Promise((resolve) => { release = resolve }) })
  const first = submit("a")
  assert.equal(await submit("b"), false, "concurrent call is dropped")
  release()
  assert.equal(await first, true)
  assert.deepEqual(calls, ["a"])
  const third = submit("c")
  release()
  assert.equal(await third, true, "the guard is released after a call settles")
  assert.deepEqual(calls, ["a", "c"])
  const failing = singleFlight(async () => { throw new Error("boom") })
  await assert.rejects(failing(), /boom/)
  await assert.rejects(failing(), /boom/, "a failed submission releases the guard")
})

// The panel unmounts when the operator switches tabs or reloads; the draft survives in the tab's sessionStorage.
test("a release draft round-trips only when well formed and free of credential material", () => {
  assert.deepEqual(parseDeploymentDraft(deploymentDraftText(input)), input)
  for (const [name, raw] of [
    ["nothing saved", null], ["not JSON", "{"], ["array", "[]"],
    ["missing field", JSON.stringify({ ...input, ci: undefined })],
    ["extra field", JSON.stringify({ ...input, token: "x" })],
    ["non-string field", JSON.stringify({ ...input, release: 1 })],
    ["unknown selector", JSON.stringify({ ...input, selector: "latest" })],
    ["saved with a token in it", JSON.stringify({ ...input, reason: `rotate ${"ghp_" + "a".repeat(36)}` })],
  ]) assert.equal(parseDeploymentDraft(raw), undefined, name)
  assert.equal(deploymentDraftText({ ...input, acceptance: "Authorization: Bearer abc" }), null, "credential material is never stored")
  assert.equal(deploymentDraftText({ ...input, chart: "https://user:pass@charts.example.test/app.tgz" }), null, "URL user info is never stored")
  assert.equal(deploymentDraftText({ ...input, acceptance: "GET https://app.example.com/x?access_token=abc 返回 200" }), null, "a token in a URL in free text is never stored")
  for (const text of [
    "postgres://app:S3cretPw@db.apps:5432/app 迁移到新库", "从 postgres://app:S3cretPw@db.apps:5432/app 迁移到新库",
    "redis://:S3cretPw@cache:6379/0 改为哨兵模式", "mongodb://root:S3cretPw@mongo:27017/admin 可连接",
  ]) {
    const draft = deploymentDraftText({ ...input, reason: text })
    assert.equal(draft, null, `a connection string password is never stored: ${text}`)
    assert.doesNotMatch(String(draft), /S3cretPw/)
  }
  const plain = { ...input, reason: "Does login work? Verify with the test password" }
  assert.deepEqual(parseDeploymentDraft(deploymentDraftText(plain)), plain, "an ordinary question in free text is kept")
})
