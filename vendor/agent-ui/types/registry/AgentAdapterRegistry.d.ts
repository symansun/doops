import type { AgentAdapter, AnyAgentAdapter } from "../worker/agentAdapter.js";
import type { AgentContentRendererRegistration } from "../react/contentRendererTypes.js";
import type { AgentToolRendererRegistration } from "../react/rendererTypes.js";
import { ContentRendererRegistry } from "./ContentRendererRegistry.js";
import { ToolRendererRegistry } from "./ToolRendererRegistry.js";
export interface AgentAdapterRegistryOptions {
    contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>;
    toolRenderers?: ToolRendererRegistry<AgentToolRendererRegistration>;
}
export declare class AgentAdapterRegistry {
    readonly contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>;
    readonly toolRenderers?: ToolRendererRegistry<AgentToolRendererRegistration>;
    private readonly adapters;
    constructor(options?: AgentAdapterRegistryOptions);
    register<TEvent, TCommand, TProjection>(adapter: AgentAdapter<TEvent, TCommand, TProjection>): void;
    get(id: string): AnyAgentAdapter | undefined;
    require(id: string): AnyAgentAdapter;
    list(): readonly AnyAgentAdapter[];
}
