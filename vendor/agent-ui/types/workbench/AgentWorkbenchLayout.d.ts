import { type ReactNode } from "react";
import type { WorkbenchContainerMode } from "../react/useWorkbenchContainerMode.js";
export interface AgentWorkbenchLayoutProps {
    conversation: ReactNode;
    conversationLabel: string;
    forcePane: boolean;
    mode: WorkbenchContainerMode;
    pane: ReactNode | null;
    paneLabel: string;
}
export declare function AgentWorkbenchLayout({ conversation, conversationLabel, forcePane, mode, pane, paneLabel, }: AgentWorkbenchLayoutProps): import("react/jsx-runtime").JSX.Element;
