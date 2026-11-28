"use client"

import dynamic from "next/dynamic"
import { useRef, useState, type ReactNode } from "react"
import { assetRequest } from "@/lib/assets"
import {
  ASSET_LEDGER_SECTIONS,
  type PluginCenterSubTab,
  type ResourceMonitorSubTab,
  type TargetTab as WorkspaceTab,
} from "@/lib/console-views"
import { fetchTargets, sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { canNavigateDestination } from "@/lib/plugins/resolver"
import type { PluginStateResponse } from "@/lib/plugins/types"
import { Button } from "./ui"
import { PluginCenterPanel } from "./plugins/plugin-center-panel"

const assetPluginLoading = (label: string) => () => (
  <div role="status" className="flex min-h-48 items-center justify-center p-6 text-sm text-muted-foreground">
    正在加载{label}…
  </div>
)

const MachineAssetsPanel = dynamic(() => import("./machine-assets-panel").then((m) => m.MachineAssetsPanel), { loading: assetPluginLoading("机器台账") })
const ClusterAssetsPanel = dynamic(() => import("./cluster-assets-panel").then((m) => m.ClusterAssetsPanel), { loading: assetPluginLoading("集群台账") })
const AssetPlatformPanel = dynamic(() => import("./asset-platform-panel").then((m) => m.AssetPlatformPanel), { loading: assetPluginLoading("Agent 运行监控") })
const BusinessAssetsPanel = dynamic(() => import("./business-assets-panel").then((m) => m.BusinessAssetsPanel), { loading: assetPluginLoading("业务系统台账") })
const SituationalDashboardPanel = dynamic(() => import("./situational-dashboard-panel").then((m) => m.SituationalDashboardPanel), { loading: assetPluginLoading("态势感知") })
const ResourceMonitorHub = dynamic(() => import("./resource-monitor-hub").then((m) => m.ResourceMonitorHub), { loading: assetPluginLoading("资源监控") })
const AutomationPanel = dynamic(() => import("./automation-panel").then((m) => m.AutomationPanel), { loading: assetPluginLoading("自动化工作台") })

// Section labels come from lib/console-views.ts, which also renders them in the
// header navigation, so the ledger never keeps a second copy of the list.
type TargetTab = "overview" | "credentials" | "k8s" | "monitor"
export type MachinePage = {
  section: "machines"; machineId: string; query?: string; tags?: string[]; kind?: "all" | "physical" | "vm"; nodesOpen?: boolean; page?: number; pageSize?: number
}
export type ClusterSourcePage = {
  section: "clusters"; source: { assetId: string; cluster: string; instance: string; namespace?: string }
  query: string; expanded: string[]
}
type TargetOrigin = (MachinePage | ClusterSourcePage) & { returnTab?: TargetTab }
type LedgerSection = "machines" | "clusters" | "systems"
export type ClusterListState = Pick<ClusterSourcePage, "query" | "expanded">
type LedgerPage = { section: LedgerSection } | ({ section: "clusters" } & ClusterListState) | TargetOrigin
export type AssetPage =
  | { section: "plugins"; sub?: PluginCenterSubTab; slug?: string }
  | { section: "situation"; clusterId?: string }
  | { section: "monitor"; sub?: ResourceMonitorSubTab; targetKey?: string; clusterId?: string }
  | { section: "inspection"; runId?: string }
  | { section: "skills"; slug?: string }
  | { section: "mcp" }
  | { section: "board"; clusterId?: string }
  | LedgerPage

export function assetReturnLabel(page: AssetPage) {
  if ("machineId" in page) return `返回机器 ${page.machineId}`
  if ("source" in page) {
    const { assetId, cluster, instance, namespace } = page.source
    return `返回集群 ${assetId} · ${cluster}/${instance}${namespace ? ` · ${namespace}` : ""}`
  }
  if (page.section === "plugins") return "返回插件中心"
  if (page.section === "situation") return "返回态势感知"
  if (page.section === "monitor") return "返回资源监控"
  if (page.section === "inspection") return "返回自动化"
  if (page.section === "skills") return "返回插件中心"
  if (page.section === "mcp") return "返回插件中心"
  if (page.section === "board") return "返回 Agent 运行监控"
  return `返回${ASSET_LEDGER_SECTIONS.find(item => item.id === page.section)!.label}`
}

type TargetAction = { target?: Target; tab: TargetTab; origin: TargetOrigin; label: string }
export type RenderAssetTargetAction = (action: TargetAction) => ReactNode
type OpenTarget = (target: Target, tab: TargetTab, namespace: string | undefined, origin: AssetPage) => void

function actionKey(origin: TargetOrigin, tab: TargetTab) {
  return JSON.stringify(origin.section === "machines" ? [tab, "machines", origin.machineId]
    : [tab, "clusters", origin.source.assetId, origin.source.cluster, origin.source.instance, origin.source.namespace])
}

async function verifyTarget(
  session: Session,
  target: Target,
  origin: TargetOrigin,
  options?: { allowOffline?: boolean; directory?: Target[] },
) {
  const matchesTarget = (item: { cluster?: string; instance?: string }) => item.cluster === target.cluster && item.instance === target.instance
  const binding = origin.section === "machines"
    ? assetRequest<{ machines: { asset_id: string; cluster?: string; instance?: string }[] }>("machines")
      .then(({ machines }) => machines.some(row => row.asset_id === origin.machineId && matchesTarget(row)))
    : assetRequest<{ clusters: { asset_id: string; cluster: string; instance: string; namespaces: { name: string }[] }[] }>("clusters")
      .then(({ clusters }) => clusters.some(row => row.asset_id === origin.source.assetId && matchesTarget(row) &&
        (!origin.source.namespace || row.namespaces.some(ns => ns.name === origin.source.namespace))))
  const [visible, bound] = await Promise.all([fetchTargets(session), binding])
  if (!bound) throw new Error("资产关联或可见范围已变化。请刷新台账。")
  const online = visible.find(item => matchesTarget(item) && item.status !== "offline")
  if (online) return online
  if (options?.allowOffline) {
    return options.directory?.find(matchesTarget) ?? { ...target, status: "offline" }
  }
  throw new Error("目标已离线或当前账号无权访问。请刷新台账。")
}

function TargetActionButton({ target, label, disabledReason, restoreFocus, enter }: {
  target?: Target; label: string; disabledReason?: string; restoreFocus: (button: HTMLButtonElement) => void
  enter: (button: HTMLButtonElement) => Promise<void>
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const blocked = !target || busy || Boolean(disabledReason)
  return <span className="inline-flex flex-col items-start gap-1">
    <Button ref={button => { if (button) restoreFocus(button) }} size="sm" disabled={blocked} title={disabledReason} onClick={async event => {
      setBusy(true)
      setError("")
      try { await enter(event.currentTarget) } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
      finally { setBusy(false) }
    }}>{busy ? "正在核验…" : label}</Button>
    {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
  </span>
}

function toTargetModel(cluster: string, instance: string, liveTargets: Target[]): Target {
  const key = `${cluster}/${instance}`
  const live = liveTargets.find((item) => item.key === key)
  if (live) return live
  return { key, cluster, instance, status: "offline", sessions: [] }
}

export function AssetInventory({
  session,
  targets,
  directory,
  selectedTarget,
  page,
  onPage,
  onOpenTarget,
  onOpenMonitorTarget,
  onOpenResourceMonitor,
  onOpenDirectK8s,
  onDeployAgent,
  onRunningChange,
  pluginState,
  pluginStatus = "ready",
  pluginError = "",
  onRefreshPlugins,
  onOpenPlugin,
  onOpenAutomation,
}: {
  session: Session
  targets: Target[]
  directory: Target[]
  selectedTarget?: Target | null
  // The header navigation owns the area, so the ledger has no tab row of its own.
  page: AssetPage
  onPage: (page: AssetPage) => void
  onOpenTarget: OpenTarget
  onOpenMonitorTarget?: (target: Target, tab: WorkspaceTab, origin: AssetPage, operationId?: string) => void
  onOpenResourceMonitor?: (origin: AssetPage, targetKey?: string) => void
  onOpenDirectK8s?: (target: Target) => void
  onDeployAgent?: () => void
  onRunningChange?: (running: boolean) => void
  pluginState?: PluginStateResponse | null
  pluginStatus?: "loading" | "ready" | "unsupported" | "error"
  pluginError?: string
  onRefreshPlugins?: () => Promise<void>
  onOpenPlugin?: (pluginId: string) => void
  onOpenAutomation?: (opts?: { sub?: "schedules" | "runs" | "bg"; runId?: string; scheduleId?: string }) => void
}) {
  const [initialAction] = useState(() => "returnTab" in page && page.returnTab
    ? actionKey(page, page.returnTab) : undefined)
  const returnFocus = useRef(initialAction)
  const attempt = useRef(0)
  const renderTargetAction: RenderAssetTargetAction = ({ target, tab, origin, label }) => {
    const key = actionKey(origin, tab)
    const navCheck = canNavigateDestination({ view: "console", tab }, pluginState, {
      kubernetesAvailable: target?.kubernetes_available,
    })
    return <TargetActionButton key={key} target={target} label={label} disabledReason={navCheck.ok ? undefined : navCheck.reason} restoreFocus={button => {
      if (returnFocus.current === key) {
        returnFocus.current = undefined
        button.scrollIntoView({ block: "nearest" })
        button.focus({ preventScroll: true })
      }
    }} enter={async trigger => {
      if (!target || !navCheck.ok) return
      const current = ++attempt.current
      const verified = await verifyTarget(session, target, origin, {
        allowOffline: tab === "monitor",
        directory,
      })
      if (current === attempt.current && trigger.isConnected) {
        onOpenTarget(verified, tab, origin.section === "clusters" ? origin.source.namespace : undefined, { ...origin, returnTab: tab })
      }
    }} />
  }
  const automationAllowed = canNavigateDestination({ view: "console", tab: "tasks" }, pluginState).ok
  const situationAllowed = canNavigateDestination({ view: "assets", area: "situation" }, pluginState).ok
  const resourceMonitorAllowed = canNavigateDestination({ view: "assets", area: "monitor" }, pluginState).ok
  const openAutomationRun = automationAllowed
    ? (runId?: string) => {
        if (onOpenAutomation) {
          onOpenAutomation(runId ? { sub: "runs", runId } : { sub: "schedules" })
          return
        }
        onPage(runId ? { section: "inspection", runId } : { section: "inspection" })
      }
    : undefined
  const openResourceMonitor = resourceMonitorAllowed
    ? (targetKey?: string) => {
        if (onOpenResourceMonitor) {
          onOpenResourceMonitor(page, targetKey)
          return
        }
        onPage(
          targetKey
            ? { section: "monitor", sub: "target", targetKey }
            : { section: "monitor", sub: "fleet" },
        )
      }
    : undefined

  // A changed online set invalidates previous reads. Gateway may still return authorized offline assets.
  const scope = sessionIsAdmin(session) ? "admin" : JSON.stringify(targets
    .filter((target) => target.status !== "offline")
    .map(({ cluster, instance }) => JSON.stringify([cluster, instance])).sort())

  const isPluginCenter = page.section === "plugins" || page.section === "skills" || page.section === "mcp"
  const pluginCenterSub: PluginCenterSubTab =
    page.section === "skills" ? "skills" : page.section === "mcp" ? "mcp" : (page.section === "plugins" ? (page.sub ?? "scenarios") : "scenarios")
  const initialSkillSlug =
    page.section === "skills" ? page.slug : (page.section === "plugins" ? page.slug : undefined)

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div key={JSON.stringify([scope, page.section, isPluginCenter ? "center" : page.section === "monitor" ? "monitor" : page])} className="min-h-0 flex-1 overflow-auto p-4">
        {isPluginCenter ? (
          <PluginCenterPanel
            session={session}
            targets={targets}
            subTab={pluginCenterSub}
            initialSkillSlug={initialSkillSlug}
            pluginState={pluginState ?? null}
            pluginStatus={pluginStatus}
            pluginError={pluginError}
            onSubTabChange={(next) => onPage({ section: "plugins", sub: next })}
            onRefreshPlugins={onRefreshPlugins ?? (async () => {})}
            onOpenInspection={openAutomationRun}
            onOpenPlugin={onOpenPlugin}
          />
        ) : page.section === "situation" ? (
          <SituationalDashboardPanel
            session={session}
            targets={targets}
            clusterFilter={page.clusterId}
            onOpenTarget={onOpenMonitorTarget
              ? (cluster, instance, tab) => onOpenMonitorTarget(toTargetModel(cluster, instance, directory), tab, page)
              : undefined}
            onOpenInspection={openAutomationRun}
            onOpenResourceMonitor={openResourceMonitor}
            onOpenPlatformMonitor={() => onPage({ section: "board" })}
            onOpenArea={(area) => {
              const check = canNavigateDestination({ view: "assets", area }, pluginState)
              if (!check.ok) return
              onPage(area === "clusters" ? { section: "clusters", query: "", expanded: [] } : { section: area })
            }}
          />
        ) : page.section === "monitor" ? (
          <ResourceMonitorHub
            session={session}
            targets={directory}
            selectedTarget={selectedTarget}
            subTab={page.sub ?? "fleet"}
            targetKey={page.targetKey}
            clusterFilter={page.clusterId}
            onSubTabChange={(next, nextTargetKey) =>
              onPage({
                section: "monitor",
                sub: next,
                ...(next === "target" && nextTargetKey ? { targetKey: nextTargetKey } : {}),
                ...(page.clusterId ? { clusterId: page.clusterId } : {}),
              })
            }
            onTargetKeyChange={(nextTargetKey) =>
              onPage({
                section: "monitor",
                sub: "target",
                targetKey: nextTargetKey,
                ...(page.clusterId ? { clusterId: page.clusterId } : {}),
              })
            }
            onOpenInspection={openAutomationRun}
            onOpenSituation={situationAllowed ? () => onPage({ section: "situation" }) : undefined}
            onOpenPlatformMonitor={() => onPage({ section: "board" })}
            onOpenSkillCenter={() => onPage({ section: "plugins", sub: "skills" })}
            onDeployAgent={onDeployAgent}
          />
        ) : page.section === "inspection" ? (
          <AutomationPanel
            session={session}
            targets={directory}
            selectedTarget={selectedTarget ?? null}
            initialRunId={page.runId}
            onRunIdChange={(runId) => onPage(runId ? { section: "inspection", runId } : { section: "inspection" })}
            onOpenSkills={() => onPage({ section: "plugins", sub: "skills" })}
          />
        ) : page.section === "board" ? (
          <AssetPlatformPanel
            session={session}
            targets={targets}
            initialClusterId={page.clusterId}
            onClusterChange={(clusterId) => onPage(clusterId ? { section: "board", clusterId } : { section: "board" })}
            onOpenTarget={onOpenMonitorTarget
              ? (target, tab, operationId) => onOpenMonitorTarget(target, tab, page, operationId)
              : undefined}
            onOpenSituation={() => onPage({ section: "situation" })}
            onOpenResourceMonitor={openResourceMonitor}
            onOpenInspection={openAutomationRun ? () => openAutomationRun() : undefined}
            onDeployAgent={onDeployAgent}
            onRunningChange={onRunningChange}
          />
        ) : (
          <>
            {page.section === "machines" && (
              <MachineAssetsPanel
                initialPage={"machineId" in page ? page : undefined}
                session={session}
                targets={targets}
                directory={directory}
                renderTargetAction={renderTargetAction}
                onOpenInspection={openAutomationRun ? () => openAutomationRun() : undefined}
              />
            )}
            {page.section === "clusters" && (
              <ClusterAssetsPanel
                session={session}
                targets={targets}
                selectedTarget={selectedTarget}
                onOpenTargetK8s={onOpenDirectK8s}
                initialPage={"query" in page ? page : undefined}
                renderTargetAction={renderTargetAction}
              />
            )}
            {page.section === "systems" && (
              <BusinessAssetsPanel
                session={session}
                targets={targets}
                onOpenSituation={situationAllowed ? () => onPage({ section: "situation" }) : undefined}
              />
            )}
          </>
        )}
      </div>
    </div>
  )
}
