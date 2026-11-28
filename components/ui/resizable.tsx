"use client"

import { Group, Panel, Separator } from "react-resizable-panels"
import { useCallback, type ComponentProps, type KeyboardEvent, type PointerEvent, type ReactNode, type RefObject } from "react"
import { cn } from "@/lib/cn"

// Split panes for the cases a stack cannot express: a list beside a detail
// pane, an editor beside a preview. react-resizable-panels owns the pointer
// maths, keyboard resizing and layout persistence (pass an id to remember the
// split across reloads).
export function ResizablePanelGroup({ className, orientation = "horizontal", ...props }: ComponentProps<typeof Group>) {
  return <Group orientation={orientation} className={cn("flex min-h-0 min-w-0", className)} {...props} />
}

export const ResizablePanel = Panel

// The separator is the drag target: it is one pixel wide but keeps a wider
// hit area so the pointer can find it.
export function ResizableHandle({ className }: { className?: string }) {
  return (
    <Separator
      className={cn(
        "relative flex w-px shrink-0 items-center justify-center bg-border transition-colors",
        "before:absolute before:inset-y-0 before:-left-1 before:w-3 before:content-['']",
        "hover:bg-primary/50 data-[separator-overlay=active]:bg-primary",
        className,
      )}
    />
  )
}

// Kept for callers that render a handle inside a vertical split.
export function ResizableVHandle({ className }: { className?: string }) {
  return <ResizableHandle className={cn("h-px w-full before:inset-x-0 before:-top-1 before:h-3 before:w-auto", className)} />
}

// Accessible drag/keyboard separator for keep-alive flex split containers where
// sibling panels stay mounted with `hidden` (so react-resizable-panels Group
// cannot unconditionally wrap/unmount them without dropping live sessions).
export function SplitResizeHandle({
  containerRef,
  valuePct,
  onChangePct,
  minPct = 28,
  maxPct = 68,
  stepPct = 4,
  label,
  className,
}: {
  containerRef: RefObject<HTMLElement | null>
  valuePct: number
  onChangePct: (nextPct: number) => void
  minPct?: number
  maxPct?: number
  stepPct?: number
  label: string
  className?: string
}) {
  const clamp = useCallback(
    (pct: number) => Math.round(Math.min(maxPct, Math.max(minPct, pct))),
    [maxPct, minPct],
  )

  const handlePointerDown = useCallback(
    (event: PointerEvent<HTMLDivElement>) => {
      const container = containerRef.current
      if (!container) return
      event.preventDefault()
      event.currentTarget.setPointerCapture?.(event.pointerId)
      const onMove = (moveEvent: globalThis.PointerEvent) => {
        const rect = container.getBoundingClientRect()
        if (rect.width <= 0) return
        const rightPct = ((rect.right - moveEvent.clientX) / rect.width) * 100
        onChangePct(clamp(rightPct))
      }
      const onUp = () => {
        window.removeEventListener("pointermove", onMove)
        window.removeEventListener("pointerup", onUp)
        window.removeEventListener("pointercancel", onUp)
      }
      window.addEventListener("pointermove", onMove)
      window.addEventListener("pointerup", onUp)
      window.addEventListener("pointercancel", onUp)
    },
    [clamp, containerRef, onChangePct],
  )

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      if (event.key === "ArrowLeft") {
        event.preventDefault()
        onChangePct(clamp(valuePct + stepPct))
      } else if (event.key === "ArrowRight") {
        event.preventDefault()
        onChangePct(clamp(valuePct - stepPct))
      } else if (event.key === "Home") {
        event.preventDefault()
        onChangePct(minPct)
      } else if (event.key === "End") {
        event.preventDefault()
        onChangePct(maxPct)
      }
    },
    [clamp, maxPct, minPct, onChangePct, stepPct, valuePct],
  )

  return (
    <div
      role="separator"
      tabIndex={0}
      aria-orientation="vertical"
      aria-label={label}
      aria-valuemin={minPct}
      aria-valuemax={maxPct}
      aria-valuenow={valuePct}
      onPointerDown={handlePointerDown}
      onKeyDown={handleKeyDown}
      className={cn(
        "relative hidden w-1 shrink-0 cursor-col-resize bg-border/60 transition-colors hover:bg-primary/60 focus-visible:bg-primary focus-visible:outline-none lg:block",
        className,
      )}
    />
  )
}

export type { ReactNode }
