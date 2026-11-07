import type { Session } from "./client"

// 粘贴 token 登录时 Gateway 不返回用户名，控制台拿不到真实身份（身份接口另立项）。
// 这里只做显示回退：占位名不进 session，也不参与 R4-2 的持久化判断
// （console-persistence 的 persistsScope 仍然只认真实 username）。
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
