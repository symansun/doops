import type { EventEnvelope, TimelineItem } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentExecutionReference } from "../agentExecutionContracts.js";
import type { AgentTimelineItem } from "../contracts.js";
export declare function executionReference(envelope: EventEnvelope | undefined, stepId?: string): AgentExecutionReference | undefined;
export declare function decorateTimelineItems(items: AgentTimelineItem[], history: readonly TimelineItem[]): AgentTimelineItem[];
