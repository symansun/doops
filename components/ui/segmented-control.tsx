import type { ReactNode } from "react"
import { cn } from "@/lib/cn"

export type SegmentedOption<T extends string> = {
  value: T
  label: ReactNode
  icon?: ReactNode
}

export function SegmentedControl<T extends string>({
  value,
  options,
  onChange,
  disabled,
  ariaLabel,
  "aria-label": ariaLabelAttr,
  className,
}: {
  value: T
  options: readonly SegmentedOption<T>[]
  onChange: (value: T) => void
  disabled?: boolean
  ariaLabel?: string
  "aria-label"?: string
  className?: string
}) {
  return (
    <div
      data-slot="segmented-control"
      className={cn(
        "inline-flex items-center gap-0.5 rounded-lg border border-border bg-background p-0.5",
        className,
      )}
      role="group"
      aria-label={ariaLabel ?? ariaLabelAttr}
    >
      {options.map((option) => {
        const active = value === option.value
        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={active}
            disabled={disabled}
            className={cn(
              "inline-flex min-h-8 shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-sm px-2.5 py-1 text-xs font-medium transition-colors",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background",
              "disabled:cursor-not-allowed disabled:opacity-50",
              active
                ? "bg-primary text-primary-foreground shadow-xs"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
            onClick={() => onChange(option.value)}
          >
            {option.icon}
            {option.label}
          </button>
        )
      })}
    </div>
  )
}
