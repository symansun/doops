import type { SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentConfigurationControl } from "../contracts.js";
export declare function projectConfiguration(snapshot: SessionSnapshot): AgentConfigurationControl[] | undefined;
