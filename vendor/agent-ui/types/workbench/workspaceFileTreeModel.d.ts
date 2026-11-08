import type { CodeWorkspaceEntry } from "./codeWorkspaceSource.js";
export interface WorkspaceTreeRow {
    depth: number;
    entry: CodeWorkspaceEntry;
}
export declare function sortWorkspaceEntries(entries: readonly CodeWorkspaceEntry[]): CodeWorkspaceEntry[];
export declare function ancestorWorkspaceDirs(path: string): string[];
export declare function flattenExpandedTree(roots: readonly CodeWorkspaceEntry[], expanded: ReadonlySet<string>, childrenByDir: ReadonlyMap<string, readonly CodeWorkspaceEntry[]>): WorkspaceTreeRow[];
