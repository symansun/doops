import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import { createAgentCommandEnvelope } from "./createAgentCommandEnvelope.js";
export type SessionCommandPayload = Parameters<typeof createAgentCommandEnvelope>[0]["command"];
export declare function executeSessionCommand(connection: AgentSessionConnection, sessionId: string, commandId: string, command: SessionCommandPayload, issuedAt: string, isCurrent: () => boolean): Promise<import("../generated/agent_workbench/v2/command_pb.js").CommandReceipt>;
