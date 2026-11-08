import type { AgentArtifactItem, AgentSessionSnapshot, AgentTimelineItem } from "./contracts.js";
export type AgentWorkspacePresentation = "developer" | "user";
export declare function userConversationItems(items: readonly AgentTimelineItem[]): AgentTimelineItem[];
export declare function userVisibleArtifacts(items: readonly AgentArtifactItem[]): AgentArtifactItem[];
export type UserVideoTaskState = "failed" | "model_quota_exhausted" | "partial" | "processing" | "provider_auth_failed" | "provider_unavailable" | "task_failed" | "verified" | "verification_failed";
export declare function userVideoTaskArtifacts(snapshot: AgentSessionSnapshot, artifacts: readonly AgentArtifactItem[]): AgentArtifactItem[];
export declare function userVideoTaskState(snapshot: AgentSessionSnapshot, artifacts: readonly AgentArtifactItem[]): UserVideoTaskState | null;
export declare function userVideoFailureState(error: string | null): Extract<UserVideoTaskState, "model_quota_exhausted" | "provider_auth_failed" | "provider_unavailable"> | null;
