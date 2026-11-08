import { type RendererRegistryEntry, type RendererReplacement } from "./rendererRegistryStore.js";
import { type ToolRendererKey } from "./rendererKeys.js";
export interface ToolRendererRegistration<TSummary = unknown, TDetail = unknown, TWorkbench = unknown> {
    summary?: TSummary;
    detail?: TDetail;
    workbench?: TWorkbench;
}
export declare class ToolRendererRegistry<TRenderer = ToolRendererRegistration> {
    private readonly store;
    register(key: ToolRendererKey, renderer: TRenderer, sourceId: string): void;
    has(key: ToolRendererKey): boolean;
    lookup(key: ToolRendererKey): TRenderer | undefined;
    entries(): readonly RendererRegistryEntry<ToolRendererKey, TRenderer>[];
    replace(key: ToolRendererKey, renderer: TRenderer, replacement: RendererReplacement): void;
}
