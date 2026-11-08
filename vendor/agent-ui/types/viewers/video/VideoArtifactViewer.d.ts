export type VideoArtifactStatus = "queued" | "rendering" | "transcoding" | "ready" | "failed";
export interface VideoArtifactVersion {
    id: string;
    label?: string;
    src?: string;
    filename?: string;
    mimeType?: string;
    posterSrc?: string;
    durationSeconds?: number;
}
export interface VideoArtifactViewerProps {
    src: string;
    filename: string;
    mimeType: string;
    posterSrc?: string;
    durationSeconds?: number;
    status: VideoArtifactStatus;
    technicalMetadata?: boolean;
    progress?: number;
    versions?: readonly VideoArtifactVersion[];
    selectedVersionId?: string;
    onSelectVersion?: (versionId: string) => void;
    onDownload?: () => void;
}
export declare function VideoArtifactViewer({ src, filename, mimeType, posterSrc, durationSeconds, status, technicalMetadata, progress, versions, selectedVersionId, onSelectVersion, onDownload, }: VideoArtifactViewerProps): import("react/jsx-runtime").JSX.Element;
