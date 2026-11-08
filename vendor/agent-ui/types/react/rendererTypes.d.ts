import type { ComponentType } from "react";
import type { LucideIcon } from "lucide-react";
import type { AgentToolActivityItem } from "../agentToolContracts.js";
import type { AgentSessionRuntime } from "../contracts.js";
import type { ToolRendererRegistration } from "../registry/ToolRendererRegistry.js";
export interface AgentToolRendererProps {
    item: AgentToolActivityItem;
}
export type AgentToolRendererComponent = ComponentType<AgentToolRendererProps>;
export interface AgentToolWorkbenchRendererProps {
    item: AgentToolActivityItem;
    runtime: AgentSessionRuntime;
    sessionId: string;
}
export type AgentToolWorkbenchRendererComponent = ComponentType<AgentToolWorkbenchRendererProps>;
export interface AgentToolPresentation {
    icon?: LucideIcon;
    inputLabel?: string;
    label?: string;
    outputLabel?: string;
}
export type AgentToolRendererRegistration = ToolRendererRegistration<AgentToolRendererComponent, AgentToolRendererComponent, AgentToolWorkbenchRendererComponent> & {
    presentation?: AgentToolPresentation;
};
