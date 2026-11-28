"use client"

import { useEffect, useRef } from "react"
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
  const drawer = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = drawer.current
    if (!mobileOpen || !dialog) return
    const desktop = window.matchMedia("(min-width: 768px)")
    function handleWidth() { if (desktop.matches) dialog?.close() }
    dialog.showModal()
    dialog.querySelector<HTMLElement>('[aria-current="page"]')?.focus()
    handleWidth()
    desktop.addEventListener("change", handleWidth)
    return () => desktop.removeEventListener("change", handleWidth)
  }, [mobileOpen])

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
      <nav aria-label="主导航" className="console-nav-list">
        {groups.map(([id, label]) => {
          const items = resolvedItems.filter((item) => groupOf(item.entry) === id)
          if (!items.length) return null
          return (
            <div role="group" aria-label={label} key={id} className="console-nav-group">
              <div aria-hidden="true" className="console-nav-label">{label}</div>
              {items.map(({ entry, icon, disabled, disabledReason }) => {
                const Icon = (icon && CONSOLE_NAV_ICONS[icon]) || CONSOLE_NAV_ICONS[entry.id] || ActivityIcon
                const title = disabled
                  ? disabledReason
                  : entry.id === "terminal"
                    ? "高级：直接执行 shell 命令"
                    : undefined
                return (
                  <button
                    key={entry.id}
                    type="button"
                    className="console-nav-item"
                    disabled={disabled}
                    aria-current={isActive(entry) ? "page" : undefined}
                    title={title}
                    onClick={() => {
                      if (mobile) drawer.current?.close()
                      onNavigate(entry)
                    }}
                  >
                    <Icon width={17} height={17} aria-hidden="true" />
                    {entry.label}
                  </button>
                )
              })}
            </div>
          )
        })}
        {!pluginsReady && (
          <div role="status" aria-label="正在加载功能插件导航" className="console-nav-group space-y-1.5 px-2 py-2">
            <div className="h-3 w-16 animate-pulse rounded bg-muted" />
            <div className="h-7 w-full animate-pulse rounded bg-muted/70" />
            <div className="h-7 w-5/6 animate-pulse rounded bg-muted/70" />
            <div className="h-7 w-4/5 animate-pulse rounded bg-muted/70" />
          </div>
        )}
      </nav>
    )
  }

  return (
    <>
      <aside className="console-sidebar hidden md:flex">{menu(false)}</aside>
      {mobileOpen && (
        <dialog
          ref={drawer}
          aria-label="控制台菜单"
          id="console-nav-drawer"
          className="console-nav-drawer"
          onClose={onDismiss}
          onClick={(event) => {
            if (event.target === event.currentTarget && event.clientX >= event.currentTarget.getBoundingClientRect().right) {
              drawer.current?.close()
            }
          }}
        >
          <div className="console-drawer-heading">
            <strong>功能菜单</strong>
            <button type="button" onClick={() => drawer.current?.close()}>关闭</button>
          </div>
          {menu(true)}
        </dialog>
      )}
    </>
  )
}
