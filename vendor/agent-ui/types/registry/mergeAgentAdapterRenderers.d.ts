import type { AgentAdapter } from "../worker/agentAdapter.js";
import type { AgentContentRendererRegistration } from "../react/contentRendererTypes.js";
import type { AgentToolRendererRegistration } from "../react/rendererTypes.js";
import { ContentRendererRegistry } from "./ContentRendererRegistry.js";
import { ToolRendererRegistry } from "./ToolRendererRegistry.js";
export interface AgentAdapterRendererSet {
    contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>;
    toolRenderers?: ToolRendererRegistry<AgentToolRendererRegistration>;
}
export declare function mergeAgentAdapterRenderers(adapter: AgentAdapter | undefined, contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>, toolRenderers?: ToolRendererRegistry<AgentToolRendererRegistration>): AgentAdapterRendererSet;
