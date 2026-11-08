import type { SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentFormState } from "../agentFormResult.js";
export declare function sessionFormState(snapshot: SessionSnapshot | undefined, appSlug: string, formId: string, instanceId: string): AgentFormState | undefined;
