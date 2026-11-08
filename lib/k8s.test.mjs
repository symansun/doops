import assert from "node:assert/strict"
import test from "node:test"
import { EMPTY_K8S_INPUT, K8S_OPERATIONS, K8S_TOOL, isK8sMutation, k8sRequest, runK8s } from "./k8s.ts"

const session = "web-k8s"
const form = (patch) => ({ ...EMPTY_K8S_INPUT, ...patch })

// Each row mirrors a `doops k8s` invocation and the payload buildK8SRequest produces for it.
const mappings = [
  ["get pods -n apps -l app=api -o wide", { operation: "get", kind: "Pods", namespace: "apps", selector: "app=api", output: "wide" },
    { operation: "get", kind: "pods", namespace: "apps", selector: "app=api", output: "wide" }],
  ["get deploy api -A", { operation: "get", kind: "deploy", name: "api", allNamespaces: true },
    { operation: "get", kind: "deploy", name: "api", all_namespaces: true }],
  ["describe deployment api -n apps", { operation: "describe", kind: "deployment", name: "api", namespace: "apps" },
    { operation: "describe", kind: "deployment", name: "api", namespace: "apps" }],
  ["logs pod/api-0 -c app --tail 200 -p --since 10m", { operation: "logs", resource: "Pod/api-0", namespace: "apps", container: "app", tail: "200", previous: true, since: "10m" },
    { operation: "logs", resource: "pod/api-0", namespace: "apps", container: "app", tail: 200, previous: true, since: "10m" }],
  ["events -n apps", { operation: "events", namespace: "apps" },
    { operation: "events", namespace: "apps" }],
  ["events -n apps --for deployment/api", { operation: "events", namespace: "apps", resource: "deployment/api" },
    { operation: "events", namespace: "apps", resource: "deployment/api" }],
  ["top nodes", { operation: "top", kind: "nodes" },
    { operation: "top", kind: "nodes" }],
  ["rollout status deployment/api --timeout 120s", { operation: "rollout-status", resource: "deployment/api", namespace: "apps", timeout: "120s" },
    { operation: "rollout-status", resource: "deployment/api", namespace: "apps", timeout: "120s" }],
  ["helm list -A", { operation: "helm-list", allNamespaces: true },
    { operation: "helm-list", all_namespaces: true }],
  ["helm history app -n apps", { operation: "helm-history", name: "app", namespace: "apps" },
    { operation: "helm-history", name: "app", namespace: "apps" }],
  ["helm status app -n apps", { operation: "helm-status", name: "app", namespace: "apps" },
    { operation: "helm-status", name: "app", namespace: "apps" }],
  ["helm values app -n apps", { operation: "helm-values", name: "app", namespace: "apps" },
    { operation: "helm-values", name: "app", namespace: "apps" }],
  ["rollout restart deployment/api --confirm", { operation: "rollout-restart", resource: "deployment/api", namespace: "apps", confirm: true },
    { operation: "rollout-restart", resource: "deployment/api", namespace: "apps", confirm: true }],
  ["scale deployment/api --replicas 0 --confirm", { operation: "scale", resource: "deployment/api", namespace: "apps", replicas: "0", confirm: true },
    { operation: "scale", resource: "deployment/api", namespace: "apps", replicas: 0, confirm: true }],
  ["node cordon node-1 --confirm", { operation: "cordon", name: "node-1", confirm: true },
    { operation: "cordon", name: "node-1", confirm: true }],
  ["node uncordon node-1 --confirm", { operation: "uncordon", name: "node-1", confirm: true },
    { operation: "uncordon", name: "node-1", confirm: true }],
  ["delete pod api-0 --confirm", { operation: "delete-pod", name: "api-0", namespace: "apps", confirm: true },
    { operation: "delete-pod", name: "api-0", namespace: "apps", confirm: true }],
]

for (const [cli, input, payload] of mappings) {
  test(`form maps to doops_k8s payload: ${cli}`, () => {
    assert.deepEqual(k8sRequest(session, form(input)), { session_id: session, ...payload })
  })
}

test("every CLI operation is covered and mutations match the CLI --confirm set", () => {
  assert.deepEqual(new Set(mappings.map(([, input]) => input.operation)), new Set(Object.keys(K8S_OPERATIONS)))
  const mutations = Object.keys(K8S_OPERATIONS).filter(isK8sMutation).sort()
  assert.deepEqual(mutations, ["cordon", "delete-pod", "rollout-restart", "scale", "uncordon"])
})

