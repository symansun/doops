import { type ClipboardEvent, type DragEvent } from "react";
import type { AgentAttachmentReference, AgentSessionRuntime } from "../contracts.js";
export type ComposerAttachmentDraft = ReturnType<typeof useComposerAttachments>;
export declare function useComposerAttachments({ runtime, sessionId, disabled, }: {
    runtime: AgentSessionRuntime;
    sessionId: string;
    disabled: boolean;
}): {
    attachments: AgentAttachmentReference[];
    uploadError: string | null;
    uploading: boolean;
    onPaste: (event: ClipboardEvent<HTMLTextAreaElement>) => void;
    onDragOver: (event: DragEvent<HTMLElement>) => void;
    onDrop: (event: DragEvent<HTMLElement>) => void;
    uploadFiles: (files: readonly File[]) => Promise<void>;
    previewUrl: (id: string) => string;
    supported: boolean;
    isUploading: () => boolean;
    clear: () => void;
    remove: (id: string) => void;
};
