import { type CommandReceipt } from "../generated/agent_workbench/v2/command_pb.js";
import type { AgentInteractionEvent } from "../agentInteractionContracts.js";
export declare function projectSessionInteractions(receipts: readonly CommandReceipt[]): AgentInteractionEvent[];
