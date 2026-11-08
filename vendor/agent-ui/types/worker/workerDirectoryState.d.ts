export interface WorkerDirectoryState {
    activeSessionId?: string;
    archivedSessionIds: readonly string[];
    expandedGroupIds: readonly string[];
    sessionOrder: readonly string[];
}
export declare const emptyWorkerDirectoryState: WorkerDirectoryState;
export declare function reconcileWorkerSessionOrder(sessionIds: readonly string[], savedOrder: readonly string[]): string[];
export declare function workerDirectoryStorageKey(workerId: string): string;
export declare function readWorkerDirectoryState(storage: Pick<Storage, "getItem"> | null, workerId: string): WorkerDirectoryState;
export declare function writeWorkerDirectoryState(storage: Pick<Storage, "setItem"> | null, workerId: string, state: WorkerDirectoryState): void;
