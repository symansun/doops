import { type SessionDeltaBatch, type SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import { type AgentSessionState } from "./agentSessionState.js";
export { AgentSessionReductionError, type AgentSessionResyncReason, type AgentSessionState, } from "./agentSessionState.js";
export declare function applySessionSnapshot(snapshot: SessionSnapshot): AgentSessionState;
export declare function applyDeltaBatch(state: AgentSessionState, batch: SessionDeltaBatch): AgentSessionState;
