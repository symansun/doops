import type { ComponentType } from "react";
import type { AgentArtifactItem } from "../agentArtifactContracts.js";
import type { AgentToolActivityItem } from "../agentToolContracts.js";
import type { ToolRendererRegistry } from "../registry/ToolRendererRegistry.js";
import type { AgentToolRendererRegistration, AgentToolWorkbenchRendererProps } from "./rendererTypes.js";
import { type AgentResultScope } from "../agentResultScope.js";
export type WorkbenchResult = {
    id: string;
    item: AgentArtifactItem;
    kind: "artifact";
} | {
    id: string;
    item: AgentToolActivityItem;
    kind: "tool";
    Renderer: ComponentType<AgentToolWorkbenchRendererProps>;
};
export declare function collectWorkbenchResults(artifacts: readonly AgentArtifactItem[], tools: readonly AgentToolActivityItem[], toolRenderers?: ToolRendererRegistry<AgentToolRendererRegistration>, verifiedArtifactsOnly?: boolean, workspaceArtifacts?: readonly AgentArtifactItem[], scope?: AgentResultScope): WorkbenchResult[];
