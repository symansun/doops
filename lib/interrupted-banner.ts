import { interruptedCommandNotice, type CommandRecord } from "./console-persistence.ts"

// 刷新恢复后要展示的「结果未知」提示。只要恢复出记录就产生提示：与当次 /api/targets
// 是否返回目标、用户当前是否在终端页都无关。提示由 console-shell 顶层横幅渲染，
// 记录留在 shell 状态里，直到用户点关闭（终端面板不再持有它们，避免重复显示）。
export interface InterruptedRestore {
  selected: { key: string } | null
  sessionId: string | null
}

export function interruptedBannerNotices(
  records: readonly CommandRecord[],
  restored: InterruptedRestore,
): string[] {
  if (!records.length) return []
  const current = restored.selected
    ? { targetKey: restored.selected.key, sessionId: restored.sessionId ?? "" }
    : null
  return records.map((record) => interruptedCommandNotice(record, current))
}

export interface InterruptedBannerView {
  role: "alert"
  notices: string[]
  dismissLabel: string
}

// 没有记录时不渲染横幅；有记录时一直可渲染（可多次渲染），关闭动作由调用方处理。
export function interruptedBannerView(notices: readonly string[]): InterruptedBannerView | null {
  if (!notices.length) return null
  return { role: "alert", notices: [...notices], dismissLabel: "知道了" }
}
