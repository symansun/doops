export interface ToolRendererKey {
    namespace: string;
    semanticKey: string;
    schemaVersion: string;
}
export interface ContentRendererKey {
    namespace?: string;
    semanticKey?: string;
    blockKind: string;
    manifestType?: string;
    mediaType?: string;
    role?: string;
    schemaVersion: string;
}
export declare function toolRendererKeyId(key: ToolRendererKey): string;
export declare function contentRendererKeyId(key: ContentRendererKey): string;
