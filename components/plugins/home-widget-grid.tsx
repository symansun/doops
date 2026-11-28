"use client"

import { useEffect, useMemo, useState, type ReactNode } from "react"
import { LayersIcon, ChevronRightIcon } from "@/components/icons"
import { CONSOLE_NAV_ICONS } from "@/components/plugins/registry"
import { Badge, Button, Card, CardContent } from "@/components/ui"
import type { Session } from "@/lib/client"
import type { ConsoleNavEntry } from "@/lib/console-views"
import { partitionHomeWidgets, type EvaluatedHomeWidget } from "@/lib/dashboard-summary"
import {
  fetchMonitorLatestMetricsForWidgets,
  formatMetricValue,
  type MonitorLatestMetricItem,
  type MonitorLatestMetricPair,
} from "@/lib/monitor-metrics"
import { resolveHomeWidgetPlugins } from "@/lib/plugins/resolver"
import {
  PLUGIN_CATEGORY_LABELS,
  type PluginCategory,
  type PluginStateResponse,
  type PluginView,
} from "@/lib/plugins/types"

const HOME_WIDGET_HIDDEN_STORAGE_KEY = "doops.console.homeWidgets.hidden.v1"
export const DEFAULT_VISIBLE_HOME_WIDGETS = 6

export interface HomeWidgetRenderContext {
  plugin: PluginView
  bySkillKey: Map<string, MonitorLatestMetricItem>
  loading: boolean
  hasAnyItems: boolean
  onOpenDestination: (dest: ConsoleNavEntry["destination"]) => void
}

export type HomeWidgetRenderer = (ctx: HomeWidgetRenderContext) => ReactNode

/**
 * Pluggable Home Widget Renderer Registry.
 * New widget visual layouts can register here without touching DashboardPanel.
 */
export const HOME_WIDGET_RENDERERS: Record<string, HomeWidgetRenderer> = {
  metric_cards: ({ plugin, bySkillKey, onOpenDestination }) => {
    const hw = plugin.manifest?.ui.slots.home_widget
    if (!hw) return null
    const iconKey = plugin.manifest?.plugin.icon || "layers"
    const Icon = CONSOLE_NAV_ICONS[iconKey] || LayersIcon
    const declaredMetrics = hw.metrics ?? []
    const matchedMetrics = declaredMetrics.map((m) => ({
      spec: m,
      point: plugin.skill_slug ? bySkillKey.get(`${plugin.skill_slug}:${m.key}`) : undefined,
    }))
    const reportingTargets = Math.max(0, ...matchedMetrics.map((m) => m.point?.reporting_targets ?? 0))
    const hasCritical = matchedMetrics.some((m) => m.point?.status === "critical")
    const hasWarning = matchedMetrics.some((m) => m.point?.status === "warning")
    const statusBadge = hasCritical
      ? { label: "严重", variant: "danger" as const }
      : hasWarning
        ? { label: "告警", variant: "warning" as const }
        : reportingTargets > 0
          ? { label: "正常", variant: "success" as const }
          : null

    return (
      <Card key={plugin.id} className={hw.span === 2 ? "md:col-span-2" : ""}>
        <CardContent className="flex h-full flex-col justify-between gap-2.5 p-3.5">
          <div className="flex items-start justify-between gap-2">
            <div className="flex min-w-0 items-center gap-2">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                <Icon width={15} height={15} aria-hidden="true" />
              </span>
              <div className="min-w-0">
                <div className="flex items-center gap-1.5">
                  <span className="truncate text-sm font-semibold text-foreground">{hw.title || plugin.name}</span>
                  {statusBadge && (
                    <Badge variant={statusBadge.variant} className="shrink-0">
                      {statusBadge.label}
                    </Badge>
                  )}
                </div>
                <div className="truncate font-mono text-2xs text-muted-foreground">
                  覆盖 {reportingTargets} 台授权节点
                </div>
              </div>
            </div>
            <Button
              size="sm"
              variant="ghost"
              className="shrink-0"
              onClick={() => onOpenDestination({ view: "plugin", pluginId: plugin.id })}
            >
              工作台
              <ChevronRightIcon width={13} height={13} />
            </Button>
          </div>

          <div className="grid grid-cols-2 gap-2">
            {matchedMetrics.map(({ spec, point }) => {
              const rawVal = !point
                ? null
                : spec.aggregate === "avg"
                  ? point.avg
                  : spec.aggregate === "latest"
                    ? point.latest
                    : point.max
              const toneClass =
                point?.status === "critical"
                  ? "text-destructive"
                  : point?.status === "warning"
                    ? "text-warning"
                    : "text-foreground"
              return (
                <div key={spec.key} className="rounded-md border border-border/70 bg-muted/25 px-2.5 py-1.5">
                  <div className="truncate text-2xs text-muted-foreground">{spec.label}</div>
                  {point ? (
                    <div className={`mt-0.5 font-mono text-sm font-semibold tabular-nums ${toneClass}`}>
                      {formatMetricValue(rawVal, spec.unit || point.unit)}
                    </div>
                  ) : (
                    <div className="mt-0.5 font-mono text-xs text-muted-foreground">—</div>
                  )}
                </div>
              )
            })}
          </div>
        </CardContent>
      </Card>
    )
  },
}

