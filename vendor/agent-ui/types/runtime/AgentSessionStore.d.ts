import { type CommandEnvelope, type CommandReceipt } from "../generated/agent_workbench/v2/command_pb.js";
import type { SessionDeltaBatch, SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import { type AgentSessionResyncReason, type AgentSessionState } from "./agentSessionReducer.js";
export interface AgentSessionTransport {
    execute(command: CommandEnvelope): Promise<CommandReceipt>;
    requestResync(sessionId: string, reason: AgentSessionResyncReason): Promise<void>;
    loadOlder?(signal?: AbortSignal, minimumHistorySize?: number): Promise<SessionSnapshot>;
    getSnapshot?(signal?: AbortSignal, minimumHistorySize?: number): Promise<SessionSnapshot>;
}
export declare class AgentSessionStore {
    private readonly transport;
    private state;
    private readonly listeners;
    private readonly seenEpochs;
    private readonly historyLoader;
    constructor(transport: AgentSessionTransport);
    getState(): AgentSessionState | null;
    subscribe(listener: () => void): () => void;
    loadOlder(signal: AbortSignal): Promise<void>;
    applySnapshot(snapshot: SessionSnapshot): void;
    replaceSnapshot(snapshot: SessionSnapshot): void;
    applyDeltaBatch(batch: SessionDeltaBatch): void;
    execute(command: CommandEnvelope): Promise<CommandReceipt>;
    getCommandReceipt(commandId: string): CommandReceipt | undefined;
    requestResync(reason: AgentSessionResyncReason): Promise<void>;
    private requireState;
    private requireReadyState;
    private commit;
    private rememberEpoch;
    private publish;
}
