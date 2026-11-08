import type { CodeWorkspaceFile } from "./codeWorkspaceSource.js";
export declare function workspaceCacheKey(tenantId: string, userId: string, sessionId: string, workspaceKey: string, path: string): string;
export declare function decodedFileBytes(file: CodeWorkspaceFile): number;
export declare class WorkspaceDocumentCache {
    private readonly files;
    private readonly order;
    get(key: string): CodeWorkspaceFile | undefined;
    set(key: string, file: CodeWorkspaceFile, retainKey?: string): void;
    delete(key: string): void;
    clearScope(prefix: string): void;
    size(): number;
    private touch;
    private evict;
    private totalBytes;
}
