import { type ReactNode } from "react";
import type { ComposerAttachmentDraft } from "./conversation/useComposerAttachments.js";
export declare function ComposerAttachments({ draft, children, disabled, }: {
    draft: ComposerAttachmentDraft;
    children?: ReactNode;
    disabled: boolean;
}): import("react/jsx-runtime").JSX.Element;
