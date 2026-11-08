import { type CodeWorkspaceFile, type CodeWorkspaceSource, type WorkbenchPreferenceScope } from "./codeWorkspaceSource.js";
import { WorkspaceDocumentCache } from "./workspaceDocumentCache.js";
export type WorkspaceTabView = "source" | "diff";
export interface WorkspaceOpenTab {
    workspaceKey: string;
    path: string;
    view: WorkspaceTabView;
}
export declare class WorkspaceDocumentStore {
    private readonly source;
    private readonly scope;
    readonly sessionId: string;
    readonly cache: WorkspaceDocumentCache;
    tabs: WorkspaceOpenTab[];
    active: WorkspaceOpenTab | null;
    private readonly generations;
    private readonly inflight;
    constructor(source: CodeWorkspaceSource, scope: WorkbenchPreferenceScope, sessionId: string, cache?: WorkspaceDocumentCache);
    get workspaceKey(): string;
    open(path: string, view?: WorkspaceTabView): void;
    activate(path: string): void;
    close(path: string): void;
    invalidate(path: string): void;
    invalidateDir(dir: string): void;
    clearScope(): void;
    load(path: string, signal: AbortSignal): Promise<CodeWorkspaceFile | null>;
    private tabFor;
    private fileKey;
    private bump;
}
