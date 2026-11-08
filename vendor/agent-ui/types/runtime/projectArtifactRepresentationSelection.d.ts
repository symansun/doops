import { type ArtifactDescriptor, type ArtifactRepresentation } from "../generated/agent_workbench/v2/artifact_pb.js";
export declare function selectArtifactRepresentation(descriptor: ArtifactDescriptor, requestedId?: string): ArtifactRepresentation | undefined;
export declare function extensionSchemaVersion(descriptor: ArtifactDescriptor): string | undefined;
