import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"
import { forwardRef, type ButtonHTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export const buttonVariants = cva(
  [
    "inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium transition-colors",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
    "disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
    "[&_svg]:pointer-events-none [&_svg]:shrink-0",
  ].join(" "),
  {
    variants: {
      variant: {
        default: "border-border bg-background text-foreground hover:bg-muted",
        primary: "border-transparent bg-primary text-primary-foreground shadow-xs hover:bg-primary/90",
        secondary: "border-border/70 bg-secondary text-secondary-foreground hover:bg-secondary/80",
        outline: "border-border bg-transparent text-foreground hover:bg-muted",
        danger: "border-transparent bg-destructive text-destructive-foreground shadow-xs hover:bg-destructive/90",
        "danger-outline": "border-destructive/55 bg-transparent text-destructive hover:border-destructive/75 hover:bg-destructive/12",
        ghost: "border-transparent bg-transparent text-muted-foreground hover:bg-muted hover:text-foreground",
        link: "border-transparent bg-transparent text-primary underline-offset-4 hover:underline",
      },
      size: {
        default: "min-h-9 px-3 py-1.5 text-sm",
        sm: "min-h-8 px-2.5 py-1 text-xs",
        md: "min-h-10 rounded-full px-5 py-2 text-sm",
        lg: "min-h-11 rounded-full px-6 py-2.5 text-base",
        icon: "size-9 p-0",
        "icon-sm": "size-8 p-0",
        "icon-xs": "size-6 p-0",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
)

export type ButtonVariant = NonNullable<VariantProps<typeof buttonVariants>["variant"]>
export type ButtonSize = NonNullable<VariantProps<typeof buttonVariants>["size"]>

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
  icon?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "default", size = "default", icon = false, asChild = false, className, ...props },
  ref,
) {
  const Comp = asChild ? Slot : "button"
  const resolvedSize: ButtonSize = icon
    ? size === "sm" || size === "icon-sm"
      ? "icon-sm"
      : "icon"
    : (size ?? "default")
  return (
    <Comp
      ref={ref}
      data-slot="button"
      className={cn(buttonVariants({ variant, size: resolvedSize }), className)}
      {...props}
    />
  )
})
