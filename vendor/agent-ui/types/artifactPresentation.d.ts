export type ArtifactKind = "audio" | "csv" | "image" | "markdown" | "video" | "pdf" | "presentation" | "html" | "svg" | "mermaid" | "code" | "text" | "file";
export declare function artifactPresentation(mimeType: string | null, filename: string): {
    kind: ArtifactKind;
    label: string;
};
