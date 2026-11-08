import type { AgentToolActivityItem } from "../agentToolContracts.js";
export declare function toolWorkspacePath(item: AgentToolActivityItem, workspaceRoot?: string): string | null;
export declare function toolWorkspaceLine(item: AgentToolActivityItem): number | undefined;
