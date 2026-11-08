import { AgentSessionConnection, type AgentSessionConnectionOptions } from "./AgentSessionConnection.js";
import { AgentSessionRuntimeV2 } from "./AgentSessionRuntimeV2.js";
import type { AgentSessionRuntimeV2Options } from "./agentSessionRuntimeOptions.js";
import type { AgentWorkbenchSessionTransport } from "./AgentWorkbenchConnectTransport.js";
export interface AgentWorkbenchRuntimeOptions extends Omit<AgentSessionRuntimeV2Options, "connection" | "loadArtifact"> {
    transport: AgentWorkbenchSessionTransport;
    live?: boolean;
    connectionOptions?: AgentSessionConnectionOptions;
    createArtifactLoader?: (connection: AgentSessionConnection) => AgentSessionRuntimeV2Options["loadArtifact"];
}
export declare function createAgentWorkbenchRuntime({ transport, live, connectionOptions, createArtifactLoader, ...options }: AgentWorkbenchRuntimeOptions): AgentSessionRuntimeV2;
