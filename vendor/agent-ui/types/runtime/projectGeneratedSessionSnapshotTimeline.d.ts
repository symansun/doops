import type { PermissionRequest, TimelineItem } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentPlanStep, AgentTimelineItem } from "../contracts.js";
import { type ArtifactCatalog } from "./projectGeneratedSessionSnapshotArtifacts.js";
export interface TimelineProjection {
    items: AgentTimelineItem[];
    hiddenArtifactIds: Set<string>;
    latestUserCommandId?: string;
    plan: AgentPlanStep[];
}
export declare function projectTimeline(history: readonly TimelineItem[], catalog: ArtifactCatalog, permissions: ReadonlyMap<string, PermissionRequest>): TimelineProjection;
