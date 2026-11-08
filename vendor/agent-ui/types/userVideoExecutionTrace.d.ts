import type { AgentArtifactItem, AgentSessionSnapshot } from "./contracts.js";
export type UserVideoExecutionStepId = "request" | "generation" | "preview" | "verification";
export type UserVideoExecutionDetail = "generation_failed" | "generation_ready" | "model_quota_exhausted" | "preview_failed" | "preview_ready" | "preparing_preview" | "provider_auth_failed" | "provider_unavailable" | "published" | "queued" | "rendering" | "request_received" | "verification_failed" | "verification_incomplete" | "verifying" | "waiting";
export interface UserVideoExecutionStep {
    detail: UserVideoExecutionDetail;
    id: UserVideoExecutionStepId;
    progress?: number;
    status: "pending" | "running" | "completed" | "failed";
}
export declare function userVideoExecutionSteps(snapshot: AgentSessionSnapshot, artifacts: readonly AgentArtifactItem[]): UserVideoExecutionStep[];
