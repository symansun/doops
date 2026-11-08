import type { ReactNode } from "react";
import type { AgentSessionSnapshot } from "./contracts.js";
import type { AgentWorkspacePresentation } from "./userWorkspacePresentation.js";
export declare function WorkspaceHeader({ actions, identity, presentation, snapshot, tabs, }: {
    actions?: ReactNode;
    identity?: ReactNode;
    presentation: AgentWorkspacePresentation;
    snapshot: AgentSessionSnapshot;
    tabs?: ReactNode;
}): import("react/jsx-runtime").JSX.Element | null;
