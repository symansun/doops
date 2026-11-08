import type { SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentSessionTransport } from "./AgentSessionStore.js";
export declare class AgentSessionHistoryLoader {
    private flight?;
    load(signal: AbortSignal, transport: AgentSessionTransport, current: () => SessionSnapshot, apply: (snapshot: SessionSnapshot) => void): Promise<void>;
    private expand;
}
