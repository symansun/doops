import type { AgentArtifactItem, AgentSessionRuntime } from "../contracts.js";
export interface ArtifactPreviewSource {
    url: string | null;
    blob: Blob | null;
}
export declare function loadBlobPreviewSource(runtime: AgentSessionRuntime, item: AgentArtifactItem, sessionId: string): Promise<ArtifactPreviewSource>;
export declare function loadLivePreviewSource(runtime: AgentSessionRuntime, item: AgentArtifactItem, sessionId: string): Promise<ArtifactPreviewSource>;
