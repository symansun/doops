import type { AgentCommand, AgentSessionRuntime, AgentSessionSnapshot } from "../contracts.js";
import type { AgentContentRendererRegistration } from "../react/contentRendererTypes.js";
import type { AgentToolRendererRegistration } from "../react/rendererTypes.js";
import type { ContentRendererKey, ToolRendererKey } from "../registry/rendererKeys.js";
import type { AgentHarnessDescriptor } from "./agentHarnessDescriptor.js";
import type { CodeWorkspaceSource } from "../workbench/codeWorkspaceSource.js";
import type { WorkspaceFileSource } from "../conversation/mentions/workspaceFileSource.js";
export interface AgentAdapterSessionContext {
    runtime: AgentSessionRuntime;
    sessionId: string;
    snapshot: AgentSessionSnapshot;
}
export interface AgentCommandProjectionContext extends AgentAdapterSessionContext {
}
export interface AgentEventProjectionContext extends AgentAdapterSessionContext {
}
export interface AgentCommandProjection<TCommand = unknown> {
    readonly commands: readonly AgentCommand[];
    project(command: AgentCommand, context: AgentCommandProjectionContext): TCommand;
}
export interface AgentEventProjection<TEvent = unknown, TProjection = unknown> {
    readonly eventVersion: string;
    decode?(event: unknown): TEvent;
    project(event: TEvent, context: AgentEventProjectionContext): TProjection | readonly TProjection[] | null;
}
export interface AgentWorkspaceCapability {
    files?: WorkspaceFileSource;
    code?: CodeWorkspaceSource;
    terminal: boolean;
}
export interface AgentPermissionCapability {
    approval: boolean;
    questions: boolean;
    customAnswers: boolean;
}
export interface AgentToolRendererEntry {
    key: ToolRendererKey;
    renderer: AgentToolRendererRegistration;
    sourceId?: string;
}
export interface AgentContentRendererEntry {
    key: ContentRendererKey;
    renderer: AgentContentRendererRegistration;
    sourceId?: string;
}
export interface AgentAdapterRendererRegistrations {
    tools?: readonly AgentToolRendererEntry[];
    content?: readonly AgentContentRendererEntry[];
}
export interface AgentAdapter<TEvent = unknown, TCommand = unknown, TProjection = unknown> {
    descriptor: AgentHarnessDescriptor;
    commandProjection?: AgentCommandProjection<TCommand>;
    eventProjection?: AgentEventProjection<TEvent, TProjection>;
    renderers?: AgentAdapterRendererRegistrations;
    workspace?: AgentWorkspaceCapability;
    permissions?: AgentPermissionCapability;
}
export type AnyAgentAdapter = AgentAdapter<any, any, any>;