function readHiddenWidgetIds(): string[] {
  try {
    if (typeof window === "undefined") return []
    const raw = window.localStorage.getItem(HOME_WIDGET_HIDDEN_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === "string") : []
  } catch {
    return []
  }
}

function writeHiddenWidgetIds(ids: string[]) {
  try {
    if (typeof window === "undefined") return
    if (ids.length === 0) {
      window.localStorage.removeItem(HOME_WIDGET_HIDDEN_STORAGE_KEY)
    } else {
      window.localStorage.setItem(HOME_WIDGET_HIDDEN_STORAGE_KEY, JSON.stringify(ids))
    }
  } catch {
    // ignore storage errors
  }
}

function PendingPluginChips({
  pending,
  onOpenDestination,
}: {
  pending: EvaluatedHomeWidget[]
  onOpenDestination: (dest: ConsoleNavEntry["destination"]) => void
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {pending.map(({ plugin }) => {
        const iconKey = plugin.manifest?.plugin.icon || "layers"
        const Icon = CONSOLE_NAV_ICONS[iconKey] || LayersIcon
        const title = plugin.manifest?.ui.slots.home_widget?.title || plugin.name
        return (
          <Button
            key={plugin.id}
            type="button"
            size="sm"
            variant="default"
            onClick={() => onOpenDestination({ view: "plugin", pluginId: plugin.id })}
            title={`进入「${title}」工作台发起首次巡检`}
          >
            <Icon width={13} height={13} aria-hidden="true" className="text-muted-foreground" />
            {title}
          </Button>
        )
      })}
    </div>
  )
}

