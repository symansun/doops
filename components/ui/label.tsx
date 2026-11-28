"use client"

import * as LabelPrimitive from "@radix-ui/react-label"
import { forwardRef, type ComponentPropsWithoutRef, type ElementRef } from "react"
import { cn } from "@/lib/cn"

export const Label = forwardRef<
  ElementRef<typeof LabelPrimitive.Root>,
  ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(function Label({ className, ...props }, ref) {
  return (
    <LabelPrimitive.Root
      ref={ref}
      data-slot="label"
      className={cn(
        "text-sm font-medium leading-none text-foreground peer-disabled:cursor-not-allowed peer-disabled:opacity-50",
        className,
      )}
      {...props}
    />
  )
})
