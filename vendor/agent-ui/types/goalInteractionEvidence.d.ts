import type { AgentGoal } from "./agentGoalContracts.js";
import type { AgentInteractionEvent } from "./agentInteractionContracts.js";
export declare function goalWithInteractionEvidence(goal: AgentGoal | undefined, sessionId: string, interactions: readonly AgentInteractionEvent[]): AgentGoal | undefined;
