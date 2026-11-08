import type { ToolExecution } from "../generated/agent_workbench/v2/tool_pb.js";
import type { AgentTimelineItem } from "../contracts.js";
import type { AgentExecutionReference } from "../agentExecutionContracts.js";
import { type ArtifactCatalog } from "./projectGeneratedSessionSnapshotArtifacts.js";
export declare function projectToolExecution(tool: ToolExecution, itemId: string, catalog: ArtifactCatalog, execution?: AgentExecutionReference): AgentTimelineItem[];
