"use client"

import { useEffect, useState } from "react"
import type { Session, Target } from "@/lib/client"
import { fetchMonitorSchedules } from "@/lib/monitor-engine"
import { fetchPlatformSkills } from "@/lib/skills-registry"
import { InspectionCenterPanel } from "./inspection-center-panel"
import { Badge, Button, Inline, PageTabs } from "./ui"

type InspectionWorkspaceTab = "runs" | "schedules"

export function InspectionWorkspacePanel({
  session,
  targets,
  initialRunId,
  onRunIdChange,
  onOpenSituation,
  onOpenSkills,
  onOpenMcp,
}: {
  session: Session
  targets: Target[]
  initialRunId?: string
  onRunIdChange?: (runId: string) => void
  onOpenSituation?: () => void
  onOpenSkills?: () => void
  onOpenMcp?: () => void
}) {
  const [subTab, setSubTab] = useState<InspectionWorkspaceTab>("runs")
  const [selectedRunId, setSelectedRunId] = useState(initialRunId || "")
  const [skillCounts, setSkillCounts] = useState({ enabled: 0, total: 0 })
  const [scheduleCount, setScheduleCount] = useState<number | null>(null)

  useEffect(() => {
    if (initialRunId) {
      setSelectedRunId(initialRunId)
      setSubTab("runs")
    }
  }, [initialRunId])

  useEffect(() => {
    const ctrl = new AbortController()
    Promise.all([
      fetchPlatformSkills(session, {}, ctrl.signal).catch(() => null),
      fetchMonitorSchedules(session, ctrl.signal).catch(() => null),
    ]).then(([skillRes, schedRes]) => {
      if (ctrl.signal.aborted) return
      if (skillRes?.skills) {
        setSkillCounts({
          enabled: skillRes.skills.filter((s) => s.enabled).length,
          total: skillRes.skills.length,
        })
      }
      if (schedRes?.schedules) {
        setScheduleCount(schedRes.schedules.length)
      }
    })
    return () => ctrl.abort()
  }, [session])

  return (
    <section aria-label="智能巡检" className="space-y-4">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 space-y-0.5">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">智能巡检</h1>
            {skillCounts.total > 0 && (
              <Badge variant="outline">
                Skill {skillCounts.enabled}/{skillCounts.total}
              </Badge>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            关联 Skill 与提示词（或任意命令）发起即时或定时巡检，查看 Markdown / JSON 可视化产物。
          </p>
        </div>
        {(onOpenSkills || onOpenMcp || onOpenSituation) && (
          <Inline gap={2}>
            {onOpenSkills && (
              <Button size="sm" onClick={onOpenSkills}>
                Skill 管理
              </Button>
            )}
            {onOpenMcp && (
              <Button size="sm" onClick={onOpenMcp}>
                MCP 管理
              </Button>
            )}
            {onOpenSituation && (
              <Button size="sm" variant="ghost" onClick={onOpenSituation}>
                态势感知
              </Button>
            )}
          </Inline>
        )}
      </header>

      <PageTabs
        ariaLabel="智能巡检视图"
        value={subTab}
        onChange={(next) => setSubTab(next as InspectionWorkspaceTab)}
        items={[
          { id: "runs", label: "执行与记录" },
          { id: "schedules", label: scheduleCount != null ? `定时计划 (${scheduleCount})` : "定时计划" },
        ]}
      />

      <InspectionCenterPanel
        session={session}
        targets={targets}
        mode={subTab}
        initialRunId={subTab === "runs" ? selectedRunId || undefined : undefined}
        onSelectRun={(runId) => {
          setSelectedRunId(runId)
          onRunIdChange?.(runId)
          if (runId && subTab !== "runs") setSubTab("runs")
        }}
        onClearInitialRun={() => {
          setSelectedRunId("")
          onRunIdChange?.("")
        }}
        onScheduleCountChange={setScheduleCount}
        onOpenSkills={onOpenSkills}
      />
    </section>
  )
}
