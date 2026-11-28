import type { Target } from "./client"
import type {
  CredentialBinding,
  CredentialResource,
  CredentialType,
  CredentialUse,
  CredentialVersion,
} from "./credentials"
import { AGENT_MODEL_MISSING, AGENT_MODEL_MISSING_HINT } from "./agent-error.ts"

// DoOps AI Assistant and Workload Deploy run purely in operational work mode;
// code workbench / file-tree editing mode is disabled while workspace artifact
// downloads stay active through workspaceArtifacts + createArtifactLoader.
export const AGENT_WORKSPACE_ALLOWED_MODES = ["work"] as const

export interface ModelProviderPreset {
  id: string
  label: string
  baseURLPlaceholder: string
  modelPlaceholder: string
}

export const MODEL_PROVIDER_PRESETS: readonly ModelProviderPreset[] = [
  {
    id: "openai",
    label: "OpenAI / OpenAI 兼容",
    baseURLPlaceholder: "https://api.openai.com/v1",
    modelPlaceholder: "gpt-5.4",
  },
  {
    id: "deepseek",
    label: "DeepSeek",
    baseURLPlaceholder: "https://api.deepseek.com/v1",
    modelPlaceholder: "deepseek-chat",
  },
  {
    id: "anthropic",
    label: "Anthropic",
    baseURLPlaceholder: "https://api.anthropic.com",
    modelPlaceholder: "claude-sonnet-4-5",
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    baseURLPlaceholder: "https://openrouter.ai/api/v1",
    modelPlaceholder: "openai/gpt-5.4",
  },
  {
    id: "custom",
    label: "自定义服务商",
    baseURLPlaceholder: "https://llm.example.com/v1",
    modelPlaceholder: "model-name",
  },
]

export interface ModelProviderPayloadInput {
  providerId: string
  modelName: string
  apiKey: string
  baseURL?: string
}

export interface RegistryPayloadInput {
  server: string
  username: string
  password: string
  email?: string
}

export interface GitTokenPayloadInput {
  url: string
  username: string
  token: string
}

export interface HelmRepositoryPayloadInput {
  url: string
  username: string
  password: string
}

export interface TlsPayloadInput {
  certificate: string
  privateKey: string
}

export interface OpaquePayloadEntry {
  key: string
  value: string
}

export interface OpaquePayloadInput {
  entries: readonly OpaquePayloadEntry[]
}

export type StructuredCredentialPayloadInput =
  | { type: "modelProvider"; values: ModelProviderPayloadInput }
  | { type: "registry"; values: RegistryPayloadInput }
  | { type: "gitToken"; values: GitTokenPayloadInput }
  | { type: "helmRepository"; values: HelmRepositoryPayloadInput }
  | { type: "tls"; values: TlsPayloadInput }
  | { type: "opaque"; values: OpaquePayloadInput }

const PROVIDER_ID_PATTERN = /^[A-Za-z0-9._-]{1,64}$/

function validateEndpointUrl(raw: string, label: string, allowRegistryHost = false): string {
  const value = raw.trim()
  if (!value) throw new Error(`请填写${label}`)
  if (allowRegistryHost && !value.includes("://")) {
    if (/[/?#@\s]/.test(value)) {
      throw new Error(`${label}格式无效，不能包含路径、凭据或空白字符`)
    }
    return value
  }
  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    throw new Error(`${label}不是合法 URL`)
  }
  if (parsed.username || parsed.password) {
    throw new Error(`${label}不能内嵌账号或密码`)
  }
  if (!parsed.hostname) {
    throw new Error(`${label}缺少主机名`)
  }
  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    throw new Error(`${label}必须使用 https:// 地址`)
  }
  if (parsed.search || parsed.hash) {
    throw new Error(`${label}不能包含查询参数或片段`)
  }
  return value
}

