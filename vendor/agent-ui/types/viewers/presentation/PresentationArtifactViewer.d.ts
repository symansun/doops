import { type PresentationArtifactViewerProps } from "./presentationContracts.js";
export { PRESENTATION_GRANTS, type PresentationArtifactAction, type PresentationArtifactViewerProps, type PresentationGrant, type PresentationSlide, type PresentationVersion, } from "./presentationContracts.js";
export declare function PresentationArtifactViewer({ actionSchemaVersion, artifactId, baseRevision, grants, initialSlideId, onAction, onRequestFullscreen, onSelectVersion, representationId, selectedVersionId, slides, versions, }: PresentationArtifactViewerProps): import("react/jsx-runtime").JSX.Element;
