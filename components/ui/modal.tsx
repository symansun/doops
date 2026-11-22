"use client"

import { useEffect, useId, useRef } from "react"
import { createPortal } from "react-dom"

// 全站唯一的模态外壳。
//
// 改动前 credential-panel / credential-bundle-dialog / credential-secret-import 各自复制了
// 一份 "fixed inset-0 z-50 … max-h-[90dvh] max-w-xl" 的骨架，只有 app-dialog 做了
// Escape 关闭、焦点归还和滚动锁定。重复的不只是标记，而是这段容易漏掉的正确性逻辑，
// 所以这里把它收成一处，调用方只描述内容。

const SIZE = {
  sm: "max-w-sm",
  md: "max-w-xl",
  lg: "max-w-3xl",
} as const

export type ModalSize = keyof typeof SIZE

export function Modal({
  title,
  onClose,
  children,
  size = "md",
  footer,
  testId,
}: {
  title: React.ReactNode
  onClose: () => void
  children: React.ReactNode
  size?: ModalSize
  footer?: React.ReactNode
  testId?: string
}) {
  const titleId = useId()
  const panelRef = useRef<HTMLDivElement>(null)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose

  useEffect(() => {
    // 关闭后把焦点还给打开弹窗的控件，否则键盘用户会掉到页面开头。
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const panel = panelRef.current
    const focusable = panel?.querySelector<HTMLElement>(
      'input:not([disabled]), textarea:not([disabled]), select:not([disabled]), button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])',
    )
    ;(focusable ?? panel)?.focus()
    return () => {
      previous?.focus()
    }
  }, [])

  useEffect(() => {
    const before = document.body.style.overflow
    document.body.style.overflow = "hidden"
    function onKey(event: KeyboardEvent) {
      if (event.key !== "Escape") return
      event.preventDefault()
      event.stopPropagation()
      onCloseRef.current()
    }
    document.addEventListener("keydown", onKey, true)
    return () => {
      document.removeEventListener("keydown", onKey, true)
      document.body.style.overflow = before
    }
  }, [])

  if (typeof document === "undefined") return null

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        data-testid={testId}
        className={`max-h-[90dvh] w-full ${SIZE[size]} overflow-auto rounded-lg border bg-card shadow-xl outline-none`}
      >
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 id={titleId} className="text-sm font-semibold text-foreground">
            {title}
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
          >
            关闭
          </button>
        </div>
        {children}
        {footer}
      </div>
    </div>,
    document.body,
  )
}
