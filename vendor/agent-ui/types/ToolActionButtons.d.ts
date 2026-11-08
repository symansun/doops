import type { AgentSessionRuntime } from "./contracts.js";
import type { AgentToolActivityItem } from "./agentToolContracts.js";
export declare function ToolActionButtons({ item, runtime, sessionId, }: {
    item: AgentToolActivityItem;
    runtime: AgentSessionRuntime;
    sessionId: string;
}): import("react/jsx-runtime").JSX.Element | null;
