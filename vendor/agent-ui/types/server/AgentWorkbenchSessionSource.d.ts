import { type CommandEnvelope, type CommandReceipt } from "../generated/agent_workbench/v2/command_pb.js";
import { type AgentEvent, type EventEnvelope, type SessionCursor, type SessionDeltaBatch, type SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
export type AgentWorkbenchEventInput = Pick<EventEnvelope, "itemId" | "turnId" | "parentId" | "causationCommandId"> & {
    createdAt?: string;
    event: AgentEvent["event"];
};
export interface AgentWorkbenchSessionSourceOptions {
    snapshot: SessionSnapshot;
    replayLimit?: number;
}
export type AgentWorkbenchCommandHandler = (command: CommandEnvelope) => Promise<CommandReceipt>;
export declare class AgentWorkbenchSessionSource {
    private readonly store;
    private readonly replayLimit;
    private readonly batches;
    private readonly listeners;
    private readonly commands;
    private publications;
    constructor(options: AgentWorkbenchSessionSourceOptions);
    getSnapshot(): SessionSnapshot;
    emit(events: readonly AgentWorkbenchEventInput[], streamEpoch: string): Promise<SessionDeltaBatch>;
    resetEpoch(snapshot: SessionSnapshot): Promise<void>;
    replay(cursor: SessionCursor): SessionDeltaBatch[];
    subscribe(cursor: SessionCursor, signal: AbortSignal): AsyncIterable<SessionDeltaBatch>;
    execute(command: CommandEnvelope, handler: AgentWorkbenchCommandHandler): Promise<CommandReceipt>;
    private enqueue;
    private wake;
}
