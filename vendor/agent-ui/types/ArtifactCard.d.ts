import type { AgentArtifactItem, AgentSessionRuntime } from "./contracts.js";
import type { AgentContentRendererRegistration } from "./react/contentRendererTypes.js";
import type { ContentRendererRegistry } from "./registry/ContentRendererRegistry.js";
export interface ArtifactCardProps {
    contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>;
    fill?: boolean;
    item: AgentArtifactItem;
    presentation?: "developer" | "user";
    runtime: AgentSessionRuntime;
    sessionId: string;
}
export declare function ArtifactCard({ contentRenderers, fill, item, presentation, runtime, sessionId, }: ArtifactCardProps): import("react/jsx-runtime").JSX.Element;
