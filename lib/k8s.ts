// Web form for the Agent doops_k8s tool. The payload mirrors buildK8SRequest in
// backend/cli/cli/doops/k8s.go so the console and `doops k8s` call the same contract.

export const K8S_TOOL = "doops_k8s"

export type K8sOperation =
  | "get"
  | "describe"
  | "logs"
  | "events"
  | "top"
  | "rollout-status"
  | "helm-list"
  | "helm-history"
  | "helm-status"
  | "helm-values"
  | "rollout-restart"
  | "scale"
  | "cordon"
  | "uncordon"
  | "delete-pod"

export type K8sField =
  | "kind"
  | "name"
  | "resource"
  | "namespace"
  | "allNamespaces"
  | "selector"
  | "output"
  | "container"
  | "tail"
  | "previous"
  | "since"
  | "timeout"
  | "replicas"

export interface K8sInput {
  operation: K8sOperation
  kind: string
  name: string
  resource: string
  namespace: string
  allNamespaces: boolean
  selector: string
  output: string
  container: string
  tail: string
  previous: boolean
  since: string
  timeout: string
  replicas: string
  confirm: boolean
}

export const EMPTY_K8S_INPUT: K8sInput = {
  operation: "get",
  kind: "pods",
  name: "",
  resource: "",
  namespace: "",
  allNamespaces: false,
  selector: "",
  output: "",
  container: "",
  tail: "",
  previous: false,
  since: "",
  timeout: "",
  replicas: "",
  confirm: false,
}

export interface K8sOperationSpec {
  label: string
  cli: string
  mutation: boolean
  fields: K8sField[]
}

export const K8S_OPERATIONS: Record<K8sOperation, K8sOperationSpec> = {
  get: { label: "查看资源 get", cli: "get <kind> [name]", mutation: false, fields: ["kind", "name", "namespace", "allNamespaces", "selector", "output"] },
  describe: { label: "详情 describe", cli: "describe <kind> <name>", mutation: false, fields: ["kind", "name", "namespace"] },
  logs: { label: "日志 logs", cli: "logs <resource>", mutation: false, fields: ["resource", "namespace", "container", "tail", "previous", "since"] },
  events: { label: "事件 events", cli: "events [--for kind/name]", mutation: false, fields: ["resource", "namespace", "allNamespaces"] },
  top: { label: "资源用量 top", cli: "top pods|nodes", mutation: false, fields: ["kind", "namespace", "allNamespaces", "selector"] },
  "rollout-status": { label: "发布状态 rollout status", cli: "rollout status <resource>", mutation: false, fields: ["resource", "namespace", "timeout"] },
  "helm-list": { label: "Helm 列表", cli: "helm list", mutation: false, fields: ["namespace", "allNamespaces"] },
  "helm-history": { label: "Helm 历史", cli: "helm history <release>", mutation: false, fields: ["name", "namespace"] },
  "helm-status": { label: "Helm 状态", cli: "helm status <release>", mutation: false, fields: ["name", "namespace"] },
  "helm-values": { label: "Helm values 键路径", cli: "helm values <release>", mutation: false, fields: ["name", "namespace"] },
  "rollout-restart": { label: "滚动重启 rollout restart", cli: "rollout restart <resource>", mutation: true, fields: ["resource", "namespace"] },
  scale: { label: "扩缩容 scale", cli: "scale <resource> --replicas N", mutation: true, fields: ["resource", "namespace", "replicas"] },
  cordon: { label: "节点停止调度 cordon", cli: "node cordon <node>", mutation: true, fields: ["name"] },
  uncordon: { label: "节点恢复调度 uncordon", cli: "node uncordon <node>", mutation: true, fields: ["name"] },
  "delete-pod": { label: "删除 Pod", cli: "delete pod <name>", mutation: true, fields: ["name", "namespace"] },
}

const NAME = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/
const KIND = /^[a-z][a-z0-9.-]*$/
const CONTAINER = /^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$/
const DURATION = /^[1-9][0-9]*(s|m|h)$/
const SELECTOR = /^[A-Za-z0-9_./!=(), -]{1,512}$/
const CREDENTIAL_KINDS = new Set(["secret", "secrets", "serviceaccount", "serviceaccounts", "sa", "token", "tokenrequest", "tokenrequests"])
const LOGGABLE = new Set(["pod", "pods", "deployment", "deployments", "deploy", "statefulset", "statefulsets", "sts", "daemonset", "daemonsets", "ds", "job", "jobs"])
const ROLLOUT = new Set(["deployment", "deployments", "deploy", "statefulset", "statefulsets", "sts", "daemonset", "daemonsets", "ds"])
const SCALABLE = new Set(["deployment", "deployments", "deploy", "statefulset", "statefulsets", "sts", "replicaset", "replicasets", "rs"])

export function isK8sMutation(operation: K8sOperation): boolean {
  return K8S_OPERATIONS[operation]?.mutation === true
}

function resourceKind(resource: string, allowed: Set<string> | null, label: string): void {
  const [kind, name, extra] = resource.split("/")
  if (extra !== undefined || !kind || !name || !KIND.test(kind) || !NAME.test(name)) {
    throw new Error(`${label}必须为 kind/name，例如 deployment/api`)
  }
  if (CREDENTIAL_KINDS.has(kind)) throw new Error("Secret 与 ServiceAccount 不开放读取")
  if (allowed && !allowed.has(kind)) throw new Error(`${label}不支持资源类型 ${kind}`)
}

function integer(value: string, label: string): number {
  if (!/^\d+$/.test(value)) throw new Error(`${label}必须为整数`)
  const n = Number(value)
  if (n > 10000) throw new Error(`${label}必须在 0 到 10000 之间`)
  return n
}

