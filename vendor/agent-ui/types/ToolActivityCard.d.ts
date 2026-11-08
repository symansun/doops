import type { AgentToolActivityItem } from "./agentToolContracts.js";
import type { AgentSessionRuntime } from "./contracts.js";
import type { AgentToolRendererRegistration } from "./react/rendererTypes.js";
import type { ToolRendererRegistry } from "./registry/ToolRendererRegistry.js";
export declare function ToolActivityCard({ item, renderers, runtime, sessionId, }: {
    item: AgentToolActivityItem;
    renderers?: ToolRendererRegistry<AgentToolRendererRegistration>;
    runtime: AgentSessionRuntime;
    sessionId: string;
}): import("react/jsx-runtime").JSX.Element;
