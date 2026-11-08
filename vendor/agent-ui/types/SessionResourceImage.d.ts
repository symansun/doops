export interface SessionResourceImageProps {
    alt: string;
    className?: string;
    loadBlob: (path: string) => Promise<Blob>;
    path?: string;
}
export declare function SessionResourceImage({ alt, className, loadBlob, path, }: SessionResourceImageProps): import("react/jsx-runtime").JSX.Element;
