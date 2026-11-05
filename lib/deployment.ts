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

// Connect and the session protocol prefix messages with "[code] "; users read the message only.
export function agentErrorText(raw: string | null | undefined): string {
  return (raw || "").replace(/^(?:\[[a-z_]+\]\s*)+/, "").trim()
}

export type DeploymentNoticeAction = "open_credentials" | "reconnect" | "upgrade_agent"

export interface DeploymentNotice {
  kind: "connecting" | "no_model" | "model_unavailable" | "upgrade_agent" | "forbidden" | "signed_out" | "offline" | "busy" | "unavailable"
  title: string
  detail: string
  actions: DeploymentNoticeAction[]
}

export type DeploymentAvailability =
  | { fieldsEditable: true; canSubmit: true }
  | { fieldsEditable: true; canSubmit: false; blockedReason: string; notice?: DeploymentNotice }

export interface DeploymentSessionState {
  connection: "connecting" | "connected" | "reconnecting" | "disconnected"
  error: string | null | undefined
  supportsApply: boolean
  running: boolean
}

// The Agent state only decides whether a release can be submitted. Fields stay editable so a release
// can be drafted and checked locally on a target that cannot run it yet.
export function deploymentAvailability(state: DeploymentSessionState): DeploymentAvailability {
  if (state.connection === "connected") {
    if (!state.supportsApply) {
      return blocked({ kind: "unavailable", title: "目标 Agent 不支持发布",
        detail: "当前 Agent 没有声明发布能力。请到「部署 → 升级 Agent」升级后再发布。", actions: ["upgrade_agent"] })
    }
    // A connected snapshot's error belongs to the last release, not to the connection.
    if (state.running) return { fieldsEditable: true, canSubmit: false, blockedReason: "上一次发布正在执行，结束后才能再次提交" }
    return { fieldsEditable: true, canSubmit: true }
  }
  if (!agentErrorText(state.error) && state.connection !== "disconnected") {
    return blocked({ kind: "connecting", title: "正在连接目标 Agent", detail: "连接完成后即可发布；可以先填写和校验发布信息。", actions: [] })
  }
  return blocked(connectionNotice(state.error || ""))
}

// Which form controls are usable. Only an in-flight submission locks the fields; Agent availability and
// local validation decide the authorization checkbox and the submit button.
export function deploymentFormControls(state: { canSubmit: boolean; valid: boolean; submitting: boolean; confirmed: boolean }) {
  const ready = state.canSubmit && state.valid && !state.submitting
  return { fieldsDisabled: state.submitting, confirmDisabled: !ready, submitDisabled: !ready || !state.confirmed }
}

function blocked(notice: DeploymentNotice): DeploymentAvailability {
  return { fieldsEditable: true, canSubmit: false, blockedReason: notice.title, notice }
}

function connectionNotice(raw: string): DeploymentNotice {
  const code = /^\[([a-z_]+)\]/.exec(raw)?.[1]
  const message = agentErrorText(raw)
  if (/还没有可下发到该节点的模型|尚未连接大模型/.test(message)) {
    return { kind: "no_model", title: "该目标还没有配置 DoAgent 模型，暂时不能发布",
      detail: "发布由目标上的 DoAgent 执行，需要 Gateway 下发的模型。请联系平台管理员为该目标绑定 modelProvider 凭据，激活后点「重新连接」。可以先填写和校验发布信息。",
      actions: ["open_credentials", "reconnect"] }
  }
  if (/模型连接不可用|\bmodel_(?:provider_[a-z_]+|auth_failed|unavailable)\b/.test(message)) {
    return { kind: "model_unavailable", title: "目标的 DoAgent 模型当前不可用，暂时不能发布",
      detail: `Agent 报告：${message}。请联系平台管理员检查该目标绑定的 modelProvider 凭据，处理后点「重新连接」。`,
      actions: ["open_credentials", "reconnect"] }
  }
  if (/结构化 ACP|必须升级 Agent/.test(message)) {
    return { kind: "upgrade_agent", title: "目标 Agent 版本过旧，需要升级后才能发布",
      detail: "该 Agent 不支持控制台的结构化执行事件。请到「部署 → 升级 Agent」升级，完成后回到这里重新连接。", actions: ["upgrade_agent"] }
  }
  if (code === "unauthenticated" || /gateway_auth_failed/.test(message)) {
    return { kind: "signed_out", title: "登录已失效", detail: "Gateway 不再接受当前登录。请断开后重新登录。", actions: [] }
  }
  if (code === "permission_denied" || /^forbidden:/.test(message)) {
    return { kind: "forbidden", title: "当前账号没有该目标的使用权限",
      detail: "请联系平台管理员在「管理 → 权限」为当前账号授予该目标。", actions: [] }
  }
  if (/target offline/.test(message)) {
    return { kind: "offline", title: "目标 Agent 离线",
      detail: "Gateway 当前没有该目标的 Agent 连接。确认 Agent 正常运行后点「重新连接」。", actions: ["reconnect"] }
  }
  if (/target busy|target queue (?:full|timeout)|运行中的任务|cancellation pending/.test(message)) {
    return { kind: "busy", title: "目标正忙，暂时不能发布",
      detail: "目标上有其他变更在执行，或上一次停止还没有确认终态。可以在「审计」查看正在执行的操作，结束后点「重新连接」。", actions: ["reconnect"] }
  }
  // The last release's failure stays in the timeline; it says nothing about why the connection dropped.
  const reason = code === "doops_operation_failed" ? "" : message
  return { kind: "unavailable", title: "暂时连不上目标 Agent",
    detail: `${reason ? `连接失败：${reason.replace(/[。.]$/, "")}。` : "连接已断开。"}稍后点「重新连接」重试。`, actions: ["reconnect"] }
}
