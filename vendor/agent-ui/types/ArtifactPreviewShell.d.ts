import type { ReactNode } from "react";
import type { ArtifactBlobState } from "./useArtifactBlobUrl.js";
interface ArtifactPreviewShellProps {
    filename: string;
    fill: boolean;
    state: Exclude<ArtifactBlobState, {
        status: "ready";
    }>;
    onLoad: () => void;
    onRetry: () => void;
}
export declare function ArtifactPreviewShell({ filename, fill, state, onLoad, onRetry, }: ArtifactPreviewShellProps): ReactNode;
export {};
