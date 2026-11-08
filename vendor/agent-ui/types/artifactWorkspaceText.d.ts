export interface ArtifactWorkspaceText {
    generatedArtifact: string;
    generationFailed: string;
    loadingUnavailable: string;
    downloadNotAuthorized: string;
    loadFailed: string;
    loading(filename: string): string;
    load(filename: string): string;
    retry(filename: string): string;
    preview(filename: string): string;
    audioPreview(filename: string): string;
    csvPreview(filename: string): string;
    csvPreviewFailed(filename: string): string;
    diagramRenderFailed: string;
    previewLimited(bytes: number): string;
    pdfPreview(filename: string): string;
    videoPreview(filename: string): string;
    videoPlaybackFailed(filename: string): string;
    videoUnsupported: string;
    fullscreenVideo: string;
    exitFullscreen: string;
    fullscreenUnavailable: string;
    open(filename: string): string;
    download(filename: string): string;
}
export declare function artifactWorkspaceText(locale: "en-US" | "zh-CN"): ArtifactWorkspaceText;
