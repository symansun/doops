import { type CommandReceipt } from "../generated/agent_workbench/v2/command_pb.js";
export declare class AgentCommandReceiptError extends Error {
    readonly receipt: CommandReceipt;
    constructor(receipt: CommandReceipt);
}
export declare function throwForFailedCommandReceipt(receipt: CommandReceipt): void;
