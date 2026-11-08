import type { AgentWorkspaceLocale } from "./agentWorkspaceText.js";
import type { AgentInteractionKind, AgentInteractionStatus } from "./agentInteractionContracts.js";
export interface ExecutionTraceText {
    cancelled: string;
    completed: string;
    failed: string;
    queued: string;
    running: string;
    title: string;
    unscoped: string;
    waiting: string;
    turn: (id: string) => string;
    interactionKind: (kind: AgentInteractionKind) => string;
    interactionStatus: (status: AgentInteractionStatus) => string;
}
export declare const executionTraceText: Record<AgentWorkspaceLocale, ExecutionTraceText>;