export function buildStructuredCredentialPayload(input: StructuredCredentialPayloadInput): string {
  switch (input.type) {
    case "modelProvider": {
      const providerId = input.values.providerId.trim()
      if (!providerId || !PROVIDER_ID_PATTERN.test(providerId) || providerId.includes("/")) {
        throw new Error("服务商标识只能包含字母、数字、点、下划线和连字符（不能含 /）")
      }
      let modelName = input.values.modelName.trim()
      if (modelName.startsWith(`${providerId}/`)) {
        modelName = modelName.slice(providerId.length + 1).trim()
      }
      if (!modelName) {
        throw new Error("请填写模型名称，例如 gpt-5.4 或 deepseek-chat")
      }
      const apiKey = input.values.apiKey.trim()
      if (!apiKey) {
        throw new Error("请填写 API Key")
      }
      if (apiKey.startsWith("{")) {
        throw new Error("API Key 必须是直接密钥值，不能使用环境变量或文件引用占位符")
      }
      const baseURL = input.values.baseURL?.trim()
        ? validateEndpointUrl(input.values.baseURL, "Base URL")
        : ""
      const options: { apiKey: string; baseURL?: string } = { apiKey }
      if (baseURL) options.baseURL = baseURL
      return JSON.stringify({
        model: `${providerId}/${modelName}`,
        provider: {
          [providerId]: { options },
        },
      })
    }
    case "registry": {
      const server = validateEndpointUrl(input.values.server, "镜像仓库地址", true)
      const username = input.values.username.trim()
      const password = input.values.password.trim()
      if (!username) throw new Error("请填写镜像仓库用户名")
      if (!password) throw new Error("请填写镜像仓库密码")
      const email = input.values.email?.trim()
      return JSON.stringify({
        server,
        username,
        password,
        ...(email ? { email } : {}),
      })
    }
    case "gitToken": {
      const url = validateEndpointUrl(input.values.url, "Git 仓库地址")
      const username = input.values.username.trim()
      const token = input.values.token.trim()
      if (!username) throw new Error("请填写 Git 用户名")
      if (!token) throw new Error("请填写 Git 访问令牌")
      return JSON.stringify({ url, username, token })
    }
    case "helmRepository": {
      const url = validateEndpointUrl(input.values.url, "Helm 仓库地址")
      const username = input.values.username.trim()
      const password = input.values.password.trim()
      if (!username) throw new Error("请填写 Helm 仓库用户名")
      if (!password) throw new Error("请填写 Helm 仓库密码")
      return JSON.stringify({ url, username, password })
    }
    case "tls": {
      const certificate = input.values.certificate.trim()
      const privateKey = input.values.privateKey.trim()
      if (!certificate) throw new Error("请粘贴 TLS 证书内容（PEM）")
      if (!privateKey) throw new Error("请粘贴 TLS 私钥内容（PEM）")
      return JSON.stringify({ certificate, privateKey })
    }
    case "opaque": {
      const data: Record<string, string> = {}
      const seen = new Set<string>()
      for (const entry of input.values.entries) {
        const key = entry.key.trim()
        const value = entry.value
        if (!key && !value) continue
        if (!key) throw new Error("存在未填写键名的条目")
        if (!value) throw new Error(`键「${key}」的值不能为空`)
        if (seen.has(key)) throw new Error(`键「${key}」重复，请去重后再提交`)
        seen.add(key)
        data[key] = value
      }
      if (seen.size === 0) {
        throw new Error("请至少填写一组键值对")
      }
      return JSON.stringify({ data })
    }
  }
}

export function credentialPayloadPlaceholder(type: CredentialType): string {
  switch (type) {
    case "modelProvider":
      return JSON.stringify({
        model: "openai/gpt-5.4",
        provider: {
          openai: {
            options: {
              apiKey: "sk-...",
              baseURL: "https://api.openai.com/v1",
            },
          },
        },
      }, null, 2)
    case "registry":
      return JSON.stringify({
        server: "registry.example.com",
        username: "deploy",
        password: "...",
      }, null, 2)
    case "gitToken":
      return JSON.stringify({
        url: "https://git.example.com",
        username: "git-bot",
        token: "...",
      }, null, 2)
    case "helmRepository":
      return JSON.stringify({
        url: "https://charts.example.com",
        username: "helm-bot",
        password: "...",
      }, null, 2)
    case "tls":
      return JSON.stringify({
        certificate: "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----",
        privateKey: "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
      }, null, 2)
    case "opaque":
      return JSON.stringify({
        data: {
          DB_HOST: "127.0.0.1",
          DB_PASSWORD: "...",
        },
      }, null, 2)
  }
}

// modelProvider credentials materialize into Agent settings.json directly and
// never create a Kubernetes Secret, so the UI does not ask the operator to
// invent a Kubernetes RBAC Secret namespace.
export function bindingNeedsExplicitNamespace(use: CredentialUse): boolean {
  return use !== "modelProvider"
}

export function defaultBindingNamespace(use: CredentialUse): string {
  return bindingNeedsExplicitNamespace(use) ? "" : "default"
}

