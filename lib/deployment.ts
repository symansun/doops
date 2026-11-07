import type { Target } from "./gateway"

export interface DeploymentInput {
  services: string
  release: string
  namespace: string
  chart: string
  values: string
  repository: string
  selector: "digest" | "tag"
  version: string
  commit: string
  ci: string
  reason: string
  acceptance: string
  credentials: string
}

export function deploymentRequest(target: Target, input: DeploymentInput) {
  for (const key of ["services", "release", "namespace", "chart", "repository", "version", "commit", "ci", "reason", "acceptance"] as const) {
    if (!input[key].trim()) throw new Error("请完整填写发布信息")
  }
  const name = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/
  if (!name.test(input.release) || !name.test(input.namespace)) throw new Error("Release 和 namespace 必须为有效 Kubernetes 名称")
  const chart = new URL(input.chart)
  if (chart.protocol !== "https:" || !chart.hostname || chart.username || chart.password) throw new Error("Chart 必须为无凭据的 HTTPS 地址")
  const values = input.values.split(/\s+/).filter(Boolean)
  for (const value of values) {
    const url = new URL(value)
    if (!["https:", "http:"].includes(url.protocol) || !url.hostname || url.username || url.password) throw new Error("Values 必须为无凭据的 HTTP(S) 地址")
  }
  if (!/^[a-z0-9][a-z0-9._:/-]*$/.test(input.repository) || input.repository.includes("//") || input.repository.endsWith("/")) throw new Error("请填写镜像仓库，不包含 tag 或 digest")
  if (input.selector === "digest" && !/^sha256:[a-f0-9]{64}$/.test(input.version)) throw new Error("镜像 digest 必须为 sha256 和 64 位十六进制")
  if (input.selector === "tag") {
    if (!/^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(input.version)) throw new Error("镜像 tag 格式无效")
  }
  if (!/^[a-f0-9]{40}$/.test(input.commit)) throw new Error("来源提交必须为完整 40 位 SHA")
  const ci = new URL(input.ci)
  if (ci.protocol !== "https:" || ci.username || ci.password) throw new Error("CI 证据必须为无凭据的 HTTPS 地址")
  const credential_refs = input.credentials.split(/[\s,]+/).filter(Boolean)
  if (credential_refs.some((name) => !/^[a-z0-9][a-z0-9-]*$/.test(name))) throw new Error("凭据只能引用资源名")
  const image = `${input.repository}${input.selector === "digest" ? "@" : ":"}${input.version}`
  const instruction = `在 target ${target.cluster}/${target.instance} 发布服务范围 ${input.services}，release ${input.release}，namespace ${input.namespace}；使用 chart ${input.chart}${values.length ? `，values URL ${values.join(", ")}` : ""}；镜像 ${image}。\n发布者已核对 CI 成功证据 ${input.ci}，来源提交 ${input.commit}。\n变更原因：${input.reason}。\n验收条件：${input.acceptance}。`
  return { instruction, operation: "apply", credential_refs }
}

export const DEPLOYMENT_EXTENSION = { namespace: "doops.operations", semanticType: "apply", schemaVersion: "1" } as const

// Builds the confirmed `doops.operations/apply` command the console sends; the server re-validates the same input.
export function deploymentExtensionCommand(target: Target, input: DeploymentInput, confirmed: boolean) {
  if (confirmed !== true) throw new Error("部署前必须勾选变更授权")
  deploymentRequest(target, input)
  return { ...DEPLOYMENT_EXTENSION, payload: { input: { ...input }, confirmed: true as const } }
}
