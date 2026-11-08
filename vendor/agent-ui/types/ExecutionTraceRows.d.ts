import type { AgentInteractionEvent } from "./agentInteractionContracts.js";
import type { AgentExecutionTraceGroup } from "./executionTraceProjection.js";
import type { ExecutionTraceText } from "./executionTraceText.js";
export declare function InteractionTrace({ interaction, text, }: {
    interaction: AgentInteractionEvent;
    text: ExecutionTraceText;
}): import("react/jsx-runtime").JSX.Element;
export declare function TraceGroup({ group, text, }: {
    group: AgentExecutionTraceGroup;
    text: ExecutionTraceText;
}): import("react/jsx-runtime").JSX.Element;
