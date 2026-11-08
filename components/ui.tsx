import {
  forwardRef,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react"

type ControlSize = "default" | "sm"

function join(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(" ")
}

export type ButtonVariant = "default" | "primary" | "danger" | "danger-outline" | "ghost"

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant
  size?: ControlSize
  icon?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "default", size = "default", icon = false, className, ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      className={join(
        "btn",
        size === "sm" && "btn-sm",
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
  return <input ref={ref} className={join("input", scale === "sm" && "input-sm", className)} {...props} />
})

export type SelectProps = SelectHTMLAttributes<HTMLSelectElement> & {
  scale?: ControlSize
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  { scale = "default", className, ...props },
  ref,
) {
  return <select ref={ref} className={join("input", scale === "sm" && "input-sm", className)} {...props} />
})

export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & {
  scale?: ControlSize
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { scale = "default", className, ...props },
  ref,
) {
  return <textarea ref={ref} className={join("input", scale === "sm" && "input-sm", className)} {...props} />
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
    <div className={join("segmented", className)} role="group" aria-label={ariaLabel}>
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
          disabled={disabled}
          aria-current={value === item.id ? "page" : undefined}
          className="page-tab"
        >
          {item.icon}
          {item.label}
        </button>
      ))}
    </nav>
  )
}
