"use client"

import { useEffect, useState } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { BackgroundTasksPanel } from "./background-tasks-panel"
import { InspectionCenterPanel } from "./inspection-center-panel"
import { Badge, Button, Card, CardContent, Inline, PageTabs, Stack } from "./ui"

export type AutomationTab = "inspections" | "background"

export function AutomationPanel({
  session,
  targets,
  selectedTarget,
  initialTab = "inspections",
  onSelectTarget,
}: {
  session: Session
  targets: Target[]
  selectedTarget: Target | null
  initialTab?: AutomationTab
  onSelectTarget?: (target: Target) => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const [subTab, setSubTab] = useState<AutomationTab>(initialTab)

  useEffect(() => {
    setSubTab(initialTab)
  }, [initialTab])

  const onlineTargets = targets.filter((t) => t.status !== "offline")

  return (
    <section aria-label="自动化" className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">自动化</h1>
            <Badge variant={isAdmin ? "primary" : "outline"}>
              {isAdmin ? "全局巡检权限" : "本人授权范围"}
            </Badge>
            {selectedTarget && (
              <Badge variant="outline">
                当前目标：{selectedTarget.cluster}/{selectedTarget.instance}
              </Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            统一管理定时策略巡检与执行记录（虚机 / SSH / K8s / 容器 / 应用 / 数据库），以及 Gateway 托管的后台运行任务（<code className="font-mono text-xs">exec --bg</code>）。
          </p>
        </div>
      </header>

      <PageTabs
        ariaLabel="自动化视图"
        value={subTab}
        onChange={(next) => setSubTab(next as AutomationTab)}
        items={[
          { id: "inspections", label: "定时巡检与记录" },
          {
            id: "background",
            label: selectedTarget
              ? `后台运行任务 (${selectedTarget.cluster}/${selectedTarget.instance})`
              : "后台运行任务",
          },
        ]}
      />

      {subTab === "inspections" ? (
        <InspectionCenterPanel session={session} targets={targets} />
      ) : selectedTarget ? (
        <BackgroundTasksPanel session={session} target={selectedTarget} embedded />
      ) : (
        <Stack gap={3}>
          <Card>
            <CardContent className="space-y-3 p-5">
              <div className="space-y-1">
                <h2 className="text-base font-semibold text-foreground">选择一个 Agent 查看后台运行任务</h2>
                <p className="text-sm text-muted-foreground">
                  Gateway 托管的 <code className="font-mono text-xs">exec --bg</code> 后台任务按目标 Agent 隔离展示。请选择一个目标查看本人提交的任务列表与实时输出：
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
                      {t.cluster}/{t.instance}
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
