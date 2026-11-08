import type { AgentArtifactItem } from "./agentArtifactContracts.js";
export declare function markdownArtifactReference(source: string | undefined, artifacts: readonly AgentArtifactItem[], origin?: string): AgentArtifactItem | undefined;
export declare function markdownWorkspaceOutputPath(source: string | undefined, origin?: string): string | undefined;
