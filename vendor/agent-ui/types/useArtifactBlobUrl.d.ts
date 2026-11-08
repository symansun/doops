import type { AgentArtifactItem, AgentSessionRuntime } from "./contracts.js";
import "./viewers/artifactPreviewStrategies.js";
export type ArtifactBlobState = {
    status: "idle";
} | {
    status: "loading";
} | {
    status: "ready";
    url: string;
    mimeType: string | null;
    text: string | null;
    textTruncated: boolean;
} | {
    status: "error";
    code: "generation_failed" | "loading_unavailable" | "authorization_required" | "load_failed";
    retryable: boolean;
    message: string;
};
export declare function useArtifactBlobUrl(item: AgentArtifactItem, runtime: AgentSessionRuntime, sessionId: string, enabled?: boolean, attempt?: number): ArtifactBlobState;
export declare const MAX_TEXT_PREVIEW_BYTES: number;
