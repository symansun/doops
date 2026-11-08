import { type CommandReceipt } from "../generated/agent_workbench/v2/command_pb.js";
export declare function validateCommandReceipt(receipt: CommandReceipt, sessionId: string): void;
export declare function upsertCommandReceipt(receipts: readonly CommandReceipt[], nextReceipt: CommandReceipt, sessionId: string): CommandReceipt[];
