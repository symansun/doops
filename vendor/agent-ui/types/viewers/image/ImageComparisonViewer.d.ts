export type ImageComparisonMode = "source" | "result" | "side-by-side" | "slider";
export interface ComparisonImage {
    alt: string;
    src: string;
}
export interface ImageComparisonViewerProps {
    defaultMode?: ImageComparisonMode;
    fill?: boolean;
    result: ComparisonImage;
    source: ComparisonImage;
}
export declare function ImageComparisonViewer({ defaultMode, fill, result, source, }: ImageComparisonViewerProps): import("react/jsx-runtime").JSX.Element;
