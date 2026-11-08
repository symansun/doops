import type { AgentExecutionReference } from "./agentExecutionContracts.js";
export type AgentExecutionStatus = "queued" | "running" | "waiting" | "completed" | "failed" | "cancelled";
export interface AgentExecutionProgress {
    current?: number;
    fraction?: number;
    message?: string;
    stage?: string;
    total?: number;
    unit?: string;
}
export interface AgentExecutionFailure {
    code?: string;
    message: string;
    retryable?: boolean;
}
export interface AgentExecutionTiming {
    durationMillis?: number;
    finishedAt?: string;
    queuedAt?: string;
    startedAt?: string;
}
export interface AgentExecutionStep {
    id: string;
    title: string;
    kind: "message" | "reasoning" | "tool" | "artifact" | "activity";
    status: AgentExecutionStatus;
    detail?: string;
    parentId?: string;
    executionId?: string;
    failure?: AgentExecutionFailure;
    progress?: AgentExecutionProgress;
    retry?: {
        attempt: number;
        maximumAttempts?: number;
        retryOfExecutionId?: string;
    };
    timing?: AgentExecutionTiming;
    execution?: AgentExecutionReference;
}
export interface AgentExecutionRun {
    id: string;
    title: string;
    status: AgentExecutionStatus;
    turnId?: string;
    steps: readonly AgentExecutionStep[];
    childRunIds: readonly string[];
}
export interface AgentExecutionView {
    runs: readonly AgentExecutionRun[];
    activeRunId?: string;
    runningCount: number;
    failedCount: number;
}
