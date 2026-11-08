import type { AgentPermissionRequest, AgentSessionRuntime } from "./contracts.js";
export declare function ApprovalDock({ disabled, onError, permissions, runtime, sessionId, }: {
    disabled?: boolean;
    onError: (error: unknown) => void;
    permissions: AgentPermissionRequest[];
    runtime: AgentSessionRuntime;
    sessionId: string;
}): import("react/jsx-runtime").JSX.Element | null;
