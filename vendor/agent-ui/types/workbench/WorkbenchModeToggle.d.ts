import type { WorkbenchMode } from "./codeWorkspaceSource.js";
export declare function WorkbenchModeToggle({ goalAvailable, mode, onChange, }: {
    goalAvailable?: boolean;
    mode: WorkbenchMode;
    onChange: (mode: WorkbenchMode) => void;
}): import("react/jsx-runtime").JSX.Element;
