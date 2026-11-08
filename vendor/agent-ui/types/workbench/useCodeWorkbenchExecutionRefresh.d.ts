import type { AgentToolActivityItem } from "../agentToolContracts.js";
import type { AgentConnectionStatus, AgentSessionStatus } from "../contracts.js";
import type { WorkspaceDocumentStore } from "./workspaceDocumentStore.js";
import { type CodeWorkspaceSource } from "./codeWorkspaceSource.js";
export declare function useCodeWorkbenchExecutionRefresh(input: {
    connection: AgentConnectionStatus;
    onInvalidateFiles: () => void;
    onRefreshChanges: () => void | Promise<void>;
    sessionId: string;
    source: CodeWorkspaceSource;
    status: AgentSessionStatus;
    store: WorkspaceDocumentStore;
    tools: readonly AgentToolActivityItem[];
    visible: boolean;
}): void;
