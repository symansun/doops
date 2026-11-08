import type { AgentToolActivityItem } from "./agentToolContracts.js";
import type { AgentTimelineItem } from "./contracts.js";
export type { AgentToolActivityItem } from "./agentToolContracts.js";
export interface ToolActivityRun {
    id: string;
    kind: "tool-run";
    tools: AgentToolActivityItem[];
}
export type GroupedTimelineItem = AgentTimelineItem | ToolActivityRun;
export declare function groupToolActivity(items: AgentTimelineItem[]): GroupedTimelineItem[];
