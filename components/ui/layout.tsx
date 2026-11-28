import { forwardRef, type ElementType, type HTMLAttributes } from "react"
import { cn } from "@/lib/cn"

// Layout primitives. These are the style framework's building blocks: a page
// composes Stack / Inline / Grid / Container instead of repeating flex and gap
// utility strings, so spacing and responsive behaviour stay consistent.
export type Space = 0 | 1 | 2 | 3 | 4 | 5 | 6 | 8

const gapClass: Record<Space, string> = {
  0: "gap-0",
  1: "gap-1",
  2: "gap-2",
  3: "gap-3",
  4: "gap-4",
  5: "gap-5",
  6: "gap-6",
  8: "gap-8",
}

export interface StackProps extends HTMLAttributes<HTMLDivElement> {
  gap?: Space
  as?: ElementType
}

// Vertical rhythm: the default for grouped content.
export const Stack = forwardRef<HTMLDivElement, StackProps>(function Stack(
  { gap = 3, as: Tag = "div", className, ...props }, ref,
) {
  return <Tag ref={ref} className={cn("flex min-w-0 flex-col", gapClass[gap], className)} {...props} />
})

export interface InlineProps extends HTMLAttributes<HTMLDivElement> {
  gap?: Space
  wrap?: boolean
  align?: "start" | "center" | "end" | "baseline" | "stretch"
  justify?: "start" | "center" | "end" | "between"
  as?: ElementType
}

const alignClass = { start: "items-start", center: "items-center", end: "items-end", baseline: "items-baseline", stretch: "items-stretch" } as const
const justifyClass = { start: "justify-start", center: "justify-center", end: "justify-end", between: "justify-between" } as const

// Horizontal grouping: toolbars, metadata rows, form actions.
export const Inline = forwardRef<HTMLDivElement, InlineProps>(function Inline(
  { gap = 2, wrap = false, align = "center", justify = "start", as: Tag = "div", className, ...props }, ref,
) {
  return (
    <Tag
      ref={ref}
      className={cn("flex min-w-0", gapClass[gap], alignClass[align], justifyClass[justify], wrap && "flex-wrap", className)}
      {...props}
    />
  )
})

export interface GridProps extends HTMLAttributes<HTMLDivElement> {
  gap?: Space
  // Column count per breakpoint. "auto" fits as many as the width allows.
  cols?: 1 | 2 | 3 | 4
  smCols?: 1 | 2 | 3 | 4
  lgCols?: 1 | 2 | 3 | 4
  auto?: boolean
  as?: ElementType
}

const colClass = { 1: "grid-cols-1", 2: "grid-cols-2", 3: "grid-cols-3", 4: "grid-cols-4" } as const
const smColClass = { 1: "sm:grid-cols-1", 2: "sm:grid-cols-2", 3: "sm:grid-cols-3", 4: "sm:grid-cols-4" } as const
const lgColClass = { 1: "lg:grid-cols-1", 2: "lg:grid-cols-2", 3: "lg:grid-cols-3", 4: "lg:grid-cols-4" } as const

// Card walls. Prefer this over hand-written grid-cols-* so a page keeps one
// rhythm when the window narrows.
export const Grid = forwardRef<HTMLDivElement, GridProps>(function Grid(
  { gap = 3, cols = 1, smCols, lgCols, auto = false, as: Tag = "div", className, ...props }, ref,
) {
  return (
    <Tag
      ref={ref}
      className={cn(
        "grid min-w-0",
        gapClass[gap],
        auto ? "grid-cols-[repeat(auto-fill,minmax(14rem,1fr))]" : colClass[cols],
        !auto && smCols && smColClass[smCols],
        !auto && lgCols && lgColClass[lgCols],
        className,
      )}
      {...props}
    />
  )
})

export interface ContainerProps extends HTMLAttributes<HTMLDivElement> {
  width?: "full" | "wide" | "prose"
  padded?: boolean
  as?: ElementType
}

const widthClass = { full: "max-w-none", wide: "max-w-7xl", prose: "max-w-3xl" } as const

export const Container = forwardRef<HTMLDivElement, ContainerProps>(function Container(
  { width = "full", padded = true, as: Tag = "div", className, ...props }, ref,
) {
  return (
    <Tag
      ref={ref}
      className={cn("mx-auto w-full min-w-0", widthClass[width], padded && "px-4 py-4", className)}
      {...props}
    />
  )
})

// Pushes the following sibling to the far edge of an Inline.
export function Spacer({ className }: { className?: string }) {
  return <span aria-hidden="true" className={cn("flex-1", className)} />
}
