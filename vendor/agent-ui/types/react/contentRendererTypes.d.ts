import type { ComponentType } from "react";
import type { AgentArtifactItem } from "../agentArtifactContracts.js";
import type { AgentSessionRuntime } from "../contracts.js";
import type { ContentRendererRegistration } from "../registry/ContentRendererRegistry.js";
import type { AgentJsonContent } from "../agentStructuredContent.js";
export interface AgentContentRendererProps {
    filename: string;
    fill?: boolean;
    item: AgentArtifactItem;
    presentation?: "developer" | "user";
    runtime: AgentSessionRuntime;
    sessionId: string;
}
export type AgentContentRendererComponent = ComponentType<AgentContentRendererProps>;
export interface AgentJsonContentRendererProps {
    readOnly?: boolean;
    content: AgentJsonContent;
    presentation?: "developer" | "user";
    runtime: AgentSessionRuntime;
    sessionId: string;
}
export type AgentJsonContentRendererComponent = ComponentType<AgentJsonContentRendererProps>;
export type AgentContentRendererRegistration = ContentRendererRegistration<AgentContentRendererComponent, AgentContentRendererComponent, AgentContentRendererComponent> & {
    json?: AgentJsonContentRendererComponent;
};
