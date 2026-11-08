import type { AgentArtifactItem, AgentSessionRuntime } from "./contracts.js";
export type ArtifactRepresentationUrlState = {
    status: "loading";
} | {
    status: "ready";
    mimeType: string;
    url: string;
} | {
    status: "error";
    message: string;
};
export declare function useArtifactRepresentationUrls(item: AgentArtifactItem, runtime: AgentSessionRuntime, sessionId: string, representationIds: readonly string[]): Readonly<Record<string, ArtifactRepresentationUrlState>>;
