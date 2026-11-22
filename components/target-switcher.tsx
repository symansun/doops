"use client"

import { useEffect, useRef, useState } from "react"
import type { Target } from "@/lib/client"
import { targetSwitcherEmptyCopy } from "@/lib/empty-targets"
import { targetModeLabel } from "@/lib/gateway"
import { ServerIcon, RefreshIcon, ChevronDownIcon, CheckIcon } from "./icons"
import { Badge, Button } from "./ui"

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
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener("mousedown", onDoc)
    return () => document.removeEventListener("mousedown", onDoc)
  }, [open])

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

  const current = selected && targets.find(target => target.key === selected.key)

  return (
    <div ref={ref} className="relative flex min-w-0 flex-wrap items-center gap-2">
      <Button
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
        <span className="max-w-[40vw] truncate text-xs font-medium text-foreground sm:max-w-[200px]">
          {current?.instance || "选择 Agent 目标"}
        </span>
        <span className="hidden truncate font-mono text-sm text-muted-foreground sm:inline">
          {current?.cluster}
        </span>
        {current && <Badge>{targetModeLabel(current.mode)}</Badge>}
        {targets.length > 1 && (
          <span className="rounded-full bg-background px-1.5 text-sm text-muted-foreground">
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

      {open && (
        <div
          role="listbox"
          aria-label="Agent 目标"
          className="absolute left-0 top-full z-50 mt-1 max-h-80 w-72 max-w-[calc(100vw_-_4.25rem)] overflow-y-auto rounded-lg border bg-card p-1 shadow-lg"
        >
          <div className="flex items-center justify-between px-2 py-1.5">
            <Button
              onClick={() => { onViewAll?.(); setOpen(false) }}
              variant="ghost"
              size="sm"
            >
              全部 Agent
            </Button>
            <span className="text-sm font-medium uppercase tracking-wide text-muted-foreground">
              {targets.length} 个目标
            </span>
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
          {targets.map((t) => {
            const active = selected?.key === t.key
            return (
              <button
                key={t.key}
                role="option"
                aria-selected={active}
                onClick={() => {
                  onSelect(t)
                  setOpen(false)
                }}
                className={`flex w-full items-center gap-2 rounded-md px-2 py-2 text-left transition-colors ${
                  active ? "bg-primary/10" : "hover:bg-muted"
                }`}
              >
                <span className={`h-2 w-2 shrink-0 rounded-full ${statusDot(t)}`} aria-hidden />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm text-foreground">{t.instance}</span>
                  <span className="block truncate font-mono text-sm text-muted-foreground">
                    {t.cluster} · {statusLabel(t)}
                  </span>
                </span>
                <Badge>{targetModeLabel(t.mode)}</Badge>
                {active && <CheckIcon width={14} height={14} className="shrink-0 text-primary" />}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
