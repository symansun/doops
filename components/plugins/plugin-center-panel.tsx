"use client"

import { useMemo, useRef, useState, type ChangeEvent } from "react"
import {
  Check,
  Layers,
  Plug,
  Puzzle,
  RefreshCw,
  RotateCcw,
  ShieldAlert,
  Sparkles,
  Trash2,
  Upload,
} from "lucide-react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import type { PluginCenterSubTab } from "@/lib/console-views"
import { formatPluginOriginLabel } from "@/lib/plugin-workbench"
import { mutatePluginState } from "@/lib/plugins/client"
import {
  deletePlatformSkill,
  extractSkillZipFile,
  syncSkillsToAgents,
  upsertPlatformSkill,
} from "@/lib/skills-registry"
import {
  activeOverrideCount,
  buildDefaultPluginState,
  formatPluginName,
  formatScenarioName,
} from "@/lib/plugins/resolver"
import {
  DEFAULT_PLUGIN_SCENARIO_ID,
  PLUGIN_CATEGORY_LABELS,
  type PluginMutationDiff,
  type PluginMutationRequest,
  type PluginStateResponse,
  type PluginView,
} from "@/lib/plugins/types"
import { SkillRegistryPanel } from "@/components/skill-registry-panel"
import { MCPRegistryPanel } from "@/components/mcp-registry-panel"
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  StatusBanner,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui"

type PendingPreview = {
  title: string
  description: string
  request: PluginMutationRequest
  diff: PluginMutationDiff
}

function depBadgeVariant(state: "ready" | "disabled" | "missing"): "success" | "warning" | "outline" {
  if (state === "ready") return "success"
  if (state === "disabled") return "warning"
  return "outline"
}

function depStateLabel(state: "ready" | "disabled" | "missing"): string {
  if (state === "ready") return "已就绪"
  if (state === "disabled") return "已停用"
  return "未安装"
}

