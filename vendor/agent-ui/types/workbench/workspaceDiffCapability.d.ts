import type { CodeWorkspaceDiff } from "./codeWorkspaceSource.js";
export declare function canTextDiff(diff: CodeWorkspaceDiff): boolean;
export declare function diffFallbackFile(diff: CodeWorkspaceDiff): {
    path: string;
    content: string;
    encoding: "utf-8" | "base64";
    contentType: string;
    bytes: number;
    truncated: boolean;
    binary: boolean;
} | null;
