import {
  forwardRef,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type TextareaHTMLAttributes,
} from "react"
import { cn } from "@/lib/cn"

type ControlSize = "default" | "sm"

export type ButtonSize = "default" | "sm" | "md" | "lg"
export type ButtonVariant = "default" | "primary" | "danger" | "danger-outline" | "ghost"

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "default", size = "default", icon = false, className, ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      className={cn(
        "btn",
        size === "sm" && "btn-sm",
        size === "md" && "btn-md",
        size === "lg" && "btn-lg",
        icon && "btn-icon",
        variant === "primary" && "btn-primary",
        variant === "danger" && "btn-danger",
        variant === "danger-outline" && "btn-danger-outline",
        variant === "ghost" && "btn-ghost",
        className,
      )}
      {...props}
    />
  )
})

export type InputProps = InputHTMLAttributes<HTMLInputElement> & {
  scale?: ControlSize
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { scale = "default", className, ...props },
  ref,
) {
  return <input ref={ref} className={cn("input", scale === "sm" && "input-sm", className)} {...props} />
})

export { Select, type SelectProps } from "./select"

export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & {
  scale?: ControlSize
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { scale = "default", className, ...props },
  ref,
) {
  return <textarea ref={ref} className={cn("input", scale === "sm" && "input-sm", className)} {...props} />
})

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
  className,
}: {
  value: T
  options: readonly SegmentedOption<T>[]
  onChange: (value: T) => void
  disabled?: boolean
  ariaLabel?: string
  className?: string
}) {
  return (
    <div className={cn("segmented", className)} role="group" aria-label={ariaLabel}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          disabled={disabled}
          className="segment-option"
          onClick={() => onChange(option.value)}
        >
          {option.icon}
          {option.label}
        </button>
      ))}
    </div>
  )
}

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
}: {
  items: readonly PageTabItem<T>[]
  value: T
  onChange: (value: T) => void
  disabled?: boolean
  ariaLabel?: string
}) {
  return (
    <nav className="flex shrink-0 gap-1 overflow-x-auto border-b bg-card px-4" aria-label={ariaLabel}>
      {items.map((item) => (
        <button
          key={item.id}
          type="button"
          onClick={() => onChange(item.id)}
          disabled={disabled || item.disabled}
          aria-current={value === item.id ? "page" : undefined}
          title={item.title}
          className={cn("page-tab", item.secondary && "ml-auto")}
        >
          {item.icon}
          {item.label}
        </button>
      ))}
    </nav>
  )
}

export { Badge, type BadgeProps } from "./ui/badge"
export { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from "./ui/card"
export { Checkbox, type CheckboxProps } from "./ui/checkbox"
export {
  Dialog,
  DialogTrigger,
  DialogPortal,
  DialogClose,
  DialogOverlay,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogBody,
  DialogFooter,
  Modal,
  type DialogSize,
  type DialogContentProps,
  type ModalProps,
} from "./ui/dialog"
export { Stack, Inline, Grid, Container, Spacer, type Space, type StackProps, type InlineProps, type GridProps, type ContainerProps } from "./ui/layout"
export {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuPortal,
  DropdownMenuGroup,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuLabel,
} from "./ui/menu"
export { ResizablePanelGroup, ResizablePanel, ResizableHandle, ResizableVHandle } from "./ui/resizable"
export { ScrollArea } from "./ui/scroll-area"
export { Separator } from "./ui/separator"
export { StatusBanner, type StatusTone, type StatusBannerProps } from "./ui/status-banner"
export { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, type TableProps } from "./ui/table"
export { Tabs, TabsList, TabsTrigger, TabsContent } from "./ui/tabs"
export { TooltipProvider, Tooltip, TooltipTrigger, TooltipContent } from "./ui/tooltip"