export function PluginCenterPanel({
  session,
  targets,
  subTab,
  initialSkillSlug,
  pluginState,
  pluginStatus,
  pluginError,
  onSubTabChange,
  onRefreshPlugins,
  onOpenInspection,
  onOpenPlugin,
}: {
  session: Session
  targets: Target[]
  subTab: PluginCenterSubTab
  initialSkillSlug?: string
  pluginState: PluginStateResponse | null
  pluginStatus: "loading" | "ready" | "unsupported" | "error"
  pluginError: string
  onSubTabChange: (next: PluginCenterSubTab) => void
  onRefreshPlugins: () => Promise<void>
  onOpenInspection?: (runId?: string) => void
  onOpenPlugin?: (pluginId: string) => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const state = pluginState ?? buildDefaultPluginState({ supported: pluginStatus !== "unsupported" })
  const canMutate = isAdmin && state.supported

  const [busyKey, setBusyKey] = useState("")
  const [actionError, setActionError] = useState("")
  const [conflictNotice, setConflictNotice] = useState(false)
  const [successNotice, setSuccessNotice] = useState("")
  const [preview, setPreview] = useState<PendingPreview | null>(null)
  const [pendingUninstall, setPendingUninstall] = useState<PluginView | null>(null)
  const zipInputRef = useRef<HTMLInputElement>(null)

  const overrideCount = useMemo(() => activeOverrideCount(state), [state])
  const enabledCount = useMemo(() => state.plugins.filter((p) => p.effective).length, [state.plugins])
  const builtinPlugins = useMemo(() => state.plugins.filter((p) => p.tier === "builtin"), [state.plugins])
  const declarativePlugins = useMemo(() => state.plugins.filter((p) => p.tier === "declarative"), [state.plugins])

  async function requestPreview(title: string, description: string, request: PluginMutationRequest) {
    if (!canMutate || busyKey) return
    setBusyKey(title)
    setActionError("")
    setConflictNotice(false)
    setSuccessNotice("")

    const dryReq = { ...request, dry_run: true } as PluginMutationRequest
    const res = await mutatePluginState(session, dryReq)
    setBusyKey("")
    if (!res.ok) {
      if (res.conflict) {
        setConflictNotice(true)
        setActionError(res.message)
      } else {
        setActionError(res.message)
      }
      return
    }
    setPreview({
      title,
      description,
      request: { ...request, dry_run: false } as PluginMutationRequest,
      diff: res.data.diff,
    })
  }

  async function commitMutation(request: PluginMutationRequest, successText: string) {
    if (!canMutate || busyKey) return
    setBusyKey("commit")
    setActionError("")
    setConflictNotice(false)
    setSuccessNotice("")

    const res = await mutatePluginState(session, request)
    setBusyKey("")
    if (!res.ok) {
      if (res.conflict) setConflictNotice(true)
      setActionError(res.message)
      return
    }
    setPreview(null)
    setSuccessNotice(successText)
    await onRefreshPlugins()
  }

  function handleScenarioClick(scenarioId: string, scenarioName: string) {
    if (!canMutate) return
    if (scenarioId === state.scenario_id && !state.customized) return
    void requestPreview(
      `切换至「${scenarioName}」场景`,
      "切换场景将按该预设重置所有内置插件开关；历史台账、巡检记录、凭据和定时计划均保留不删除。",
      {
        action: "apply_scenario",
        scenario_id: scenarioId,
        expected_revision: state.revision,
      },
    )
  }

  function handleTogglePlugin(plugin: PluginView) {
    if (!canMutate) return
    const nextEnabled = !plugin.effective
    if (!nextEnabled) {
      void requestPreview(
        `隐藏「${plugin.name}」插件`,
        "隐藏后侧栏与对应页面入口将收起，历史数据与 CLI 执行能力保持不受影响。",
        {
          action: "set_plugin",
          plugin_id: plugin.id,
          enabled: false,
          expected_revision: state.revision,
        },
      )
      return
    }
    void commitMutation(
      {
        action: "set_plugin",
        plugin_id: plugin.id,
        enabled: true,
        expected_revision: state.revision,
      },
      `已启用「${plugin.name}」插件`,
    )
  }

  function handleResetDefaults() {
    if (!canMutate) return
    void requestPreview(
      "恢复默认全功能模式",
      "将场景重置为「全功能模式」并清除全部自定义插件覆盖项。",
      {
        action: "reset",
        expected_revision: state.revision,
      },
    )
  }

  async function handleClearStale(staleId: string) {
    if (!canMutate || busyKey) return
    await commitMutation(
      {
        action: "clear_override",
        plugin_id: staleId,
        expected_revision: state.revision,
      },
      `已清理陈旧覆盖项 ${staleId}`,
    )
  }

  async function handlePluginZipSelected(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    event.target.value = ""
    if (!file || !canMutate || busyKey) return
    if (session.demo) {
      setActionError("演示模式下暂不支持安装外部插件包，请连接真实 Gateway 后上传 .zip 插件包。")
      return
    }
    setBusyKey("install-zip")
    setActionError("")
    setConflictNotice(false)
    setSuccessNotice("")
    setPendingUninstall(null)
    try {
      const extracted = await extractSkillZipFile(file)
      const hasManifest = extracted.files.some((f) => f.path.toLowerCase() === "plugin.yaml")
      if (!hasManifest) {
        throw new Error("压缩包根目录缺少 plugin.yaml 声明文件；如需导入纯文本 Skill 请前往「Skill 能力包」页签")
      }
      if (!extracted.suggestedSlug) {
        throw new Error("无法从 SKILL.md 或文件名解析合法的插件标识（slug）")
      }
      await upsertPlatformSkill(session, {
        slug: extracted.suggestedSlug,
        name: extracted.name,
        description: extracted.description,
        scope: "system",
        category: "ops",
        enabled: true,
        skill_md: extracted.skillMD,
        files: extracted.files,
      })
      if (targets.some((t) => t.status !== "offline")) {
        try {
          await syncSkillsToAgents(session, { slug: extracted.suggestedSlug })
        } catch {
          // Best-effort sync; monitor runs also auto-sync on demand.
        }
      }
      await onRefreshPlugins()
      setSuccessNotice(
        `已安装外部插件「${extracted.name}」（skill.${extracted.suggestedSlug}）。如插件声明为按需启用，请在下方点击「启用」挂载工作台；其声明的独立 MCP 服务已自动注册。`,
      )
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "安装插件包失败")
    } finally {
      setBusyKey("")
    }
  }

  async function handleConfirmUninstall(plugin: PluginView) {
    if (!canMutate || session.demo || !plugin.skill_slug || busyKey) return
    setBusyKey(`uninstall:${plugin.id}`)
    setActionError("")
    setConflictNotice(false)
    setSuccessNotice("")
    try {
      await deletePlatformSkill(session, plugin.skill_slug)
      setPendingUninstall(null)
      await onRefreshPlugins()
      setSuccessNotice(`已卸载外部插件「${plugin.name}」（${plugin.id}），并自动清理插件覆盖记录与伴生 MCP 服务。`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "卸载插件失败")
    } finally {
      setBusyKey("")
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-lg font-semibold text-foreground">插件中心</h1>
            <Badge variant="primary">{formatScenarioName(state.scenario_id)}</Badge>
            {state.customized && <Badge variant="warning">已自定义 +{overrideCount}</Badge>}
            <Badge variant="outline">已启用 {enabledCount}/{state.plugins.length} 个功能插件</Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            统一装配控制台场景与前端功能插件，并管理 Agent Skill 能力包与 MCP 工具服务。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {canMutate && !session.demo && (
            <>
              <input
                ref={zipInputRef}
                type="file"
                accept=".zip,application/zip"
                aria-label="选择外部插件压缩包 (.zip)"
                className="sr-only"
                onChange={(e) => void handlePluginZipSelected(e)}
              />
              <Button
                size="sm"
                variant="primary"
                disabled={Boolean(busyKey)}
                onClick={() => zipInputRef.current?.click()}
              >
                <Upload className="size-3.5" aria-hidden="true" />
                {busyKey === "install-zip" ? "正在安装…" : "安装插件包 (.zip)"}
              </Button>
            </>
          )}
          <Button
            size="sm"
            variant="default"
            disabled={pluginStatus === "loading" || Boolean(busyKey)}
            onClick={() => void onRefreshPlugins()}
          >
            <RefreshCw className={`size-3.5 ${pluginStatus === "loading" ? "animate-spin" : ""}`} aria-hidden="true" />
            刷新
          </Button>
          {canMutate && (state.scenario_id !== DEFAULT_PLUGIN_SCENARIO_ID || state.customized) && (
            <Button size="sm" variant="ghost" disabled={Boolean(busyKey)} onClick={handleResetDefaults}>
              <RotateCcw className="size-3.5" aria-hidden="true" />
              恢复全功能默认
            </Button>
          )}
        </div>
      </div>

      <Tabs value={subTab} onValueChange={(val) => onSubTabChange(val as PluginCenterSubTab)}>
        <TabsList aria-label="插件中心分区" className="h-auto flex-wrap">
          <TabsTrigger value="scenarios">
            <Puzzle className="mr-1.5 size-3.5" aria-hidden="true" />
            场景与功能插件
          </TabsTrigger>
          <TabsTrigger value="skills">
            <Sparkles className="mr-1.5 size-3.5" aria-hidden="true" />
            Skill 能力包
          </TabsTrigger>
          <TabsTrigger value="mcp">
            <Plug className="mr-1.5 size-3.5" aria-hidden="true" />
            MCP 工具服务
          </TabsTrigger>
        </TabsList>

        <TabsContent value="scenarios" className="mt-4 space-y-5">
          {session.demo && (
            <StatusBanner tone="info">
              演示模式：你可以在当前会话中切换场景和开关功能插件，刷新页面后将恢复默认。
            </StatusBanner>
          )}

          {!state.supported && (
            <StatusBanner tone="warning">
              当前连接的 Gateway 版本尚未提供 <code>/v1/plugins</code> 接口，控制台已按「全功能模式」兼容展示。请升级 Gateway 后再调整场景与插件配置。
            </StatusBanner>
          )}

          {pluginError && (
            <StatusBanner tone="warning">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span>{pluginError}</span>
                <Button size="sm" variant="default" onClick={() => void onRefreshPlugins()}>
                  重新拉取
                </Button>
              </div>
            </StatusBanner>
          )}

          {!isAdmin && state.supported && (
            <StatusBanner tone="info">
              当前账号为普通用户（只读视图）。场景包与平台级插件开关由平台管理员统一配置；你可以在「Skill 能力包」页签上传和管理个人自定义 Skill。
            </StatusBanner>
          )}

          {actionError && (
            <StatusBanner tone={conflictNotice ? "warning" : "error"}>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span>{actionError}</span>
                {conflictNotice && (
                  <Button
                    size="sm"
                    variant="default"
                    onClick={() => {
                      setConflictNotice(false)
                      setActionError("")
                      setPreview(null)
                      void onRefreshPlugins()
                    }}
                  >
                    拉取最新配置
                  </Button>
                )}
              </div>
            </StatusBanner>
          )}

          {successNotice && <StatusBanner tone="success">{successNotice}</StatusBanner>}

          {pendingUninstall && (
            <Card className="border-danger/40 bg-danger/5 shadow-sm" role="region" aria-label="确认卸载外部插件">
              <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4 text-sm">
                <div className="space-y-1">
                  <div className="font-semibold text-foreground">
                    确认卸载外部插件「{pendingUninstall.name}」（{pendingUninstall.id}）？
                  </div>
                  <p className="text-xs text-muted-foreground">
                    卸载后将移除插件包文件、侧栏覆盖记录及其自动注册的伴生 MCP 服务；历史巡检记录与线上实际服务不受影响。
                  </p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    size="sm"
                    variant="danger"
                    disabled={Boolean(busyKey)}
                    onClick={() => void handleConfirmUninstall(pendingUninstall)}
                  >
                    {busyKey.startsWith("uninstall:") ? "正在卸载…" : "确认卸载"}
                  </Button>
                  <Button size="sm" variant="default" disabled={Boolean(busyKey)} onClick={() => setPendingUninstall(null)}>
                    取消
                  </Button>
                </div>
              </CardContent>
            </Card>
          )}

          {preview && (
            <Card className="border-primary/40 bg-card shadow-sm" role="region" aria-label="变更预览确认">
              <CardHeader className="pb-2">
                <CardTitle className="flex flex-wrap items-center justify-between gap-2 text-base">
                  <span>{preview.title}</span>
                  <Badge variant="outline">预览差异（尚未生效）</Badge>
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3 text-sm">
                <p className="text-muted-foreground">{preview.description}</p>
                <div className="grid gap-3 sm:grid-cols-2">
                  <div className="rounded-md border bg-muted/30 p-3">
                    <div className="text-xs font-medium text-muted-foreground">将新增显示（{preview.diff.show.length}）</div>
                    {preview.diff.show.length === 0 ? (
                      <p className="mt-1 text-xs text-muted-foreground">无新增插件</p>
                    ) : (
                      <div className="mt-1.5 flex flex-wrap gap-1.5">
                        {preview.diff.show.map((id) => (
                          <Badge key={id} variant="success">+ {formatPluginName(id, state)}</Badge>
                        ))}
                      </div>
                    )}
                  </div>
                  <div className="rounded-md border bg-muted/30 p-3">
                    <div className="text-xs font-medium text-muted-foreground">将隐藏入口（{preview.diff.hide.length}）</div>
                    {preview.diff.hide.length === 0 ? (
                      <p className="mt-1 text-xs text-muted-foreground">无隐藏插件</p>
                    ) : (
                      <div className="mt-1.5 flex flex-wrap gap-1.5">
                        {preview.diff.hide.map((id) => (
                          <Badge key={id} variant="outline">- {formatPluginName(id, state)}</Badge>
                        ))}
                      </div>
                    )}
                  </div>
                </div>

                {preview.diff.dropped_overrides.length > 0 && (
                  <p className="text-xs text-muted-foreground">
                    本次切换将重置之前手动覆盖的插件：{preview.diff.dropped_overrides.map((id) => formatPluginName(id, state)).join("、")}。
                  </p>
                )}

                {preview.diff.affected_schedules.length > 0 && (
                  <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-xs">
                    <div className="flex items-center gap-1.5 font-medium text-foreground">
                      <ShieldAlert className="size-3.5 text-warning" aria-hidden="true" />
                      关联的 {preview.diff.affected_schedules.length} 条定时巡检计划将继续运行（不会被自动停用）：
                    </div>
                    <ul className="mt-1.5 list-disc space-y-0.5 pl-4 text-muted-foreground">
                      {preview.diff.affected_schedules.map((s) => (
                        <li key={s.id}>{s.name}（{s.skills.join(", ") || "默认巡检"}）</li>
                      ))}
                    </ul>
                  </div>
                )}

                <div className="flex flex-wrap gap-2 pt-1">
                  <Button
                    size="sm"
                    disabled={Boolean(busyKey)}
                    onClick={() => void commitMutation(preview.request, `已完成：${preview.title}`)}
                  >
                    {busyKey === "commit" ? "正在保存…" : "确认生效"}
                  </Button>
                  <Button size="sm" variant="ghost" disabled={Boolean(busyKey)} onClick={() => setPreview(null)}>
                    取消
                  </Button>
                </div>
              </CardContent>
            </Card>
          )}

          {isAdmin && state.stale_overrides.length > 0 && (
            <StatusBanner tone="warning">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span>检测到 {state.stale_overrides.length} 条陈旧插件覆盖记录（原插件或 Skill 已不存在）：{state.stale_overrides.join("、")}</span>
                <div className="flex flex-wrap gap-1.5">
                  {state.stale_overrides.map((id) => (
                    <Button key={id} size="sm" variant="default" disabled={!canMutate || Boolean(busyKey)} onClick={() => void handleClearStale(id)}>
                      <Trash2 className="mr-1 size-3" aria-hidden="true" />
                      清理 {id}
                    </Button>
                  ))}
                </div>
              </div>
            </StatusBanner>
          )}

          {/* Section 1: 5 Scenario Packs */}
          <section aria-label="场景预设包" className="space-y-2.5">
            <div className="flex flex-wrap items-baseline justify-between gap-1.5">
              <h2 className="flex items-center gap-1.5 text-sm font-semibold text-foreground">
                <Layers className="size-4 shrink-0 text-primary" aria-hidden="true" />
                场景预设包（一键装配界面能力）
              </h2>
              <span className="text-xs text-muted-foreground">内核能力（首页 / AI 助手 / 终端 / 知识库 / 凭据 / 审计 / 升级）始终保留</span>
            </div>
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {state.scenarios.map((scenario) => {
                const isActive = scenario.id === state.scenario_id
                return (
                  <Card
                    key={scenario.id}
                    className={isActive ? "border-primary/60 bg-primary/5" : "bg-card"}
                  >
                    <CardContent className="flex h-full flex-col justify-between gap-3 p-4">
                      <div className="space-y-2">
                        <div className="flex items-start justify-between gap-2">
                          <div>
                            <div className="text-sm font-semibold text-foreground">{scenario.name}</div>
                            <div className="font-mono text-[11px] text-muted-foreground">{scenario.id}</div>
                          </div>
                          {isActive && (
                            <Badge variant={state.customized ? "warning" : "primary"}>
                              {state.customized ? `当前 · 已微调 +${overrideCount}` : "当前场景"}
                            </Badge>
                          )}
                        </div>
                        <p className="text-xs leading-relaxed text-muted-foreground">{scenario.description}</p>
                        <div className="flex flex-wrap gap-1">
                          {scenario.defaults.length === 0 ? (
                            <Badge variant="outline">仅保留核心工作台</Badge>
                          ) : (
                            scenario.defaults.map((pluginId) => (
                              <Badge key={pluginId} variant="outline">{formatPluginName(pluginId)}</Badge>
                            ))
                          )}
                        </div>
                      </div>
                      <div className="flex items-center justify-between border-t pt-2.5">
                        <span className="text-xs text-muted-foreground">默认启用 {scenario.defaults.length} 个功能插件</span>
                        {canMutate && (
                          <Button
                            size="sm"
                            variant={isActive && !state.customized ? "ghost" : "default"}
                            disabled={(isActive && !state.customized) || Boolean(busyKey)}
                            onClick={() => handleScenarioClick(scenario.id, scenario.name)}
                          >
                            {isActive && !state.customized ? (
                              <>
                                <Check className="mr-1 size-3.5" aria-hidden="true" />
                                已使用
                              </>
                            ) : isActive ? (
                              "恢复此场景默认"
                            ) : (
                              "预览并切换"
                            )}
                          </Button>
                        )}
                      </div>
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          </section>

          {/* Section 2: Built-in L1 Plugins */}
          <section aria-label="内置功能插件清单" className="space-y-2.5">
            <div className="flex flex-wrap items-baseline justify-between gap-1.5">
              <h2 className="flex items-center gap-1.5 text-sm font-semibold text-foreground">
                <Puzzle className="size-4 shrink-0 text-primary" aria-hidden="true" />
                内置功能插件（L1 · 按需单项微调）
              </h2>
              <span className="text-xs text-muted-foreground">修订版本 r{state.revision}</span>
            </div>
            <div className="grid gap-3 md:grid-cols-2">
              {builtinPlugins.map((plugin) => {
                const isOverridden = plugin.override !== null && plugin.override !== plugin.scenario_default
                return (
                  <Card key={plugin.id} className={plugin.effective ? "bg-card" : "bg-muted/20 opacity-85"}>
                    <CardContent className="space-y-3 p-4">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0 space-y-1">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="text-sm font-semibold text-foreground">{plugin.name}</span>
                            <Badge variant="outline">{PLUGIN_CATEGORY_LABELS[plugin.category] ?? plugin.category}</Badge>
                            {isOverridden && <Badge variant="warning">已覆盖</Badge>}
                          </div>
                          <p className="text-xs leading-relaxed text-muted-foreground">{plugin.description}</p>
                        </div>
                        <div className="flex shrink-0 flex-col items-end gap-1.5">
                          <Badge variant={plugin.effective ? "success" : "outline"}>
                            {plugin.effective ? "已启用" : "已隐藏"}
                          </Badge>
                          {canMutate && (
                            <Button
                              size="sm"
                              variant={plugin.effective ? "default" : "primary"}
                              disabled={Boolean(busyKey)}
                              onClick={() => handleTogglePlugin(plugin)}
                            >
                              {plugin.effective ? "隐藏" : "启用"}
                            </Button>
                          )}
                        </div>
                      </div>

                      {(plugin.dependencies.skills.length > 0 || plugin.dependencies.mcp.length > 0) && (
                        <div className="flex flex-wrap items-center gap-1.5 border-t pt-2.5 text-[11px]">
                          <span className="text-muted-foreground">关联能力：</span>
                          {plugin.dependencies.skills.map((dep) => (
                            <button
                              key={`skill-${dep.slug}`}
                              type="button"
                              onClick={() => onSubTabChange("skills")}
                              className="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] hover:bg-muted"
                              title={`查看 Skill ${dep.slug}（${depStateLabel(dep.state)}）`}
                            >
                              <span className="font-mono">Skill:{dep.slug}</span>
                              <Badge variant={depBadgeVariant(dep.state)} className="px-1 py-0 text-[10px]">
                                {depStateLabel(dep.state)}
                              </Badge>
                            </button>
                          ))}
                          {plugin.dependencies.mcp.map((dep) => (
                            <button
                              key={`mcp-${dep.slug}`}
                              type="button"
                              onClick={() => onSubTabChange("mcp")}
                              className="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] hover:bg-muted"
                              title={`查看 MCP ${dep.slug}（${depStateLabel(dep.state)}）`}
                            >
                              <span className="font-mono">MCP:{dep.slug}</span>
                              <Badge variant={depBadgeVariant(dep.state)} className="px-1 py-0 text-[10px]">
                                {depStateLabel(dep.state)}
                              </Badge>
                            </button>
                          ))}
                        </div>
                      )}
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          </section>

          {/* Section 3: Declarative L2 Skill Plugins */}
          <section aria-label="声明式场景插件清单" className="space-y-2.5">
            <div className="flex flex-wrap items-baseline justify-between gap-1.5">
              <h2 className="flex items-center gap-1.5 text-sm font-semibold text-foreground">
                <Sparkles className="size-4 shrink-0 text-primary" aria-hidden="true" />
                扩展能力与插件工作台
              </h2>
              <span className="text-xs text-muted-foreground">上传含 plugin.yaml 的插件包即可自动装配独立工作台、资产目录与 MCP 工具</span>
            </div>
            {declarativePlugins.length === 0 ? (
              <Card>
                <CardContent className="p-4 text-xs text-muted-foreground">
                  当前暂无可装配的扩展插件。点击顶栏「安装插件包 (.zip)」或在「Skill 能力包」上传含 <code>plugin.yaml</code> 的能力包即可自动装配。
                </CardContent>
              </Card>
            ) : (
              <div className="grid gap-3 md:grid-cols-2">
                {declarativePlugins.map((plugin) => {
                  const isOverridden = plugin.override !== null && plugin.override !== plugin.scenario_default
                  const isSystem = plugin.scope === "system"
                  const canToggleL2 = canMutate && isSystem && !plugin.manifest_error && plugin.skill_enabled !== false
                  return (
                    <Card key={plugin.id} className={plugin.effective ? "bg-card" : "bg-muted/20 opacity-85"}>
                      <CardContent className="space-y-3 p-4">
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0 space-y-1">
                            <div className="flex flex-wrap items-center gap-1.5">
                              <span className="text-sm font-semibold text-foreground">{plugin.name}</span>
                              <Badge variant={isSystem ? (plugin.builtin ? "primary" : "success") : "success"}>
                                {formatPluginOriginLabel(plugin)}
                              </Badge>
                              <Badge variant="outline">{PLUGIN_CATEGORY_LABELS[plugin.category] ?? plugin.category}</Badge>
                              {plugin.manifest?.plugin.opt_in && <Badge variant="outline">按需启用</Badge>}
                              {isOverridden && <Badge variant="warning">已覆盖</Badge>}
                            </div>
                            <div className="font-mono text-[11px] text-muted-foreground">{plugin.id}</div>
                            <p className="text-xs leading-relaxed text-muted-foreground">{plugin.description}</p>
                          </div>
                          <div className="flex shrink-0 flex-col items-end gap-1.5">
                            <Badge variant={plugin.effective ? "success" : plugin.manifest_error ? "danger" : "outline"}>
                              {plugin.manifest_error
                                ? "清单异常"
                                : plugin.effective
                                  ? "已启用"
                                  : plugin.manifest?.plugin.opt_in
                                    ? "按需未启用"
                                    : "已隐藏"}
                            </Badge>
                            <div className="flex flex-wrap justify-end gap-1.5">
                              {plugin.effective && onOpenPlugin && (
                                <Button size="sm" variant="ghost" onClick={() => onOpenPlugin(plugin.id)}>
                                  打开工作台
                                </Button>
                              )}
                              {canToggleL2 && (
                                <Button
                                  size="sm"
                                  variant={plugin.effective ? "default" : "primary"}
                                  disabled={Boolean(busyKey)}
                                  onClick={() => handleTogglePlugin(plugin)}
                                >
                                  {plugin.effective ? "隐藏" : "启用"}
                                </Button>
                              )}
                              {canMutate && !session.demo && plugin.can_delete && plugin.skill_slug && (
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  disabled={Boolean(busyKey)}
                                  aria-label={`卸载 ${plugin.name}`}
                                  onClick={() => {
                                    setPendingUninstall(plugin)
                                    setPreview(null)
                                    setActionError("")
                                  }}
                                >
                                  <Trash2 className="size-3.5 text-danger" aria-hidden="true" />
                                  卸载
                                </Button>
                              )}
                            </div>
                          </div>
                        </div>

                        {plugin.manifest_error && (
                          <div className="rounded border border-destructive/40 bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive">
                            plugin.yaml 解析失败：{plugin.manifest_error}
                          </div>
                        )}

                        {plugin.manifest && (
                          <div className="flex flex-wrap items-center gap-1.5 border-t pt-2.5 text-[11px] text-muted-foreground">
                            <span>能力插槽：</span>
                            {plugin.manifest.ui.slots.sidebar?.enabled && <Badge variant="outline">侧栏</Badge>}
                            {plugin.manifest.ui.slots.target_tab?.enabled && <Badge variant="outline">目标页签</Badge>}
                            {plugin.manifest.ui.slots.home_widget?.enabled && <Badge variant="outline">首页指标卡</Badge>}
                            {(plugin.manifest.capabilities?.catalog_inputs?.length ?? 0) > 0 && <Badge variant="outline">资产目录联动</Badge>}
                            {(plugin.manifest.catalog_schemas?.length ?? 0) > 0 && (
                              <Badge variant="outline">插件资产目录 ({plugin.manifest.catalog_schemas?.length})</Badge>
                            )}
                            {plugin.manifest.mcp_server && (
                              <Badge variant="outline">自带 MCP: {plugin.manifest.mcp_server.slug}</Badge>
                            )}
                            {(plugin.manifest.ui.actions?.length ?? 0) > 0 && <Badge variant="outline">就地操作台</Badge>}
                            {plugin.manifest.capabilities?.companion_agent_skill && (
                              <Badge variant="outline">伴生 Skill: {plugin.manifest.capabilities.companion_agent_skill}</Badge>
                            )}
                            {plugin.manifest.schedule_template && <Badge variant="outline">推荐定时计划</Badge>}
                            {!isSystem && (
                              <button
                                type="button"
                                onClick={() => onSubTabChange("skills")}
                                className="ml-auto text-primary hover:underline"
                              >
                                在 Skill 能力包管理开关 →
                              </button>
                            )}
                          </div>
                        )}
                      </CardContent>
                    </Card>
                  )
                })}
              </div>
            )}
          </section>
        </TabsContent>

        <TabsContent value="skills" className="mt-4">
          <SkillRegistryPanel
            session={session}
            targets={targets}
            initialSlug={initialSkillSlug}
            onOpenInspection={onOpenInspection}
            onOpenMcp={() => onSubTabChange("mcp")}
          />
        </TabsContent>

        <TabsContent value="mcp" className="mt-4">
          <MCPRegistryPanel
            session={session}
            targets={targets}
            onOpenSkills={() => onSubTabChange("skills")}
            onOpenInspection={() => onOpenInspection?.()}
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}
