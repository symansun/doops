import type { ContentBlock } from "../generated/agent_workbench/v2/content_pb.js";
import type { AgentActivityItem, AgentAttachmentItem, AgentArtifactItem, AgentToolResult } from "../contracts.js";
import { type ArtifactCatalog } from "./projectGeneratedSessionSnapshotArtifacts.js";
export interface TimelineContentProjection {
    artifacts: AgentArtifactItem[];
    attachments: AgentAttachmentItem[];
    evidence: AgentActivityItem[];
    text: string[];
}
export declare function projectTimelineContent(blocks: readonly ContentBlock[], parentId: string, catalog: ArtifactCatalog): TimelineContentProjection;
export declare function projectToolResultBlocks(blocks: readonly ContentBlock[], resultId: string, catalog: ArtifactCatalog): AgentToolResult[];
