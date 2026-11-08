import { type AgentWorkbenchSessionTransport } from "./AgentWorkbenchConnectTransport.js";
import { AgentSessionStore } from "./AgentSessionStore.js";
export type AgentSessionConnectionStatus = "idle" | "connecting" | "connected" | "reconnecting" | "disconnected" | "failed";
export interface AgentSessionConnectionOptions {
    maxConsecutiveFailures?: number;
    retryDelay?: (attempt: number, signal: AbortSignal) => Promise<void>;
}
export declare class AgentSessionConnection {
    private readonly transport;
    private readonly listeners;
    private readonly maxConsecutiveFailures;
    private readonly retryDelay;
    private readonly store;
    private error;
    private status;
    private lifecycle?;
    constructor(transport: AgentWorkbenchSessionTransport, options?: AgentSessionConnectionOptions);
    open(): Promise<void>;
    close(): void;
    getStore(): AgentSessionStore;
    getStatus(): AgentSessionConnectionStatus;
    getError(): Error | null;
    loadOlder(): Promise<void>;
    subscribe(listener: () => void): () => void;
    private activeSignal;
    private requestResync;
    private run;
    private fail;
    private setStatus;
}
