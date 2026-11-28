"use client"

import { forwardRef, type ButtonHTMLAttributes, type HTMLAttributes } from "react"
import { cn } from "@/lib/cn"

export const Sidebar = forwardRef<HTMLElement, HTMLAttributes<HTMLElement>>(function Sidebar(
  { className, ...props },
  ref,
) {
  return (
    <aside
      ref={ref}
      data-slot="sidebar"
      className={cn(
        "hidden min-h-0 w-52 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex lg:w-56",
        className,
      )}
      {...props}
    />
  )
})

export const SidebarHeader = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function SidebarHeader(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="sidebar-header"
      className={cn("flex shrink-0 items-center justify-between gap-2 border-b border-sidebar-border px-3 py-2.5", className)}
      {...props}
    />
  )
})

export const SidebarContent = forwardRef<HTMLElement, HTMLAttributes<HTMLElement>>(function SidebarContent(
  { className, ...props },
  ref,
) {
  return (
    <nav
      ref={ref}
      data-slot="sidebar-content"
      className={cn("docs-scroll min-h-0 w-full flex-1 overflow-y-auto overscroll-contain p-2", className)}
      {...props}
    />
  )
})

export const SidebarFooter = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function SidebarFooter(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      data-slot="sidebar-footer"
      className={cn("shrink-0 border-t border-sidebar-border p-2", className)}
      {...props}
    />
  )
})

export const SidebarGroup = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function SidebarGroup(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      role="group"
      data-slot="sidebar-group"
      className={cn("mt-2 border-t border-sidebar-border/70 pt-2 first:mt-0 first:border-t-0 first:pt-0", className)}
      {...props}
    />
  )
})

export const SidebarGroupLabel = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(function SidebarGroupLabel(
  { className, ...props },
  ref,
) {
  return (
    <div
      ref={ref}
      aria-hidden="true"
      data-slot="sidebar-group-label"
      className={cn("px-2.5 py-1 text-xs font-semibold tracking-wide text-sidebar-foreground/65", className)}
      {...props}
    />
  )
})

export const SidebarMenu = forwardRef<HTMLUListElement, HTMLAttributes<HTMLUListElement>>(function SidebarMenu(
  { className, ...props },
  ref,
) {
  return (
    <ul
      ref={ref}
      data-slot="sidebar-menu"
      className={cn("flex flex-col gap-0.5", className)}
      {...props}
    />
  )
})

export const SidebarMenuItem = forwardRef<HTMLLIElement, HTMLAttributes<HTMLLIElement>>(function SidebarMenuItem(
  { className, ...props },
  ref,
) {
  return <li ref={ref} data-slot="sidebar-menu-item" className={cn("list-none", className)} {...props} />
})

export interface SidebarMenuButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  isActive?: boolean
  size?: "default" | "touch"
}

export const SidebarMenuButton = forwardRef<HTMLButtonElement, SidebarMenuButtonProps>(function SidebarMenuButton(
  { isActive = false, size = "default", className, ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type="button"
      data-slot="sidebar-menu-button"
      data-active={isActive ? "true" : undefined}
      aria-current={isActive ? "page" : undefined}
      className={cn(
        "flex w-full cursor-pointer items-center gap-2.5 rounded-md border-l-2 px-2.5 py-1.5 text-left text-sm font-medium transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-sidebar-ring",
        "disabled:cursor-not-allowed disabled:opacity-45",
        size === "touch" ? "min-h-11" : "min-h-9",
        isActive
          ? "border-sidebar-primary-foreground/85 bg-sidebar-primary font-semibold text-sidebar-primary-foreground shadow-xs"
          : "border-transparent text-sidebar-foreground/90 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
        className,
      )}
      {...props}
    />
  )
})
