export interface InitialConnection {
    promise: Promise<void>;
    reject: (error: Error) => void;
    resolve: () => void;
    settled: boolean;
}
export interface AgentSessionConnectionLifecycle {
    controller: AbortController;
    initial: InitialConnection;
    iteration?: AbortController;
    historyLoad?: Promise<void>;
    resyncRequested: boolean;
}
export declare function createInitialConnection(): InitialConnection;
export declare function resolveInitialConnection(initial: InitialConnection | undefined): void;
export declare function rejectInitialConnection(initial: InitialConnection | undefined, error: Error): void;
export declare function sessionCursor(snapshot: {
    latestSequence: bigint;
    revision: bigint;
    sessionId: string;
    streamEpoch: string;
}): import("../generated/agent_workbench/v2/session_pb.js").SessionCursor;
export declare function cursorIdentity(snapshot: {
    latestSequence: bigint;
    revision: bigint;
    streamEpoch: string;
}): string;
export declare function asConnectionError(cause: unknown): Error;
