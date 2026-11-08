export type CodeWorkspaceFailure = "access" | "not_found" | "refresh";
export declare function codeWorkspaceFailure(error: unknown): CodeWorkspaceFailure;
