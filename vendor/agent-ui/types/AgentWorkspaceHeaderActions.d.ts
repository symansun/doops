import type { ReactNode } from "react";
import type { WorkbenchMode } from "./workbench/codeWorkspaceSource.js";
export declare function AgentWorkspaceHeaderActions({ fullscreen, goalAvailable, headerActions, onWorkbenchModeChange, showFullscreen, showToggle, workbenchMode, }: {
    fullscreen: {
        active: boolean;
        supported: boolean;
        toggle: () => void;
    };
    goalAvailable?: boolean;
    headerActions?: ReactNode;
    onWorkbenchModeChange: (mode: WorkbenchMode) => void;
    showFullscreen: boolean;
    showToggle: boolean;
    workbenchMode: WorkbenchMode;
}): import("react/jsx-runtime").JSX.Element;
