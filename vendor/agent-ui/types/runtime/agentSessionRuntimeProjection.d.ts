import type { AgentSessionState } from "./agentSessionState.js";
import type { AgentSessionSnapshot } from "../contracts.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
export interface AgentSessionProjectionContext {
    agentLabel: string;
    interactionMode: "acp" | "pty";
    sessionId: string;
    title: string;
}
export declare function projectConnectedSession(connection: AgentSessionConnection, context: AgentSessionProjectionContext, state?: AgentSessionState | null): AgentSessionSnapshot;
