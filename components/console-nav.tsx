"use client"

import { ArrowUpCircle, Boxes, ClipboardCheck, Database, Globe2, Layers, PackagePlus, Plug, Radar, Server, Shield, Sparkles, Workflow } from "lucide-react"
import { useEffect, useRef } from "react"
import type { ComponentType } from "react"
import {
  consoleNavEntries,
  type CatalogKind,
  type AssetArea,
  type ConsoleNavEntry,
  type ConsoleView,
  type DeploySection,
  type TargetTab,
} from "@/lib/console-views"
import { ActivityIcon, FileIcon, FileTextIcon, HistoryIcon, KeyIcon, RocketIcon, SparkIcon, TerminalIcon } from "./icons"

type NavIcon = ComponentType<{ width?: number; height?: number; "aria-hidden"?: boolean | "true" | "false" }>

// Icons stay in the component; lib/console-views.ts owns order, labels,
// destinations and role visibility so the flat list can be tested without JSX.
const ICONS: Record<string, NavIcon> = {
  home: ActivityIcon,
  overview: ActivityIcon,
  ask: SparkIcon,
  terminal: TerminalIcon,
  tasks: Workflow,
  database: Database,
  website: Globe2,
  k8s: Boxes,
  image: Boxes,
  files: FileIcon,
  assets: Boxes,
  monitor: ActivityIcon,
  credentials: KeyIcon,
  audit: HistoryIcon,
  kb: FileTextIcon,
  "situational-awareness": Radar,
  "smart-inspection": ClipboardCheck,
  "skill-registry": Sparkles,
  "mcp-registry": Plug,
  "platform-monitor": ActivityIcon,
  "ledger-machines": Server,
  vm: Server,
  "ledger-systems": Layers,
  "deploy-workload": RocketIcon,
  "deploy-upgrade": ArrowUpCircle,
  "deploy-onboard": PackagePlus,
  "deploy-cicd": TerminalIcon,
  "platform-admin": Shield,
}

const groups = [
  ["home", "导航"], ["console", "目标运维"], ["catalog", "资产"], ["assets", "台账与监控"], ["deploy", "发布部署"], ["admin", "平台管理"],
] as const

export function ConsoleNav({ view, tab, assetArea, deploySection, catalogKind, isAdmin, hasTarget, onNavigate, mobileOpen, onDismiss }: {
  view: ConsoleView
  tab: TargetTab
  assetArea: AssetArea
  deploySection: DeploySection
  catalogKind: CatalogKind
  isAdmin: boolean
  hasTarget: boolean
  onNavigate: (entry: ConsoleNavEntry) => void
  mobileOpen: boolean
  onDismiss: () => void
}) {
  const entries = consoleNavEntries(isAdmin)
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

  function isActive({ destination }: ConsoleNavEntry) {
    if (view === "console" && destination.view === "assets" && destination.area === "clusters" && tab === "k8s") return true
    if (view === "console" && destination.view === "catalog") return destination.kind === tab
    if (destination.view !== view) return false
    switch (destination.view) {
      case "home": return true
      case "console": return destination.tab === tab
      case "assets": return destination.area === assetArea
      case "deploy": return destination.section === deploySection
      case "catalog": return destination.kind === catalogKind
      case "admin": return true
    }
  }

  function menu(mobile: boolean) {
    return <nav aria-label="主导航" className="console-nav-list">
      {groups.map(([id, label]) => {
        const items = entries.filter(entry => (entry.id === "k8s" ? "catalog" : entry.destination.view) === id)
        if (!items.length) return null
        return <div role="group" aria-label={label} key={id} className="console-nav-group">
          <div aria-hidden="true" className="console-nav-label">{label}</div>
          {items.map(entry => {
            const Icon = ICONS[entry.id] ?? ActivityIcon
            const disabled = Boolean(entry.needsTarget) && !hasTarget
            return <button key={entry.id} type="button" className="console-nav-item" disabled={disabled}
              aria-current={isActive(entry) ? "page" : undefined}
              title={disabled ? "请先选择在线 Agent" : entry.id === "terminal" ? "高级：直接执行 shell 命令" : undefined}
              onClick={() => { if (mobile) drawer.current?.close(); onNavigate(entry) }}>
              <Icon width={17} height={17} aria-hidden="true" />{entry.label}
            </button>
          })}
        </div>
      })}
    </nav>
  }
  return <>
    <aside className="console-sidebar hidden md:flex">{menu(false)}</aside>
    {mobileOpen && <dialog ref={drawer} aria-label="控制台菜单" id="console-nav-drawer" className="console-nav-drawer"
      onClose={onDismiss} onClick={event => { if (event.target === event.currentTarget && event.clientX >= event.currentTarget.getBoundingClientRect().right) drawer.current?.close() }}>
      <div className="console-drawer-heading"><strong>功能菜单</strong><button type="button" onClick={() => drawer.current?.close()}>关闭</button></div>
      {menu(true)}
    </dialog>}
  </>
}
