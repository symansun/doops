"use client"

import { useState } from "react"
import { ArrowUpCircle, PackagePlus, Rocket } from "lucide-react"
import type { Session, Target } from "@/lib/client"
import { AgentUpgradePanel } from "./agent-upgrade-panel"
import { DeployGuideBody } from "./deploy-guide"
import { WorkloadDeployPanel } from "./workload-deploy-panel"

type Section = "workload" | "upgrade" | "onboard"

const SECTIONS: { id: Section; label: string; icon: typeof Rocket; needsTarget: boolean }[] = [
  { id: "workload", label: "发布工作负载", icon: Rocket, needsTarget: true },
  { id: "upgrade", label: "升级 Agent", icon: ArrowUpCircle, needsTarget: true },
  { id: "onboard", label: "首次接入", icon: PackagePlus, needsTarget: false },
]

export function DeployView({ session, target, sessionId, onEnterConsole }: {
  session: Session
  target: Target | null
  sessionId: string
  onEnterConsole: () => void
}) {
  const [section, setSection] = useState<Section>(target ? "workload" : "onboard")
  const active = !target && SECTIONS.find((item) => item.id === section)?.needsTarget ? "onboard" : section

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <nav aria-label="部署" className="flex shrink-0 gap-1 overflow-x-auto border-b bg-card px-2">
        {SECTIONS.map((item) => {
          const Icon = item.icon
          const disabled = item.needsTarget && !target
          return (
            <button
              key={item.id}
              type="button"
              disabled={disabled}
              title={disabled ? "请先选择目标机器" : undefined}
              aria-current={active === item.id ? "page" : undefined}
              onClick={() => setSection(item.id)}
              className={`-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2.5 text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
                active === item.id ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
            >
              <Icon aria-hidden="true" className="size-4" />{item.label}
            </button>
          )
        })}
      </nav>
      {active === "workload" && target ? (
        <WorkloadDeployPanel key={`workload-${target.key}`} session={session} target={target} />
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
