"use client"

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { ClipboardPaste, FileInput } from "lucide-react"
import {
  clampContextMenuPosition,
  normalizeTerminalCopyText,
  summarizeTerminalSelection,
  type TerminalAiActionMode,
} from "@/lib/terminal-ai"
import { cn } from "@/lib/cn"
import { CopyIcon, SparkIcon } from "./icons"

export async function copyTerminalText(raw: string): Promise<boolean> {
  const text = normalizeTerminalCopyText(raw)
  if (!text) return false
  try {
    if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 非 HTTPS 内网地址或浏览器权限受限时走离线 textarea 复制兜底。
  }
  if (typeof document === "undefined") return false
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const textarea = document.createElement("textarea")
  textarea.value = text
  textarea.setAttribute("readonly", "true")
  textarea.style.position = "fixed"
  textarea.style.top = "-9999px"
  textarea.style.left = "-9999px"
  textarea.style.opacity = "0"
  document.body.appendChild(textarea)
  try {
    textarea.focus()
    textarea.select()
    return document.execCommand("copy")
  } catch {
    return false
  } finally {
    textarea.remove()
    active?.focus?.()
  }
}

export function getSelectionTextWithin(container: HTMLElement | null): string {
  if (!container || typeof window === "undefined") return ""
  const selection = window.getSelection()
  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) return ""
  const { anchorNode, focusNode } = selection
  if ((anchorNode && container.contains(anchorNode)) || (focusNode && container.contains(focusNode))) {
    return selection.toString()
  }
  return ""
}

export interface TerminalContextMenuState {
  x: number
  y: number
  selection: string
}

