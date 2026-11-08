import { type CommandEnvelope } from "../generated/agent_workbench/v2/command_pb.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import { createAgentCommandEnvelope } from "./createAgentCommandEnvelope.js";
export declare function retrySessionCommand(connection: AgentSessionConnection, input: Parameters<typeof createAgentCommandEnvelope>[0]): Promise<CommandEnvelope>;
export declare function forgetRejectedSessionCommand(connection: AgentSessionConnection, envelope: CommandEnvelope): void;
