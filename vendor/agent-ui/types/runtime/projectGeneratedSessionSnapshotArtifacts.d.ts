import type { ArtifactDescriptor } from "../generated/agent_workbench/v2/artifact_pb.js";
import type { AgentActivityItem, AgentArtifactItem } from "../contracts.js";
export type ArtifactCatalog = ReadonlyMap<string, ArtifactDescriptor>;
export interface ArtifactProjectionReference {
    artifactId: string;
    filename?: string;
    mediaType?: string;
    representationId?: string;
    revision?: bigint;
    role?: string;
}
export declare function createArtifactCatalog(artifacts: readonly ArtifactDescriptor[]): ArtifactCatalog;
export declare function projectArtifactDescriptor(descriptor: ArtifactDescriptor, id: string, options?: {
    representationId?: string;
    role?: string;
    schemaVersion?: string;
}): AgentArtifactItem;
export declare function projectArtifactReference(reference: ArtifactProjectionReference | undefined, id: string, catalog: ArtifactCatalog, options?: {
    label?: string;
    schemaVersion?: string;
}): AgentArtifactItem | AgentActivityItem;
