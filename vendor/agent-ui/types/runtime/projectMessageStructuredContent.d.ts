import type { ContentBlock } from "../generated/agent_workbench/v2/content_pb.js";
import type { AgentMessageContent } from "../agentStructuredContent.js";
export { isTranscriptMetadata } from "./transcriptMessageVisibility.js";
export declare function projectMessageStructuredContent(blocks: readonly ContentBlock[]): AgentMessageContent[] | undefined;
