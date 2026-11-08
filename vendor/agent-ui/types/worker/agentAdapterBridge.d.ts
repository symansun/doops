import type { AgentCommand, AgentSessionRuntime } from "../contracts.js";
import type { AgentAdapter, AgentAdapterSessionContext } from "./agentAdapter.js";
export interface AgentAdapterBridge<TEvent = unknown, TCommand = unknown, TProjection = unknown> {
    readonly adapter: AgentAdapter<TEvent, TCommand, TProjection>;
    readonly sessionId: string;
    context(): AgentAdapterSessionContext;
    projectCommand(command: AgentCommand): TCommand;
    decodeEvent(event: unknown): TEvent;
    projectEvent(event: unknown): TProjection | readonly TProjection[] | null;
}
export declare function createAgentAdapterBridge<TEvent, TCommand, TProjection>(adapter: AgentAdapter<TEvent, TCommand, TProjection>, runtime: AgentSessionRuntime, sessionId: string): AgentAdapterBridge<TEvent, TCommand, TProjection>;
