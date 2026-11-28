import { forwardRef, type TextareaHTMLAttributes } from "react"
import { type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/cn"
import { inputVariants } from "./input"

export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & VariantProps<typeof inputVariants>

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { scale = "default", className, ...props },
  ref,
) {
  return (
    <textarea
      ref={ref}
      data-slot="textarea"
      className={cn(inputVariants({ scale }), "min-h-20 py-2", className)}
      {...props}
    />
  )
})
