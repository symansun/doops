import type { ToolRendererKey } from "./registry/rendererKeys.js";
import type { AgentFormSurfaceDocument } from "./agentFormSurface.js";
import type { AgentExecutionReference } from "./agentExecutionContracts.js";
export type AgentMessageContent = {
    kind: "text";
    text: string;
} | AgentJsonContent;
export interface AgentJsonContent {
    kind: "json";
    id: string;
    identity?: ToolRendererKey;
    mediaType: string;
    raw: string;
    value?: unknown;
    valid: boolean;
    form?: AgentFormSurfaceDocument;
}
export interface AgentMessageItem {
    content?: readonly AgentMessageContent[];
    id: string;
    kind: "message";
    role: "user" | "assistant" | "system";
    text: string;
    status: "pending" | "streaming" | "completed" | "failed";
    execution?: AgentExecutionReference;
}
