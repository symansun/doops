import { type ReactNode } from "react";
import type { CodeWorkspaceSource } from "./codeWorkspaceSource.js";
export declare function WorkspaceFileSearch({ children, onOpenFile, sessionId, source, }: {
    children: ReactNode;
    onOpenFile: (path: string) => void;
    sessionId: string;
    source: CodeWorkspaceSource;
}): import("react/jsx-runtime").JSX.Element;
