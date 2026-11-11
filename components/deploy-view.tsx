"use client"

import { useState } from "react"
import { ArrowUpCircle, PackagePlus, Rocket } from "lucide-react"
import type { Session, Target } from "@/lib/client"
import { AgentUpgradePanel } from "./agent-upgrade-panel"
import { DeployGuideBody } from "./deploy-guide"
import { WorkloadDeployPanel } from "./workload-deploy-panel"
import { PageTabs } from "./ui"

type Section = "workload" | "upgrade" | "onboard"

const SECTIONS: { id: Section; label: string; icon: typeof Rocket; needsTarget: boolean }[] = [
  { id: "workload", label: "发布工作负载", icon: Rocket, needsTarget: true },
  { id: "upgrade", label: "升级 Agent", icon: ArrowUpCircle, needsTarget: true },
  { id: "onboard", label: "首次接入", icon: PackagePlus, needsTarget: false },
]

export function DeployView({ session, target, sessionId, onEnterConsole, onOpenCredentials }: {
  session: Session
  target: Target | null
  sessionId: string
  onEnterConsole: () => void
  // Only passed for platform admins: where the target's modelProvider binding is configured.
  onOpenCredentials?: () => void
}) {
  const [section, setSection] = useState<Section>(target ? "workload" : "onboard")
  const active = !target && SECTIONS.find((item) => item.id === section)?.needsTarget ? "onboard" : section

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageTabs
        ariaLabel="部署"
        value={active}
        onChange={setSection}
        items={SECTIONS.map(({ id, label, icon: Icon, needsTarget }) => ({
          id,
          label,
          icon: <Icon aria-hidden="true" className="size-4" />,
          disabled: needsTarget && !target,
          title: needsTarget && !target ? "请先选择目标机器" : undefined,
        }))}
      />
      {active === "workload" && target ? (
        <WorkloadDeployPanel key={`workload-${target.key}`} session={session} target={target}
          onOpenCredentials={onOpenCredentials} onOpenUpgrade={() => setSection("upgrade")} />
      ) : active === "upgrade" && target ? (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <AgentUpgradePanel key={target.key} session={session} target={target} sessionId={sessionId} />
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <DeployGuideBody onEnterConsole={onEnterConsole} />
        </div>
      )}
    </div>
  )
}
