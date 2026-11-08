import type { WorkspaceOpenTab } from "./workspaceDocumentStore.js";
export declare function CodeWorkbenchToolbar({ activePath, canCopy, collapsed, onClose, onCopy, onFind, onRefresh, onReference, onSelect, onToggleFiles, stalePaths, tabs, workspaceLabel, }: {
    activePath: string | null;
    canCopy: boolean;
    collapsed: boolean;
    onClose: (path: string) => void;
    onCopy: () => void;
    onFind: () => void;
    onRefresh: () => void;
    onReference: () => void;
    onSelect: (path: string) => void;
    onToggleFiles: () => void;
    stalePaths: ReadonlySet<string>;
    tabs: readonly WorkspaceOpenTab[];
    workspaceLabel: string;
}): import("react/jsx-runtime").JSX.Element;
