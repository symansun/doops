import type { CodeWorkspaceChanges } from "./codeWorkspaceSource.js";
export declare function WorkspaceChangesList({ changes, onOpenDiff, selectedPath, }: {
    changes: CodeWorkspaceChanges | null;
    onOpenDiff: (path: string) => void;
    selectedPath: string | null;
}): import("react/jsx-runtime").JSX.Element;
