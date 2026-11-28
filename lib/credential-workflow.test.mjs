import assert from "node:assert/strict"
import test from "node:test"
import {
  AGENT_WORKSPACE_ALLOWED_MODES,
  bindingNeedsExplicitNamespace,
  buildStructuredCredentialPayload,
  classifyAgentModelError,
  credentialPayloadPlaceholder,
  defaultBindingNamespace,
  resolveCredentialStage,
  sortTargetCredentialsForAssistant,
} from "./credential-workflow.ts"
import { AGENT_MODEL_MISSING, AGENT_MODEL_MISSING_HINT } from "./agent-error.ts"

const target = { cluster: "prod-cn", instance: "web-01" }

test("AGENT_WORKSPACE_ALLOWED_MODES restricts AI Assistant to work mode only (no code mode)", () => {
  assert.deepEqual([...AGENT_WORKSPACE_ALLOWED_MODES], ["work"])
})

test("buildStructuredCredentialPayload serializes valid modelProvider payload and strips duplicate provider prefix", () => {
  const raw = buildStructuredCredentialPayload({
    type: "modelProvider",
    values: {
      providerId: "deepseek",
      modelName: "deepseek/deepseek-chat",
      apiKey: "sk-synthetic-canary-12345",
      baseURL: "https://api.deepseek.com/v1",
    },
  })
  assert.deepEqual(JSON.parse(raw), {
    model: "deepseek/deepseek-chat",
    provider: {
      deepseek: {
        options: {
          apiKey: "sk-synthetic-canary-12345",
          baseURL: "https://api.deepseek.com/v1",
        },
      },
    },
  })

  // Optional baseURL omitted cleanly when blank.
  const withoutBase = buildStructuredCredentialPayload({
    type: "modelProvider",
    values: {
      providerId: "openai",
      modelName: "gpt-5.4",
      apiKey: "sk-synthetic-canary-67890",
      baseURL: "   ",
    },
  })
  assert.deepEqual(JSON.parse(withoutBase), {
    model: "openai/gpt-5.4",
    provider: {
      openai: {
        options: {
          apiKey: "sk-synthetic-canary-67890",
        },
      },
    },
  })
})

test("buildStructuredCredentialPayload rejects invalid modelProvider, URLs, indirect key refs, and duplicate opaque keys", () => {
  assert.throws(() => buildStructuredCredentialPayload({
    type: "modelProvider",
    values: { providerId: "bad/id", modelName: "gpt-5.4", apiKey: "sk-synthetic" },
  }), /服务商标识/)

  assert.throws(() => buildStructuredCredentialPayload({
    type: "modelProvider",
    values: { providerId: "openai", modelName: "gpt-5.4", apiKey: "{env:OPENAI_API_KEY}" },
  }), /不能使用环境变量或文件引用/)

  assert.throws(() => buildStructuredCredentialPayload({
    type: "modelProvider",
    values: { providerId: "openai", modelName: "gpt-5.4", apiKey: "sk-synthetic", baseURL: "https://user:pass@api.example.com/v1" },
  }), /不能内嵌账号或密码/)

  assert.throws(() => buildStructuredCredentialPayload({
    type: "opaque",
    values: { entries: [{ key: "DB_URL", value: "v1" }, { key: "DB_URL", value: "v2" }] },
  }), /重复/)

  assert.throws(() => buildStructuredCredentialPayload({
    type: "opaque",
    values: { entries: [{ key: "  ", value: "" }] },
  }), /至少填写一组键值对/)
})

test("buildStructuredCredentialPayload supports registry, gitToken, helmRepository, tls, and opaque types", () => {
  assert.deepEqual(
    JSON.parse(buildStructuredCredentialPayload({
      type: "registry",
      values: { server: "registry.example.test", username: "bot", password: "synthetic-registry-pass" },
    })),
    { server: "registry.example.test", username: "bot", password: "synthetic-registry-pass" },
  )
  assert.deepEqual(
    JSON.parse(buildStructuredCredentialPayload({
      type: "gitToken",
      values: { url: "https://git.example.test", username: "bot", token: "synthetic-git-token" },
    })),
    { url: "https://git.example.test", username: "bot", token: "synthetic-git-token" },
  )
  assert.deepEqual(
    JSON.parse(buildStructuredCredentialPayload({
      type: "helmRepository",
      values: { url: "https://charts.example.test", username: "bot", password: "synthetic-helm-pass" },
    })),
    { url: "https://charts.example.test", username: "bot", password: "synthetic-helm-pass" },
  )
  assert.deepEqual(
    JSON.parse(buildStructuredCredentialPayload({
      type: "tls",
      values: { certificate: "SYNTHETIC-CERT", privateKey: "SYNTHETIC-KEY" },
    })),
    { certificate: "SYNTHETIC-CERT", privateKey: "SYNTHETIC-KEY" },
  )
  assert.deepEqual(
    JSON.parse(buildStructuredCredentialPayload({
      type: "opaque",
      values: { entries: [{ key: "DB_USER", value: "app" }, { key: "DB_PASS", value: "synthetic-db-pass" }] },
    })),
    { data: { DB_USER: "app", DB_PASS: "synthetic-db-pass" } },
  )

  for (const type of ["modelProvider", "registry", "gitToken", "helmRepository", "tls", "opaque"]) {
    assert.doesNotThrow(() => JSON.parse(credentialPayloadPlaceholder(type)), `placeholder for ${type} must be valid JSON`)
  }
})

