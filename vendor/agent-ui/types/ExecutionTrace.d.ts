import type { AgentTimelineItem } from "./contracts.js";
import type { AgentInteractionEvent } from "./agentInteractionContracts.js";
import type { AgentExecutionView } from "./executionViewContracts.js";
export declare function ExecutionTrace({ interactions, items, execution, }: {
    interactions?: readonly AgentInteractionEvent[];
    items: readonly AgentTimelineItem[];
    execution?: AgentExecutionView;
}): import("react/jsx-runtime").JSX.Element | null;
