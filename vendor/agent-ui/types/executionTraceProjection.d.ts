import type { AgentTimelineItem } from "./contracts.js";
export interface AgentExecutionTraceGroup {
    id: string;
    items: AgentTimelineItem[];
    status: "completed" | "failed" | "running";
    turnId?: string;
}
export declare function executionTraceGroups(items: readonly AgentTimelineItem[]): AgentExecutionTraceGroup[];
