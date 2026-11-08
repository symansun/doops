import type { AgentSessionRuntime, AgentTimelineItem } from "./contracts.js";
import type { AgentToolRendererRegistration } from "./react/rendererTypes.js";
import type { AgentContentRendererRegistration } from "./react/contentRendererTypes.js";
import type { ContentRendererRegistry } from "./registry/ContentRendererRegistry.js";
import type { ToolRendererRegistry } from "./registry/ToolRendererRegistry.js";
export declare function ActivityTimeline({ items, runtime, sessionId, contentRenderers, toolRenderers, }: {
    contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>;
    items: AgentTimelineItem[];
    runtime: AgentSessionRuntime;
    sessionId: string;
    toolRenderers?: ToolRendererRegistry<AgentToolRendererRegistration>;
}): import("react/jsx-runtime").JSX.Element;
