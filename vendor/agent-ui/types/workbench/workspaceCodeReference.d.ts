export interface WorkspaceCodeReference {
    contentDigest: string;
    endLine: number;
    path: string;
    source: "workspace" | "diff-before" | "diff-after";
    startLine: number;
    text: string;
}
export declare function codeReferencePreamble(references: readonly WorkspaceCodeReference[]): string;
export declare function selectionLineRange(text: string, from: number, to: number): {
    endLine: number;
    startLine: number;
} | null;
