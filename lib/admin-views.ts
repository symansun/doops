import type { AdminInstance, AdminToken, AdminUser } from "./admin"

// 离线不超过这个天数的实例仍算活跃；离线超过的折叠为历史实例。
export const ACTIVE_INSTANCE_WINDOW_DAYS = 7
const DAY_MS = 24 * 60 * 60 * 1000

// Gateway 用 Go time.Time 输出时间，空值是 0001-01-01T00:00:00Z；这类值和无法解析的值都当作没有。
export function gatewayTime(value?: string): number | null {
  if (!value) return null
  const ms = Date.parse(value)
  if (!Number.isFinite(ms) || ms <= 0) return null
  return ms
}

/* ============================ 实例 ============================ */

export type InstanceActivity = "online" | "recent" | "history"

// 在线、离线 7 天以内（含恰好 7 天）算活跃；离线超过 7 天或没有心跳时间算历史。
export function instanceActivity(row: AdminInstance, now: number): InstanceActivity {
  if (row.status === "online") return "online"
  const lastSeen = gatewayTime(row.last_seen)
  if (lastSeen === null) return "history"
  return now - lastSeen <= ACTIVE_INSTANCE_WINDOW_DAYS * DAY_MS ? "recent" : "history"
}

// 离线整天数，向下取整；在线或没有心跳时间时返回 null。
export function offlineDays(row: AdminInstance, now: number): number | null {
  if (row.status === "online") return null
  const lastSeen = gatewayTime(row.last_seen)
  if (lastSeen === null) return null
  return Math.max(0, Math.floor((now - lastSeen) / DAY_MS))
}

export function offlineLabel(row: AdminInstance, now: number): string {
  if (row.status === "online") return ""
  const days = offlineDays(row, now)
  if (days === null) return "离线时间未知"
  if (days < 1) return "离线不到 1 天"
  return `离线 ${days} 天`
}

// 在线优先，再按最后心跳倒序；没有心跳时间的排最后，最后按集群/实例名排稳定顺序。
export function compareInstances(a: AdminInstance, b: AdminInstance): number {
  const onlineA = a.status === "online" ? 0 : 1
  const onlineB = b.status === "online" ? 0 : 1
  if (onlineA !== onlineB) return onlineA - onlineB
  const seenA = gatewayTime(a.last_seen) ?? -Infinity
  const seenB = gatewayTime(b.last_seen) ?? -Infinity
  if (seenA !== seenB) return seenB > seenA ? 1 : -1
  return a.cluster.localeCompare(b.cluster) || a.instance.localeCompare(b.instance)
}

export interface InstanceGroups {
  active: AdminInstance[]
  history: AdminInstance[]
}

// cluster 为空表示全部集群。
export function groupInstances(rows: AdminInstance[], now: number, cluster = ""): InstanceGroups {
  const groups: InstanceGroups = { active: [], history: [] }
  for (const row of [...rows].sort(compareInstances)) {
    if (cluster && row.cluster !== cluster) continue
    if (instanceActivity(row, now) === "history") groups.history.push(row)
    else groups.active.push(row)
  }
  return groups
}

export interface InstanceClusterOption {
  name: string
  online: number
  total: number
}

export function instanceClusterOptions(rows: AdminInstance[]): InstanceClusterOption[] {
  const byName = new Map<string, InstanceClusterOption>()
  for (const row of rows) {
    const option = byName.get(row.cluster) || { name: row.cluster, online: 0, total: 0 }
    option.total++
    if (row.status === "online") option.online++
    byName.set(row.cluster, option)
  }
  return [...byName.values()].sort((a, b) => a.name.localeCompare(b.name))
}

export interface InstanceScope {
  active: number
  online: number
  history: number
}

export function summarizeInstanceScope(rows: AdminInstance[], now: number): InstanceScope {
  const scope: InstanceScope = { active: 0, online: 0, history: 0 }
  for (const row of rows) {
    const activity = instanceActivity(row, now)
    if (activity === "history") {
      scope.history++
      continue
    }
    scope.active++
    if (activity === "online") scope.online++
  }
  return scope
}

/* ============================ 令牌 ============================ */

export type TokenState = "active" | "revoked" | "user_disabled" | "user_missing" | "expired"

export const TOKEN_STATE_LABELS: Record<TokenState, string> = {
  active: "有效",
  revoked: "已撤销",
  user_disabled: "用户已停用，不可用",
  user_missing: "用户不存在，不可用",
  expired: "已过期",
}

// 与 Gateway 校验令牌的条件一致：未撤销、用户存在且未停用、未到期。
export function tokenState(token: AdminToken, user: AdminUser | undefined, now: number): TokenState {
  if (token.revoked) return "revoked"
  if (!user) return "user_missing"
  if (user.disabled) return "user_disabled"
  const expires = gatewayTime(token.expires_at)
  if (expires !== null && now >= expires) return "expired"
  return "active"
}

