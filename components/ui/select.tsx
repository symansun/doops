"use client"

import * as SelectPrimitive from "@radix-ui/react-select"
import { Check, ChevronDown, ChevronUp } from "lucide-react"
import {
  Children,
  Fragment,
  forwardRef,
  isValidElement,
  useCallback,
  useMemo,
  useRef,
  useState,
  type ComponentPropsWithoutRef,
  type ElementRef,
  type ReactElement,
  type ReactNode,
  type SelectHTMLAttributes,
} from "react"
import { cn } from "@/lib/cn"

export const SelectRoot = SelectPrimitive.Root
export const SelectGroup = SelectPrimitive.Group
export const SelectValue = SelectPrimitive.Value

export const SelectTrigger = forwardRef<
  ElementRef<typeof SelectPrimitive.Trigger>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Trigger> & { scale?: "default" | "sm" }
>(function SelectTrigger({ className, scale = "default", children, ...props }, ref) {
  return (
    <SelectPrimitive.Trigger
      ref={ref}
      data-slot="select-trigger"
      className={cn(
        "inline-flex w-full min-w-0 cursor-pointer items-center justify-between gap-2 rounded-md border border-input bg-background text-left text-foreground shadow-xs transition-colors",
        "focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        "disabled:cursor-not-allowed disabled:opacity-50",
        "aria-[invalid=true]:border-destructive",
        "[&>span]:truncate",
        scale === "sm" ? "min-h-8 px-2.5 py-1 text-xs" : "min-h-9 px-3 py-1.5 text-sm",
        className,
      )}
      {...props}
    >
      {children}
      <SelectPrimitive.Icon asChild>
        <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  )
})

export const SelectScrollUpButton = forwardRef<
  ElementRef<typeof SelectPrimitive.ScrollUpButton>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.ScrollUpButton>
>(function SelectScrollUpButton({ className, ...props }, ref) {
  return (
    <SelectPrimitive.ScrollUpButton
      ref={ref}
      className={cn("flex cursor-default items-center justify-center py-1 text-muted-foreground", className)}
      {...props}
    >
      <ChevronUp className="size-3.5" aria-hidden="true" />
    </SelectPrimitive.ScrollUpButton>
  )
})

export const SelectScrollDownButton = forwardRef<
  ElementRef<typeof SelectPrimitive.ScrollDownButton>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.ScrollDownButton>
>(function SelectScrollDownButton({ className, ...props }, ref) {
  return (
    <SelectPrimitive.ScrollDownButton
      ref={ref}
      className={cn("flex cursor-default items-center justify-center py-1 text-muted-foreground", className)}
      {...props}
    >
      <ChevronDown className="size-3.5" aria-hidden="true" />
    </SelectPrimitive.ScrollDownButton>
  )
})

export const SelectContent = forwardRef<
  ElementRef<typeof SelectPrimitive.Content>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Content>
>(function SelectContent({ className, children, position = "popper", ...props }, ref) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        ref={ref}
        data-slot="select-content"
        position={position}
        className={cn(
          "relative z-50 max-h-80 min-w-32 overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-lg",
          "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
          position === "popper" &&
            "data-[side=bottom]:translate-y-1 data-[side=left]:-translate-x-1 data-[side=right]:translate-x-1 data-[side=top]:-translate-y-1",
          className,
        )}
        {...props}
      >
        <SelectScrollUpButton />
        <SelectPrimitive.Viewport
          className={cn(
            "p-1",
            position === "popper" &&
              "h-[var(--radix-select-trigger-height)] w-full min-w-[var(--radix-select-trigger-width)]",
          )}
        >
          {children}
        </SelectPrimitive.Viewport>
        <SelectScrollDownButton />
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  )
})

export const SelectLabel = forwardRef<
  ElementRef<typeof SelectPrimitive.Label>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Label>
