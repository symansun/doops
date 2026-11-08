import type { AgentEvent, SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
export declare function invalidatesHistoryVisibility(snapshot: SessionSnapshot, events: readonly AgentEvent[]): boolean;
