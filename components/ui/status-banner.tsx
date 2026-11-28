import { cva, type VariantProps } from "class-variance-authority"
import { forwardRef, type HTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export const statusBannerVariants = cva(
  "rounded-md border px-3 py-2 text-sm leading-relaxed",
  {
    variants: {
      tone: {
        error: "border-destructive/30 bg-destructive/12 text-destructive",
        warning: "border-warning/35 bg-warning/12 text-warning",
        success: "border-success/30 bg-success/12 text-success",
        info: "border-border bg-muted/55 text-muted-foreground",
      },
      compact: {
        true: "px-2.5 py-1.5 text-xs",
        false: "",
      },
    },
    defaultVariants: {
      tone: "info",
      compact: false,
    },
  },
)

export type StatusTone = NonNullable<VariantProps<typeof statusBannerVariants>["tone"]>

export interface StatusBannerProps
  extends HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof statusBannerVariants> {}

export const StatusBanner = forwardRef<HTMLDivElement, StatusBannerProps>(function StatusBanner(
  { tone = "info", compact = false, role, className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="status-banner"
      role={role ?? (tone === "error" ? "alert" : "status")}
      className={cn(statusBannerVariants({ tone, compact }), className)}
      {...props}
    />
  )
})
