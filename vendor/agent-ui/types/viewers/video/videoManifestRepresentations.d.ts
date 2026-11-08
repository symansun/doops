import type { AgentArtifactRepresentation, AgentVideoManifest } from "../../agentArtifactContracts.js";
import type { ArtifactRepresentationUrlState } from "../../useArtifactRepresentationUrls.js";
import type { VideoArtifactVersion } from "./VideoArtifactViewer.js";
export declare function videoRepresentations(representations: readonly AgentArtifactRepresentation[], manifest: AgentVideoManifest): AgentArtifactRepresentation[];
export declare function videoPosterRepresentation(representations: readonly AgentArtifactRepresentation[], manifest: AgentVideoManifest): AgentArtifactRepresentation | undefined;
export declare function videoVersions(representations: readonly AgentArtifactRepresentation[], resources: Readonly<Record<string, ArtifactRepresentationUrlState>>): VideoArtifactVersion[];
