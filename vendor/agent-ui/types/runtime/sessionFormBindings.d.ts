import type { AgentSessionRuntime } from "../agentSessionRuntime.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import type { SessionCommandPayload } from "./executeSessionCommand.js";
import type { CommandReceipt } from "../generated/agent_workbench/v2/command_pb.js";
export declare function sessionFormBindings(connection: AgentSessionConnection, requireSession: (id: string) => void, execute: (sessionId: string, commandId: string, command: SessionCommandPayload) => Promise<CommandReceipt>): Pick<AgentSessionRuntime, "executeExtension" | "getFormState">;
