export interface RendererReplacement {
    expectedSourceId: string;
    sourceId: string;
}
export interface RendererRegistryEntry<TKey, TRenderer> {
    key: TKey;
    renderer: TRenderer;
    sourceId: string;
}
export declare class RendererRegistryStore<TKey, TRenderer> {
    private readonly registryEntries;
    private readonly keyId;
    constructor(keyId: (key: TKey) => string);
    register(key: TKey, renderer: TRenderer, sourceId: string): void;
    has(key: TKey): boolean;
    lookup(key: TKey): TRenderer | undefined;
    entries(): readonly RendererRegistryEntry<TKey, TRenderer>[];
    replace(key: TKey, renderer: TRenderer, replacement: RendererReplacement): void;
}
