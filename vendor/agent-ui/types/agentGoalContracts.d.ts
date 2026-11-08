export type AgentGoalStatus = "draft" | "running" | "blocked" | "completed" | "failed";
export type AgentGoalCriterionStatus = "pending" | "running" | "passed" | "failed";
export interface AgentGoalCriterion {
    id: string;
    title: string;
    status: AgentGoalCriterionStatus;
    evidenceIds: readonly string[];
}
export interface AgentGoalEvidence {
    id: string;
    label: string;
    kind: "artifact" | "change" | "test" | "message";
    href?: string;
}
export interface AgentGoal {
    goalId: string;
    title: string;
    description?: string;
    status: AgentGoalStatus;
    progress: number;
    activeSessionIds: readonly string[];
    criteria: readonly AgentGoalCriterion[];
    evidence: readonly AgentGoalEvidence[];
    actions?: AgentGoalActions;
}
export interface AgentGoalActions {
    cancel?: () => Promise<void>;
    start?: () => Promise<void>;
    verify?: () => Promise<void>;
}
