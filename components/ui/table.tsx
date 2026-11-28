import {
  forwardRef,
  type HTMLAttributes,
  type TableHTMLAttributes,
  type TdHTMLAttributes,
  type ThHTMLAttributes,
} from "react"
import { cn } from "@/lib/cn"

export interface TableProps extends TableHTMLAttributes<HTMLTableElement> {
  wrapperClassName?: string
  bare?: boolean
}

export const Table = forwardRef<HTMLTableElement, TableProps>(function Table(
  { className, wrapperClassName, bare = false, ...props },
  ref,
) {
  const table = (
    <table
      ref={ref}
      className={cn("w-full border-collapse text-left text-sm", className)}
      {...props}
    />
  )
  if (bare) return table
  return <div className={cn("w-full overflow-x-auto", wrapperClassName)}>{table}</div>
})

export const TableHeader = forwardRef<HTMLTableSectionElement, HTMLAttributes<HTMLTableSectionElement>>(
  function TableHeader({ className, ...props }, ref) {
    return <thead ref={ref} className={cn("border-b text-xs text-muted-foreground", className)} {...props} />
  },
)

export const TableBody = forwardRef<HTMLTableSectionElement, HTMLAttributes<HTMLTableSectionElement>>(
  function TableBody({ className, ...props }, ref) {
    return <tbody ref={ref} className={cn("[&_tr:last-child]:border-0", className)} {...props} />
  },
)

export const TableRow = forwardRef<HTMLTableRowElement, HTMLAttributes<HTMLTableRowElement>>(
  function TableRow({ className, ...props }, ref) {
    return (
      <tr
        ref={ref}
        className={cn("border-b border-border/60 transition-colors hover:bg-muted/30", className)}
        {...props}
      />
    )
  },
)

export const TableHead = forwardRef<HTMLTableCellElement, ThHTMLAttributes<HTMLTableCellElement>>(
  function TableHead({ className, scope = "col", ...props }, ref) {
    return (
      <th
        ref={ref}
        scope={scope}
        className={cn("px-3 py-2 font-medium text-muted-foreground", className)}
        {...props}
      />
    )
  },
)

export const TableCell = forwardRef<HTMLTableCellElement, TdHTMLAttributes<HTMLTableCellElement>>(
  function TableCell({ className, ...props }, ref) {
    return <td ref={ref} className={cn("px-3 py-2.5 align-middle", className)} {...props} />
  },
)