>(function SelectLabel({ className, ...props }, ref) {
  return (
    <SelectPrimitive.Label
      ref={ref}
      className={cn("px-2 py-1.5 text-xs font-medium text-muted-foreground", className)}
      {...props}
    />
  )
})

export const SelectItem = forwardRef<
  ElementRef<typeof SelectPrimitive.Item>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Item>
>(function SelectItem({ className, children, ...props }, ref) {
  return (
    <SelectPrimitive.Item
      ref={ref}
      data-slot="select-item"
      className={cn(
        "relative flex w-full cursor-pointer select-none items-center gap-2 rounded-sm py-1.5 pl-2.5 pr-8 text-sm outline-none transition-colors",
        "data-[highlighted]:bg-muted data-[highlighted]:text-foreground",
        "data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
        className,
      )}
      {...props}
    >
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
      <span className="absolute right-2 flex size-3.5 items-center justify-center">
        <SelectPrimitive.ItemIndicator>
          <Check className="size-3.5 text-primary" aria-hidden="true" />
        </SelectPrimitive.ItemIndicator>
      </span>
    </SelectPrimitive.Item>
  )
})

export const SelectSeparator = forwardRef<
  ElementRef<typeof SelectPrimitive.Separator>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Separator>
>(function SelectSeparator({ className, ...props }, ref) {
  return <SelectPrimitive.Separator ref={ref} className={cn("-mx-1 my-1 h-px bg-border", className)} {...props} />
})

/* -------------------------------------------------------------------------- */
/* Drop-in <Select> adapter backed by Radix Select + synced native <select>.  */
/* Replaces hand-computed pixel popup positioning while preserving standard   */
/* <option>/<optgroup> children, FormData, required validation & FormAssist.  */
/* -------------------------------------------------------------------------- */

const EMPTY_SENTINEL = "__doops_select_empty__"

type ParsedOption = {
  kind: "option"
  value: string
  label: string
  disabled: boolean
  key: string
}

type ParsedGroup = {
  kind: "group"
  label: string
  options: ParsedOption[]
  key: string
}

type ParsedNode = ParsedOption | ParsedGroup

function extractText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node)
  if (Array.isArray(node)) return node.map(extractText).join("")
  if (isValidElement<{ children?: ReactNode }>(node)) return extractText(node.props.children)
  return ""
}

function parseSelectChildren(children: ReactNode, prefix = ""): ParsedNode[] {
  const result: ParsedNode[] = []
  let idx = 0
  Children.forEach(children, (child) => {
    if (!isValidElement(child)) return
    const el = child as ReactElement<{
      value?: string | number
      disabled?: boolean
      label?: string
      children?: ReactNode
    }>
    const key = `${prefix}${el.key ?? idx++}`
    if (el.type === Fragment) {
      result.push(...parseSelectChildren(el.props.children, `${key}-`))
      return
    }
    if (el.type === "optgroup") {
      const groupOptions = parseSelectChildren(el.props.children, `${key}-`).filter(
        (n): n is ParsedOption => n.kind === "option",
      )
      result.push({
        kind: "group",
        label: el.props.label ?? "",
        options: groupOptions,
        key,
      })
      return
    }
    if (el.type === "option") {
      const label = el.props.label ?? extractText(el.props.children)
      const rawValue = el.props.value !== undefined ? String(el.props.value) : label
      result.push({
        kind: "option",
        value: rawValue,
        label,
        disabled: Boolean(el.props.disabled),
        key,
      })
    }
  })
  return result
}

function encodeValue(val: string): string {
  return val === "" ? EMPTY_SENTINEL : val
}

function decodeValue(val: string): string {
  return val === EMPTY_SENTINEL ? "" : val
}

