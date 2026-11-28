import { beforeUnloadWarning, type CommandJournal } from "./console-persistence.ts"

// K25 根因：真实 Chrome 刷新的事件顺序是 beforeunload → 被中断的 RPC 流 end()
// → pagehide。只在 pagehide 里 setUnloading(true) 太晚：end() 已经因为 unloading
// 仍是 false 删掉了 journal 记录，新页面 takeInterrupted 取不到，横幅不会出现。
//
// 所以 beforeunload 里就置位；用户取消刷新时不会再收到 pagehide，需要一个延迟重置，
// 真正的导航会在延迟内触发 pagehide 并取消失效。
export const UNLOAD_RESET_DELAY_MS = 500

// 只用到 activeCount/setUnloading，测试可以传计数即可。
export type UnloadJournal = Pick<CommandJournal, "activeCount" | "setUnloading">

export interface UnloadEventLike {
  preventDefault?: () => void
  returnValue?: unknown
}

// window 满足这个结构；测试用假 target 记录监听器。
export interface JournalUnloadTarget {
  addEventListener: (type: string, listener: (event: UnloadEventLike) => void) => void
  removeEventListener: (type: string, listener: (event: UnloadEventLike) => void) => void
}

export interface UnloadTimers {
  set: (callback: () => void, delayMs: number) => unknown
  clear: (handle: unknown) => void
}

const DEFAULT_TIMERS: UnloadTimers = {
  set: (callback, delayMs) => setTimeout(callback, delayMs),
  clear: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
}

export interface InstallJournalUnloadOptions {
  resetDelayMs?: number
  timers?: UnloadTimers
}

// 安装 beforeunload/pagehide/pageshow 处理，返回卸载函数。
export function installJournalUnloadHandlers(
  target: JournalUnloadTarget,
  journal: UnloadJournal,
  options: InstallJournalUnloadOptions = {},
): () => void {
  const resetDelayMs = options.resetDelayMs ?? UNLOAD_RESET_DELAY_MS
  const timers = options.timers ?? DEFAULT_TIMERS
  let pendingReset: unknown = null

  function cancelPendingReset(): void {
    if (pendingReset === null) return
    timers.clear(pendingReset)
    pendingReset = null
  }

  function onBeforeUnload(event: UnloadEventLike): void {
    const warning = beforeUnloadWarning(journal.activeCount())
    if (!warning) return
    // 必须在 end() 之前置位：Chrome 在 beforeunload 之后就中断 RPC 流。
    journal.setUnloading(true)
    cancelPendingReset()
    pendingReset = timers.set(() => {
      pendingReset = null
      // 期间没有 pagehide：用户取消了刷新，页面还在，正常结束的命令不算中断。
      journal.setUnloading(false)
    }, resetDelayMs)
    event.preventDefault?.()
    event.returnValue = warning
  }

  function onPageHide(): void {
    // 导航确实发生：取消失效标记，保持卸载中，让 end() 保留记录。
    cancelPendingReset()
    journal.setUnloading(true)
  }

  function onPageShow(): void {
    cancelPendingReset()
    journal.setUnloading(false)
  }

  target.addEventListener("beforeunload", onBeforeUnload)
  target.addEventListener("pagehide", onPageHide)
  target.addEventListener("pageshow", onPageShow)
  return () => {
    cancelPendingReset()
    target.removeEventListener("beforeunload", onBeforeUnload)
    target.removeEventListener("pagehide", onPageHide)
    target.removeEventListener("pageshow", onPageShow)
  }
}
