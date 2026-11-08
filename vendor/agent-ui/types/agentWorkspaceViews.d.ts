import type { AgentArtifactItem, AgentSessionSnapshot, AgentTimelineItem } from "./contracts.js";
import { type UserVideoExecutionStep } from "./userVideoExecutionTrace.js";
import { type AgentWorkspacePresentation } from "./userWorkspacePresentation.js";
export interface AgentWorkspaceViews {
    allArtifacts: AgentArtifactItem[];
    artifacts: AgentArtifactItem[];
    conversationItems: AgentTimelineItem[];
    videoExecutionSteps: UserVideoExecutionStep[];
}
export declare function agentWorkspaceViews(snapshot: AgentSessionSnapshot, presentation: AgentWorkspacePresentation): AgentWorkspaceViews;
