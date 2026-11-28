import type { ReactNode } from "react"
import { cn } from "@/lib/cn"

export type PageTabItem<T extends string> = {
  id: T
  label: ReactNode
  icon?: ReactNode
  secondary?: boolean
  title?: string
  disabled?: boolean
}

export function PageTabs<T extends string>({
  items,
  value,
  onChange,
  disabled,
  ariaLabel,
  className,
}: {
  items: readonly PageTabItem<T>[]
  value: T
  onChange: (value: T) => void
  disabled?: boolean
  ariaLabel?: string
  className?: string
}) {
  return (
    <nav
      data-slot="page-tabs"
      className={cn("flex shrink-0 items-center gap-1 overflow-x-auto border-b border-border bg-card px-4", className)}
      aria-label={ariaLabel}
    >
      {items.map((item) => {
        const active = value === item.id
        return (
          <button
            key={item.id}
            type="button"
            onClick={() => onChange(item.id)}
            disabled={disabled || item.disabled}
            aria-current={active ? "page" : undefined}
            title={item.title}
            className={cn(
              "-mb-px inline-flex min-h-11 shrink-0 cursor-pointer items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium transition-colors",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
              "disabled:cursor-not-allowed disabled:opacity-50",
              active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
              item.secondary && "ml-auto",
            )}
          >
            {item.icon}
            {item.label}
          </button>
        )
      })}
    </nav>
  )
}
