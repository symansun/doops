import { forwardRef, type ElementType, type HTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export interface PortalContainerProps extends HTMLAttributes<HTMLDivElement> {
  as?: ElementType
}

/**
 * Unified outer width rail shared across every public portal page:
 * Header, Home sections, Cases, Docs shell, Blog layout, and Footer.
 * Pure Tailwind responsive rem scale (max-w-7xl = 80rem, px-4 / sm:px-6 / lg:px-8).
 */
export const PortalContainer = forwardRef<HTMLDivElement, PortalContainerProps>(function PortalContainer(
  { as: Tag = "div", className, ...props },
  ref,
) {
  return (
    <Tag
      ref={ref}
      data-slot="portal-container"
      className={cn("mx-auto w-full max-w-7xl px-4 sm:px-6 lg:px-8", className)}
      {...props}
    />
  )
})

export interface PortalFrameProps extends HTMLAttributes<HTMLDivElement> {
  as?: ElementType
}

export const PortalFrame = forwardRef<HTMLDivElement, PortalFrameProps>(function PortalFrame(
  { as: Tag = "div", className, ...props },
  ref,
) {
  return (
    <Tag
      ref={ref}
      data-slot="portal-frame"
      className={cn("portal-frame", className)}
      {...props}
    />
  )
})
