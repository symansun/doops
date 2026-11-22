"use client"

import type { Session, Target } from "@/lib/client"
import { DEPLOY_SECTIONS, type DeploySection } from "@/lib/console-views"
import { AgentUpgradePanel } from "./agent-upgrade-panel"
import { DeployGuideBody } from "./deploy-guide"
import { CicdGuide } from "./cicd-guide"
import { WorkloadDeployPanel } from "./workload-deploy-panel"

// The list lives in lib/console-views.ts because the header navigation renders
// the same sections; the view only needs to know which ones require a target.

export function DeployView({ section, onSection, session, target, sessionId, onEnterConsole, onOpenCredentials }: {
  section: DeploySection
  onSection: (section: DeploySection) => void
  session: Session
  target: Target | null
  sessionId: string
  onEnterConsole: () => void
  // Only passed for platform admins: where the target's modelProvider binding is configured.
  onOpenCredentials?: () => void
}) {
  const active = !target && DEPLOY_SECTIONS.find((item) => item.id === section)?.needsTarget ? "onboard" : section

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {active === "workload" && target ? (
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
