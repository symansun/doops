import { type RendererRegistryEntry, type RendererReplacement } from "./rendererRegistryStore.js";
import { type ContentRendererKey } from "./rendererKeys.js";
export interface ContentRendererRegistration<TInline = unknown, TViewer = unknown, TEditor = unknown> {
    inline?: TInline;
    viewer: TViewer;
    editor?: TEditor;
}
export declare class ContentRendererRegistry<TRenderer = ContentRendererRegistration> {
    private readonly store;
    register(key: ContentRendererKey, renderer: TRenderer, sourceId: string): void;
    has(key: ContentRendererKey): boolean;
    lookup(key: ContentRendererKey): TRenderer | undefined;
    entries(): readonly RendererRegistryEntry<ContentRendererKey, TRenderer>[];
    replace(key: ContentRendererKey, renderer: TRenderer, replacement: RendererReplacement): void;
}
