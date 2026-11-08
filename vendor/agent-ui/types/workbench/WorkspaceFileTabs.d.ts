import type { WorkspaceOpenTab } from "./workspaceDocumentStore.js";
export declare function WorkspaceFileTabs({ activePath, onClose, onSelect, stalePaths, tabs, }: {
    activePath: string | null;
    onClose: (path: string) => void;
    onSelect: (path: string) => void;
    stalePaths: ReadonlySet<string>;
    tabs: readonly WorkspaceOpenTab[];
}): import("react/jsx-runtime").JSX.Element | null;
