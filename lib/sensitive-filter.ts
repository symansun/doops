import { gatewayAuthMessage } from "./gateway-auth.ts"

// Gateway 敏感词过滤（/v1/admin/sensitive-filter）的类型、输入校验和错误文案。
// 校验与 backend/agent/internal/server/sensitive_filter.go 保持一致，最终以 Gateway 返回为准。

export type SensitiveScope = "all" | "prompt" | "command"
export type SensitiveWhitelistKind = "user" | "target"

export interface SensitiveRule {
  id: string
  text: string
  scope: SensitiveScope
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface SensitiveWhitelistEntry {
  kind: SensitiveWhitelistKind
  user_id?: string
  cluster?: string
  instance?: string
  created_at?: string
}

export interface SensitiveFilterView {
  enabled: boolean
  rules: SensitiveRule[]
  whitelist: SensitiveWhitelistEntry[]
}

export interface SensitiveRuleInput {
  text: string
  scope: SensitiveScope
}

export interface SensitiveWhitelistInput {
  kind: SensitiveWhitelistKind
  user_id?: string
  cluster?: string
  instance?: string
}

export const SENSITIVE_SCOPE_OPTIONS: { value: SensitiveScope; label: string; hint: string }[] = [
  { value: "all", label: "全部", hint: "同时匹配提示词和原始命令" },
  { value: "prompt", label: "提示词", hint: "只匹配 AI 助手提交的提示词" },
  { value: "command", label: "命令", hint: "只匹配终端和 Shell 工具提交的命令" },
]

export function sensitiveScopeLabel(scope: string): string {
  return SENSITIVE_SCOPE_OPTIONS.find((item) => item.value === scope)?.label || scope
}

// 与 Gateway 的匹配口径一致：去首尾空白、折叠连续空白、忽略大小写。
export function normalizeSensitiveTerm(value: string): string {
  return value.trim().replace(/\s+/g, " ").toLowerCase()
}

export type ParsedSensitiveRules =
  | { ok: true; rules: SensitiveRuleInput[]; duplicates: number }
  | { ok: false; error: string }

// 批量输入一行一条。先整体校验，任一行不合法就不提交，避免只写入一半。
export function parseSensitiveRules(raw: string, scope: SensitiveScope): ParsedSensitiveRules {
  if (!SENSITIVE_SCOPE_OPTIONS.some((item) => item.value === scope)) {
    return { ok: false, error: "请选择适用范围" }
  }
  const rules: SensitiveRuleInput[] = []
  const seen = new Set<string>()
  let duplicates = 0
  const lines = raw.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    const text = lines[i].trim().replace(/\s+/g, " ")
    if (!text) continue
    if (Array.from(text).length > 128) {
      return { ok: false, error: `第 ${i + 1} 行超过 128 个字符` }
    }
    if (/[\u0000-\u001f\u007f]/.test(text)) {
      return { ok: false, error: `第 ${i + 1} 行包含控制字符` }
    }
    const key = `${normalizeSensitiveTerm(text)}\u0000${scope}`
    if (seen.has(key)) {
      duplicates++
      continue
    }
    seen.add(key)
    rules.push({ text, scope })
  }
  if (rules.length === 0) return { ok: false, error: "请至少输入一条非空规则" }
  return { ok: true, rules, duplicates }
}

export function sensitiveWhitelistTarget(entry: Pick<SensitiveWhitelistInput, "cluster" | "instance">): string {
  const cluster = (entry.cluster || "").trim()
  const instance = (entry.instance || "").trim()
  return instance ? `${cluster}/${instance}` : `${cluster}/*`
}

export function sensitiveWhitelistLabel(entry: SensitiveWhitelistEntry): string {
  if (entry.kind === "user") return `用户 ${entry.user_id || ""}`.trim()
  return `目标 ${sensitiveWhitelistTarget(entry)}`
}

export function sensitiveWhitelistKey(entry: SensitiveWhitelistEntry): string {
  return [entry.kind, entry.user_id || "", entry.cluster || "", entry.instance || ""].join("\u0000")
}

export interface SensitiveWhitelistForm {
  userID: string
  cluster: string
  instance: string
}

export type SensitiveWhitelistParse =
  | { ok: true; value: SensitiveWhitelistInput }
  | { ok: false; error: string }

export function parseSensitiveWhitelist(kind: SensitiveWhitelistKind, form: SensitiveWhitelistForm): SensitiveWhitelistParse {
  if (kind === "user") {
    const userID = form.userID.trim()
    if (!userID) return { ok: false, error: "请选择用户" }
    if (/\s/.test(userID)) return { ok: false, error: "用户 ID 不能包含空白" }
    return { ok: true, value: { kind, user_id: userID } }
  }
  const cluster = form.cluster.trim()
  const instance = form.instance.trim()
  if (!cluster) return { ok: false, error: "请填写集群" }
  if (/\s/.test(cluster) || /\s/.test(instance)) return { ok: false, error: "集群和实例不能包含空白" }
  if (cluster.length > 128 || instance.length > 128) return { ok: false, error: "集群或实例名过长" }
  // 实例留空表示该集群下的全部 Agent。
  return { ok: true, value: { kind, cluster, ...(instance ? { instance } : {}) } }
}

