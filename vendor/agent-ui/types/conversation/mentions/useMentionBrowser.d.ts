import { type Dispatch, type KeyboardEvent, type RefObject, type SetStateAction } from "react";
import type { WorkspaceFileEntry } from "./workspaceFileSource.js";
import { type MentionItem } from "./mentionSerialize.js";
import type { MentionState } from "./mentionToken.js";
export interface MentionBrowserParams {
    mention: MentionState | null;
    setMention: (next: MentionState | null) => void;
    mentionEntries: readonly WorkspaceFileEntry[];
    text: string;
    setText: (next: string) => void;
    textareaRef: RefObject<HTMLTextAreaElement | null>;
    isMobile?: boolean;
}
export interface MentionBrowser {
    mentionIndex: number;
    mentionOpen: boolean;
    mentionedItems: MentionItem[];
    setMentionedItems: Dispatch<SetStateAction<MentionItem[]>>;
    attachMention: (path: string, isDir: boolean) => void;
    openMentionDir: (path: string) => void;
    removeMentionedItem: (index: number) => void;
    handleKeyDown: (e: KeyboardEvent<HTMLTextAreaElement>) => boolean;
    dismiss: () => void;
}
export declare function useMentionBrowser(params: MentionBrowserParams): MentionBrowser;
