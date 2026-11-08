export interface AgentExecutionReference {
    attempt?: number;
    causationCommandId?: string;
    parentId?: string;
    sequence?: bigint;
    sourceEventId?: string;
    stepId?: string;
    turnId?: string;
}
