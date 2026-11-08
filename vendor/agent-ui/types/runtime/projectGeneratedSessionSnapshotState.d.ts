import type { PermissionRequest, SessionResource, SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentActivityItem, AgentConnectionStatus, AgentPermissionRequest, AgentWorkspaceCapabilities, TerminalResource } from "../contracts.js";
export declare function projectPermissions(requests: readonly PermissionRequest[]): {
    evidence: AgentActivityItem[];
    permissions: AgentPermissionRequest[];
};
export declare function projectTerminals(resources: readonly SessionResource[]): TerminalResource[];
export declare function projectCapabilities(snapshot: SessionSnapshot, connection: AgentConnectionStatus, terminals: readonly TerminalResource[], interactionMode: "acp" | "pty"): AgentWorkspaceCapabilities;
