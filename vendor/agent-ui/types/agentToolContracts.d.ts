import type { AgentJsonContent } from "./agentStructuredContent.js";
import type { ToolRendererKey } from "./registry/rendererKeys.js";
import type { AgentResultScope } from "./agentResultScope.js";
import type { AgentExecutionReference } from "./agentExecutionContracts.js";
export type AgentToolStatus = "pending" | "running" | "waiting" | "completed" | "failed" | "cancelled";
export interface AgentToolProgress {
    current?: number;
    fraction?: number;
    message?: string;
    stage?: string;
    total?: number;
    unit?: string;
}
export interface AgentToolFailure {
    code?: string;
    message: string;
    retryable?: boolean;
}
export interface AgentToolTiming {
    durationMillis?: number;
    finishedAt?: string;
    queuedAt?: string;
    startedAt?: string;
}
export interface AgentToolAction {
    actionId: string;
    actionType: string;
    confirmationRequired: boolean;
    inputSchema?: unknown;
    expiresAt?: string;
    label?: string;
    requiredGrantAction?: string;
    schemaVersion: string;
}
export interface AgentToolApprovalReference {
    approvalId?: string;
    permissionRequestId: string;
}
export interface AgentToolActionCommand {
    actionId: string;
    actionType: string;
    commandId: string;
    payload?: unknown;
    schemaVersion: string;
    toolExecutionId: string;
}
export type AgentToolResult = {
    id: string;
    kind: "text";
    text: string;
} | {
    id: string;
    kind: "data";
    content?: AgentJsonContent;
    value: unknown;
} | {
    artifactId: string;
    id: string;
    kind: "artifact";
    mediaType: string | null;
    representationId: string | null;
    revision: bigint;
    role: string;
    schemaVersion: string;
};
export interface AgentToolActivityItem {
    id: string;
    kind: "tool";
    identity: ToolRendererKey;
    title: string;
    detail?: string;
    failure?: AgentToolFailure;
    actions: readonly AgentToolAction[];
    approval?: AgentToolApprovalReference;
    input?: string;
    inputValue?: unknown;
    output?: string;
    progress?: AgentToolProgress;
    results: readonly AgentToolResult[];
    scope?: AgentResultScope;
    status: AgentToolStatus;
    timing?: AgentToolTiming;
    execution?: AgentExecutionReference;
    executionId?: string;
    retry?: {
        attempt: number;
        maximumAttempts?: number;
        retryOfExecutionId?: string;
    };
}
