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

// Every field of a release form, in the order the console stores them and the server checks them.
export const DEPLOYMENT_FIELDS = ["services", "release", "namespace", "chart", "values", "repository", "selector", "version", "commit", "ci", "reason", "acceptance", "credentials"] as const

// The session route refuses a larger `doops.operations/apply` form payload.
export const DEPLOYMENT_PAYLOAD_LIMIT = 64_000

// Mirrors the Gateway's inline credential check (credential_scope.go credentialScalarContainsInlineMaterial), so text
// it would refuse is stopped before the release starts and never reaches the prompt, a stored draft or the audit.
const credentialPatterns = [
  /\bgh[pousr]_[A-Za-z0-9]{20,255}\b/i, /\bgithub_pat_[A-Za-z0-9_]{20,255}\b/i, /\bglpat-[A-Za-z0-9_-]{20,255}\b/,
  /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/, /\bAIza[0-9A-Za-z_-]{35}\b/, /\bxox[baprs]-[A-Za-z0-9-]{10,255}\b/,
  /\b(?:sk|rk)_live_[A-Za-z0-9]{16,255}\b/, /\bnpm_[A-Za-z0-9]{36,255}\b/,
]
const secretKeys = new Set(["token", "password", "auth", "authorization", "cookie", "data", "stringdata", "secret", "sig", "clientsecret", "apikey", "privatekey", "registrypassword"])
const secretKeySuffixes = ["token", "password", "privatekey", "clientsecret", "apikey", "accesskey", "secretkey", "credential", "pat", "passphrase", "dockerconfigjson", "sshidentity", "signature"]

function secretLikeKey(key: string): boolean {
  const normalized = key.trim().toLowerCase().replace(/[_\-. ]/g, "")
  return secretKeys.has(normalized) || secretKeySuffixes.some((suffix) => normalized.endsWith(suffix))
}

