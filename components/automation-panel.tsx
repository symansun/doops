"use client"

import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { BackgroundTasksPanel } from "./background-tasks-panel"
import { Badge, Button, Card, CardContent, Inline, Stack } from "./ui"

export function AutomationPanel({
  session,
  targets,
  selectedTarget,
  onSelectTarget,
  onOpenInspection,
}: {
  session: Session
  targets: Target[]
  selectedTarget: Target | null
  onSelectTarget?: (target: Target) => void
  onOpenInspection?: () => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const onlineTargets = targets.filter((t) => t.status !== "offline")

  return (
    <section aria-label="后台任务" className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">后台任务</h1>
            <Badge variant={isAdmin ? "primary" : "outline"}>
              {isAdmin ? "全局权限" : "本人授权范围"}
            </Badge>
            {selectedTarget && (
              <Badge variant="outline">
                当前目标：{selectedTarget.cluster}/{selectedTarget.instance}
              </Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            管理当前目标 Agent 上由 Gateway 托管的 <code className="font-mono text-xs">exec --bg</code> 后台运行任务；全局定时巡检与安全巡检 MCP 请前往独立的「智能巡检」功能。
          </p>
        </div>
        {onOpenInspection && (
          <Button size="sm" variant="ghost" onClick={onOpenInspection}>
            前往智能巡检
          </Button>
        )}
      </header>

      {selectedTarget ? (
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
