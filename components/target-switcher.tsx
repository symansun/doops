"use client"

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { createPortal } from "react-dom"
import type { Target } from "@/lib/client"
import { targetSwitcherEmptyCopy } from "@/lib/empty-targets"
import { targetModeLabel } from "@/lib/gateway"
import { ServerIcon, RefreshIcon, ChevronDownIcon, CheckIcon } from "./icons"
import { Badge, Button, Input } from "./ui"

function statusDot(t: Target): string {
  if (t.status === "offline") return "bg-muted-foreground/50"
  if (t.busy) return "bg-warning"
  if (t.status === "active") return "bg-accent"
  return "bg-success"
}

function statusLabel(t: Target): string {
  if (t.status === "offline") return "离线"
  if (t.busy) return "忙碌"
  if (t.status === "active") return "活动"
  return "空闲"
}

interface MenuCoords {
  top: number
  left: number
  width: number
  maxHeight: number
}

const MENU_WIDTH = 304
const VIEWPORT_GAP = 8

export function TargetSwitcher({
  targets,
  selected,
  onSelect,
  onRefresh,
  loading,
  isAdmin,
  onViewAll,
  needsTarget,
}: {
  targets: Target[]
  selected: Target | null
  onSelect: (t: Target) => void
  onRefresh: () => void
  loading: boolean
  // 未传入时空状态用对两种角色都成立的说法。
  isAdmin?: boolean
  onViewAll?: () => void
  // 用户点了需要在线目标的功能但没有目标时，在这里就地提示。
  needsTarget?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [coords, setCoords] = useState<MenuCoords | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  const updatePosition = useCallback(() => {
    const el = triggerRef.current
    if (!el || typeof window === "undefined") return
    const rect = el.getBoundingClientRect()
    const width = Math.min(MENU_WIDTH, Math.max(240, window.innerWidth - VIEWPORT_GAP * 2))
    // Right-align to the trigger button so a header-right switcher never clips off-screen.
    const preferredLeft = rect.right - width
    const left = Math.max(VIEWPORT_GAP, Math.min(preferredLeft, window.innerWidth - width - VIEWPORT_GAP))
    const top = Math.min(rect.bottom + 6, Math.max(VIEWPORT_GAP, window.innerHeight - 160))
    const maxHeight = Math.max(180, Math.min(420, window.innerHeight - top - VIEWPORT_GAP * 2))
    setCoords({ top, left, width, maxHeight })
  }, [])

  useLayoutEffect(() => {
    if (!open) return
    updatePosition()
  }, [open, updatePosition, targets.length])

  useEffect(() => {
    if (!open) {
      setQuery("")
      return
    }
    function onDoc(e: MouseEvent) {
      const target = e.target as Node | null
      if (!target) return
      if (ref.current?.contains(target) || menuRef.current?.contains(target)) return
      setOpen(false)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false)
        triggerRef.current?.focus({ preventScroll: true })
      }
    }
    function onReposition() {
      updatePosition()
    }
    document.addEventListener("mousedown", onDoc)
    document.addEventListener("keydown", onKey)
    window.addEventListener("resize", onReposition)
    window.addEventListener("scroll", onReposition, true)
    return () => {
      document.removeEventListener("mousedown", onDoc)
      document.removeEventListener("keydown", onKey)
      window.removeEventListener("resize", onReposition)
      window.removeEventListener("scroll", onReposition, true)
    }
  }, [open, updatePosition])

  const filteredTargets = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return targets
    return targets.filter(
      (t) =>
        t.instance.toLowerCase().includes(q) ||
        t.cluster.toLowerCase().includes(q) ||
        targetModeLabel(t.mode).toLowerCase().includes(q),
    )
  }, [targets, query])

  if (targets.length === 0) {
    const empty = targetSwitcherEmptyCopy(isAdmin)
    return (
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <div title={empty.hint} className="flex items-center gap-2 rounded-lg border bg-muted/40 px-2.5 py-1.5">
          <ServerIcon width={14} height={14} className="text-muted-foreground" />
          <span className="whitespace-nowrap text-xs text-muted-foreground">{empty.label}</span>
          <Button
            onClick={onRefresh}
            disabled={loading}
            variant="ghost"
            size="sm"
            icon
            aria-label="刷新 Agent 目标列表"
          >
            <RefreshIcon width={13} height={13} className={loading ? "animate-spin" : ""} />
          </Button>
        </div>
        {needsTarget && (
          <span role="alert" className="basis-full text-xs text-warning">
            当前没有可用的 Agent 目标，请选择在线目标
          </span>
        )}
      </div>
    )
  }

  const current = selected && targets.find((target) => target.key === selected.key)

  const menuNode =
    open && coords && typeof document !== "undefined"
      ? createPortal(
          <div
            ref={menuRef}
            role="listbox"
            aria-label="Agent 目标"
            style={{ top: coords.top, left: coords.left, width: coords.width, maxHeight: coords.maxHeight }}
            className="fixed z-[70] flex flex-col overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-2xl ring-1 ring-black/40"
          >
            <div className="flex shrink-0 items-center justify-between gap-2 border-b border-border/70 bg-popover px-2.5 py-2">
              <Button
                onClick={() => {
                  onViewAll?.()
                  setOpen(false)
                }}
                variant="ghost"
                size="sm"
                className="h-7 px-2 text-xs"
              >
                全部 Agent
              </Button>
              <div className="flex items-center gap-1">
                <span className="text-xs font-medium tabular-nums text-muted-foreground">
                  {targets.length} 个目标
                </span>
                <Button
                  onClick={onRefresh}
                  disabled={loading}
                  variant="ghost"
                  size="sm"
                  icon
                  aria-label="刷新 Agent 目标列表"
                  className="h-7 w-7"
                >
                  <RefreshIcon width={13} height={13} className={loading ? "animate-spin" : ""} />
                </Button>
              </div>
            </div>

            {targets.length >= 6 && (
              <div className="shrink-0 border-b border-border/60 bg-popover px-2.5 py-2">
                <Input
                  scale="sm"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜索实例、集群或运行形态…"
                  aria-label="搜索 Agent 目标"
                  className="h-8 text-xs"
                />
              </div>
            )}

            <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto overscroll-contain p-1.5">
              {filteredTargets.length === 0 ? (
                <p className="px-2.5 py-6 text-center text-xs text-muted-foreground">
                  没有匹配的 Agent 目标
                </p>
              ) : (
                filteredTargets.map((t) => {
                  const active = selected?.key === t.key
                  return (
                    <button
                      key={t.key}
                      type="button"
                      role="option"
                      aria-selected={active}
                      onClick={() => {
                        onSelect(t)
                        setOpen(false)
                      }}
                      className={`flex w-full cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors ${
                        active
                          ? "bg-primary/15 ring-1 ring-primary/30"
                          : "hover:bg-muted/80"
                      }`}
                    >
                      <span className={`h-2 w-2 shrink-0 rounded-full ${statusDot(t)}`} aria-hidden />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium leading-tight text-foreground">
                          {t.instance}
                        </span>
                        <span className="mt-0.5 block truncate font-mono text-2xs leading-tight text-muted-foreground">
                          {t.cluster} · {statusLabel(t)}
                        </span>
                      </span>
                      <Badge className="shrink-0">{targetModeLabel(t.mode)}</Badge>
                      {active && <CheckIcon width={14} height={14} className="shrink-0 text-primary" />}
                    </button>
                  )
                })
              )}
            </div>
          </div>,
          document.body,
        )
      : null

  return (
    <div ref={ref} className="relative flex min-w-0 flex-wrap items-center gap-2">
      <Button
        ref={triggerRef}
        onClick={() => setOpen((v) => !v)}
        size="sm"
        className="max-w-full"
        title={current ? `${current.key} · ${statusLabel(current)}` : "选择 Agent 目标"}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <span
          className={`h-2 w-2 shrink-0 rounded-full ${current ? statusDot(current) : "bg-muted-foreground"}`}
          aria-hidden
        />
        <span className="max-w-32 truncate text-xs font-medium text-foreground sm:max-w-48">
          {current?.instance || "选择 Agent 目标"}
        </span>
        <span className="hidden truncate font-mono text-xs text-muted-foreground sm:inline">
          {current?.cluster}
        </span>
        {current && <Badge>{targetModeLabel(current.mode)}</Badge>}
        {targets.length > 1 && (
          <span className="rounded-full bg-background px-1.5 text-xs tabular-nums text-muted-foreground">
            {targets.length}
          </span>
        )}
        <ChevronDownIcon width={13} height={13} className={`text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`} />
      </Button>

      {needsTarget && (
        <span role="alert" className="basis-full text-xs text-warning">
          当前没有可用的 Agent 目标，请选择在线目标
        </span>
      )}

      {menuNode}
    </div>
  )
}
