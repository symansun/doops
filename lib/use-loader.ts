"use client"

import { useCallback, useEffect, useRef, useState } from "react"

// 面板读取数据的统一骨架。
//
// 改动前有 10 处各自手写同一段 Effect：new AbortController() → setLoading(true) →
// 请求 → "if (signal.aborted) return" 守卫 → catch 里清空数据并写错误 → finally 里
// 同样守卫着 setLoading(false)。重复的不只是样板，而是取消语义；每抄一遍就多一次
// 漏掉加载失败收尾的机会（k8s-resource-overview 就缺少 catch，失败后会一直转圈）。
//
// 这里把三件事固定下来：
// 1. 每次依赖变化都中断上一次请求，中断不产生错误提示；
// 2. 失败时清空旧数据并给出可读错误，加载态一定结束；
// 3. 调用方只描述"怎么读"，不再关心生命周期。

export type LoaderState<T> = {
  data: T | null
  error: string
  loading: boolean
  /** 结果是否来自当前这一轮加载；切走后再回来读它才有意义。 */
  settled: boolean
  reload: () => void
}

export type LoaderResult<T> = {
  data: T | null
  error: string
  loading: boolean
  settled: boolean
  reload: () => void
}

function message(reason: unknown): string {
  return reason instanceof Error ? reason.message : String(reason)
}

/**
 * 在依赖变化时读取一次数据。
 *
 * @param read 收到 AbortSignal 的读取函数；抛错即视为失败。
 * @param deps 任一变化就重新读取（与 useEffect 语义一致）。
 * @param options.initialData 读取完成前的数据，默认 null。
 * @param options.enabled 为 false 时不读取，也不进入加载态。
 */
export function useLoader<T>(
  read: (signal: AbortSignal) => Promise<T>,
  deps: readonly unknown[],
  options: { initialData?: T | null; enabled?: boolean } = {},
): LoaderState<T> {
  const { enabled = true } = options
  const [data, setData] = useState<T | null>(options.initialData ?? null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(enabled)
  const [settled, setSettled] = useState(false)
  const [revision, reload] = useState(0)
  // read 通常是内联闭包，放进 ref 以免它本身触发重新加载；依赖仍由 deps 显式决定。
  const readRef = useRef(read)
  readRef.current = read

  useEffect(() => {
    if (!enabled) {
      setLoading(false)
      return
    }
    const controller = new AbortController()
    setLoading(true)
    setError("")
    setSettled(false)
    let live = true
    void (async () => {
      try {
        const next = await readRef.current(controller.signal)
        if (!live || controller.signal.aborted) return
        setData(next)
        setSettled(true)
      } catch (reason) {
        if (!live || controller.signal.aborted) return
        setData(null)
        setError(message(reason))
        setSettled(true)
      } finally {
        if (live && !controller.signal.aborted) setLoading(false)
      }
    })()
    return () => {
      // live 必须在 abort 之前翻掉：请求可能已经进入 then，abort 之后它仍会排队执行。
      live = false
      controller.abort()
    }
  }, [enabled, revision, ...deps])

  return { data, error, loading, settled, reload: useCallback(() => reload((n) => n + 1), []) }
}
