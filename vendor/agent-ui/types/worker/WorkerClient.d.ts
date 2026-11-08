import type { AgentSessionRuntime } from "../agentSessionRuntime.js";
import { type WorkerRef, type WorkerTransport } from "./contracts.js";
import { type AgentAdapterBridge } from "./agentAdapterBridge.js";
import type { AgentAdapter } from "./agentAdapter.js";
export declare class WorkerClient {
    private readonly transports;
    private readonly sessions;
    register(transport: WorkerTransport): void;
    transportFor(ref: WorkerRef): WorkerTransport;
    resolveSession(ref: WorkerRef, signal?: AbortSignal): Promise<string>;
    runtimeFor(ref: WorkerRef, sessionId: string): AgentSessionRuntime;
    adapterFor(ref: WorkerRef, sessionId: string): AgentAdapter | undefined;
    adapterBridgeFor(ref: WorkerRef, sessionId: string, providedAdapter?: AgentAdapter): AgentAdapterBridge | undefined;
    release(sessionId: string): void;
    clearSession(ref: WorkerRef): void;
}
