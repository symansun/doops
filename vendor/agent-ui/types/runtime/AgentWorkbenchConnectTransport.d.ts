import type { CommandEnvelope, CommandReceipt } from "../generated/agent_workbench/v2/command_pb.js";
import type { SessionCursor, SessionDeltaBatch, SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
export interface AgentWorkbenchSessionTransport {
    execute(command: CommandEnvelope, signal?: AbortSignal): Promise<CommandReceipt>;
    getSnapshot(signal?: AbortSignal, minimumHistorySize?: number): Promise<SessionSnapshot>;
    loadOlder?(signal?: AbortSignal, minimumHistorySize?: number): Promise<SessionSnapshot>;
    streamDeltas(cursor: SessionCursor, signal: AbortSignal): AsyncIterable<SessionDeltaBatch>;
}
export interface AgentWorkbenchConnectTransportOptions {
    baseUrl: string;
    fetch?: typeof globalThis.fetch;
    getAccessToken: () => Promise<string> | string;
    tenantId: string;
    replayLimit?: number;
    sessionId: string;
}
export declare class AgentWorkbenchConnectTransport implements AgentWorkbenchSessionTransport {
    private readonly client;
    private readonly tenantId;
    private readonly replayLimit;
    private readonly sessionId;
    private historyLimit;
    private expansionPending;
    constructor(options: AgentWorkbenchConnectTransportOptions);
    getSnapshot(signal?: AbortSignal, minimumHistorySize?: number): Promise<SessionSnapshot>;
    loadOlder(signal?: AbortSignal, minimumHistorySize?: number): Promise<SessionSnapshot>;
    streamDeltas(cursor: SessionCursor, signal: AbortSignal): AsyncIterable<SessionDeltaBatch>;
    execute(command: CommandEnvelope, signal?: AbortSignal): Promise<CommandReceipt>;
}
export declare function isAgentWorkbenchCursorRejected(error: unknown): boolean;
