import type { CodeWorkspaceEntry } from "./codeWorkspaceSource.js";
export declare function WorkspaceFileTreeRow({ depth, entry, expanded, onOpen, selected, top }: {
    depth: number;
    entry: CodeWorkspaceEntry;
    expanded: boolean;
    onOpen: () => void;
    selected: boolean;
    top: number;
}): import("react/jsx-runtime").JSX.Element;
export declare function toggleWorkspaceDirectory(path: string, expanded: Set<string>, setExpanded: (next: Set<string>) => void, loadDirectory: (path: string) => Promise<void>): void;
