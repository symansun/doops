"use client"

import { useState } from "react"
import { Puzzle } from "lucide-react"
import type { Session } from "@/lib/client"
import { mutatePluginState } from "@/lib/plugins/client"
import { formatScenarioName } from "@/lib/plugins/resolver"
import type { PluginOwnerRef, PluginStateResponse } from "@/lib/plugins/types"
import { Badge, Button, Card, CardContent, StatusBanner } from "@/components/ui"

export function PluginDisabledState({
  session,
  plugin,
  pluginState,
  isAdmin,
  onEnabled,
  onOpenPluginCenter,
  onGoHome,
}: {
  session: Session
  plugin: PluginOwnerRef
  pluginState: PluginStateResponse
  isAdmin: boolean
  onEnabled: () => void
  onOpenPluginCenter: () => void
  onGoHome: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const isCustomL2 = plugin.tier === "declarative" && plugin.scope === "custom"
  const skillUnderlyingDisabled = plugin.tier === "declarative" && plugin.skill_enabled === false
  const canOneClickEnable =
    isAdmin &&
    pluginState.supported &&
    !isCustomL2 &&
    !skillUnderlyingDisabled &&
    (plugin.tier === "builtin" || plugin.scope === "system")

  async function enableNow() {
    if (!canOneClickEnable || busy) return
    setBusy(true)
    setError("")
    const res = await mutatePluginState(session, {
      action: "set_plugin",
      plugin_id: plugin.id,
      enabled: true,
      expected_revision: pluginState.revision,
    })
    setBusy(false)
    if (!res.ok) {
      setError(res.message)
      return
    }
    onEnabled()
  }

  return (
    <section aria-label="插件已停用" className="min-h-0 flex-1 overflow-auto p-6">
      <Card className="mx-auto max-w-xl">
        <CardContent className="space-y-4 p-6">
          <div className="flex items-start gap-3">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-muted/50 text-muted-foreground">
              <Puzzle className="size-5" aria-hidden="true" />
            </div>
            <div className="min-w-0 space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-base font-semibold text-foreground">「{plugin.name}」插件当前已停用</h2>
                <Badge variant="outline">{formatScenarioName(pluginState.scenario_id)}</Badge>
                {plugin.tier === "declarative" && <Badge variant="outline">L2 声明式插件</Badge>}
              </div>
              <p className="text-sm text-muted-foreground">{plugin.description}</p>
            </div>
          </div>

          <p className="text-xs leading-relaxed text-muted-foreground">
            {isCustomL2
              ? "这是由你个人上传的自定义 Skill 插件，请在「插件中心 → Skill 能力包」中重新启用对应 Skill。"
              : skillUnderlyingDisabled
                ? "该插件关联的系统 Skill 当前处于停用状态，请在「插件中心 → Skill 能力包」中启用对应 Skill 后恢复页面。"
                : "当前平台场景已隐藏该前端功能模块。历史台账、巡检记录、凭据及 CLI 命令均完整保留，不会因隐藏界面而被删除。"}
          </p>

          {error && <StatusBanner tone="error">{error}</StatusBanner>}

          <div className="flex flex-wrap gap-2">
            {canOneClickEnable && (
              <Button size="sm" disabled={busy} onClick={enableNow}>
                {busy ? "正在启用…" : `一键启用「${plugin.name}」`}
              </Button>
            )}
            <Button size="sm" variant={canOneClickEnable ? "default" : "primary"} onClick={onOpenPluginCenter}>
              前往插件中心
            </Button>
            <Button size="sm" variant="ghost" onClick={onGoHome}>
              返回首页
            </Button>
          </div>
        </CardContent>
      </Card>
    </section>
  )
}
