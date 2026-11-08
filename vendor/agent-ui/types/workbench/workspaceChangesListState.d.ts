import type { CodeWorkspaceChange, CodeWorkspaceChanges } from "./codeWorkspaceSource.js";
export type ChangesListState = {
    kind: "unsupported";
} | {
    kind: "unavailable";
} | {
    kind: "empty";
    baselineEmpty: boolean;
} | {
    kind: "list";
    changes: readonly CodeWorkspaceChange[];
    baselineEmpty: boolean;
};
export declare function changesListState(changes: CodeWorkspaceChanges): ChangesListState;