export function TerminalContextMenu({
  state,
  onClose,
  onCopy,
  onSingleShotAi,
  onSendToAi,
  onPaste,
  aiDisabled = false,
  aiDisabledReason,
  pasteDisabled = false,
}: {
  state: TerminalContextMenuState | null
  onClose: (restoreFocus?: boolean) => void
  onCopy: (selection: string) => void
  onSingleShotAi?: (selection: string) => void
  onSendToAi?: (selection: string, mode: TerminalAiActionMode) => void
  onPaste?: () => void
  aiDisabled?: boolean
  aiDisabledReason?: string
  pasteDisabled?: boolean
}) {
  const menuRef = useRef<HTMLDivElement | null>(null)
  const [coords, setCoords] = useState<{ left: number; top: number } | null>(null)

  const updateCoords = useCallback(() => {
    if (!state || typeof window === "undefined") return
    const el = menuRef.current
    const width = el?.offsetWidth || 256
    const height = el?.offsetHeight || 196
    setCoords(
      clampContextMenuPosition(
        { x: state.x, y: state.y },
        { width, height },
        { width: window.innerWidth, height: window.innerHeight },
      ),
    )
  }, [state])

  useLayoutEffect(() => {
    if (!state) {
      setCoords(null)
      return
    }
    updateCoords()
    const first = menuRef.current?.querySelector<HTMLButtonElement>('[role="menuitem"]:not(:disabled)')
    if (first) first.focus()
    else menuRef.current?.focus()
  }, [state, updateCoords])

  useEffect(() => {
    if (!state || typeof window === "undefined") return
    const handlePointerDown = (event: MouseEvent) => {
      const target = event.target as Node | null
      if (target && menuRef.current?.contains(target)) return
      onClose(false)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault()
        event.stopPropagation()
        onClose(true)
        return
      }
      if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return
      const items = Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [])
      if (!items.length) return
      event.preventDefault()
      const current = items.indexOf(document.activeElement as HTMLButtonElement)
      let next = 0
      if (event.key === "Home") next = 0
      else if (event.key === "End") next = items.length - 1
      else if (event.key === "ArrowDown") next = current < 0 ? 0 : (current + 1) % items.length
      else if (event.key === "ArrowUp") next = current <= 0 ? items.length - 1 : current - 1
      items[next]?.focus()
    }
    const handleViewportChange = () => onClose(false)
    document.addEventListener("mousedown", handlePointerDown)
    document.addEventListener("keydown", handleKeyDown, true)
    window.addEventListener("resize", handleViewportChange)
    window.addEventListener("blur", handleViewportChange)
    window.addEventListener("scroll", handleViewportChange, true)
    return () => {
      document.removeEventListener("mousedown", handlePointerDown)
      document.removeEventListener("keydown", handleKeyDown, true)
      window.removeEventListener("resize", handleViewportChange)
      window.removeEventListener("blur", handleViewportChange)
      window.removeEventListener("scroll", handleViewportChange, true)
    }
  }, [state, onClose])

  if (!state || typeof document === "undefined") return null

  const summary = summarizeTerminalSelection(state.selection)
  const canUseAi = summary.hasSelection && Boolean(onSendToAi) && !aiDisabled
  const shortcutLabel = typeof navigator !== "undefined" && /Mac|iPhone|iPad/i.test(navigator.platform || "") ? "⌘C" : "Ctrl+C"
  const pasteShortcutLabel = typeof navigator !== "undefined" && /Mac|iPhone|iPad/i.test(navigator.platform || "") ? "⌘V" : "Ctrl+V"

  const itemClass = cn(
    "flex w-full cursor-pointer select-none items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm outline-none transition-colors",
    "hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
    "disabled:pointer-events-none disabled:opacity-50",
  )

  return createPortal(
    <div
      ref={menuRef}
      role="menu"
      tabIndex={-1}
      aria-label="终端右键菜单"
      data-testid="terminal-context-menu"
      style={{
        left: coords?.left ?? state.x,
        top: coords?.top ?? state.y,
      }}
      onContextMenu={(event) => event.preventDefault()}
      className="fixed z-50 w-68 rounded-lg border bg-popover p-1 text-popover-foreground shadow-lg outline-none"
    >
      <div className="px-2.5 py-1.5 text-xs text-muted-foreground">
        {summary.label}
      </div>
      <div role="separator" className="-mx-1 my-1 h-px bg-border" />
      <button
        type="button"
        role="menuitem"
        data-testid="terminal-menu-copy"
        disabled={!summary.hasSelection}
        onClick={() => {
          const text = state.selection
          onClose(true)
          onCopy(text)
        }}
        className={itemClass}
      >
        <CopyIcon width={15} height={15} aria-hidden="true" className="shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate">复制选中内容</span>
        <span className="shrink-0 font-mono text-xs text-muted-foreground">{shortcutLabel}</span>
      </button>
      {onSingleShotAi && (
        <button
          type="button"
          role="menuitem"
          data-testid="terminal-menu-single-ai"
          disabled={!canUseAi}
          title={!summary.hasSelection ? "请先在终端拖选内容" : aiDisabled ? aiDisabledReason : "单次调用模型快速解释选中内容并给出建议命令（不打开 AI 协同）"}
          onClick={() => {
            const text = state.selection
            onClose(false)
            onSingleShotAi(text)
          }}
          className={itemClass}
        >
          <SparkIcon width={15} height={15} aria-hidden="true" className="shrink-0 text-primary" />
          <span className="min-w-0 flex-1 truncate font-medium">AI 单次解释 / 排查</span>
          <span className="shrink-0 text-xs text-muted-foreground">
            {aiDisabled && aiDisabledReason ? aiDisabledReason : "单次直答"}
          </span>
        </button>
      )}
      {onSendToAi && (
        <>
          <button
            type="button"
            role="menuitem"
            data-testid="terminal-menu-send-ai"
            disabled={!canUseAi}
            title={!summary.hasSelection ? "请先在终端拖选内容" : aiDisabled ? aiDisabledReason : "复制选中内容并转入右侧多轮 AI 协同助手深入排查"}
            onClick={() => {
              const text = state.selection
              onClose(false)
              onSendToAi(text, "execute")
            }}
            className={itemClass}
          >
            <SparkIcon width={15} height={15} aria-hidden="true" className="shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">{onSingleShotAi ? "转入 AI 协同深入排查" : "发送到 AI 执行"}</span>
            <span className="shrink-0 text-xs text-muted-foreground">
              {aiDisabled && aiDisabledReason ? aiDisabledReason : onSingleShotAi ? "多轮助手" : "立即处理"}
            </span>
          </button>
          <button
            type="button"
            role="menuitem"
            data-testid="terminal-menu-prefill-ai"
            disabled={!canUseAi}
            title={!summary.hasSelection ? "请先在终端拖选内容" : aiDisabled ? aiDisabledReason : "将选中内容填入右侧 AI 协同输入框，补充说明后再发送"}
            onClick={() => {
              const text = state.selection
              onClose(false)
              onSendToAi(text, "prefill")
            }}
            className={itemClass}
          >
            <FileInput aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">{onSingleShotAi ? "填入 AI 协同输入框" : "填入 AI 输入框"}</span>
            <span className="shrink-0 text-xs text-muted-foreground">补充说明</span>
          </button>
        </>
      )}
      {onPaste && (
        <>
          <div role="separator" className="-mx-1 my-1 h-px bg-border" />
          <button
            type="button"
            role="menuitem"
            data-testid="terminal-menu-paste"
            disabled={pasteDisabled}
            onClick={() => {
              onClose(true)
              onPaste()
            }}
            className={itemClass}
          >
            <ClipboardPaste aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">粘贴到终端</span>
            <span className="shrink-0 font-mono text-xs text-muted-foreground">{pasteShortcutLabel}</span>
          </button>
        </>
      )}
    </div>,
    document.body,
  )
}
