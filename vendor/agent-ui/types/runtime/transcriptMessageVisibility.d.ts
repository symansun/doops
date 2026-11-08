import type { ContentBlock, UnsupportedValue } from "../generated/agent_workbench/v2/content_pb.js";
export declare function isTranscriptMetadata(block: ContentBlock): boolean;
export declare function isHiddenTranscriptMessage(blocks: readonly ContentBlock[]): boolean;
export declare function isHiddenPreservedTranscriptMessage(value: UnsupportedValue): boolean;
