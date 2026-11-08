import { type Ref } from "react";
import type { CodeWorkspaceFile } from "./codeWorkspaceSource.js";
import { type WorkspaceCodeReference } from "./workspaceCodeReference.js";
export interface WorkspaceCodePreviewHandle {
    openFind(): void;
    revealLine(line: number): void;
    reference(): void;
}
export declare function WorkspaceCodePreview({ file, onDownload, path, previewRef, revealLine, showToolbar, onReference, }: {
    file: CodeWorkspaceFile | null;
    onDownload?: (path: string) => void;
    path: string;
    previewRef?: Ref<WorkspaceCodePreviewHandle | null>;
    revealLine?: number;
    showToolbar?: boolean;
    onReference?: (reference: WorkspaceCodeReference) => void;
}): import("react/jsx-runtime").JSX.Element | null;
