export type WorkbenchMode = "code" | "work" | "goal";
export type CodeWorkspaceGitStatus = "ready" | "no_repository" | "no_head" | "unavailable";
export interface CodeWorkspaceCapabilities {
    list: boolean;
    read: boolean;
    diff: boolean;
    search?: boolean;
}
export interface CodeWorkspaceEntry {
    name: string;
    path: string;
    type: "file" | "directory";
    bytes?: number | null;
    modifiedAt?: number | null;
}
export interface CodeWorkspaceFile {
    path: string;
    content: string;
    encoding: "utf-8" | "base64";
    contentType: string;
    bytes: number;
    truncated: boolean;
    binary: boolean;
}
export interface CodeWorkspaceChange {
    path: string;
    name: string;
    status: "created" | "modified" | "deleted" | "renamed";
    oldPath?: string;
    newPath?: string;
    bytes?: number | null;
    modifiedAt?: number | null;
}
export interface CodeWorkspaceChanges {
    gitStatus: CodeWorkspaceGitStatus;
    gitAvailable: boolean;
    changes: CodeWorkspaceChange[];
}
export interface CodeWorkspaceDiffSide {
    present: boolean;
    content: string;
    encoding: "utf-8" | "base64";
    truncated: boolean;
}
export interface CodeWorkspaceDiff {
    path: string;
    oldPath?: string;
    newPath?: string;
    baseline: string | null;
    gitStatus: CodeWorkspaceGitStatus;
    stale: boolean;
    before: CodeWorkspaceDiffSide;
    after: CodeWorkspaceDiffSide;
}
export interface CodeWorkspaceSearchResult {
    entries: CodeWorkspaceEntry[];
    truncated: boolean;
    incompleteReason?: string;
}
export interface CodeWorkspaceGitRef {
    current: boolean;
    kind: "local" | "remote";
    name: string;
    oid: string;
    upstream?: string;
}
export interface CodeWorkspaceGitSummary {
    branch: string | null;
    clean: boolean;
    detached: boolean;
    head: string | null;
    refs: CodeWorkspaceGitRef[];
    status: CodeWorkspaceGitStatus;
}
export interface CodeWorkspaceGitCommit {
    authoredAt: string;
    author: string;
    oid: string;
    parents: string[];
    subject: string;
}
export interface CodeWorkspaceGitLog {
    commits: CodeWorkspaceGitCommit[];
    cursor: string | null;
    ref: string;
    truncated: boolean;
}
export interface CodeWorkspaceGitSource {
    log(sessionId: string, ref: string, signal: AbortSignal): Promise<CodeWorkspaceGitLog>;
    summary(sessionId: string, signal: AbortSignal): Promise<CodeWorkspaceGitSummary>;
}
export interface WorkbenchPreferenceScope {
    tenantId: string;
    userId: string;
}
export interface CodeWorkspaceSource {
    workspaceKey: string;
    workspaceKeyForSession?: (sessionId: string) => string;
    workspaceRoot?: string;
    workspaceRootForSession?: (sessionId: string) => string | undefined;
    capabilities: CodeWorkspaceCapabilities;
    list(sessionId: string, dir: string, signal: AbortSignal): Promise<CodeWorkspaceEntry[]>;
    read(sessionId: string, path: string, signal: AbortSignal): Promise<CodeWorkspaceFile>;
    changes(sessionId: string, signal: AbortSignal): Promise<CodeWorkspaceChanges>;
    diff(sessionId: string, path: string, signal: AbortSignal): Promise<CodeWorkspaceDiff>;
    search?(sessionId: string, query: string, signal: AbortSignal): Promise<CodeWorkspaceSearchResult>;
    git?: CodeWorkspaceGitSource;
}
export declare function workspaceKeyForSession(source: CodeWorkspaceSource, sessionId: string): string;
export declare function workspaceRootForSession(source: CodeWorkspaceSource, sessionId: string): string | undefined;
