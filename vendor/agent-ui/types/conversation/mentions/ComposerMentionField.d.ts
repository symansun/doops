import type { ClipboardEventHandler, KeyboardEventHandler, RefObject } from "react";
import type { useComposerMentions } from "./useComposerMentions.js";
type Mentions = ReturnType<typeof useComposerMentions>;
export declare function ComposerMentionField({ mentions, value, onChange, onKeyDown, onPaste, disabled, ariaLabel, placeholder, textareaRef, mobile, }: {
    mentions: Mentions;
    value: string;
    onChange: (value: string, caret: number) => void;
    onKeyDown: KeyboardEventHandler<HTMLTextAreaElement>;
    onPaste: ClipboardEventHandler<HTMLTextAreaElement>;
    disabled: boolean;
    ariaLabel: string;
    placeholder: string;
    textareaRef: RefObject<HTMLTextAreaElement | null>;
    mobile?: boolean;
}): import("react/jsx-runtime").JSX.Element;
export {};
