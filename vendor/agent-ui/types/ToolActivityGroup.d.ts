import type { AgentToolRendererRegistration } from "./react/rendererTypes.js";
import type { ToolRendererRegistry } from "./registry/ToolRendererRegistry.js";
import type { AgentToolActivityItem } from "./toolActivityGrouping.js";
import type { AgentSessionRuntime } from "./contracts.js";
export declare function ToolActivityGroup({ renderers, tools, runtime, sessionId, }: {
    renderers?: ToolRendererRegistry<AgentToolRendererRegistration>;
    tools: AgentToolActivityItem[];
    runtime: AgentSessionRuntime;
    sessionId: string;
}): import("react/jsx-runtime").JSX.Element;
