"use client"

import { useEffect, useState } from "react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import {
  getInspectionMcpServices,
  subscribeInspectionMcpServices,
} from "@/lib/inspection-mcp-store"
import { fetchPlatformSkills } from "@/lib/skills-registry"
import { AssetInspectionPanel } from "./asset-inspection-panel"
import { InspectionCenterPanel } from "./inspection-center-panel"
import { InspectionMcpPanel } from "./inspection-mcp-panel"
import { Badge, Button, Inline, PageTabs } from "./ui"

type InspectionWorkspaceTab = "runs" | "mcp" | "compliance"

export function InspectionWorkspacePanel({
  session,
  targets,
  initialRunId,
  onRunIdChange,
  onOpenSituation,
  onOpenSkills,
}: {
  session: Session
  targets: Target[]
  initialRunId?: string
  onRunIdChange?: (runId: string) => void
  onOpenSituation?: () => void
  onOpenSkills?: () => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const [subTab, setSubTab] = useState<InspectionWorkspaceTab>("runs")
  const [selectedRunId, setSelectedRunId] = useState(initialRunId || "")
  const [skillCounts, setSkillCounts] = useState<{ enabled: number; total: number; custom: number }>({
    enabled: 0,
    total: 0,
    custom: 0,
  })
  const [mcpCounts, setMcpCounts] = useState(() => {
    const list = getInspectionMcpServices()
    return { enabled: list.filter((s) => s.enabled).length, total: list.length }
  })

  useEffect(() => {
    if (initialRunId) {
      setSelectedRunId(initialRunId)
      setSubTab("runs")
    }
  }, [initialRunId])

  useEffect(() => {
    const ctrl = new AbortController()
    fetchPlatformSkills(session, {}, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        const list = res.skills || []
        setSkillCounts({
          enabled: list.filter((s) => s.enabled).length,
          total: list.length,
          custom: list.filter((s) => s.scope === "custom").length,
        })
      })
      .catch(() => {})
    return () => ctrl.abort()
  }, [session])

  useEffect(() => {
    const sync = () => {
      const list = getInspectionMcpServices()
      setMcpCounts({ enabled: list.filter((s) => s.enabled).length, total: list.length })
    }
    sync()
    return subscribeInspectionMcpServices(sync)
  }, [])

  return (
    <section aria-label="智能巡检" className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">智能巡检</h1>
            <Badge variant={isAdmin ? "primary" : "outline"}>
              {isAdmin ? "全局巡检权限" : "本人授权范围"}
            </Badge>
            {skillCounts.total > 0 && (
              <Badge variant="primary">
                巡检 Skill {skillCounts.enabled}/{skillCounts.total} 已启用
                {skillCounts.custom > 0 ? `（含 ${skillCounts.custom} 个自定义）` : ""}
              </Badge>
            )}
            <Badge variant={mcpCounts.enabled >= 5 ? "success" : "warning"}>
              安全巡检 MCP {mcpCounts.enabled}/{mcpCounts.total} 已启用
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            巡检由系统驱动 Skill（系统内置 Skill + 自定义上传 Skill）统一执行，同时管理安全巡检 MCP 服务开关与周度合规修复复验台账。
          </p>
        </div>
        {(onOpenSkills || onOpenSituation) && (
          <Inline gap={2} className="flex-wrap">
            {onOpenSkills && (
              <Button size="sm" onClick={onOpenSkills}>
                管理 / 上传 Skill
              </Button>
            )}
            {onOpenSituation && (
              <Button size="sm" variant="ghost" onClick={onOpenSituation}>
                前往态势感知
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
          { id: "runs", label: "Skill 驱动巡检与记录" },
          { id: "mcp", label: `安全巡检 MCP 服务 (${mcpCounts.enabled}/${mcpCounts.total})` },
          { id: "compliance", label: "周度合规与修复台账" },
        ]}
      />

      {subTab === "runs" ? (
        <InspectionCenterPanel
          session={session}
          targets={targets}
          initialRunId={selectedRunId || undefined}
          onClearInitialRun={() => {
            setSelectedRunId("")
            onRunIdChange?.("")
          }}
          onOpenSkills={onOpenSkills}
        />
      ) : subTab === "mcp" ? (
        <InspectionMcpPanel
          session={session}
          onOpenRunDetail={(runId) => {
            setSelectedRunId(runId)
            onRunIdChange?.(runId)
            setSubTab("runs")
          }}
        />
      ) : (
        <AssetInspectionPanel session={session} targets={targets} />
      )}
    </section>
  )
}
