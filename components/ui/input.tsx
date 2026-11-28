import { cva, type VariantProps } from "class-variance-authority"
import { forwardRef, type InputHTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export const inputVariants = cva(
  [
    "min-w-0 rounded-md border border-input bg-background text-foreground shadow-xs transition-colors",
    "placeholder:text-muted-foreground",
    "focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
    "disabled:cursor-not-allowed disabled:opacity-50",
    "aria-[invalid=true]:border-destructive aria-[invalid=true]:focus-visible:ring-destructive/40",
  ].join(" "),
  {
    variants: {
      scale: {
        default: "min-h-9 px-3 py-1.5 text-sm",
        sm: "min-h-8 px-2.5 py-1 text-xs",
      },
    },
    defaultVariants: {
      scale: "default",
    },
  },
)

export type InputProps = InputHTMLAttributes<HTMLInputElement> & VariantProps<typeof inputVariants>

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { scale = "default", className, ...props },
  ref,
) {
  return (
    <input
      ref={ref}
      data-slot="input"
      className={cn(inputVariants({ scale }), className)}
      {...props}
    />
  )
})
