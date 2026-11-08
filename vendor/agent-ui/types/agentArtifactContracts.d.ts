import type { AgentExecutionReference } from "./agentExecutionContracts.js";
export interface AgentArtifactDimensions {
    height: number;
    width: number;
}
export interface AgentArtifactRepresentation {
    byteSize?: bigint;
    digest?: string;
    dimensions?: AgentArtifactDimensions;
    durationMillis?: bigint;
    filename?: string;
    mediaType: string;
    representationId: string;
    revision: bigint;
    role?: string;
    status: "queued" | "processing" | "ready" | "failed" | "deleted" | "unknown";
}
export interface AgentArtifactGrant {
    actions: string[];
    expiresAt?: string;
    grantId: string;
    issuedAt?: string;
    issuer?: string;
    maximumRevision?: bigint;
    minimumRevision?: bigint;
    representationIds: string[];
    subject?: string;
}
export interface AgentArtifactProvenance {
    commandId?: string;
    publicationToolExecutionId?: string;
    producerId?: string;
    producerNamespace?: string;
    producerType?: string;
}
export interface AgentNormalizedRegion {
    height: number;
    width: number;
    x: number;
    y: number;
}
export interface AgentNormalizedPoint {
    x: number;
    y: number;
}
export interface AgentStructuredPayload {
    data: Uint8Array;
    mediaType: string;
}
export interface AgentImageAnnotation {
    annotationId: string;
    label?: string;
    path: AgentNormalizedPoint[];
    style?: AgentStructuredPayload;
}
export interface AgentImageEditManifest {
    annotations: AgentImageAnnotation[];
    candidateRepresentationIds: string[];
    exifOrientation?: string;
    kind: "image_edit";
    maskRepresentationId?: string;
    regions: AgentNormalizedRegion[];
    resultRepresentationId?: string;
    sourceDimensions: AgentArtifactDimensions;
    sourceRepresentationId: string;
}
export interface AgentVideoManifest {
    derivativeRepresentationIds: string[];
    dimensions?: AgentArtifactDimensions;
    durationMillis?: bigint;
    kind: "video";
    originalRepresentationId?: string;
    playableRepresentationId?: string;
    posterRepresentationId?: string;
    progressFraction?: number;
    stage: "queued" | "rendering" | "transcoding" | "ready" | "failed" | "unknown";
    thumbnailRepresentationIds: string[];
}
export interface AgentPresentationSlide {
    notes?: string;
    pageRepresentationId?: string;
    position: number;
    slideId: string;
    thumbnailRepresentationId?: string;
    title?: string;
}
export interface AgentPresentationVersion {
    id: string;
    label: string;
    revision: bigint;
}
export interface AgentPresentationManifest {
    deckRevision: bigint;
    kind: "presentation";
    selectedVersionId?: string;
    slides: AgentPresentationSlide[];
    versions: AgentPresentationVersion[];
}
export interface AgentExtensionArtifactManifest {
    kind: "extension";
    namespace: string;
    payload?: AgentStructuredPayload;
    schemaVersion: string;
    semanticType: string;
}
export interface AgentUnsupportedArtifactManifest {
    identity?: {
        namespace: string;
        schemaVersion: string;
        semanticKey: string;
        sourceType?: string;
    };
    kind: "unsupported";
    payload?: AgentStructuredPayload;
    reason: "unknown" | "unsupported" | "invalid" | "unspecified";
}
export type AgentArtifactManifest = AgentImageEditManifest | AgentVideoManifest | AgentPresentationManifest | AgentExtensionArtifactManifest | AgentUnsupportedArtifactManifest;
export interface AgentArtifactItem {
    actions: string[];
    artifactId: string;
    filename: string;
    grants: AgentArtifactGrant[];
    id: string;
    kind: "artifact";
    manifest: AgentArtifactManifest | null;
    mimeType: string | null;
    provenance?: AgentArtifactProvenance;
    representations: AgentArtifactRepresentation[];
    revision: bigint;
    role: string;
    scope?: AgentResultScope;
    schemaVersion: string;
    selectedRepresentationId: string | null;
    status: "queued" | "processing" | "completed" | "failed";
    execution?: AgentExecutionReference;
}
export interface AgentArtifactActionCommand<TActionType extends string = string, TPayload = unknown> {
    actionSchemaVersion: string;
    actionType: TActionType;
    artifactId: string;
    baseRevision: bigint;
    commandId: string;
    payload: TPayload;
    representationId?: string;
}
import type { AgentResultScope } from "./agentResultScope.js";
