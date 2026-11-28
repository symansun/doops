"use client"

import { useEffect, useState } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import type { AgentOverviewSubTab, TargetTab } from "@/lib/console-views"
import { resolveDeclarativeTargetTabPlugins } from "@/lib/plugins/resolver"
import type { PluginStateResponse } from "@/lib/plugins/types"
import { WorkspaceScopeSection } from "./agent-asset-scope"
import { CurrentMachineSection } from "./dashboard-panel"
import { NodeInfoCard } from "./node-info-card"
import { TargetManifestCard } from "./target-manifest-card"
import { Button, PageTabs } from "./ui"

export type { AgentOverviewSubTab }

export function AgentOverviewPanel({
  session,
  target,
  sessionId,
  online,
  pluginState,
  cmdbEnabled,
  monitorEnabled,
  subTab: controlledSubTab,
  initialSubTab = "summary",
  onSubTabChange,
  onSelectTab,
  onOpenResourceMonitor,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  online: boolean
  pluginState: PluginStateResponse | null
  cmdbEnabled: boolean
  monitorEnabled: boolean
  subTab?: AgentOverviewSubTab
  initialSubTab?: AgentOverviewSubTab
  onSubTabChange?: (next: AgentOverviewSubTab) => void
  onSelectTab: (tab: TargetTab) => void
  onOpenResourceMonitor?: () => void
  onRunningChange: (running: boolean) => void
}) {
  const demo = Boolean(session.demo)
  const isAdmin = sessionIsAdmin(session)

  const resolveTab = (requested: AgentOverviewSubTab): AgentOverviewSubTab => {
    if (requested === "manifest" && demo) return cmdbEnabled ? "workspace" : "summary"
    if (requested === "workspace" && !cmdbEnabled) return "summary"
    return requested
  }

  const [internalSubTab, setInternalSubTab] = useState<AgentOverviewSubTab>(() =>
    resolveTab(controlledSubTab ?? initialSubTab),
  )

  useEffect(() => {
    if (controlledSubTab !== undefined) {
      setInternalSubTab(resolveTab(controlledSubTab))
      return
    }
    setInternalSubTab(resolveTab(initialSubTab))
  }, [controlledSubTab, initialSubTab, target.key, demo, cmdbEnabled])

  const subTab = resolveTab(controlledSubTab ?? internalSubTab)
  const changeSubTab = (next: AgentOverviewSubTab) => {
    const resolved = resolveTab(next)
    setInternalSubTab(resolved)
    onSubTabChange?.(resolved)
  }

  const targetPlugins = resolveDeclarativeTargetTabPlugins(pluginState, target.mode)
  const items: Array<{ id: AgentOverviewSubTab; label: string }> = [
    { id: "summary", label: "运行概览" },
    ...(!demo ? [{ id: "manifest" as const, label: "资源对账清单" }] : []),
    ...(cmdbEnabled ? [{ id: "workspace" as const, label: "台账分配范围" }] : []),
  ]

  return (
    <div className="min-h-0 flex-1 space-y-4 overflow-auto p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <PageTabs
          ariaLabel="Agent 概览视图"
          value={subTab}
          onChange={changeSubTab}
          items={items}
        />
        {monitorEnabled && onOpenResourceMonitor && (
          <Button type="button" size="sm" variant="ghost" onClick={onOpenResourceMonitor}>
            查看资源监控
          </Button>
        )}
      </div>

      {subTab === "summary" && (
        <div className="space-y-4">
          <CurrentMachineSection selected={target} />
          {targetPlugins.length > 0 && (
            <section aria-label="目标场景扩展" className="card-surface flex flex-wrap items-center justify-between gap-2 p-4">
              <span className="text-xs font-medium text-muted-foreground">目标场景扩展页签</span>
              <div className="flex flex-wrap gap-1.5">
                {targetPlugins.map((p) => (
                  <Button
                    key={p.id}
                    size="sm"
                    variant="default"
                    onClick={() => onSelectTab(`plugin:${p.id}` as TargetTab)}
                  >
                    {p.manifest?.ui.slots.target_tab?.label || p.name}
                  </Button>
                ))}
              </div>
            </section>
          )}
          {!demo && (
            <TargetManifestCard
              key={target.key}
              target={target}
              mode="summary"
              onOpenAssets={() => changeSubTab("manifest")}
            />
          )}
          <NodeInfoCard
            key={`node-info-${target.key}`}
            session={session}
            target={target}
            sessionId={sessionId}
            online={online}
            onRunningChange={onRunningChange}
          />
        </div>
      )}

      {subTab === "manifest" && !demo && (
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            对比该 Agent 现场观测到的资源（集群、命名空间、容器、站点、数据库等）与平台台账登记状态，支持重新同步。
            {isAdmin
              ? "平台管理员可将「已发现未登记」资源一键登记至台账。"
              : "当前账号可只读核对实时观测差异；将新发现资源登记到台账需要平台管理员权限。"}
          </p>
          <TargetManifestCard key={`${target.key}-full`} target={target} mode="full" />
        </div>
      )}

      {subTab === "workspace" && cmdbEnabled && (
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            展示直接关联或平台显式分配至当前 Agent 的机器、集群与业务系统范围；跨 Agent 批量分配可在「平台管理 → Agent管理」维护。
          </p>
          <WorkspaceScopeSection
            key={`${target.key}-scope`}
            target={target}
            manage={false}
            allowManageToggle={isAdmin}
          />
        </div>
      )}
    </div>
  )
}