export type CredentialStageKey =
  | "revoked"
  | "needs_payload"
  | "needs_binding"
  | "needs_verify"
  | "ready_to_promote"
  | "active"

export type CredentialPrimaryAction = "payload" | "binding" | "verify" | "promote" | null

export interface CredentialStage {
  key: CredentialStageKey
  step: number
  label: string
  hint: string
  primaryAction: CredentialPrimaryAction
  verifyBinding?: CredentialBinding
  targetBound: boolean
}

export function resolveCredentialStage(params: {
  resource: CredentialResource
  versions: readonly CredentialVersion[]
  selectedVersion?: CredentialVersion
  target?: Pick<Target, "cluster" | "instance">
  verified?: boolean
}): CredentialStage {
  const { resource, versions, selectedVersion, target, verified = false } = params
  const activeBindings = (resource.bindings ?? []).filter((binding) => binding.state === "active")
  const currentTargetBinding = target
    ? activeBindings.find(
      (binding) => binding.scope.cluster === target.cluster && binding.scope.instance === target.instance,
    )
    : undefined
  const targetBound = Boolean(currentTargetBinding)

  if (resource.state !== "active") {
    return {
      key: "revoked",
      step: 0,
      label: resource.state === "revoking" ? "吊销中" : "已吊销",
      hint: "该凭据已停止使用",
      primaryAction: null,
      targetBound,
    }
  }

  const hasActiveVersion = versions.some((version) => version.state === "active")
  const stagedVersion = selectedVersion?.state === "staged"
    ? selectedVersion
    : versions.find((version) => version.state === "staged")

  if (!stagedVersion && !hasActiveVersion) {
    return {
      key: "needs_payload",
      step: 1,
      label: "1/4 待写内容",
      hint: "先写入凭据内容生成待激活版本",
      primaryAction: "payload",
      targetBound,
    }
  }

  if (stagedVersion) {
    if (activeBindings.length === 0) {
      return {
        key: "needs_binding",
        step: 2,
        label: "2/4 待绑目标",
        hint: "暂存版本已写入，请绑定到目标 Agent",
        primaryAction: "binding",
        targetBound,
      }
    }
    if (!verified) {
      return {
        key: "needs_verify",
        step: 3,
        label: "3/4 待验证",
        hint: activeBindings.length > 1
          ? `需对全部 ${activeBindings.length} 个已绑定目标完成「验证暂存」后再激活`
          : "点击「验证暂存」确认目标可用后再激活",
        primaryAction: "verify",
        verifyBinding: currentTargetBinding ?? activeBindings[0],
        targetBound,
      }
    }
    return {
      key: "ready_to_promote",
      step: 4,
      label: "4/4 待激活",
      hint: "验证已通过，点击「激活」立即生效",
      primaryAction: "promote",
      targetBound,
    }
  }

  if (activeBindings.length === 0) {
    return {
      key: "needs_binding",
      step: 2,
      label: "待绑目标",
      hint: "已有生效版本，绑定到目标后即可使用",
      primaryAction: "binding",
      targetBound,
    }
  }

  return {
    key: "active",
    step: 5,
    label: target && !targetBound ? "未绑当前目标" : "已生效",
    hint: target && !targetBound
      ? "已在其他目标生效，可点击「绑定目标」复用到当前目标"
      : "当前版本已生效，可随时写入新版本轮换",
    primaryAction: target && !targetBound ? "binding" : null,
    targetBound,
  }
}

export function classifyAgentModelError(error: string | null | undefined): {
  isModelConfigError: boolean
  missingModel: boolean
} {
  const message = (error ?? "").trim()
  if (!message) return { isModelConfigError: false, missingModel: false }
  const missingModel = message.includes(AGENT_MODEL_MISSING)
    || message.includes(AGENT_MODEL_MISSING_HINT)
    || message.includes("还没有绑定 DoAgent 模型")
  const isModelConfigError = missingModel
    || message.includes("无法确认该 Agent 当前使用的模型")
    || message.includes("DoAgent 模型连接不可用")
  return { isModelConfigError, missingModel }
}

export function sortTargetCredentialsForAssistant(
  credentials: readonly CredentialResource[],
): CredentialResource[] {
  return [...credentials].sort((a, b) => {
    const aModel = a.type === "modelProvider" ? 0 : 1
    const bModel = b.type === "modelProvider" ? 0 : 1
    if (aModel !== bModel) return aModel - bModel
    return a.name.localeCompare(b.name)
  })
}
