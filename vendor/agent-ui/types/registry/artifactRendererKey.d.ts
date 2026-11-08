import type { AgentArtifactItem } from "../agentArtifactContracts.js";
import type { ContentRendererKey } from "./rendererKeys.js";
export declare function artifactRendererKey(item: AgentArtifactItem): ContentRendererKey | null;
export declare function artifactUnsupportedReason(item: AgentArtifactItem): string | null;
