import { type ReactNode } from "react";
import type { AgentArtifactItem, AgentSessionRuntime } from "./contracts.js";
export declare function MarkdownArtifactReference({ artifact, image, label, runtime, sessionId, }: {
    artifact: AgentArtifactItem;
    image?: boolean;
    label?: ReactNode;
    runtime: AgentSessionRuntime;
    sessionId: string;
}): import("react/jsx-runtime").JSX.Element;
export declare function isSafeBlobPreview(mimeType: string | null, filename: string): boolean;
