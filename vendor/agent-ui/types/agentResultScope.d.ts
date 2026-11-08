export interface AgentResultScope {
    workerId?: string;
    sessionId: string;
    turnId?: string;
    producerId?: string;
    workspaceKey?: string;
}
export declare function resultScopeKey(scope: AgentResultScope): string;
