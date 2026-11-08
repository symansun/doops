import type { ContentBlock } from "../generated/agent_workbench/v2/content_pb.js";
import type { AgentActivityItem } from "../contracts.js";
export declare function unsupportedContentEvidence(block: ContentBlock, id: string): AgentActivityItem | undefined;
export declare function toolArtifactMetadata(block: ContentBlock): unknown | undefined;