export type SelectProps = Omit<SelectHTMLAttributes<HTMLSelectElement>, "multiple" | "size"> & {
  scale?: "default" | "sm"
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  {
    children,
    className,
    scale = "default",
    id,
    title,
    disabled,
    required,
    value,
    defaultValue,
    onChange,
    "aria-label": ariaLabel,
    "aria-labelledby": ariaLabelledBy,
    "aria-describedby": ariaDescribedBy,
    "aria-invalid": ariaInvalid,
    ...fieldProps
  },
  ref,
) {
  const nativeRef = useRef<HTMLSelectElement | null>(null)
  const parsed = useMemo(() => parseSelectChildren(children), [children])
  const flatOptions = useMemo(
    () => parsed.flatMap((node) => (node.kind === "option" ? [node] : node.options)),
    [parsed],
  )

  const controlled = value !== undefined
  const initialUncontrolled = useMemo(() => {
    if (defaultValue !== undefined) return String(defaultValue)
    return flatOptions[0]?.value ?? ""
  }, [defaultValue, flatOptions])
  const [uncontrolledValue, setUncontrolledValue] = useState(initialUncontrolled)

  const currentValue = controlled ? String(value) : uncontrolledValue
  const encodedCurrent = flatOptions.some((o) => o.value === currentValue)
    ? encodeValue(currentValue)
    : undefined

  const assignNativeRef = useCallback(
    (node: HTMLSelectElement | null) => {
      nativeRef.current = node
      if (typeof ref === "function") ref(node)
      else if (ref) ref.current = node
    },
    [ref],
  )

  const handleRadixChange = useCallback(
    (nextEncoded: string) => {
      const next = decodeValue(nextEncoded)
      if (!controlled) setUncontrolledValue(next)
      const selectEl = nativeRef.current
      if (selectEl) {
        selectEl.value = next
        selectEl.dispatchEvent(new Event("change", { bubbles: true }))
      } else if (onChange) {
        const synthetic = {
          target: { value: next, name: fieldProps.name ?? "" },
          currentTarget: { value: next, name: fieldProps.name ?? "" },
        } as unknown as React.ChangeEvent<HTMLSelectElement>
        onChange(synthetic)
      }
    },
    [controlled, fieldProps.name, onChange],
  )

  return (
    <span data-slot="select-field" className={cn("relative inline-flex min-w-0", className)}>
      <select
        ref={assignNativeRef}
        id={id ? `${id}-native` : undefined}
        aria-hidden="true"
        tabIndex={-1}
        disabled={disabled}
        required={required}
        value={controlled ? currentValue : undefined}
        defaultValue={controlled ? undefined : initialUncontrolled}
        onChange={(event) => {
          if (!controlled) setUncontrolledValue(event.target.value)
          onChange?.(event)
        }}
        className="pointer-events-none absolute inset-0 size-0 opacity-0"
        {...fieldProps}
      >
        {children}
      </select>

      <SelectRoot
        value={encodedCurrent}
        disabled={disabled}
        onValueChange={handleRadixChange}
      >
        <SelectTrigger
          id={id}
          title={title}
          scale={scale}
          aria-label={ariaLabel}
          aria-labelledby={ariaLabelledBy}
          aria-describedby={ariaDescribedBy}
          aria-invalid={ariaInvalid}
          className="w-full"
        >
          <SelectValue placeholder={flatOptions.find((o) => o.value === currentValue)?.label ?? ""} />
        </SelectTrigger>
        <SelectContent>
          {parsed.map((node) => {
            if (node.kind === "group") {
              return (
                <SelectGroup key={node.key}>
                  {node.label && <SelectLabel>{node.label}</SelectLabel>}
                  {node.options.map((opt) => (
                    <SelectItem
                      key={opt.key}
                      value={encodeValue(opt.value)}
                      disabled={opt.disabled}
                    >
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              )
            }
            return (
              <SelectItem
                key={node.key}
                value={encodeValue(node.value)}
                disabled={node.disabled}
              >
                {node.label}
              </SelectItem>
            )
          })}
        </SelectContent>
      </SelectRoot>
    </span>
  )
})
