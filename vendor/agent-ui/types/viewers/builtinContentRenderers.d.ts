import type { AgentContentRendererRegistration, AgentJsonContentRendererComponent } from "../react/contentRendererTypes.js";
import { ContentRendererRegistry } from "../registry/ContentRendererRegistry.js";
export declare function createBuiltinContentRenderers(options?: {
    jsonRenderer?: AgentJsonContentRendererComponent;
}): ContentRendererRegistry<AgentContentRendererRegistration>;
