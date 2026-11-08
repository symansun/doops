import type { AgentSessionRuntime } from "../contracts.js";
import type { ContentRendererRegistry } from "../registry/ContentRendererRegistry.js";
import type { AgentContentRendererRegistration } from "./contentRendererTypes.js";
import type { WorkbenchResult } from "./workbenchResults.js";
export interface WorkbenchResultsPaneProps {
    compact?: boolean;
    contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>;
    onSelect: (id: string) => void;
    presentation: "developer" | "user";
    results: readonly WorkbenchResult[];
    runtime: AgentSessionRuntime;
    selected: WorkbenchResult | undefined;
    sessionId: string;
}
export declare function WorkbenchResultsPane({ compact, contentRenderers, onSelect, presentation, results, runtime, selected, sessionId, }: WorkbenchResultsPaneProps): import("react/jsx-runtime").JSX.Element;