test("modelProvider binding does not require manual Kubernetes Secret namespace while other uses still do", () => {
  assert.equal(bindingNeedsExplicitNamespace("modelProvider"), false)
  assert.equal(defaultBindingNamespace("modelProvider"), "default")
  for (const use of ["imagePull", "tls", "opaqueSecret", "database", "helmPull", "gitCheckout"]) {
    assert.equal(bindingNeedsExplicitNamespace(use), true, use)
    assert.equal(defaultBindingNamespace(use), "", use)
  }
})

test("resolveCredentialStage walks create -> put -> bind -> verify -> promote -> active accurately", () => {
  const baseResource = {
    id: "cred-1", name: "llm-prod", scope: "platform", type: "modelProvider", state: "active", bindings: [],
  }
  const staged = { id: "ver-1", credential_id: "cred-1", payload_digest: "sha256:aaa", state: "staged" }
  const active = { id: "ver-1", credential_id: "cred-1", payload_digest: "sha256:aaa", state: "active" }
  const binding = {
    id: "grant-1", credential_id: "cred-1", state: "active",
    scope: { cluster: "prod-cn", instance: "web-01", namespace: "default", use: "modelProvider", workload: { kind: "", name: "" } },
  }

  assert.equal(resolveCredentialStage({ resource: baseResource, versions: [], target }).key, "needs_payload")
  assert.equal(resolveCredentialStage({ resource: baseResource, versions: [staged], selectedVersion: staged, target }).key, "needs_binding")

  const needsVerify = resolveCredentialStage({
    resource: { ...baseResource, bindings: [binding] },
    versions: [staged],
    selectedVersion: staged,
    target,
    verified: false,
  })
  assert.equal(needsVerify.key, "needs_verify")
  assert.equal(needsVerify.primaryAction, "verify")
  assert.equal(needsVerify.verifyBinding?.id, "grant-1")

  const readyPromote = resolveCredentialStage({
    resource: { ...baseResource, bindings: [binding] },
    versions: [staged],
    selectedVersion: staged,
    target,
    verified: true,
  })
  assert.equal(readyPromote.key, "ready_to_promote")
  assert.equal(readyPromote.primaryAction, "promote")

  const activeStage = resolveCredentialStage({
    resource: { ...baseResource, bindings: [binding] },
    versions: [active],
    selectedVersion: active,
    target,
  })
  assert.equal(activeStage.key, "active")
  assert.equal(activeStage.targetBound, true)
  assert.equal(activeStage.primaryAction, null)

  // Bound to another target -> suggests binding current target.
  const otherTargetStage = resolveCredentialStage({
    resource: { ...baseResource, bindings: [binding] },
    versions: [active],
    selectedVersion: active,
    target: { cluster: "staging", instance: "db-01" },
  })
  assert.equal(otherTargetStage.key, "active")
  assert.equal(otherTargetStage.targetBound, false)
  assert.equal(otherTargetStage.primaryAction, "binding")
})

test("classifyAgentModelError identifies modelProvider configuration errors and sortTargetCredentialsForAssistant prioritizes modelProvider", () => {
  assert.deepEqual(classifyAgentModelError(AGENT_MODEL_MISSING), { isModelConfigError: true, missingModel: true })
  assert.deepEqual(classifyAgentModelError(AGENT_MODEL_MISSING_HINT), { isModelConfigError: true, missingModel: true })
  assert.deepEqual(classifyAgentModelError("DoAgent 模型连接不可用"), { isModelConfigError: true, missingModel: false })
  assert.deepEqual(classifyAgentModelError("target offline: prod-cn/web-01"), { isModelConfigError: false, missingModel: false })

  const sorted = sortTargetCredentialsForAssistant([
    { id: "c1", name: "z-registry", scope: "personal", type: "registry", state: "active" },
    { id: "c2", name: "a-git", scope: "personal", type: "gitToken", state: "active" },
    { id: "c3", name: "z-llm", scope: "platform", type: "modelProvider", state: "active" },
  ])
  assert.deepEqual(sorted.map((item) => item.id), ["c3", "c2", "c1"])
})
