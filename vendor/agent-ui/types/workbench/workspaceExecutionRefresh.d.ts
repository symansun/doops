import type { AgentToolStatus } from "../agentToolContracts.js";
export interface RefreshToolEdge {
    id: string;
    path: string | null;
    semanticKey: string;
    status: AgentToolStatus;
}
export declare function nextRefresh(input: {
    seen: ReadonlyMap<string, AgentToolStatus>;
    tools: readonly RefreshToolEdge[];
}): {
    invalidatePaths: string[];
    refreshChangesAndCurrent: boolean;
    seen: Map<string, AgentToolStatus>;
};
export declare function parentWorkspaceDir(path: string): string;