function normalizeResource(value: string): string {
  const parts = value.trim().split("/")
  if (parts.length !== 2) return value.trim()
  return parts[0].trim().toLowerCase() + "/" + parts[1].trim()
}

// Builds the doops_k8s arguments. Throws a user-facing error on invalid input,
// including a write operation that has not been explicitly confirmed.
export function k8sRequest(sessionId: string, input: K8sInput): Record<string, unknown> {
  const spec = K8S_OPERATIONS[input.operation]
  if (!spec) throw new Error("不支持的 Kubernetes 操作")
  const uses = (field: K8sField) => spec.fields.includes(field)
  const kind = input.kind.trim().toLowerCase()
  const name = input.name.trim()
  const resource = normalizeResource(input.resource)
  const namespace = input.namespace.trim()
  const payload: Record<string, unknown> = { session_id: sessionId, operation: input.operation }

  if (uses("kind")) {
    if (!kind) throw new Error(input.operation === "top" ? "请选择 pods 或 nodes" : "请填写资源类型")
    if (!KIND.test(kind)) throw new Error("资源类型格式无效")
    if (CREDENTIAL_KINDS.has(kind)) throw new Error("Secret 与 ServiceAccount 不开放读取")
    if (input.operation === "top" && kind !== "pods" && kind !== "nodes") throw new Error("top 只支持 pods 或 nodes")
    payload.kind = kind
  }
  if (uses("name")) {
    const required = input.operation !== "get"
    if (!name && required) {
      const label = input.operation.startsWith("helm-") ? "Release 名称" : input.operation === "delete-pod" ? "Pod 名称" : input.operation === "describe" ? "资源名称" : "节点名称"
      throw new Error(`请填写${label}`)
    }
    if (name) {
      if (!NAME.test(name)) throw new Error("名称必须为有效 Kubernetes 名称")
      payload.name = name
    }
  }
  if (uses("resource")) {
    if (resource) {
      const allowed = input.operation === "logs" ? LOGGABLE
        : input.operation === "rollout-status" || input.operation === "rollout-restart" ? ROLLOUT
        : input.operation === "scale" ? SCALABLE
        : null
      resourceKind(resource, allowed, input.operation === "events" ? "事件对象" : "资源")
      payload.resource = resource
    } else if (input.operation !== "events") {
      throw new Error("请填写资源，例如 deployment/api")
    }
  }
  if (uses("namespace") && namespace) {
    if (!NAME.test(namespace)) throw new Error("命名空间必须为有效 Kubernetes 名称")
    payload.namespace = namespace
  }
  if (uses("allNamespaces") && input.allNamespaces) {
    if (namespace) throw new Error("命名空间与「全部命名空间」只能选一个")
    payload.all_namespaces = true
  }
  if (input.operation.startsWith("helm-") && input.operation !== "helm-list" && !namespace) {
    throw new Error("请填写 Release 所在命名空间")
  }
  if (uses("selector") && input.selector.trim()) {
    if (!SELECTOR.test(input.selector.trim())) throw new Error("标签选择器格式无效")
    payload.selector = input.selector.trim()
  }
  if (uses("output") && input.output.trim()) payload.output = input.output.trim()
  if (uses("container") && input.container.trim()) {
    if (!CONTAINER.test(input.container.trim())) throw new Error("容器名格式无效")
    payload.container = input.container.trim()
  }
  if (uses("tail") && input.tail.trim()) payload.tail = integer(input.tail.trim(), "日志行数")
  if (uses("previous") && input.previous) payload.previous = true
  if (uses("since") && input.since.trim()) {
    if (!DURATION.test(input.since.trim())) throw new Error("时间范围格式如 10m、2h")
    payload.since = input.since.trim()
  }
  if (uses("timeout") && input.timeout.trim()) {
    if (!DURATION.test(input.timeout.trim())) throw new Error("超时格式如 120s、5m")
    payload.timeout = input.timeout.trim()
  }
  if (uses("replicas")) {
    if (!input.replicas.trim()) throw new Error("请填写副本数")
    payload.replicas = integer(input.replicas.trim(), "副本数")
  }
  if (spec.mutation) {
    if (input.confirm !== true) throw new Error("写操作需要先勾选确认")
    payload.confirm = true
  }
  return payload
}

// Human-readable description shown next to the confirmation checkbox.
export function k8sSummary(input: K8sInput): string {
  const ns = input.namespace.trim() ? `（命名空间 ${input.namespace.trim()}）` : ""
  const resource = normalizeResource(input.resource) || "<资源>"
  const name = input.name.trim() || "<名称>"
  switch (input.operation) {
    case "rollout-restart": return `滚动重启 ${resource}${ns}`
    case "scale": return `将 ${resource} 扩缩到 ${input.replicas.trim() || "?"} 个副本${ns}`
    case "cordon": return `将节点 ${name} 标记为不可调度`
    case "uncordon": return `恢复节点 ${name} 的调度`
    case "delete-pod": return `删除 Pod ${name}${ns}`
    default: return `${K8S_OPERATIONS[input.operation]?.label || input.operation} ${ns}`.trim()
  }
}

export interface K8sCallOptions {
  cluster: string
  instance: string
  tool: string
  arguments: Record<string, unknown>
}

// Validates first; nothing is sent when the request is invalid or a write is unconfirmed.
export async function runK8s(
  send: (opts: K8sCallOptions) => Promise<void>,
  target: { cluster: string; instance: string },
  sessionId: string,
  input: K8sInput,
): Promise<void> {
  const args = k8sRequest(sessionId, input)
  await send({ cluster: target.cluster, instance: target.instance, tool: K8S_TOOL, arguments: args })
}
