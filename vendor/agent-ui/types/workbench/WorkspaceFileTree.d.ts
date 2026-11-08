import { type CodeWorkspaceSource } from "./codeWorkspaceSource.js";
import { WorkspaceDirectoryStore } from "./workspaceDirectoryStore.js";
export declare function WorkspaceFileTree({ onOpenFile, revealPath, selectedPath, sessionId, source, directoryStore }: {
    onOpenFile: (path: string) => void;
    revealPath?: string | null;
    selectedPath: string | null;
    sessionId: string;
    source: CodeWorkspaceSource;
    directoryStore?: WorkspaceDirectoryStore;
}): import("react/jsx-runtime").JSX.Element;
