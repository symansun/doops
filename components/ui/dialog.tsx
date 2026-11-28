"use client"

import * as DialogPrimitive from "@radix-ui/react-dialog"
import { forwardRef, type ComponentPropsWithoutRef, type ElementRef, type HTMLAttributes, type ReactNode } from "react"
import { cn } from "@/lib/cn"

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogPortal = DialogPrimitive.Portal
export const DialogClose = DialogPrimitive.Close

export type DialogSize = "sm" | "md" | "lg" | "xl"

const sizeClass: Record<DialogSize, string> = {
  sm: "max-w-md",
  md: "max-w-lg",
  lg: "max-w-xl",
  xl: "max-w-3xl",
}

export const DialogOverlay = forwardRef<
  ElementRef<typeof DialogPrimitive.Overlay>,
  ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(function DialogOverlay({ className, ...props }, ref) {
  return (
    <DialogPrimitive.Overlay
      ref={ref}
      className={cn(
        "fixed inset-0 z-50 bg-black/65 backdrop-blur-xs",
        "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
        className,
      )}
      {...props}
    />
  )
})

export interface DialogContentProps extends ComponentPropsWithoutRef<typeof DialogPrimitive.Content> {
  size?: DialogSize
}

export const DialogContent = forwardRef<
  ElementRef<typeof DialogPrimitive.Content>,
  DialogContentProps
>(function DialogContent({ className, size = "lg", children, ...props }, ref) {
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        ref={ref}
        className={cn(
          "fixed left-1/2 top-1/2 z-50 flex max-h-[min(88dvh,48rem)] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border bg-card text-card-foreground shadow-xl outline-none",
          "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
          sizeClass[size],
          className,
        )}
        {...props}
      >
        {children}
      </DialogPrimitive.Content>
    </DialogPortal>
  )
})

export const DialogHeader = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function DialogHeader(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      className={cn("flex shrink-0 flex-wrap items-start justify-between gap-3 border-b px-4 py-3", className)}
      {...props}
    />
  )
})

export const DialogTitle = forwardRef<
  ElementRef<typeof DialogPrimitive.Title>,
  ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(function DialogTitle({ className, ...props }, ref) {
  return (
    <DialogPrimitive.Title
      ref={ref}
      className={cn("text-sm font-semibold text-card-foreground", className)}
      {...props}
    />
  )
})

export const DialogDescription = forwardRef<
  ElementRef<typeof DialogPrimitive.Description>,
  ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(function DialogDescription({ className, ...props }, ref) {
  return (
    <DialogPrimitive.Description
      ref={ref}
      className={cn("text-xs text-muted-foreground", className)}
      {...props}
    />
  )
})

export const DialogBody = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function DialogBody(
  { className, ...props },
  ref,
) {
  return <div ref={ref} className={cn("min-h-0 flex-1 overflow-y-auto p-4", className)} {...props} />
})

export const DialogFooter = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function DialogFooter(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      className={cn("flex shrink-0 flex-wrap items-center justify-end gap-2 border-t px-4 py-3", className)}
      {...props}
    />
  )
})

export interface ModalProps {
  open?: boolean
  onClose: () => void
  title: ReactNode
  description?: ReactNode
  headerExtra?: ReactNode
  footer?: ReactNode
  size?: DialogSize
  hideCloseButton?: boolean
  dismissOnOutsideClick?: boolean
  contentClassName?: string
  bodyClassName?: string
  titleClassName?: string
  onOpenAutoFocus?: DialogContentProps["onOpenAutoFocus"]
  "data-testid"?: string
  children: ReactNode
}

export function Modal({
  open = true,
  onClose,
  title,
  description,
  headerExtra,
  footer,
  size = "lg",
  hideCloseButton = false,
  dismissOnOutsideClick = true,
  contentClassName,
  bodyClassName,
  titleClassName,
  onOpenAutoFocus,
  "data-testid": testId,
  children,
}: ModalProps) {
  const descProps = description ? {} : { "aria-describedby": undefined }
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent
        size={size}
        data-testid={testId}
        {...descProps}
        onOpenAutoFocus={onOpenAutoFocus}
        onInteractOutside={(event) => {
          if (!dismissOnOutsideClick) event.preventDefault()
        }}
        className={contentClassName}
      >
        <DialogHeader>
          <div className="min-w-0 space-y-1">
            <DialogTitle className={titleClassName}>{title}</DialogTitle>
            {description ? <DialogDescription>{description}</DialogDescription> : null}
          </div>
          {(headerExtra || !hideCloseButton) && (
            <div className="flex shrink-0 items-center gap-2">
              {headerExtra}
              {!hideCloseButton && (
                <button type="button" onClick={onClose} className="btn btn-ghost btn-sm">
                  关闭
                </button>
              )}
            </div>
          )}
        </DialogHeader>
        <DialogBody className={bodyClassName}>{children}</DialogBody>
        {footer ? <DialogFooter>{footer}</DialogFooter> : null}
      </DialogContent>
    </Dialog>
  )
}
