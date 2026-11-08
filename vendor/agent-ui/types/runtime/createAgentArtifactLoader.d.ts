import type { ArtifactDescriptor, ArtifactRepresentation } from "../generated/agent_workbench/v2/artifact_pb.js";
import type { AgentArtifactLoadRequest } from "./AgentSessionRuntimeV2.js";
export interface AgentArtifactTransportResources {
    getArtifacts(sessionId: string): readonly ArtifactDescriptor[];
    loadDownload(url: string, context: AgentArtifactTransportContext): Promise<Blob>;
    loadResource(resourceId: string, context: AgentArtifactTransportContext): Promise<Blob>;
}
export interface AgentArtifactTransportContext extends AgentArtifactLoadRequest {
    descriptor: ArtifactDescriptor;
    representation: ArtifactRepresentation;
}
export declare function createAgentArtifactLoader(resources: AgentArtifactTransportResources): (request: AgentArtifactLoadRequest) => Promise<Blob>;
