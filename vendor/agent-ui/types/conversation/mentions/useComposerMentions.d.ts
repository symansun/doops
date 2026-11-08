import { type RefObject } from "react";
import type { WorkspaceFileEntry, WorkspaceFileSource } from "./workspaceFileSource.js";
export declare function useComposerMentions(options: {
    value: string;
    setValue: (next: string) => void;
    textareaRef: RefObject<HTMLTextAreaElement | null>;
    sessionId: string;
    harness: string | null;
    workspaceFiles?: WorkspaceFileSource;
    isMobile?: boolean;
}): {
    enabled: boolean;
    ranked: WorkspaceFileEntry[];
    browser: import("./useMentionBrowser.js").MentionBrowser;
    refreshMention: (text: string, caret: number) => void;
    composeOutboundText: (message: string) => string;
    clearMentions: () => void;
};
