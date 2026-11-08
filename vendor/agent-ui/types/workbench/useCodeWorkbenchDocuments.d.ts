import type { CodeWorkspaceDiff, CodeWorkspaceFile, CodeWorkspaceSource, WorkbenchPreferenceScope } from "./codeWorkspaceSource.js";
import { WorkspaceDocumentStore, type WorkspaceTabView } from "./workspaceDocumentStore.js";
export declare function useCodeWorkbenchDocuments(input: {
    preferenceScope?: WorkbenchPreferenceScope;
    sessionId: string;
    source: CodeWorkspaceSource;
}): {
    activate: (path: string) => void;
    close: (path: string) => void;
    deleted: boolean;
    diff: CodeWorkspaceDiff | null;
    failure: "refresh" | "access" | null;
    file: CodeWorkspaceFile | null;
    markStale: (path: string) => void;
    open: (path: string, view?: WorkspaceTabView) => void;
    refresh: () => Promise<void>;
    stalePaths: Set<string>;
    store: WorkspaceDocumentStore;
};
