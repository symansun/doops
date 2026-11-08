import { type MentionItem } from "./mentionSerialize.js";
export declare function MentionChips({ items, disabled, onRemove, }: {
    items: readonly MentionItem[];
    disabled?: boolean;
    onRemove: (index: number) => void;
}): import("react/jsx-runtime").JSX.Element | null;
