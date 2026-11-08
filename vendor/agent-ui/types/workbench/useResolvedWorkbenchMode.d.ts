import type { CodeWorkspaceSource, WorkbenchMode, WorkbenchPreferenceScope } from "./codeWorkspaceSource.js";
export declare function useResolvedWorkbenchMode(input: {
    codeWorkspace?: CodeWorkspaceSource;
    controlled?: WorkbenchMode;
    hostDefault?: WorkbenchMode;
    onChange?: (mode: WorkbenchMode) => void;
    preferenceScope?: WorkbenchPreferenceScope;
    sessionId: string;
    goalAvailable?: boolean;
}): {
    setMode: (next: WorkbenchMode) => void;
    mode: WorkbenchMode;
    showToggle: boolean;
    unavailable: boolean;
};
