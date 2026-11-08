import type { AdminToken, AdminUser } from "./admin"
import { gatewayTime } from "./instance-activity.ts"
export {
  ACTIVE_INSTANCE_WINDOW_DAYS,
  compareInstances,
  gatewayTime,
  groupInstances,
  instanceActivity,
  instanceClusterOptions,
  offlineDays,
  offlineLabel,
  summarizeInstanceScope,
  type InstanceActivity,
  type InstanceClusterOption,
  type InstanceGroups,
  type InstanceScope,
} from "./instance-activity.ts"

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
