import assert from "node:assert/strict"
import test from "node:test"
import { DEPLOYMENT_EXTENSION, deploymentExtensionCommand, deploymentRequest } from "./deployment.ts"

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
