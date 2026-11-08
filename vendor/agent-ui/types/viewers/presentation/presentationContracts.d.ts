import type { AgentArtifactActionCommand } from "../../contracts.js";
export declare const PRESENTATION_GRANTS: {
    readonly exportPresentation: "presentation.export";
    readonly regenerateSlide: "presentation.regenerate_slide";
    readonly reorderSlide: "presentation.reorder_slide";
    readonly replaceSlide: "presentation.replace_slide";
    readonly selectVersion: "presentation.select_version";
};
export type PresentationGrant = (typeof PRESENTATION_GRANTS)[keyof typeof PRESENTATION_GRANTS];
export interface PresentationSlide {
    readonly imageSrc: string;
    readonly notes?: string;
    readonly position: number;
    readonly representationId?: string;
    readonly slideId: string;
    readonly thumbnailSrc?: string;
    readonly title?: string;
}
export interface PresentationVersion {
    readonly id: string;
    readonly label: string;
    readonly revision: bigint;
}
export type PresentationActionPayload = {
    readonly slideId: string;
} | {
    readonly slideId: string;
    readonly targetIndex: number;
} | {
    readonly format: "pptx";
    readonly slideId: string;
};
export interface PresentationArtifactAction extends AgentArtifactActionCommand<PresentationGrant, PresentationActionPayload> {
}
export interface PresentationArtifactViewerProps {
    readonly actionSchemaVersion: string;
    readonly artifactId: string;
    readonly baseRevision: bigint;
    readonly grants: readonly PresentationGrant[];
    readonly initialSlideId?: string;
    readonly onAction: (action: PresentationArtifactAction) => void;
    readonly onRequestFullscreen?: () => void | Promise<void>;
    readonly onSelectVersion?: (versionId: string) => void;
    readonly representationId?: string;
    readonly selectedVersionId: string;
    readonly slides: readonly PresentationSlide[];
    readonly versions: readonly PresentationVersion[];
}
