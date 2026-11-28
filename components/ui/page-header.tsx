import { forwardRef, type HTMLAttributes, type ReactNode } from "react"
import { cn } from "@/lib/cn"

export interface PageHeaderProps extends Omit<HTMLAttributes<HTMLDivElement>, "title"> {
  title: ReactNode
  description?: ReactNode
  badge?: ReactNode
  actions?: ReactNode
}

export const PageHeader = forwardRef<HTMLDivElement, PageHeaderProps>(function PageHeader(
  { title, description, badge, actions, className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="page-header"
      className={cn("flex flex-wrap items-start justify-between gap-3", className)}
      {...props}
    >
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-xl font-semibold tracking-tight text-foreground">{title}</h1>
          {badge}
        </div>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  )
})

export interface PageSectionProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  title?: ReactNode
  description?: ReactNode
  actions?: ReactNode
}

export const PageSection = forwardRef<HTMLElement, PageSectionProps>(function PageSection(
  { title, description, actions, className, children, ...props },
  ref,
) {
  return (
    <section ref={ref} data-slot="page-section" className={cn("min-w-0 space-y-3", className)} {...props}>
      {(title || description || actions) && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="min-w-0 space-y-0.5">
            {title ? <h2 className="text-base font-semibold text-foreground">{title}</h2> : null}
            {description ? <p className="text-xs text-muted-foreground">{description}</p> : null}
          </div>
          {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
      )}
      {children}
    </section>
  )
})

export interface StatCardProps extends HTMLAttributes<HTMLDivElement> {
  label: ReactNode
  value: ReactNode
  detail?: ReactNode
  icon?: ReactNode
  tone?: "default" | "primary" | "success" | "warning" | "danger"
}

const statToneClass = {
  default: "text-foreground",
  primary: "text-primary",
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
} as const

export const StatCard = forwardRef<HTMLDivElement, StatCardProps>(function StatCard(
  { label, value, detail, icon, tone = "default", className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="stat-card"
      className={cn("rounded-lg border border-border bg-card p-4 text-card-foreground shadow-xs", className)}
      {...props}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        {icon ? <span className="text-muted-foreground">{icon}</span> : null}
      </div>
      <div className={cn("mt-1.5 font-mono text-2xl font-semibold tracking-tight", statToneClass[tone])}>
        {value}
      </div>
      {detail ? <p className="mt-1 text-xs text-muted-foreground">{detail}</p> : null}
    </div>
  )
})

export const FilterBar = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function FilterBar(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="filter-bar"
      className={cn("flex flex-wrap items-center gap-2 rounded-lg border border-border/70 bg-card/60 p-2.5", className)}
      {...props}
    />
  )
})

export interface EmptyStateProps extends Omit<HTMLAttributes<HTMLDivElement>, "title"> {
  icon?: ReactNode
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
}

export const EmptyState = forwardRef<HTMLDivElement, EmptyStateProps>(function EmptyState(
  { icon, title, description, action, className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="empty-state"
      className={cn(
        "flex flex-col items-center justify-center rounded-lg border border-dashed border-border bg-card/40 px-6 py-10 text-center",
        className,
      )}
      {...props}
    >
      {icon ? <div className="mb-3 text-muted-foreground">{icon}</div> : null}
      <p className="text-sm font-medium text-foreground">{title}</p>
      {description ? <p className="mt-1 max-w-md text-xs leading-relaxed text-muted-foreground">{description}</p> : null}
      {action ? <div className="mt-4 flex flex-wrap items-center justify-center gap-2">{action}</div> : null}
    </div>
  )
})
