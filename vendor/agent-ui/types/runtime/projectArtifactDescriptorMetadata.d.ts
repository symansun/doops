import { ArtifactStatus, type ArtifactGrant, type ArtifactRepresentation } from "../generated/agent_workbench/v2/artifact_pb.js";
import type { AgentArtifactGrant, AgentArtifactItem, AgentArtifactRepresentation } from "../agentArtifactContracts.js";
export declare function projectRepresentations(representations: readonly ArtifactRepresentation[]): AgentArtifactRepresentation[];
export declare function projectGrants(grants: readonly ArtifactGrant[]): AgentArtifactGrant[];
export declare function projectGrantActions(grants: readonly AgentArtifactGrant[], revision: bigint): string[];
export declare function projectArtifactStatus(status: ArtifactStatus): AgentArtifactItem["status"];