export function filterSensitiveRules(rules: SensitiveRule[], query: string): SensitiveRule[] {
  const q = normalizeSensitiveTerm(query)
  if (!q) return rules
  return rules.filter((rule) => normalizeSensitiveTerm(rule.text).includes(q))
}

export function filterSensitiveWhitelist(entries: SensitiveWhitelistEntry[], query: string): SensitiveWhitelistEntry[] {
  const q = normalizeSensitiveTerm(query)
  if (!q) return entries
  return entries.filter((entry) => normalizeSensitiveTerm(sensitiveWhitelistLabel(entry)).includes(q))
}

export type SensitiveFilterOp = "load" | "toggleFilter" | "addRules" | "toggleRule" | "deleteRule" | "addWhitelist" | "deleteWhitelist"

export interface SensitiveFilterErrorView {
  kind: "auth" | "forbidden" | "error"
  message: string
  reload: boolean
}

const OP_LABEL: Record<SensitiveFilterOp, string> = {
  load: "加载敏感词配置",
  toggleFilter: "切换敏感词过滤总开关",
  addRules: "添加敏感词规则",
  toggleRule: "修改敏感词规则",
  deleteRule: "删除敏感词规则",
  addWhitelist: "添加白名单",
  deleteWhitelist: "删除白名单",
}

const CATEGORY_MESSAGES: Record<string, string> = {
  invalid_sensitive_filter_input: "规则或白名单格式无效",
  sensitive_rule_not_found: "该规则已被其他管理员删除，列表已刷新",
  sensitive_whitelist_not_found: "该白名单条目已被其他管理员删除，列表已刷新",
  sensitive_whitelist_exists: "该白名单条目已存在，列表已刷新",
  sensitive_rule_limit_reached: "敏感词规则数量已达上限（500 条）",
  sensitive_whitelist_limit_reached: "白名单数量已达上限（500 条）",
  sensitive_filter_storage_unavailable: "Gateway 敏感词配置存储不可用，本次修改未生效，请稍后重试",
}

// 管理接口返回的类别优先；旧版 Gateway 只回纯文本，因此同时兼容 status 与 SyntaxError。
export function describeSensitiveFilterError(error: unknown, op: SensitiveFilterOp): SensitiveFilterErrorView {
  const label = OP_LABEL[op]
  const status = typeof (error as { status?: unknown } | null)?.status === "number"
    ? ((error as { status: number }).status)
    : undefined
  const category = typeof (error as { category?: unknown } | null)?.category === "string"
    ? ((error as { category: string }).category).trim()
    : ""
  const raw = category || (error instanceof Error ? error.message.trim() : "")
  const fail = (message: string, reload = false): SensitiveFilterErrorView => ({
    kind: "error",
    message: `${label}失败：${message}`,
    reload,
  })
  const unknown = (detail: string): SensitiveFilterErrorView => ({
    kind: "error",
    message: `${label}结果未知：${detail}，配置可能已经变更；列表已刷新，请核对当前状态`,
    reload: true,
  })
  const mutation = op !== "load"
  const authMessage = gatewayAuthMessage(raw)
  if (authMessage) return { kind: "auth", message: authMessage, reload: false }
  if (status === 401) return { kind: "auth", message: "登录已失效或令牌已被撤销，请重新登录后再管理敏感词。", reload: false }
  if (status === 403 && (raw === "Forbidden" || raw === "forbidden")) {
    return { kind: "forbidden", message: "当前账号不是平台管理员，无权查看或修改敏感词配置。", reload: false }
  }
  // 演示模式没有 HTTP 状态，但错误仍带 Gateway 类别，应与真实接口给出同一套中文文案。
  const categoryMessage = CATEGORY_MESSAGES[raw]
  if (categoryMessage) return fail(categoryMessage, status === 404 || status === 409 || status === undefined)
  if (status === undefined) {
    if (!(error instanceof Error)) return fail("未知错误")
    if (error instanceof SyntaxError) {
      const detail = "服务端返回了无法识别的响应（可能是反向代理错误页）"
      return mutation ? unknown(detail) : fail(`${detail}，请稍后重试`)
    }
    if (error instanceof TypeError && mutation) return unknown(`请求中断（${raw}）`)
    return fail(raw || "未知错误")
  }
  if (status === 400 && raw === "invalid sensitive rule request") return fail("请求格式无效")
  if (status === 400 && raw === "invalid sensitive whitelist request") return fail("白名单请求格式无效")
  if (status === 400 && raw === "id and enabled are required") return fail("缺少规则 ID 或状态")
  if (status === 400 && raw === "missing id") return fail("缺少规则 ID")
  if (status === 404 && raw === "404 page not found") {
    return fail("当前 Gateway 没有敏感词管理接口（返回 404），请先升级 Gateway")
  }
  if (status === 503 && raw === "audit_unavailable") {
    return fail("Gateway 审计存储不可用，本次修改未执行，请稍后重试")
  }
  if (status === 503 && raw.startsWith("audit_completion_failed")) return unknown("Gateway 审计未写完")
  if ((status === 502 || status === 504) && mutation) return unknown(`${raw || "Gateway 无响应"}（HTTP ${status}）`)
  return fail(`${raw || "请求失败"}（HTTP ${status}）`)
}
