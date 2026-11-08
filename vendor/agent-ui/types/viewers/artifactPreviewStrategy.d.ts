import type { ReactNode } from "react";
import type { AgentArtifactItem, AgentSessionRuntime } from "../contracts.js";
import type { ArtifactKind } from "../artifactPresentation.js";
import type { ArtifactPreviewSource } from "./artifactPreviewLoad.js";
export interface ArtifactPreviewContext {
    filename: string;
    fill: boolean;
    url: string;
    mimeType: string | null;
    text: string | null;
    textTruncated: boolean;
    onRetry: () => void;
}
export interface ArtifactPreviewStrategy {
    readonly kinds: readonly ArtifactKind[];
    readonly needsText: boolean;
    load(runtime: AgentSessionRuntime, item: AgentArtifactItem, sessionId: string): Promise<ArtifactPreviewSource>;
    render(context: ArtifactPreviewContext): ReactNode;
}
export declare function registerArtifactPreviewStrategy(strategy: ArtifactPreviewStrategy): void;
export declare function artifactPreviewStrategy(kind: ArtifactKind): ArtifactPreviewStrategy | undefined;
