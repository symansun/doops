"use client"

import { useRef, useState, type ReactNode } from "react"
import { assetRequest } from "@/lib/assets"
import { ASSET_LEDGER_SECTIONS, type TargetTab as WorkspaceTab } from "@/lib/console-views"
import { fetchTargets, sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { Button } from "./ui"
import { MachineAssetsPanel } from "./machine-assets-panel"
import { ClusterAssetsPanel } from "./cluster-assets-panel"
import { AssetPlatformPanel } from "./asset-platform-panel"
import { BusinessAssetsPanel } from "./business-assets-panel"

import type { ComponentProps } from "react"
import { PlatformAssetScope } from "./agent-asset-scope"

// Section labels come from lib/console-views.ts, which also renders them in the
// header navigation, so the ledger never keeps a second copy of the list.
type TargetTab = "overview" | "credentials" | "k8s" | "monitor"
export type MachinePage = {
  section: "machines"; machineId: string; query?: string; tags?: string[]; kind?: "all" | "physical" | "vm"; nodesOpen?: boolean
}
export type ClusterSourcePage = {
  section: "clusters"; source: { assetId: string; cluster: string; instance: string; namespace?: string }
  query: string; expanded: string[]
}
type TargetOrigin = (MachinePage | ClusterSourcePage) & { returnTab?: TargetTab }
type LedgerSection = "machines" | "clusters" | "systems"
export type ClusterListState = Pick<ClusterSourcePage, "query" | "expanded">
type LedgerPage = { section: LedgerSection } | ({ section: "clusters" } & ClusterListState) | TargetOrigin
export type AssetPage = { section: "board"; clusterId?: string } | { section: "scope" } | LedgerPage

export function assetReturnLabel(page: AssetPage) {
  if ("machineId" in page) return `返回机器 ${page.machineId}`
  if ("source" in page) {
    const { assetId, cluster, instance, namespace } = page.source
    return `返回集群 ${assetId} · ${cluster}/${instance}${namespace ? ` · ${namespace}` : ""}`
  }
  if (page.section === "board") return "返回平台监控"
  if (page.section === "scope") return "返回 Agent管理"
  return `返回${ASSET_LEDGER_SECTIONS.find(item => item.id === page.section)!.label}`
}

type TargetAction = { target?: Target; tab: TargetTab; origin: TargetOrigin; label: string }
export type RenderAssetTargetAction = (action: TargetAction) => ReactNode
type OpenTarget = (target: Target, tab: TargetTab, namespace: string | undefined, origin: AssetPage) => void

function actionKey(origin: TargetOrigin, tab: TargetTab) {
  return JSON.stringify(origin.section === "machines" ? [tab, "machines", origin.machineId]
    : [tab, "clusters", origin.source.assetId, origin.source.cluster, origin.source.instance, origin.source.namespace])
}

async function verifyTarget(session: Session, target: Target, origin: TargetOrigin) {
  const matchesTarget = (item: { cluster?: string; instance?: string }) => item.cluster === target.cluster && item.instance === target.instance
  const binding = origin.section === "machines"
    ? assetRequest<{ machines: { asset_id: string; cluster?: string; instance?: string }[] }>("machines")
      .then(({ machines }) => machines.some(row => row.asset_id === origin.machineId && matchesTarget(row)))
    : assetRequest<{ clusters: { asset_id: string; cluster: string; instance: string; namespaces: { name: string }[] }[] }>("clusters")
      .then(({ clusters }) => clusters.some(row => row.asset_id === origin.source.assetId && matchesTarget(row) &&
        (!origin.source.namespace || row.namespaces.some(ns => ns.name === origin.source.namespace))))
  const [visible, bound] = await Promise.all([fetchTargets(session), binding])
  const online = visible.find(item => matchesTarget(item) && item.status !== "offline")
  if (!online) throw new Error("目标已离线或当前账号无权访问。请刷新台账。")
  if (!bound) throw new Error("资产关联或可见范围已变化。请刷新台账。")
  return online
}

function TargetActionButton({ target, label, restoreFocus, enter }: {
  target?: Target; label: string; restoreFocus: (button: HTMLButtonElement) => void
  enter: (button: HTMLButtonElement) => Promise<void>
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  return <span className="inline-flex flex-col items-start gap-1">
    <Button ref={button => { if (button) restoreFocus(button) }} size="sm" disabled={!target || busy} onClick={async event => {
      setBusy(true)
      setError("")
      try { await enter(event.currentTarget) } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
      finally { setBusy(false) }
    }}>{busy ? "正在核验…" : label}</Button>
    {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
  </span>
}

export function AssetInventory({ session, targets, selectedTarget, catalog, page, onPage, onOpenTarget, onOpenMonitorTarget, onOpenDirectK8s, onDeployAgent, onRunningChange }: {
  session: Session
  targets: Target[]
  selectedTarget?: Target | null
  catalog: ComponentProps<typeof PlatformAssetScope>
  // The header navigation owns the area, so the ledger has no tab row of its own.
  page: AssetPage
  onPage: (page: AssetPage) => void
  onOpenTarget: OpenTarget
  onOpenMonitorTarget?: (target: Target, tab: WorkspaceTab, origin: AssetPage, operationId?: string) => void
  onOpenDirectK8s?: (target: Target) => void
  onDeployAgent?: () => void
  onRunningChange?: (running: boolean) => void
}) {
  const [initialAction] = useState(() => "returnTab" in page && page.returnTab
    ? actionKey(page, page.returnTab) : undefined)
  const returnFocus = useRef(initialAction)
  const attempt = useRef(0)
  const renderTargetAction: RenderAssetTargetAction = ({ target, tab, origin, label }) => {
    const key = actionKey(origin, tab)
    return <TargetActionButton key={key} target={target} label={label} restoreFocus={button => {
      if (returnFocus.current === key) {
        returnFocus.current = undefined
        button.scrollIntoView({ block: "nearest" })
        button.focus({ preventScroll: true })
      }
    }} enter={async trigger => {
      if (!target) return
      const current = ++attempt.current
      const online = await verifyTarget(session, target, origin)
      if (current === attempt.current && trigger.isConnected) {
        onOpenTarget(online, tab, origin.section === "clusters" ? origin.source.namespace : undefined, { ...origin, returnTab: tab })
      }
    }} />
  }
  // A changed online set invalidates previous reads. Gateway may still return authorized offline assets.
  const scope = sessionIsAdmin(session) ? "admin" : JSON.stringify(targets
    .filter((target) => target.status !== "offline")
    .map(({ cluster, instance }) => JSON.stringify([cluster, instance])).sort())
  const onlineTargets = targets.filter((target) => target.status !== "offline")
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div key={JSON.stringify([scope, page])} className="min-h-0 flex-1 overflow-auto p-4">
        {page.section === "board" ? (
          <AssetPlatformPanel
            session={session}
            targets={targets}
            initialClusterId={page.clusterId}
            onClusterChange={(clusterId) => onPage(clusterId ? { section: "board", clusterId } : { section: "board" })}
            onOpenTarget={onOpenMonitorTarget
              ? (target, tab, operationId) => onOpenMonitorTarget(target, tab, page, operationId)
              : undefined}
            onDeployAgent={onDeployAgent}
            onRunningChange={onRunningChange}
          />
        ) : page.section === "scope" && sessionIsAdmin(session) ? <PlatformAssetScope {...catalog} /> : <>
            {page.section === "machines" && <MachineAssetsPanel initialPage={"machineId" in page ? page : undefined} session={session} targets={targets} renderTargetAction={renderTargetAction} />}
            {page.section === "clusters" && <ClusterAssetsPanel session={session} targets={targets}
              selectedTarget={selectedTarget} onOpenTargetK8s={onOpenDirectK8s}
              initialPage={"query" in page ? page : undefined} renderTargetAction={renderTargetAction} />}
            {page.section === "systems" && <BusinessAssetsPanel session={session} targets={targets}/>}
          </>}
      </div>
    </div>
  )
}
