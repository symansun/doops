"use client"

import * as DialogPrimitive from "@radix-ui/react-dialog"
import { cva, type VariantProps } from "class-variance-authority"
import { X } from "lucide-react"
import { forwardRef, type ComponentPropsWithoutRef, type ElementRef, type HTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export const Sheet = DialogPrimitive.Root
export const SheetTrigger = DialogPrimitive.Trigger
export const SheetClose = DialogPrimitive.Close
export const SheetPortal = DialogPrimitive.Portal

export const SheetOverlay = forwardRef<
  ElementRef<typeof DialogPrimitive.Overlay>,
  ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(function SheetOverlay({ className, ...props }, ref) {
  return (
    <DialogPrimitive.Overlay
      ref={ref}
      data-slot="sheet-overlay"
      className={cn(
        "fixed inset-0 z-50 bg-black/65 backdrop-blur-xs",
        "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
        className,
      )}
      {...props}
    />
  )
})

const sheetVariants = cva(
  [
    "fixed z-50 flex flex-col bg-card text-card-foreground shadow-xl outline-none transition ease-in-out",
    "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:duration-200 data-[state=open]:duration-300",
  ].join(" "),
  {
    variants: {
      side: {
        left: "inset-y-0 left-0 h-dvh w-4/5 max-w-xs border-r border-border data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left",
        right: "inset-y-0 right-0 h-dvh w-4/5 max-w-md border-l border-border data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right",
        top: "inset-x-0 top-0 max-h-dvh border-b border-border data-[state=closed]:slide-out-to-top data-[state=open]:slide-in-from-top",
        bottom: "inset-x-0 bottom-0 max-h-dvh border-t border-border data-[state=closed]:slide-out-to-bottom data-[state=open]:slide-in-from-bottom",
      },
    },
    defaultVariants: {
      side: "right",
    },
  },
)

export interface SheetContentProps
  extends ComponentPropsWithoutRef<typeof DialogPrimitive.Content>,
    VariantProps<typeof sheetVariants> {
  hideCloseButton?: boolean
}

export const SheetContent = forwardRef<
  ElementRef<typeof DialogPrimitive.Content>,
  SheetContentProps
>(function SheetContent({ side = "right", hideCloseButton = false, className, children, ...props }, ref) {
  return (
    <SheetPortal>
      <SheetOverlay />
      <DialogPrimitive.Content
        ref={ref}
        data-slot="sheet-content"
        className={cn(sheetVariants({ side }), className)}
        {...props}
      >
        {children}
        {!hideCloseButton && (
          <DialogPrimitive.Close
            aria-label="关闭抽屉"
            className="absolute right-3 top-3 inline-flex size-8 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X className="size-4" aria-hidden="true" />
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </SheetPortal>
  )
})

export const SheetHeader = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function SheetHeader(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="sheet-header"
      className={cn("flex shrink-0 flex-col gap-1 border-b border-border px-4 py-3", className)}
      {...props}
    />
  )
})

export const SheetTitle = forwardRef<
  ElementRef<typeof DialogPrimitive.Title>,
  ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(function SheetTitle({ className, ...props }, ref) {
  return (
    <DialogPrimitive.Title
      ref={ref}
      data-slot="sheet-title"
      className={cn("text-sm font-semibold text-foreground", className)}
      {...props}
    />
  )
})

export const SheetDescription = forwardRef<
  ElementRef<typeof DialogPrimitive.Description>,
  ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(function SheetDescription({ className, ...props }, ref) {
  return (
    <DialogPrimitive.Description
      ref={ref}
      data-slot="sheet-description"
      className={cn("text-xs text-muted-foreground", className)}
      {...props}
    />
  )
})

export const SheetBody = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function SheetBody(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="sheet-body"
      className={cn("min-h-0 flex-1 overflow-y-auto p-4", className)}
      {...props}
    />
  )
})

export const SheetFooter = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function SheetFooter(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="sheet-footer"
      className={cn("flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-border px-4 py-3", className)}
      {...props}
    />
  )
})
