import type { AgentExecutionReference } from "./agentExecutionContracts.js";
export type AgentInteractionKind = "artifact_action" | "extension" | "permission" | "tool_action";
export type AgentInteractionStatus = "pending" | "running" | "succeeded" | "failed" | "rejected" | "cancelled";
export interface AgentInteractionEvent {
    commandId: string;
    error?: string;
    execution?: AgentExecutionReference;
    id: string;
    kind: AgentInteractionKind;
    result?: unknown;
    sourceId?: string;
    status: AgentInteractionStatus;
    updatedAt: string;
}
export interface AgentExtensionCapability {
    actions: readonly string[];
    namespace: string;
    schemaVersion: string;
    semanticType: string;
}
