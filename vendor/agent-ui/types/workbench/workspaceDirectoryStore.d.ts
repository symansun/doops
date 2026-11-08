import type { CodeWorkspaceEntry, CodeWorkspaceSource } from "./codeWorkspaceSource.js";
export type WorkspaceDirectoryStatus = "idle" | "loading" | "ready" | "failed";
export interface WorkspaceDirectorySnapshot {
    entries: ReadonlyMap<string, readonly CodeWorkspaceEntry[]>;
    states: ReadonlyMap<string, WorkspaceDirectoryStatus>;
}
export declare class WorkspaceDirectoryStore {
    private readonly source;
    private readonly sessionId;
    private readonly listeners;
    private readonly entries;
    private readonly states;
    private snapshot;
    private readonly requests;
    private disposed;
    constructor(source: CodeWorkspaceSource, sessionId: string);
    getSnapshot(): WorkspaceDirectorySnapshot;
    subscribe(listener: () => void): () => void;
    load(dir: string): Promise<void>;
    invalidate(dir: string): void;
    invalidateAll(): void;
    dispose(): void;
    private notify;
}
