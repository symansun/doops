import type { WorkbenchMode, WorkbenchPreferenceScope } from "./codeWorkspaceSource.js";
export declare function workbenchModeStorageKey(scope: WorkbenchPreferenceScope, sessionId: string, workspaceKey: string): string;
export declare function readWorkbenchMode(storage: Pick<Storage, "getItem">, scope: WorkbenchPreferenceScope, sessionId: string, workspaceKey: string): WorkbenchMode | null;
export declare function writeWorkbenchMode(storage: Pick<Storage, "setItem">, scope: WorkbenchPreferenceScope, sessionId: string, workspaceKey: string, mode: WorkbenchMode): void;
