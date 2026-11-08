import type { ComposerModelControlRenderer } from "./composerModelControl.js";
import type { AgentSessionRuntime, AgentSessionSnapshot } from "./contracts.js";
import type { AgentWorkspacePresentation } from "./userWorkspacePresentation.js";
export declare function ComposerReadOnlyBar({ onError, renderModelControl, presentation, runtime, snapshot, }: {
    onError: (error: unknown) => void;
    renderModelControl?: ComposerModelControlRenderer;
    presentation: AgentWorkspacePresentation;
    runtime: AgentSessionRuntime;
    snapshot: AgentSessionSnapshot;
}): import("react/jsx-runtime").JSX.Element;
