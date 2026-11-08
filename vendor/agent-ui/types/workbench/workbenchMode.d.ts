import type { WorkbenchMode } from "./codeWorkspaceSource.js";
export declare function resolveWorkbenchMode(input: {
    controlled?: WorkbenchMode;
    stored?: WorkbenchMode | null;
    hostDefault?: WorkbenchMode;
    hasCodeSource: boolean;
    goalAvailable?: boolean;
}): {
    mode: WorkbenchMode;
    showToggle: boolean;
    unavailable: boolean;
};
