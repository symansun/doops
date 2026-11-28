// AI 助手正在执行回合时的常驻提示。面板切到其他标签只是 hidden，任务仍在跑；
// 原来只有切回「AI 助手」才有停止入口，其他页面只会提示「请先停止当前 AI 任务」
// 却没有任何可点的控件，用户等于无法中断。
export const AGENT_BUSY_TEXT = "AI 助手正在执行任务；切到其他页面不会停止它。"

export type AgentBusySnapshot = {
  status?: string
  items?: readonly { kind?: string; role?: string; status?: string }[]
  interactions?: readonly { status?: string }[]
}

export type AgentBusyBanner = {
  text: string
  stopLabel: string
  openLabel: string
}

// 与 isDoopsAgentSessionBusy 判定一致：运行中、有待处理的用户消息、或有待处理的交互。
export function agentTurnRunning(snapshot: AgentBusySnapshot | null | undefined): boolean {
  if (!snapshot) return false
  if (snapshot.status === "running") return true
  if ((snapshot.items ?? []).some((item) => item.kind === "message" && item.role === "user" && item.status === "pending")) return true
  return (snapshot.interactions ?? []).some((interaction) => interaction.status === "pending" || interaction.status === "running")
}

// 只在未展示 AI 助手面板的标签页给出提示：停在「AI 助手」或已在终端打开 AI 协同侧栏时，输入框自带停止按钮。
export function agentBusyView(
  snapshot: AgentBusySnapshot | null | undefined,
  tab: string,
  agentVisible = tab === "ask",
): AgentBusyBanner | null {
  if (agentVisible || tab === "ask") return null
  if (!agentTurnRunning(snapshot)) return null
  return { text: AGENT_BUSY_TEXT, stopLabel: "停止 AI 任务", openLabel: "查看" }
}
