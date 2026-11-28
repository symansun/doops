import { forwardRef, type HTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export type StatusTone = "error" | "warning" | "success" | "info"

const toneClass: Record<StatusTone, string> = {
  error: "status-error",
  warning: "status-warning",
  success: "status-success",
  info: "status-info",
}

export interface StatusBannerProps extends HTMLAttributes<HTMLDivElement> {
  tone?: StatusTone
  compact?: boolean
}

export const StatusBanner = forwardRef<HTMLDivElement, StatusBannerProps>(function StatusBanner(
  { tone = "info", compact = false, role, className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      role={role ?? (tone === "error" ? "alert" : "status")}
      className={cn(toneClass[tone], compact && "px-3 py-2 text-xs", className)}
      {...props}
    />
  )
})
