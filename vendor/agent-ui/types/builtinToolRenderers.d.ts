import type { AgentToolRendererRegistration } from "./react/rendererTypes.js";
import { ToolRendererRegistry } from "./registry/ToolRendererRegistry.js";
export declare function createBuiltinToolRenderers(namespacesOverride?: readonly string[]): ToolRendererRegistry<AgentToolRendererRegistration>;
