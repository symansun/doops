import type { AgentArtifactItem, AgentSessionSnapshot } from "./contracts.js";
import type { AgentWorkspaceLocale } from "./agentWorkspaceText.js";
export interface UserVideoTaskPresentationProps {
    artifacts: readonly AgentArtifactItem[];
    locale?: AgentWorkspaceLocale;
    snapshot: AgentSessionSnapshot;
}
export declare function UserVideoTaskPresentation({ artifacts, locale, snapshot, }: UserVideoTaskPresentationProps): import("react/jsx-runtime").JSX.Element;