export function containsCredentialMaterial(value: string): boolean {
  value = value.trim()
  if (!value) return false
  const lower = value.toLowerCase()
  if (lower.includes("-----begin ") && lower.includes("private key-----")) return true
  if (credentialPatterns.some((pattern) => pattern.test(value))) return true
  if (lower.includes("bearer ") || lower.includes("basic ")) return true
  if (/^[a-z][a-z0-9+.-]*:\/\/[^/?#\s]*@/i.test(value)) return true
  const query = value.split("#", 1)[0]
  const start = query.indexOf("?")
  if (start < 0) return false
  return query.slice(start + 1).split("&").some((part) => {
    try { return secretLikeKey(decodeURIComponent(part.split("=", 1)[0].replace(/\+/g, " "))) } catch { return false }
  })
}

function parseUrl(raw: string, label: string): URL {
  try { return new URL(raw) } catch { throw new Error(`${label}不是有效 URL`) }
}

export function deploymentRequest(target: Target, input: DeploymentInput) {
  for (const key of ["services", "release", "namespace", "chart", "repository", "version", "commit", "ci", "reason", "acceptance"] as const) {
    if (!input[key].trim()) throw new Error("请完整填写发布信息")
  }
  if (new TextEncoder().encode(JSON.stringify({ input, confirmed: true })).length > DEPLOYMENT_PAYLOAD_LIMIT) {
    throw new Error("发布信息过长（超过 64 KB），请精简变更原因或验收条件")
  }
  const name = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/
  if (!name.test(input.release) || !name.test(input.namespace)) throw new Error("Release 和 namespace 必须为有效 Kubernetes 名称")
  if (input.release.length > 53) throw new Error("Helm release 名称不能超过 53 个字符")
  if (input.namespace.length > 63) throw new Error("Namespace 不能超过 63 个字符")
  // The Agent downloads the Chart itself and refuses these forms (helm_artifact.go managedHelmURL).
  if (/\s/.test(input.chart)) throw new Error("Chart 地址不能包含空格")
  if (/[^\x21-\x7e]/.test(input.chart)) throw new Error("Chart 地址只能包含 ASCII 字符")
  if (/[%\\]/.test(input.chart)) throw new Error("Chart 地址不能包含百分号编码或反斜杠")
  if (input.chart.includes("?")) throw new Error("Chart 地址不能带查询参数")
  if (input.chart.includes("#")) throw new Error("Chart 地址不能带片段（#）")
  const chart = parseUrl(input.chart, "Chart 地址")
  if (chart.protocol !== "https:" || !chart.hostname || chart.username || chart.password) throw new Error("Chart 必须为无凭据的 HTTPS 地址")
  if (!chart.pathname.endsWith(".tgz")) throw new Error("Chart 地址必须指向 .tgz 包")
  const values = input.values.split(/\s+/).filter(Boolean)
  for (const value of values) {
    const url = parseUrl(value, "Values 地址")
    if (!["https:", "http:"].includes(url.protocol) || !url.hostname || url.username || url.password) throw new Error("Values 必须为无凭据的 HTTP(S) 地址")
    if (containsCredentialMaterial(value)) throw new Error("Values 地址不能携带凭据（如 token、签名参数）")
  }
  if (!/^[a-z0-9][a-z0-9._:/-]*$/.test(input.repository) || input.repository.includes("//") || input.repository.endsWith("/")) throw new Error("请填写镜像仓库，不包含 tag 或 digest")
  if (input.selector === "digest" && !/^sha256:[a-f0-9]{64}$/.test(input.version)) throw new Error("镜像 digest 必须为 sha256 和 64 位十六进制")
  if (input.selector === "tag") {
    if (!/^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(input.version)) throw new Error("镜像 tag 格式无效")
  }
  if (!/^[a-f0-9]{40}$/.test(input.commit)) throw new Error("来源提交必须为完整 40 位 SHA")
  const ci = parseUrl(input.ci, "CI 记录地址")
  if (ci.protocol !== "https:" || ci.username || ci.password) throw new Error("CI 证据必须为无凭据的 HTTPS 地址")
  if (containsCredentialMaterial(input.ci)) throw new Error("CI 记录地址不能携带凭据（如 access_token）")
  const credential_refs = input.credentials.split(/[\s,]+/).filter(Boolean)
  if (credential_refs.some((name) => !/^[a-z0-9][a-z0-9-]*$/.test(name))) throw new Error("凭据只能引用资源名")
  // The Gateway refuses a repeated name (tunnel_hub.go validateExplicitCredentialNames).
  const repeated = credential_refs.find((name, index) => credential_refs.indexOf(name) !== index)
  if (repeated) throw new Error(`凭据资源名重复：${repeated}`)
  for (const [key, label] of [["services", "服务范围"], ["reason", "变更原因"], ["acceptance", "验收条件"]] as const) {
    if (containsCredentialMaterial(input[key])) throw new Error(`${label}不能包含凭据内容（token、私钥或认证头）`)
  }
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

// The draft kept in the tab's sessionStorage, so switching tabs or reloading keeps what was typed. A draft with
// credential material is never stored.
export function deploymentDraftText(input: DeploymentInput): string | null {
  if (DEPLOYMENT_FIELDS.some((key) => key !== "selector" && containsCredentialMaterial(input[key]))) return null
  return JSON.stringify(Object.fromEntries(DEPLOYMENT_FIELDS.map((key) => [key, input[key]])))
}

export function parseDeploymentDraft(raw: string | null | undefined): DeploymentInput | undefined {
  if (!raw) return undefined
  let value: unknown
  try { value = JSON.parse(raw) } catch { return undefined }
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined
  const record = value as Record<string, unknown>
  if (Object.keys(record).length !== DEPLOYMENT_FIELDS.length || DEPLOYMENT_FIELDS.some((key) => typeof record[key] !== "string")) return undefined
  if (record.selector !== "digest" && record.selector !== "tag") return undefined
  const draft = { ...record } as unknown as DeploymentInput
  return deploymentDraftText(draft) === null ? undefined : draft
}

// Runs one call at a time; a call made while another is in flight is dropped and resolves to false.
export function singleFlight<A extends unknown[]>(run: (...args: A) => Promise<unknown>): (...args: A) => Promise<boolean> {
  let running = false
  return async (...args: A) => {
    if (running) return false
    running = true
    try { await run(...args); return true } finally { running = false }
  }
}

// Connect and the session protocol prefix messages with "[code] "; users read the message only.
export function agentErrorText(raw: string | null | undefined): string {
  return (raw || "").replace(/^(?:\[[a-z_]+\]\s*)+/, "").trim()
}

function errorCode(raw: string | null | undefined): string | undefined {
  return /^\[([a-z_]+)\]/.exec(raw || "")?.[1]
}

// Agent and Helm errors can carry a whole stderr; summaries stay readable and the trace keeps the full text.
function clip(text: string, limit = 240): string {
  const trimmed = text.trim().replace(/[。.]$/, "")
  return trimmed.length > limit ? `${trimmed.slice(0, limit)}…` : trimmed
}

export type DeploymentNoticeAction = "open_credentials" | "reconnect" | "upgrade_agent"

export interface DeploymentNotice {
  kind: "connecting" | "no_model" | "model_unavailable" | "upgrade_agent" | "forbidden" | "signed_out" | "console_policy" | "offline" | "busy" | "unavailable"
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

const busyPattern = /target busy|target queue (?:full|timeout)|session has an active operation|运行中的任务|cancellation pending/
// Messages from the console's own Gateway allowlist (gateway-policy.ts), answered with 403 like a missing grant.
const consolePolicyPattern = /gateway (?:地址|协议)|未获控制台授权|生产 gateway|gateway allowlist|只允许访问本机 gateway/

function grantRefused(code: string | undefined, message: string): boolean {
  // HandleClientRPC answers a user without a grant with 403 before the WebSocket upgrade.
  return /^forbidden:/.test(message) || (code === "permission_denied" && message === "Gateway 拒绝连接")
}

function connectionNotice(raw: string): DeploymentNotice {
  const code = errorCode(raw)
  const message = agentErrorText(raw)
  if (/还没有可下发到该节点的模型|尚未连接大模型/.test(message)) {
    return { kind: "no_model", title: "该目标还没有配置 DoAgent 模型，暂时不能发布",
      detail: "发布由目标上的 DoAgent 执行，需要 Gateway 下发的模型。请联系平台管理员为该目标绑定 modelProvider 凭据，激活后点「重新连接」。可以先填写和校验发布信息。",
      actions: ["open_credentials", "reconnect"] }
  }
  if (/模型连接不可用|\bmodel_(?:provider_[a-z_]+|auth_failed|unavailable)\b/.test(message)) {
    return { kind: "model_unavailable", title: "目标的 DoAgent 模型当前不可用，暂时不能发布",
      detail: `Agent 报告：${clip(message)}。请联系平台管理员检查该目标绑定的 modelProvider 凭据，处理后点「重新连接」。`,
      actions: ["open_credentials", "reconnect"] }
  }
  if (/结构化 ACP|必须升级 Agent/.test(message)) {
    return { kind: "upgrade_agent", title: "目标 Agent 版本过旧，需要升级后才能发布",
      detail: "该 Agent 不支持控制台的结构化执行事件。请到「部署 → 升级 Agent」升级，完成后回到这里重新连接。", actions: ["upgrade_agent"] }
  }
  if (code === "unauthenticated" || /gateway_auth_failed/.test(message)) {
    return { kind: "signed_out", title: "登录已失效", detail: "Gateway 不再接受当前登录。请断开后重新登录。", actions: [] }
  }
  if (consolePolicyPattern.test(message)) {
    return { kind: "console_policy", title: "控制台不允许访问这个 Gateway",
      detail: `${clip(message)}。请联系控制台运维检查 DOOPS_CONSOLE_ALLOWED_GATEWAYS 配置。`, actions: [] }
  }
  if (grantRefused(code, message)) {
    return { kind: "forbidden", title: "当前账号没有该目标的使用权限",
      detail: "请联系平台管理员在「管理 → 权限」为当前账号授予该目标。", actions: [] }
  }
  if (/target offline/.test(message)) {
    return { kind: "offline", title: "目标 Agent 离线",
      detail: "Gateway 当前没有该目标的 Agent 连接。确认 Agent 正常运行后点「重新连接」。", actions: ["reconnect"] }
  }
  if (busyPattern.test(message)) {
    return { kind: "busy", title: "目标正忙，暂时不能发布",
      detail: "目标上有其他变更在执行，或上一次停止还没有确认终态。可以在「审计」查看正在执行的操作，结束后点「重新连接」。", actions: ["reconnect"] }
  }
  // The last release's failure stays in the timeline; it says nothing about why the connection dropped.
  const reason = code === "doops_operation_failed" || code === "doops_apply_outcome_unknown" ? "" : clip(message)
  return { kind: "unavailable", title: "暂时连不上目标 Agent",
    detail: `${reason ? `连接失败：${reason}。` : "连接已断开。"}稍后点「重新连接」重试。`, actions: ["reconnect"] }
}

export interface DeploymentOutcome {
  kind: "idle" | "running" | "succeeded" | "unknown" | "cancelled" | "failed"
  title: string
  detail: string
  actions: DeploymentNoticeAction[]
}

const notStarted = "本次发布没有执行。"
const unchanged = "目标没有被修改。"

// What the last release in this session did to the target. Failure categories come from the Gateway, the Agent and the
// managed Helm tool (next.go, helm_release.go, NATURAL_LANGUAGE_DEPLOYMENT.md).
export function deploymentOutcome(state: { status: string; error: string | null | undefined }): DeploymentOutcome {
  const outcome = (kind: DeploymentOutcome["kind"], title: string, detail = "", actions: DeploymentNoticeAction[] = []): DeploymentOutcome =>
    ({ kind, title, detail, actions })
  if (state.status === "running" || state.status === "waiting" || state.status === "launching") {
    return outcome("running", "发布执行中…", "rollout 与最终 Deployment 状态以 Agent 返回为准。")
  }
  if (state.status === "completed") {
    return outcome("succeeded", "发布已结束", "请按验收条件核对 Agent 返回的 rollout 和最终 Deployment 状态。")
  }
  if (state.status !== "failed") return outcome("idle", "填写并确认发布信息后，执行过程显示在这里")
  const code = errorCode(state.error)
  const message = agentErrorText(state.error)
  if (code === "doops_apply_outcome_unknown" || /execution_outcome_unknown|结果未确认|终态未确认/.test(message)) {
    return outcome("unknown", "发布结果未确认",
      "Agent 返回终态前连接中断，目标可能已经变更。请在「Kubernetes」核对 release 的 helm-history 和工作负载状态，或在「审计」查看本次操作。控制台不会自动重试。")
  }
  if (/operation canceled|已停止/.test(message)) {
    return outcome("cancelled", "发布已停止", "停止的发布不会自动回滚，Helm 可能已经写入新的 revision。请在「Kubernetes」查看 helm-history 核对 release 当前状态。")
  }
  if (/credential_reference_ambiguous|helm_credential_binding_ambiguous/.test(message)) {
    return outcome("failed", "目标绑定了多个 Helm 仓库凭据",
      `Gateway 无法判断这个 Chart 来自哪个仓库。请在「凭据资源名」填写该 Chart 仓库对应的 helmPull 凭据后重新发布。${notStarted}`, ["open_credentials"])
  }
  if (/helm_chart_requires_credential/.test(message)) {
    return outcome("failed", "Chart 仓库需要凭据",
      `Chart 仓库拒绝匿名下载。请为该目标绑定这个仓库的 helmPull 凭据，绑定后不必在表单中填写，重新发布即可。${unchanged}`, ["open_credentials"])
  }
  if (/credential_version_not_found/.test(message)) {
    return outcome("failed", "凭据没有激活的版本",
      `凭据已绑定，但没有验证并激活（promote）过的版本。请在凭据页完成验证和激活后重新发布。${notStarted}`, ["open_credentials"])
  }
  if (/credential_(?:grant_denied|forbidden|not_found|revoked|binding_target_mismatch|binding_scope_invalid)/.test(message)) {
    return outcome("failed", "填写的凭据在该目标上不可用",
      `请核对「凭据资源名」是否为绑定到该目标的凭据；留空时会自动使用目标绑定的 Helm 凭据。${notStarted}`)
  }
  if (busyPattern.test(message)) {
    return outcome("failed", "目标正忙", `目标上有其他变更在执行。${notStarted}可以在「审计」查看正在执行的操作，结束后重新提交。`)
  }
  if (grantRefused(code, message)) {
    return outcome("failed", "当前账号没有该目标的使用权限", `授权可能已被收回。${notStarted}请联系平台管理员在「管理 → 权限」授予该目标。`)
  }
  if (/target offline/.test(message)) {
    return outcome("failed", "目标 Agent 离线", `${notStarted}确认 Agent 正常运行后重新提交。`)
  }
  const helm = /\b(helm_[a-z]+(?:_[a-z]+)*(?:_\d{3})?)(?::\s*([\s\S]*))?/.exec(message)
  if (helm) {
    const reason = clip(helm[2] || helm[1])
    if (helm[1] === "helm_preflight_failed" && /invalid ownership metadata|cannot be imported into the current release/.test(message)) {
      return outcome("failed", "目标上已有同名对象，不属于本 release",
        `Helm 预检发现已存在的对象没有本 release 的所有权元数据，默认拒绝接管：${reason}。${unchanged}如确需把这些对象纳入本 release，请在变更原因中明确要求接管已存在的对象后重新发布。`)
    }
    if (helm[1] === "helm_preflight_failed") return outcome("failed", "Helm 预检失败", `${reason}。dry-run 没有通过，${unchanged}`)
    if (/^helm_(?:manifest_invalid|no_workload|image_mismatch)$/.test(helm[1])) {
      return outcome("failed", "Chart 渲染结果没有通过检查", `${reason}。${unchanged}请核对 Chart 和 values。`)
    }
    if (/^helm_(?:artifact_http_\d+|values_http_\d+|repository_[a-z_]+|values_[a-z_]+|chart_[a-z_]+|url_invalid|oci_unsupported)$/.test(helm[1])) {
      return outcome("failed", "无法获取 Chart 或 values", `${reason}。Agent 没有拿到发布资产，${unchanged}请确认地址可以从目标集群访问。`)
    }
    if (helm[1] === "helm_mutation_failed") {
      return outcome("failed", "Helm 变更失败", `${reason}。Helm 可能已经写入失败的 revision，请在「Kubernetes」用 helm-history 查看 release 状态。`)
    }
    if (helm[1] === "helm_verification_failed") {
      return outcome("failed", "发布后校验未通过", `${reason}。新 revision 已经写入，但集群状态与渲染结果不一致或没有就绪。请在「Kubernetes」查看对应工作负载和 Pod。`)
    }
    if (helm[1] === "helm_interrupted") {
      return outcome("failed", "发布超时", `${reason}。请在「Kubernetes」核对 release 当前状态，再决定是否重新发布。`)
    }
  }
  if (/doagent_apply_tool_evidence_missing/.test(message)) {
    return outcome("failed", "Agent 没有返回可信的变更证据", "Agent 没有完成受管 Helm 发布，或证据没有通过校验，不能视为成功。请查看执行过程并核对目标状态。")
  }
  return outcome("failed", "发布失败", `${clip(message) || "Agent 没有返回原因"}。详情见执行过程。`)
}

// Why a submission was refused, and whether the release may have started anyway.
export function deploymentSubmitError(raw: string): string {
  const code = errorCode(raw)
  const message = agentErrorText(raw)
  if (/运行中的任务/.test(message)) return "上一次发布还在执行，本次没有提交。请等它结束后再提交。"
  if (/禁止重放|不能复用此会话|结果未确认/.test(message)) return "上一次发布结果未确认，本次没有提交。请先在「Kubernetes」核对 release 状态，再点「新发布」。"
  if (/command_position_mismatch|cursor_/.test(message)) return "执行过程刚刚更新，本次没有提交。请核对后重新勾选授权再提交。"
  if (/agent_workbench_command_/.test(message)) return "Agent 没有接受本次提交，发布没有执行。详情见执行过程。"
  if (/agent_workbench_/.test(message)) return "与目标 Agent 的连接未就绪，本次没有提交。请等待连接完成后重试。"
  if (/target offline/.test(message)) return `目标 Agent 离线，${notStarted}`
  if (grantRefused(code, message)) return `当前账号没有该目标的使用权限，${notStarted}`
  if (/还没有可下发到该节点的模型|尚未连接大模型/.test(message)) return `该目标还没有配置 DoAgent 模型，${notStarted}`
  if (code === "unavailable" || code === "deadline_exceeded" || code === "unknown" || code === "internal" || /Failed to fetch|NetworkError/.test(message)) {
    return "提交请求没有收到确认，发布可能已经开始。请查看执行过程或「审计」，不要重复提交。"
  }
  return message || "提交失败"
}

// The Gateway serializes mutations within one session only (tunnel_hub.go exclusiveLockKey), so a new release
// session started after an unconfirmed result could run beside a release that is still going.
export function deploymentNewSessionWarning(kind: DeploymentOutcome["kind"]): string | undefined {
  if (kind !== "unknown") return undefined
  return "上一次发布结果未确认，目标可能仍在变更。新的发布会话不会等它结束，请先在「Kubernetes」核对 release 状态。确定要开始新发布吗？"
}

// A stop only asks the Agent to stop; this says what happened to the release when it did not take effect.
export function deploymentStopFailure(raw: string): string {
  const message = agentErrorText(raw)
  if (/已请求停止，远端终态未确认/.test(message)) return "已请求停止，但 Agent 没有确认终态，发布可能仍在执行。请在「Kubernetes」核对 release 状态。"
  if (/任务已结束，取消未生效|当前没有运行中的任务/.test(message)) return "发布已经结束，停止没有生效。结果以执行过程为准。"
  if (/不能复用此会话|结果未确认/.test(message)) return "上一次发布结果未确认，无法停止。请在「Kubernetes」核对 release 状态。"
  if (/中断目标不是当前任务/.test(message)) return "要停止的不是当前发布，请刷新后重试。"
  return `停止请求失败：${message || "未知错误"}`
}
