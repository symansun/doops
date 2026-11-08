import type { Session } from "./client"

// 真实身份由 Gateway /v1/auth/me 返回；缺少用户名的会话只显示占位文案。
export const TOKEN_LOGIN_LABEL = "令牌登录"

type SessionDisplay = Pick<Session, "username" | "demo">

// 顶部用户位置和概览标题共用同一个名字来源，避免两处不一致。
export function displayUserName(session: SessionDisplay): string {
  const username = session.username?.trim()
  if (username) return username
  return session.demo ? "演示用户" : TOKEN_LOGIN_LABEL
}

export function greeting(session: SessionDisplay): string {
  return `你好，${displayUserName(session)}`
}
