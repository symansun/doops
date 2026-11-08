export type AgentHarnessProfile = "structured" | "terminal" | "hybrid" | "unknown";
export type AgentQuestionMode = "structured" | "terminal" | "none" | "unknown";
export interface AgentHarnessWorkspaceCapabilities {
    files: boolean;
    code: boolean;
    changes: boolean;
    terminal: boolean;
}
export interface AgentHarnessPermissionCapabilities {
    approval: boolean;
    questions: boolean;
    customAnswers: boolean;
}
export interface AgentHarnessCapabilities {
    artifacts: boolean;
    codeWorkspace: boolean;
    goals: boolean;
    permissions: boolean;
    subagents: boolean;
    terminal: boolean;
}
export interface AgentHarnessDescriptor {
    id: string;
    profile: AgentHarnessProfile;
    questionMode: AgentQuestionMode;
    protocol: string;
    displayName: string;
    sourceNamespaces: readonly string[];
    capabilities: AgentHarnessCapabilities;
    workspaceCapabilities?: AgentHarnessWorkspaceCapabilities;
    permissionCapabilities?: AgentHarnessPermissionCapabilities;
}
export declare const defaultAgentHarnessCapabilities: AgentHarnessCapabilities;
export declare const defaultAgentHarnessWorkspaceCapabilities: AgentHarnessWorkspaceCapabilities;
export declare const defaultAgentHarnessPermissionCapabilities: AgentHarnessPermissionCapabilities;
export declare function createAgentHarnessDescriptor(input: {
    id: string;
    displayName: string;
    questionMode?: AgentQuestionMode;
    profile?: AgentHarnessProfile;
    protocol?: string;
    sourceNamespaces?: readonly string[];
    capabilities?: Partial<AgentHarnessCapabilities>;
    workspaceCapabilities?: Partial<AgentHarnessWorkspaceCapabilities>;
    permissionCapabilities?: Partial<AgentHarnessPermissionCapabilities>;
}): AgentHarnessDescriptor;
