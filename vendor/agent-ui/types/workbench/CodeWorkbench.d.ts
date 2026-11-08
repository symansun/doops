import type { AgentToolActivityItem } from "../agentToolContracts.js";
import type { AgentConnectionStatus, AgentSessionStatus } from "../contracts.js";
import type { WorkbenchContainerMode } from "../react/useWorkbenchContainerMode.js";
import type { CodeWorkspaceSource, WorkbenchPreferenceScope } from "./codeWorkspaceSource.js";
import type { WorkspaceCodeReference } from "./workspaceCodeReference.js";
export declare function CodeWorkbench({ connection, containerMode, onDownload, preferenceScope, sessionId, source, status, tools, unavailable, visible, onReference, }: {
    connection?: AgentConnectionStatus;
    containerMode: WorkbenchContainerMode;
    onDownload?: (path: string) => void;
    preferenceScope?: WorkbenchPreferenceScope;
    sessionId: string;
    source?: CodeWorkspaceSource;
    status?: AgentSessionStatus;
    tools?: readonly AgentToolActivityItem[];
    unavailable?: boolean;
    visible?: boolean;
    onReference?: (reference: WorkspaceCodeReference) => void;
}): import("react/jsx-runtime").JSX.Element;
