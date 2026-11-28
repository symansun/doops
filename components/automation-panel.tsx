"use client"

import { useCallback, useEffect, useState } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import type { AutomationSubTab } from "@/lib/console-views"
import {
  backgroundTaskTabBadge,
  scheduleAppliesToTarget,
  summarizeBackgroundTasks,
  type TaskStatusSummary,
} from "@/lib/automation"
import { listBackgroundTasks } from "@/lib/background-tasks"
import { fetchMonitorSchedules } from "@/lib/monitor-engine"
import { BackgroundTasksPanel } from "./background-tasks-panel"
import { MonitorTaskWorkspace } from "./monitor-task-workspace"
import { Badge, Button, Card, CardContent, Inline, PageTabs, Stack } from "./ui"

export type AutomationTab = AutomationSubTab

export function AutomationPanel({
  session,
  targets,
  selectedTarget,
  sessionId,
  activeTab,
  initialRunId,
  focusedScheduleId,
  onTabChange,
  onRunIdChange,
  onSelectTarget,
  onOpenSkills,
  onExplainTask,
}: {
  session: Session
  targets: Target[]
  selectedTarget: Target | null
  sessionId?: string
  activeTab?: AutomationTab
  initialRunId?: string
  focusedScheduleId?: string
  onTabChange?: (tab: AutomationTab) => void
  onRunIdChange?: (runId: string) => void
  onSelectTarget?: (target: Target) => void
  onOpenSkills?: () => void
  onExplainTask?: (prompt: string) => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const onlineTargets = targets.filter((t) => t.status !== "offline")
  const [internalTab, setInternalTab] = useState<AutomationTab>(activeTab ?? (initialRunId ? "runs" : "schedules"))
  const [taskSummary, setTaskSummary] = useState<TaskStatusSummary | null>(null)
  const [scheduleCounts, setScheduleCounts] = useState<{ targetCount: number; totalCount: number } | null>(null)

  const currentTab: AutomationTab = activeTab ?? internalTab
  const selectSubTab = useCallback(
    (next: AutomationTab) => {
      if (activeTab === undefined) setInternalTab(next)
      onTabChange?.(next)
      if (next === "bg") onRunIdChange?.("")
    },
    [activeTab, onTabChange, onRunIdChange],
  )

  useEffect(() => {
    if (focusedScheduleId) {
      if (activeTab === undefined) setInternalTab("schedules")
      onTabChange?.("schedules")
    }
  }, [focusedScheduleId, activeTab, onTabChange])

  useEffect(() => {
    const ctrl = new AbortController()
    fetchMonitorSchedules(session, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        const list = res.schedules || []
        const targetCount = list.filter((s) => scheduleAppliesToTarget(s, selectedTarget)).length
        setScheduleCounts({ targetCount, totalCount: list.length })
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setScheduleCounts(null)
      })
    return () => ctrl.abort()
  }, [session, selectedTarget])

  // Preload lightweight background task summary for the selected target so Tab 3
  // shows a highlighted 「运行中 N」 badge immediately when active commands exist,
  // and stays clean (no misleading historical count) when idle.
  useEffect(() => {
    setTaskSummary(null)
    if (!selectedTarget) return
    let cancelled = false
    listBackgroundTasks(session, selectedTarget)
      .then((list) => {
        if (!cancelled) setTaskSummary(summarizeBackgroundTasks(list))
      })
      .catch(() => {
        if (!cancelled) setTaskSummary(null)
      })
    return () => {
      cancelled = true
    }
  }, [session, selectedTarget?.key])

  const schedulesTabLabel = (
    <span className="inline-flex items-center gap-1.5">
      <span>定时任务</span>
      {scheduleCounts !== null && (
        <span className="font-mono text-2xs opacity-80">
          ({selectedTarget ? scheduleCounts.targetCount : scheduleCounts.totalCount})
        </span>
      )}
    </span>
  )

  const bgBadge = backgroundTaskTabBadge(taskSummary)
  const bgTabLabel = (
    <span className="inline-flex items-center gap-1.5">
      <span>一次性后台执行</span>
      {bgBadge && <Badge variant={bgBadge.variant}>{bgBadge.label}</Badge>}
    </span>
  )

  return (
    <section aria-label="自动化" className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">自动化</h1>
            <Badge variant={isAdmin ? "primary" : "outline"}>
              {isAdmin ? "全局权限" : "本人授权范围"}
            </Badge>
            {selectedTarget && (
              <Badge variant={selectedTarget.status === "offline" ? "warning" : "outline"} className="font-mono">
                当前目标：{selectedTarget.cluster}/{selectedTarget.instance}
                {selectedTarget.status === "offline" ? " · 离线" : ""}
              </Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            统一管理定时任务与 Skill 巡检、查看全部执行记录与报告，并托管当前目标的一次性后台命令（<code className="font-mono text-xs">exec --bg</code>）。
          </p>
        </div>
        {onOpenSkills && (
          <Button size="sm" variant="ghost" onClick={onOpenSkills}>
            Skill 管理
          </Button>
        )}
      </header>

      <PageTabs
        ariaLabel="自动化视图"
        value={currentTab}
        onChange={(next) => selectSubTab(next as AutomationTab)}
        items={[
          { id: "schedules", label: schedulesTabLabel },
          { id: "runs", label: "执行记录" },
          { id: "bg", label: bgTabLabel },
        ]}
      />

      {currentTab === "schedules" || currentTab === "runs" ? (
        <MonitorTaskWorkspace
          session={session}
          targets={targets}
          selectedTarget={selectedTarget}
          activeTab={currentTab}
          initialRunId={initialRunId}
          focusedScheduleId={focusedScheduleId}
          onSelectRun={onRunIdChange}
          onClearInitialRun={() => onRunIdChange?.("")}
          onScheduleCountChange={setScheduleCounts}
          onOpenSkills={onOpenSkills}
        />
      ) : selectedTarget ? (
        <BackgroundTasksPanel
          session={session}
          target={selectedTarget}
          sessionId={sessionId}
          embedded
          onSummaryChange={setTaskSummary}
          onExplainTask={onExplainTask}
          onOpenSchedules={() => selectSubTab("schedules")}
        />
      ) : (
        <Stack gap={3}>
          <Card>
            <CardContent className="space-y-3 p-5">
              <div className="space-y-1">
                <h2 className="text-base font-semibold text-foreground">选择一个 Agent 管理一次性后台执行</h2>
                <p className="text-sm text-muted-foreground">
                  一次性后台执行承接当前目标上通过 CLI <code className="font-mono text-xs">doops exec --bg</code> 或控制台提交的长耗时命令（如压测、备份、迁移），按目标 Agent 隔离展示。请选择一个目标：
                </p>
              </div>
              {targets.length === 0 ? (
                <p className="text-sm text-muted-foreground">当前没有可用的 Agent 目标。</p>
              ) : (
                <Inline gap={2} className="flex-wrap">
                  {(onlineTargets.length ? onlineTargets : targets).map((t) => (
                    <Button
                      key={t.key}
                      size="sm"
                      variant={t.status !== "offline" ? "primary" : "default"}
                      onClick={() => onSelectTarget?.(t)}
                    >
                      <span className="font-mono">{t.cluster}/{t.instance}</span>
                      {t.status === "offline" ? "（离线）" : ""}
                    </Button>
                  ))}
                </Inline>
              )}
            </CardContent>
          </Card>
        </Stack>
      )}
    </section>
  )
}
