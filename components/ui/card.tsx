import { forwardRef, type ElementType, type HTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  as?: ElementType
}

// shadcn/ui card surface. The console already themes card/foreground through
// app/globals.css, so these classes only bind spacing and the border radius.
export const Card = forwardRef<HTMLDivElement, CardProps>(function Card(
  { as: Tag = "div", className, ...props }, ref,
) {
  return <Tag ref={ref} className={cn("rounded-lg border bg-card text-card-foreground shadow-sm", className)} {...props} />
})

export const CardHeader = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function CardHeader(
  { className, ...props }, ref,
) {
  return <div ref={ref} className={cn("flex flex-col gap-1 p-4", className)} {...props} />
})

export const CardTitle = forwardRef<HTMLHeadingElement, HTMLAttributes<HTMLHeadingElement>>(function CardTitle(
  { className, ...props }, ref,
) {
  return <h3 ref={ref} className={cn("text-sm font-medium leading-none", className)} {...props} />
})

export const CardDescription = forwardRef<HTMLParagraphElement, HTMLAttributes<HTMLParagraphElement>>(function CardDescription(
  { className, ...props }, ref,
) {
  return <p ref={ref} className={cn("text-xs text-muted-foreground", className)} {...props} />
})

export const CardContent = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function CardContent(
  { className, ...props }, ref,
) {
  return <div ref={ref} className={cn("p-4 pt-0", className)} {...props} />
})

export const CardFooter = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function CardFooter(
  { className, ...props }, ref,
) {
  return <div ref={ref} className={cn("flex items-center gap-2 p-4 pt-0", className)} {...props} />
})
