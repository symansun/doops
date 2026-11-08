import type { AgentSessionRuntime, AgentSessionSnapshot } from "./contracts.js";
export declare function TaskQueuePreview({ runtime, sessionId, queue, }: {
    runtime: AgentSessionRuntime;
    sessionId: string;
    queue: AgentSessionSnapshot["queue"];
}): import("react/jsx-runtime").JSX.Element | null;
