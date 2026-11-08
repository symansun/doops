import type { CodeWorkspaceChanges, CodeWorkspaceSource } from "./codeWorkspaceSource.js";
import type { WorkspaceDirectoryStore } from "./workspaceDirectoryStore.js";
export declare function CodeWorkbenchSidebar({ changes, collapsed, nav, onNav, onOpenDiff, onOpenFile, revealPath, selectedPath, sessionId, source, directoryStore, }: {
    changes: CodeWorkspaceChanges | null;
    collapsed: boolean;
    nav: "files" | "changes" | "git";
    onNav: (nav: "files" | "changes" | "git") => void;
    onOpenDiff: (path: string) => void;
    onOpenFile: (path: string) => void;
    revealPath?: string | null;
    selectedPath: string | null;
    sessionId: string;
    source: CodeWorkspaceSource;
    directoryStore?: WorkspaceDirectoryStore;
}): import("react/jsx-runtime").JSX.Element | null;
