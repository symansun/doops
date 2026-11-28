"use client"

import { useMemo, useState } from "react"
import type { MonitorAssetFocus } from "@/lib/agent-monitor"
import type { Session, Target } from "@/lib/client"
import type { ResourceMonitorSubTab } from "@/lib/console-views"
import { targetModeLabel } from "@/lib/gateway"
import { AgentMonitor } from "./asset-monitor-panel"
import { SystemResourceMonitor } from "./system-resource-monitor"
import { Badge, Button, Inline, PageTabs, Select, Stack } from "./ui"

function resolveTargetByKey(
  key: string | undefined,
  targets: Target[],
  selectedTarget?: Target | null,
): Target | null {
  if (key) {
    const matched = targets.find((t) => t.key === key)
    if (matched) return matched
    const slash = key.indexOf("/")
    if (slash > 0 && slash < key.length - 1) {
      return {
        key,
        cluster: key.slice(0, slash),
        instance: key.slice(slash + 1),
        status: "offline",
        sessions: [],
      }
    }
  }
  if (selectedTarget) {
    return targets.find((t) => t.key === selectedTarget.key) ?? selectedTarget
  }
  return targets.find((t) => t.status !== "offline") ?? targets[0] ?? null
}

export function ResourceMonitorHub({
  session,
  targets,
  selectedTarget,
  subTab,
  targetKey,
  clusterFilter,
  onSubTabChange,
  onTargetKeyChange,
  onOpenInspection,
  onOpenSituation,
  onOpenPlatformMonitor,
  onOpenSkillCenter,
  onDeployAgent,
}: {
  session: Session
  targets: Target[]
  selectedTarget?: Target | null
  subTab: ResourceMonitorSubTab
  targetKey?: string
  clusterFilter?: string
  onSubTabChange: (next: ResourceMonitorSubTab, nextTargetKey?: string) => void
  onTargetKeyChange: (nextTargetKey: string) => void
  onOpenInspection?: (runId?: string) => void
  onOpenSituation?: () => void
  onOpenPlatformMonitor?: () => void
  onOpenSkillCenter?: () => void
  onDeployAgent?: () => void
}) {
  const [drillFocus, setDrillFocus] = useState<{ targetKey: string; focus: MonitorAssetFocus } | null>(null)

  const activeTarget = useMemo(
    () => resolveTargetByKey(targetKey, targets, selectedTarget),
    [targetKey, targets, selectedTarget],
  )

  const activeFocus =
    activeTarget && drillFocus && drillFocus.targetKey === activeTarget.key
      ? drillFocus.focus
      : null

  const selectableTargets = useMemo(() => {
    if (!activeTarget) return targets
    if (targets.some((t) => t.key === activeTarget.key)) return targets
    return [activeTarget, ...targets]
  }, [activeTarget, targets])

  const onlineCount = targets.filter((t) => t.status !== "offline").length

  return (
    <section aria-label="资源监控" className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">资源监控</h1>
            <Badge variant="outline">全网汇总与单目标时序</Badge>
            <Badge variant="outline">
              在线 {onlineCount} / 可见 {targets.length} 台 Agent
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            统一查看全网主机、集群、容器、应用与数据库的实时资源水位，以及单目标设备级曲线、阈值告警规则与 Skill 巡检趋势。
          </p>
        </div>

        <Inline gap={2} className="flex-wrap">
          {onOpenSituation && (
            <Button size="sm" variant="ghost" onClick={onOpenSituation}>
              态势感知
            </Button>
          )}
          {onOpenInspection && (
            <Button size="sm" variant="ghost" onClick={() => onOpenInspection()}>
              智能巡检
            </Button>
          )}
          {onOpenPlatformMonitor && (
            <Button size="sm" variant="ghost" onClick={onOpenPlatformMonitor}>
              Agent 运行监控
            </Button>
          )}
        </Inline>
      </header>

      <PageTabs
        ariaLabel="资源监控视图"
        value={subTab}
        onChange={(next) => onSubTabChange(next as ResourceMonitorSubTab, activeTarget?.key)}
        items={[
          { id: "fleet", label: "全网资源总览" },
          {
            id: "target",
            label: activeTarget
              ? `单目标时序与告警 (${activeTarget.instance})`
              : "单目标时序与告警",
          },
        ]}
      />

      {subTab === "fleet" ? (
        <SystemResourceMonitor
          session={session}
          targets={targets}
          clusterFilter={clusterFilter}
          onOpenTarget={(cluster, instance, _tab, focus) => {
            const nextKey = `${cluster}/${instance}`
            setDrillFocus(focus ? { targetKey: nextKey, focus } : null)
            onSubTabChange("target", nextKey)
          }}
          onOpenRunDetail={onOpenInspection ? (runId) => onOpenInspection(runId) : undefined}
        />
      ) : (
        <Stack gap={4}>
          <div className="card-surface flex flex-wrap items-center justify-between gap-3 p-3">
            <div className="flex flex-wrap items-center gap-2">
              <label className="flex items-center gap-2 text-xs text-muted-foreground">
                <span>监控目标 Agent</span>
                <Select
                  aria-label="选择监控目标 Agent"
                  scale="sm"
                  className="w-64 font-mono"
                  value={activeTarget?.key ?? ""}
                  disabled={selectableTargets.length === 0}
                  onChange={(e) => {
                    setDrillFocus(null)
                    onTargetKeyChange(e.target.value)
                  }}
                >
                  {selectableTargets.length === 0 ? (
                    <option value="">暂无可见 Agent</option>
                  ) : (
                    selectableTargets.map((t) => (
                      <option key={t.key} value={t.key}>
                        {t.cluster}/{t.instance}
                        {t.status === "offline" ? " · 离线（历史曲线）" : " · 在线"}
                      </option>
                    ))
                  )}
                </Select>
              </label>
              {activeTarget && (
                <>
                  <Badge variant={activeTarget.status === "offline" ? "warning" : "success"}>
                    {activeTarget.status === "offline" ? "离线" : "在线"}
                  </Badge>
                  <Badge variant="outline">{targetModeLabel(activeTarget.mode)}</Badge>
                </>
              )}
            </div>
            <span className="text-xs text-muted-foreground">
              离线目标仍可查阅历史时序曲线、告警记录与已配置的阈值规则；切换此处的目标不影响工作区会话。
            </span>
          </div>

          {activeTarget ? (
            <AgentMonitor
              key={activeTarget.key}
              session={session}
              target={activeTarget}
              clusterId={clusterFilter}
              initialFocus={activeFocus}
              onOpenInspection={onOpenInspection ? () => onOpenInspection() : undefined}
              onOpenSkillCenter={onOpenSkillCenter}
            />
          ) : (
            <div className="card-surface space-y-3 py-10 text-center text-sm text-muted-foreground">
              <p>当前账号暂无可见的 Agent 目标。接入或获得目标授权后即可查看设备级时序曲线并配置阈值告警规则。</p>
              {onDeployAgent && (
                <div>
                  <Button size="sm" variant="primary" onClick={onDeployAgent}>
                    首次接入 Agent
                  </Button>
                </div>
              )}
            </div>
          )}
        </Stack>
      )}
    </section>
  )
}
