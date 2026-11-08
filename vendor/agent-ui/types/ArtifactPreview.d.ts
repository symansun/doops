import type { ArtifactKind } from "./artifactPresentation.js";
import type { ArtifactBlobState } from "./useArtifactBlobUrl.js";
import "./viewers/artifactPreviewStrategies.js";
interface ArtifactPreviewProps {
    filename: string;
    fill?: boolean;
    kind: ArtifactKind;
    state: ArtifactBlobState;
    onLoad: () => void;
    onRetry: () => void;
}
export declare function ArtifactPreview({ filename, fill, kind, state, onLoad, onRetry, }: ArtifactPreviewProps): string | number | bigint | boolean | Iterable<import("react").ReactNode> | Promise<string | number | bigint | boolean | import("react").ReactPortal | import("react").ReactElement<unknown, string | import("react").JSXElementConstructor<any>> | Iterable<import("react").ReactNode> | null | undefined> | import("react/jsx-runtime").JSX.Element | null;
export {};
