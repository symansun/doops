"use client"

import dynamic from "next/dynamic"
import type { Session, Target } from "@/lib/client"
import { DEPLOY_SECTIONS, type DeploySection } from "@/lib/console-views"
import { AgentUpgradePanel } from "./agent-upgrade-panel"
import { DeployGuideBody } from "./deploy-guide"
import { StatusBanner } from "./ui"

const CicdGuide = dynamic(() => import("./cicd-guide").then((m) => m.CicdGuide), {
  loading: () => <div role="status" className="p-6 text-sm text-muted-foreground">正在加载 CI/CD 接入指引…</div>,
})
const WorkloadDeployPanel = dynamic(() => import("./workload-deploy-panel").then((m) => m.WorkloadDeployPanel), {
  loading: () => <div role="status" className="p-6 text-sm text-muted-foreground">正在加载发布工作负载…</div>,
})

// The list lives in lib/console-views.ts because the header navigation renders
// the same sections; the view only needs to know which ones require a target.

export function DeployView({ section, onSection, session, target, kubernetesAvailable, sessionId, onEnterConsole, onOpenCredentials }: {
  section: DeploySection
  onSection: (section: DeploySection) => void
  session: Session
  target: Target | null
  kubernetesAvailable?: boolean
  sessionId: string
  onEnterConsole: () => void
  // Only passed for platform admins: where the target's modelProvider binding is configured.
  onOpenCredentials?: () => void
}) {
  const active = !target && DEPLOY_SECTIONS.find((item) => item.id === section)?.needsTarget ? "onboard" : section

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {section === "workload" && kubernetesAvailable === false ? (
        <div className="p-4"><StatusBanner tone="warning">当前目标没有可用的 Kubernetes 集群，无法通过 Helm 发布工作负载。</StatusBanner></div>
      ) : active === "workload" && target ? (
        <WorkloadDeployPanel key={`workload-${target.key}`} session={session} target={target}
          onOpenCredentials={onOpenCredentials} onOpenUpgrade={() => onSection("upgrade")} />
      ) : active === "upgrade" && target ? (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <AgentUpgradePanel key={target.key} session={session} target={target} sessionId={sessionId} />
        </div>
      ) : active === "cicd" ? (
        <div className="min-h-0 flex-1 overflow-y-auto"><CicdGuide session={session} /></div>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <DeployGuideBody onEnterConsole={onEnterConsole} />
        </div>
      )}
    </div>
  )
}
