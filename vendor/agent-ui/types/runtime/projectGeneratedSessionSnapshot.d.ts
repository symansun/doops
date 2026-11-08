import type { SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentConnectionStatus, AgentSessionSnapshot } from "../contracts.js";
export interface GeneratedSessionSnapshotProjectionOptions {
    title: string;
    agentLabel: string;
    connection: AgentConnectionStatus;
    interactionMode: "acp" | "pty";
    hasOlderItems: boolean;
}
export declare function projectGeneratedSessionSnapshot(snapshot: SessionSnapshot, options: GeneratedSessionSnapshotProjectionOptions): AgentSessionSnapshot;
