import { type ArtifactManifest } from "../generated/agent_workbench/v2/artifact_pb.js";
import type { AgentArtifactManifest } from "../agentArtifactContracts.js";
export declare function projectManifest(manifest: ArtifactManifest | undefined): AgentArtifactManifest | null;
