export type WorkspaceCodeLanguage = "javascript" | "jsx" | "typescript" | "tsx" | "json" | "html" | "css" | "markdown" | "python" | "go" | "yaml" | "shell" | "plaintext";
export declare const maxHighlightBytes: number;
export declare function languageForPath(path: string, mime?: string): WorkspaceCodeLanguage;
export declare function shouldHighlightContent(content: string, encoding: string): boolean;
