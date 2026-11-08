import type { AgentJsonContent } from "./agentStructuredContent.js";
import { type AgentFormWorkflow, type FormGateRecord, type AgentDatePolicy } from "./agentFormWorkflow.js";
export interface AgentFormAction {
    id: string;
    label: string;
    behavior: "validate" | "send";
    confirmation?: {
        title: string;
        description: string;
        confirmLabel: string;
    };
}
export interface AgentExtensionCommand {
    namespace: string;
    semanticType: string;
    schemaVersion: string;
    payload: unknown;
}
export interface AgentFormFieldIssue {
    field: string;
    description: string;
}
export interface AgentFormSurfaceDocument {
    workflow?: AgentFormWorkflow;
    gates?: Record<string, FormGateRecord>;
    datePolicy?: AgentDatePolicy;
    kind: "form";
    appSlug: string;
    formId: string;
    instanceId: string;
    dataRevision: number;
    data: Record<string, unknown>;
    schema: Record<string, unknown>;
    uiSchema?: Record<string, unknown>;
    readOnly: boolean;
    actions: AgentFormAction[];
    spec?: Record<string, unknown>;
    errors?: {
        dataRevision: number;
        fields: AgentFormFieldIssue[];
    };
}
export declare function parseAgentFormSurface(content: AgentJsonContent): AgentFormSurfaceDocument | undefined;
