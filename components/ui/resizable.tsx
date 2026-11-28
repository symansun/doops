"use client"

import { Group, Panel, Separator } from "react-resizable-panels"
import type { ComponentProps, ReactNode } from "react"
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

export type { ReactNode }
