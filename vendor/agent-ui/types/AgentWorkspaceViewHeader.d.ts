import type { ComponentProps } from "react";
import type { AgentSessionSnapshot } from "./contracts.js";
import { AgentWorkspaceHeaderActions } from "./AgentWorkspaceHeaderActions.js";
import { WorkspaceHeader } from "./WorkspaceHeader.js";
import { WorkspaceViewTabs } from "./WorkspaceViewTabs.js";
export declare function AgentWorkspaceViewHeader({ actions, presentation, snapshot, tabs }: {
    actions: ComponentProps<typeof AgentWorkspaceHeaderActions>;
    presentation: ComponentProps<typeof WorkspaceHeader>["presentation"];
    snapshot: AgentSessionSnapshot;
    tabs: ComponentProps<typeof WorkspaceViewTabs>;
}): import("react/jsx-runtime").JSX.Element;
