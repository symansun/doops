"use client"

import {
  createAgentWorkbenchRuntime,
  type AgentSessionRuntimeV2,
} from "@agent-cloud/agent-ui/runtime"
import type { Session, Target } from "./client"
import type { AgentSessionSnapshot } from "@agent-cloud/agent-ui"
import { createDoopsAgentSessionTransport } from "./agent-session-transport"

export function isDoopsAgentSessionBusy(snapshot: AgentSessionSnapshot): boolean {
  return snapshot.status === "running" ||
    snapshot.items.some((item) => item.kind === "message" && item.role === "user" && item.status === "pending") ||
    snapshot.interactions.some((interaction) => interaction.status === "pending" || interaction.status === "running")
}

export function createDoopsAgentUiRuntime(options: {
  session: Session
  target: Target
  sessionId: string
}): AgentSessionRuntimeV2 {
  return createAgentWorkbenchRuntime({
    sessionId: options.sessionId,
    title: "DoAgent",
    agentLabel: "DoAgent",
    interactionMode: "acp",
    transport: createDoopsAgentSessionTransport(options),
    connectionOptions: { maxConsecutiveFailures: 2 },
    projectSnapshot: (snapshot) => {
      const model = snapshot.configuration?.find((control) => control.id === "model")?.value
      return {
        ...snapshot,
        metadata: [
          { id: "target", label: "目标", value: `${options.target.cluster}/${options.target.instance}` },
          ...(model ? [{ id: "model", label: "模型", value: model }] : []),
        ],
      }
    },
  })
}