export interface TokenRow {
  token: AdminToken
  state: TokenState
}

export interface TokenGroup {
  key: string
  userName: string
  userDisabled: boolean
  // 该用户的全部令牌数，包括已撤销的。
  total: number
  usable: number
  revoked: number
  // 当前要显示的行；隐藏已撤销时不含已撤销令牌。
  rows: TokenRow[]
}

export function groupTokensByUser(
  tokens: AdminToken[],
  users: AdminUser[],
  options: { now: number; showRevoked: boolean },
): TokenGroup[] {
  const usersById = new Map(users.map((user) => [user.id, user]))
  const groups = new Map<string, TokenGroup>()
  for (const token of tokens) {
    const key = token.user_id || token.user_name || ""
    const user = token.user_id ? usersById.get(token.user_id) : undefined
    const state = tokenState(token, user, options.now)
    let group = groups.get(key)
    if (!group) {
      group = {
        key,
        userName: user?.name || token.user_name || token.user_id || "未知用户",
        userDisabled: user?.disabled === true,
        total: 0,
        usable: 0,
        revoked: 0,
        rows: [],
      }
      groups.set(key, group)
    }
    group.total++
    if (state === "active") group.usable++
    if (state === "revoked") group.revoked++
    if (state !== "revoked" || options.showRevoked) group.rows.push({ token, state })
  }
  const createdAt = (row: TokenRow) => gatewayTime(row.token.created_at) ?? -Infinity
  return [...groups.values()]
    .filter((group) => group.rows.length > 0)
    .map((group) => ({
      ...group,
      rows: [...group.rows].sort((a, b) => (createdAt(b) > createdAt(a) ? 1 : createdAt(b) < createdAt(a) ? -1 : 0)),
    }))
    .sort((a, b) =>
      Number(a.userDisabled) - Number(b.userDisabled) ||
      a.userName.localeCompare(b.userName) ||
      a.key.localeCompare(b.key),
    )
}

// 令牌多于这个数的用户默认折叠，避免一个人的令牌占满整页。
export const TOKEN_GROUP_COLLAPSE_AT = 10

// toggled 记录用户点过展开/收起的分组；点过一次就和默认状态相反。
export function isTokenGroupCollapsed(group: TokenGroup, toggled: ReadonlySet<string>): boolean {
  const byDefault = group.rows.length > TOKEN_GROUP_COLLAPSE_AT
  return toggled.has(group.key) ? !byDefault : byDefault
}

/* ============================ 用户 ============================ */

// 已停用用户排到最后，其余保持 Gateway 返回的顺序（按创建时间）。
export function sortUsersForDisplay(users: AdminUser[]): AdminUser[] {
  return [...users].sort((a, b) => Number(a.disabled) - Number(b.disabled))
}

export interface UserSummary {
  total: number
  admins: number
  disabled: number
}

// Gateway 只把未停用的 admin 当作平台管理员。
export function summarizeUsers(users: AdminUser[]): UserSummary {
  return {
    total: users.length,
    admins: users.filter((user) => user.is_admin && !user.disabled).length,
    disabled: users.filter((user) => user.disabled).length,
  }
}

/* ============================ 审计摘要 ============================ */

export const COMMAND_PREVIEW_MAX = 120
export const COMMAND_TITLE_MAX = 1000
const BASE64_RUN = /[A-Za-z0-9+/]{40,}={0,2}/g

export interface CommandPreview {
  // 单行预览：空白折叠，base64 长串换成占位，超长截断。
  text: string
  full: string
  // 悬停提示：完整内容，过长时截断并提示点击展开。
  title: string
  shortened: boolean
}

// 十六进制摘要（只有小写和数字）不算 base64，按普通文本截断。
function looksLikeBase64(run: string): boolean {
  return /[A-Z]/.test(run) && /[a-z]/.test(run) && /[0-9+/]/.test(run)
}

export function commandPreview(value: string | undefined, max = COMMAND_PREVIEW_MAX): CommandPreview {
  const full = (value || "").trim()
  let text = full
    .replace(/\s+/g, " ")
    .replace(BASE64_RUN, (run) => (looksLikeBase64(run) ? `‹base64 ${run.length} 字符›` : run))
  const chars = Array.from(text)
  if (chars.length > max) text = `${chars.slice(0, max - 1).join("")}…`
  const fullChars = Array.from(full)
  const title = fullChars.length > COMMAND_TITLE_MAX
    ? `${fullChars.slice(0, COMMAND_TITLE_MAX).join("")}…（点击展开完整内容）`
    : full
  return { text, full, title, shortened: text !== full }
}
