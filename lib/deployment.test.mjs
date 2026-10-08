import assert from "node:assert/strict"
import test from "node:test"
import { deploymentRequest } from "./deployment.ts"

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
