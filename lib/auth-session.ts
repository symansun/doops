export const SESSION_GENERATION_HEADER = "X-Doops-Session-Generation"
export const SESSION_CHANGED_CODE = "browser_session_changed"

export class SessionChangedError extends Error {
  constructor() {
    super("登录会话已变更或失效，本次操作已停止；请重新登录后重试")
  }
}
