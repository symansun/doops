import type { WorkspaceFileEntry } from "./workspaceFileSource.js";
export declare function MentionMenu({ entries, selectedIndex, onSelect, onOpenDir, }: {
    entries: readonly WorkspaceFileEntry[];
    selectedIndex: number;
    onSelect: (path: string, isDir: boolean) => void;
    onOpenDir: (path: string) => void;
}): import("react/jsx-runtime").JSX.Element | null;
