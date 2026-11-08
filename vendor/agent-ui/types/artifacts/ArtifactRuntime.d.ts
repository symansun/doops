import type { ArtifactActionCommand } from "./artifactAction.js";
export type ArtifactGrant<TActionType extends string = string> = TActionType;
export interface ArtifactDescriptor<TActionType extends string = string> {
    readonly artifactId: string;
    readonly representationId: string;
    readonly revision: bigint;
    readonly mimeType: string;
    readonly filename: string;
    readonly grants: readonly ArtifactGrant<TActionType>[];
}
export type ArtifactPayload = Blob;
export interface ArtifactDownloadRequest {
    readonly artifactId: string;
    readonly representationId: string;
    readonly revision: bigint;
    readonly filename: string;
}
export interface ArtifactRuntime<TRepresentationPayload = ArtifactPayload, TActionResult = unknown> {
    loadRepresentation(artifactId: string, representationId: string, revision: bigint): Promise<TRepresentationPayload>;
    download(request: ArtifactDownloadRequest): Promise<void>;
    executeAction(command: ArtifactActionCommand): Promise<TActionResult>;
    subscribe(artifactId: string, listener: () => void): () => void;
}
