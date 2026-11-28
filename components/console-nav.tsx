"use client"

import { useEffect } from "react"
import {
  type AssetArea,
  type CatalogKind,
  type ConsoleNavEntry,
  type ConsoleView,
  type DeploySection,
  type TargetTab,
} from "@/lib/console-views"
import { resolveConsoleNavItems } from "@/lib/plugins/resolver"
import type { PluginStateResponse } from "@/lib/plugins/types"
import { ActivityIcon } from "./icons"
import { CONSOLE_NAV_ICONS, isPluginCenterArea } from "./plugins/registry"
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  Skeleton,
} from "./ui"

const groups = [
  ["home", "导航"],
  ["console", "目标运维"],
  ["ledger", "资产台账"],
  ["observability", "监控与态势"],
  ["extensions", "场景与扩展"],
  ["deploy", "发布部署"],
  ["admin", "平台管理"],
] as const

export function ConsoleNav({
  view,
  tab,
  assetArea,
  deploySection,
  catalogKind,
  pluginId,
  isAdmin,
  hasTarget,
  kubernetesAvailable,
  pluginState,
  pluginsReady = true,
  onNavigate,
  mobileOpen,
  onDismiss,
}: {
  view: ConsoleView
  tab: TargetTab
  assetArea: AssetArea
  deploySection: DeploySection
  catalogKind: CatalogKind
  pluginId?: string
  isAdmin: boolean
  hasTarget: boolean
  kubernetesAvailable?: boolean
  pluginState?: PluginStateResponse | null
  pluginsReady?: boolean
  onNavigate: (entry: ConsoleNavEntry) => void
  mobileOpen: boolean
  onDismiss: () => void
}) {
  const resolvedItems = resolveConsoleNavItems({
    isAdmin,
    hasTarget,
    kubernetesAvailable,
    pluginState,
    pluginsReady,
  })

  // Auto-dismiss mobile drawer if viewport expands past md breakpoint.
  useEffect(() => {
    if (!mobileOpen || typeof window === "undefined") return
    const desktop = window.matchMedia("(min-width: 768px)")
    const handleWidth = () => {
      if (desktop.matches) onDismiss()
    }
    handleWidth()
    desktop.addEventListener("change", handleWidth)
    return () => desktop.removeEventListener("change", handleWidth)
  }, [mobileOpen, onDismiss])

  function isActive(entry: ConsoleNavEntry) {
    const { destination } = entry
    if (entry.id === "plugin-center" && view === "assets" && isPluginCenterArea(assetArea)) return true
    if (entry.id === "overview" && view === "console" && tab === "assets") return true
    if (entry.id === "resource-monitor" && view === "console" && tab === "monitor") return true
    if (entry.id === "tasks" && view === "assets" && assetArea === "inspection") return true
    if (view === "console" && destination.view === "assets" && destination.area === "clusters" && tab === "k8s") return true
    if (view === "console" && destination.view === "catalog") return destination.kind === tab
    if (destination.view !== view) return false
    switch (destination.view) {
      case "home": return true
      case "console": return destination.tab === tab
      case "assets": return destination.area === assetArea
      case "deploy": return destination.section === deploySection
      case "catalog": return destination.kind === catalogKind
      case "plugin": return destination.pluginId === pluginId
      case "admin": return true
    }
  }

  function groupOf(entry: ConsoleNavEntry): string {
    if (entry.id === "plugin-center" || entry.destination.view === "plugin") return "extensions"
    if (entry.destination.view === "catalog") return "ledger"
    if (entry.destination.view === "assets") {
      switch (entry.destination.area) {
        case "machines":
        case "clusters":
        case "systems":
          return "ledger"
        case "situation":
        case "monitor":
        case "inspection":
        case "board":
          return "observability"
        case "plugins":
        case "skills":
        case "mcp":
          return "extensions"
      }
    }
    return entry.destination.view
  }

  function menu(mobile: boolean) {
    return (
      <SidebarContent aria-label="主导航">
        {groups.map(([id, label]) => {
          const items = resolvedItems.filter((item) => groupOf(item.entry) === id)
          if (!items.length) return null
          return (
            <SidebarGroup aria-label={label} key={id}>
              <SidebarGroupLabel>{label}</SidebarGroupLabel>
              <SidebarMenu>
                {items.map(({ entry, icon, disabled, disabledReason }) => {
                  const Icon = (icon && CONSOLE_NAV_ICONS[icon]) || CONSOLE_NAV_ICONS[entry.id] || ActivityIcon
                  const title = disabled
                    ? disabledReason
                    : entry.id === "terminal"
                      ? "高级：直接执行 shell 命令"
                      : undefined
                  return (
                    <SidebarMenuItem key={entry.id}>
                      <SidebarMenuButton
                        size={mobile ? "touch" : "default"}
                        disabled={disabled}
                        isActive={isActive(entry)}
                        title={title}
                        onClick={() => {
                          if (mobile) onDismiss()
                          onNavigate(entry)
                        }}
                      >
                        <Icon width={16} height={16} aria-hidden="true" className="shrink-0" />
                        <span className="truncate">{entry.label}</span>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </SidebarGroup>
          )
        })}
        {!pluginsReady && (
          <SidebarGroup role="status" aria-label="正在加载功能插件导航" className="space-y-1.5 px-2 py-2">
            <Skeleton className="h-3 w-16" />
            <Skeleton className="h-7 w-full" />
            <Skeleton className="h-7 w-5/6" />
            <Skeleton className="h-7 w-4/5" />
          </SidebarGroup>
        )}
      </SidebarContent>
    )
  }

  return (
    <>
      <Sidebar>{menu(false)}</Sidebar>
      <Sheet open={mobileOpen} onOpenChange={(open) => { if (!open) onDismiss() }}>
        <SheetContent
          side="left"
          id="console-nav-drawer"
          aria-label="控制台菜单"
          className="border-sidebar-border bg-sidebar p-0 text-sidebar-foreground"
        >
          <SheetHeader className="border-sidebar-border">
            <SheetTitle className="text-sidebar-foreground">功能菜单</SheetTitle>
          </SheetHeader>
          {menu(true)}
        </SheetContent>
      </Sheet>
    </>
  )
}