export function HomePluginWidgetGrid({
  session,
  pluginState,
  refreshVersion,
  onOpenDestination,
}: {
  session: Session
  pluginState: PluginStateResponse
  refreshVersion: number
  onOpenDestination: (dest: ConsoleNavEntry["destination"]) => void
}) {
  const allWidgetPlugins = useMemo(() => resolveHomeWidgetPlugins(pluginState), [pluginState])
  const [hiddenIds, setHiddenIds] = useState<string[]>(() => readHiddenWidgetIds())
  const [categoryFilter, setCategoryFilter] = useState<string>("all")
  const [expandedAll, setExpandedAll] = useState(false)
  const [customizing, setCustomizing] = useState(false)
  const [items, setItems] = useState<MonitorLatestMetricItem[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  const hiddenSet = useMemo(() => new Set(hiddenIds), [hiddenIds])

  const visibleWidgets = useMemo(
    () => allWidgetPlugins.filter((p) => !hiddenSet.has(p.id)),
    [allWidgetPlugins, hiddenSet],
  )

  // Collect exact (skill, key) pairs across ALL visible widgets (zero hard truncation!).
  const metricPairs = useMemo<MonitorLatestMetricPair[]>(() => {
    const pairs: MonitorLatestMetricPair[] = []
    for (const p of visibleWidgets) {
      if (!p.skill_slug) continue
      for (const m of p.manifest?.ui.slots.home_widget?.metrics ?? []) {
        if (m.key) pairs.push({ skill: p.skill_slug, key: m.key })
      }
    }
    return pairs
  }, [visibleWidgets])

  const pairsSignature = useMemo(() => metricPairs.map((p) => `${p.skill}:${p.key}`).join(","), [metricPairs])

  useEffect(() => {
    if (metricPairs.length === 0) {
      setItems([])
      setError("")
      setLoading(false)
      return
    }
    const ctrl = new AbortController()
    const timeout = setTimeout(() => ctrl.abort(), 10_000)
    setLoading(true)
    setError("")
    fetchMonitorLatestMetricsForWidgets(session, metricPairs, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        setItems(res.items || [])
        setError(res.partialErrors.length > 0 ? res.partialErrors[0] : "")
        setLoading(false)
      })
      .catch((err) => {
        if (ctrl.signal.aborted) {
          setError("部分聚合指标查询超时（10 秒），可刷新重试")
        } else {
          setError(err instanceof Error ? err.message : "聚合指标加载失败")
        }
        setLoading(false)
      })
      .finally(() => clearTimeout(timeout))
    return () => {
      clearTimeout(timeout)
      ctrl.abort()
    }
  }, [session, pairsSignature, refreshVersion])

  const bySkillKey = useMemo(() => {
    const map = new Map<string, MonitorLatestMetricItem>()
    for (const it of items) {
      map.set(`${it.skill}:${it.key}`, it)
    }
    return map
  }, [items])

  const partition = useMemo(
    () => partitionHomeWidgets(visibleWidgets, bySkillKey),
    [visibleWidgets, bySkillKey],
  )

  const activeCategories = useMemo(() => {
    const seen = new Set<PluginCategory>()
    for (const w of partition.active) seen.add(w.plugin.category)
    return [...seen]
  }, [partition.active])

  const effectiveCategory =
    categoryFilter !== "all" && activeCategories.includes(categoryFilter as PluginCategory)
      ? categoryFilter
      : "all"

  const filteredActive = useMemo(() => {
    if (effectiveCategory === "all") return partition.active
    return partition.active.filter((w) => w.plugin.category === effectiveCategory)
  }, [partition.active, effectiveCategory])

  const displayedActive = useMemo(() => {
    if (expandedAll || filteredActive.length <= DEFAULT_VISIBLE_HOME_WIDGETS) return filteredActive
    return filteredActive.slice(0, DEFAULT_VISIBLE_HOME_WIDGETS)
  }, [filteredActive, expandedAll])

  if (allWidgetPlugins.length === 0) return null

  function toggleWidgetHidden(id: string) {
    setHiddenIds((prev) => {
      const next = prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]
      writeHiddenWidgetIds(next)
      return next
    })
  }

  const isCompactEmptyStrip =
    !customizing && !loading && visibleWidgets.length > 0 && partition.active.length === 0 && !error

  if (isCompactEmptyStrip) {
    return (
      <section aria-label="专项巡检监测" className="card-surface flex flex-wrap items-center justify-between gap-2 px-3.5 py-2.5">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="inline-flex shrink-0 items-center gap-1.5 text-xs font-semibold text-foreground">
            <LayersIcon width={14} height={14} className="text-primary" aria-hidden="true" />
            专项能力
          </span>
          <span className="text-2xs text-muted-foreground">首次体检入口：</span>
          <PendingPluginChips pending={partition.pending} onOpenDestination={onOpenDestination} />
        </div>
        <Button type="button" size="sm" variant="ghost" onClick={() => setCustomizing(true)}>
          自定义
        </Button>
      </section>
    )
  }

  return (
    <section aria-label="专项巡检监测" className="card-surface p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <LayersIcon width={15} height={15} className="text-primary" aria-hidden="true" />
            专项能力监测
          </h2>
          {partition.active.length > 0 && (
            <Badge variant="outline" className="font-mono">
              已采集 {partition.active.length}/{visibleWidgets.length}
            </Badge>
          )}
          {activeCategories.length > 1 && (
            <div className="flex flex-wrap items-center gap-1" role="group" aria-label="按分类筛选专项监测">
              <Button
                type="button"
                size="sm"
                variant={effectiveCategory === "all" ? "primary" : "ghost"}
                onClick={() => setCategoryFilter("all")}
              >
                全部
              </Button>
              {activeCategories.map((cat) => (
                <Button
                  key={cat}
                  type="button"
                  size="sm"
                  variant={effectiveCategory === cat ? "primary" : "ghost"}
                  onClick={() => setCategoryFilter(cat)}
                >
                  {PLUGIN_CATEGORY_LABELS[cat] ?? cat}
                </Button>
              ))}
            </div>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {error && <span className="text-xs text-warning">{error}</span>}
          <Button type="button" size="sm" variant="ghost" onClick={() => setCustomizing((v) => !v)}>
            {customizing ? "完成" : "自定义"}
          </Button>
        </div>
      </div>

      {customizing && (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-lg border border-dashed bg-muted/20 p-3 text-xs">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-muted-foreground">首页监测项：</span>
            {allWidgetPlugins.map((p) => {
              const shown = !hiddenSet.has(p.id)
              return (
                <Button
                  key={p.id}
                  type="button"
                  size="sm"
                  variant={shown ? "primary" : "default"}
                  onClick={() => toggleWidgetHidden(p.id)}
                >
                  {shown ? "✓ " : "+ "}
                  {p.manifest?.ui.slots.home_widget?.title || p.name}
                </Button>
              )
            })}
          </div>
          {hiddenIds.length > 0 && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => {
                setHiddenIds([])
                writeHiddenWidgetIds([])
              }}
            >
              恢复默认
            </Button>
          )}
        </div>
      )}

      {visibleWidgets.length === 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-background/50 p-3 text-xs text-muted-foreground">
          <span>已隐藏全部专项监测项。</span>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => {
              setCategoryFilter("all")
              setHiddenIds([])
              writeHiddenWidgetIds([])
            }}
          >
            恢复显示
          </Button>
        </div>
      ) : loading && items.length === 0 ? (
        <div role="status" className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
          <span className="size-2 animate-pulse rounded-full bg-primary" aria-hidden="true" />
          正在读取专项巡检聚合指标…
        </div>
      ) : (
        <div className="space-y-3">
          {displayedActive.length > 0 && (
            <>
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                {displayedActive.map(({ plugin }) => {
                  const renderer = HOME_WIDGET_RENDERERS.metric_cards
                  return renderer({
                    plugin,
                    bySkillKey,
                    loading,
                    hasAnyItems: items.length > 0,
                    onOpenDestination,
                  })
                })}
              </div>
              {filteredActive.length > DEFAULT_VISIBLE_HOME_WIDGETS && (
                <div className="flex justify-center">
                  <Button type="button" size="sm" variant="ghost" onClick={() => setExpandedAll((v) => !v)}>
                    {expandedAll
                      ? "收起更多监测卡"
                      : `展开其余 ${filteredActive.length - DEFAULT_VISIBLE_HOME_WIDGETS} 项（共 ${filteredActive.length} 项）`}
                  </Button>
                </div>
              )}
            </>
          )}

          {partition.pending.length > 0 && (
            <div className={displayedActive.length > 0 ? "border-t border-border/60 pt-3" : ""}>
              <div className="mb-2 text-xs text-muted-foreground">
                {displayedActive.length > 0
                  ? "未采集巡检数据（点击进入工作台首次运行）："
                  : "尚无专项巡检数据，点击任一能力进入工作台即可发起首次采集："}
              </div>
              <PendingPluginChips pending={partition.pending} onOpenDestination={onOpenDestination} />
            </div>
          )}
        </div>
      )}
    </section>
  )
}