test("fields hidden for an operation are not sent", () => {
  const payload = k8sRequest(session, form({ operation: "describe", kind: "pod", name: "api-0", selector: "app=api", tail: "9", replicas: "3", confirm: true }))
  assert.deepEqual(payload, { session_id: session, operation: "describe", kind: "pod", name: "api-0" })
})

const invalid = [
  ["unconfirmed restart", { operation: "rollout-restart", resource: "deployment/api" }, /确认/],
  ["unconfirmed scale", { operation: "scale", resource: "deployment/api", replicas: "2" }, /确认/],
  ["unconfirmed cordon", { operation: "cordon", name: "node-1" }, /确认/],
  ["unconfirmed uncordon", { operation: "uncordon", name: "node-1" }, /确认/],
  ["unconfirmed delete pod", { operation: "delete-pod", name: "api-0" }, /确认/],
  ["secret read", { operation: "get", kind: "secrets" }, /Secret/],
  ["serviceaccount describe", { operation: "describe", kind: "sa", name: "default" }, /Secret/],
  ["secret logs resource", { operation: "logs", resource: "secret/x" }, /Secret|不支持/],
  ["describe without name", { operation: "describe", kind: "pod" }, /名称/],
  ["logs without resource", { operation: "logs" }, /资源/],
  ["logs bad resource", { operation: "logs", resource: "api" }, /kind\/name/],
  ["logs non-loggable kind", { operation: "logs", resource: "service/api" }, /不支持/],
  ["rollout status replicaset", { operation: "rollout-status", resource: "rs/api" }, /不支持/],
  ["scale daemonset", { operation: "scale", resource: "ds/agent", replicas: "1", confirm: true }, /不支持/],
  ["scale without replicas", { operation: "scale", resource: "deployment/api", confirm: true }, /副本数/],
  ["scale negative replicas", { operation: "scale", resource: "deployment/api", replicas: "-1", confirm: true }, /整数/],
  ["scale too many replicas", { operation: "scale", resource: "deployment/api", replicas: "10001", confirm: true }, /10000/],
  ["tail not integer", { operation: "logs", resource: "pod/api", tail: "1e3" }, /整数/],
  ["top services", { operation: "top", kind: "services" }, /pods 或 nodes/],
  ["namespace and all namespaces", { operation: "get", kind: "pods", namespace: "apps", allNamespaces: true }, /只能选一个/],
  ["helm status without namespace", { operation: "helm-status", name: "app" }, /命名空间/],
  ["helm history without release", { operation: "helm-history", namespace: "apps" }, /Release/],
  ["invalid namespace", { operation: "get", kind: "pods", namespace: "Apps_1" }, /命名空间/],
  ["invalid since", { operation: "logs", resource: "pod/api", since: "10 minutes" }, /10m/],
  ["invalid selector", { operation: "get", kind: "pods", selector: "app=$(id)" }, /选择器/],
]

for (const [name, input, message] of invalid) {
  test(`invalid input is rejected before sending: ${name}`, async () => {
    assert.throws(() => k8sRequest(session, form(input)), message)
    let sent = 0
    await assert.rejects(runK8s(async () => { sent += 1 }, { cluster: "test", instance: "node" }, session, form(input)), message)
    assert.equal(sent, 0)
  })
}

test("confirmed write is sent once to the doops_k8s tool on the selected target", async () => {
  const calls = []
  await runK8s(async (opts) => { calls.push(opts) }, { cluster: "test", instance: "node" }, session, form({ operation: "delete-pod", name: "api-0", namespace: "apps", confirm: true }))
  assert.deepEqual(calls, [{
    cluster: "test",
    instance: "node",
    tool: K8S_TOOL,
    arguments: { session_id: session, operation: "delete-pod", name: "api-0", namespace: "apps", confirm: true },
  }])
  assert.equal(K8S_TOOL, "doops_k8s")
})

test("read operations never carry confirm even when the checkbox state is set", () => {
  const payload = k8sRequest(session, form({ operation: "get", kind: "pods", confirm: true }))
  assert.equal("confirm" in payload, false)
})
