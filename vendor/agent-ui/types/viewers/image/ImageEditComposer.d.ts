import type { AgentArtifactActionCommand } from "../../contracts.js";
import type { ImageDimensions } from "./imageGeometry.js";
export interface NormalizedImageRegion {
    height: number;
    width: number;
    x: number;
    y: number;
}
export interface EditImagePayload {
    instruction: string;
    normalizedRegion?: NormalizedImageRegion;
    sourceDimensions: ImageDimensions;
}
export type EditImageAction = AgentArtifactActionCommand<"image.edit", EditImagePayload>;
export interface ImageEditComposerProps {
    actionSchemaVersion: string;
    artifactId: string;
    baseRevision: bigint;
    disabled?: boolean;
    normalizedRegion?: NormalizedImageRegion;
    onSubmit: (action: EditImageAction) => void;
    representationId?: string;
    sourceDimensions: ImageDimensions;
}
export declare function ImageEditComposer({ actionSchemaVersion, artifactId, baseRevision, disabled, normalizedRegion, onSubmit, representationId, sourceDimensions, }: ImageEditComposerProps): import("react/jsx-runtime").JSX.Element;
