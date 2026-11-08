import type { AgentMessageItem } from "./agentStructuredContent.js";
export type { AgentJsonContent, AgentMessageContent, AgentMessageItem } from "./agentStructuredContent.js";
import type { AgentArtifactItem } from "./agentArtifactContracts.js";
import type { AgentToolActivityItem } from "./agentToolContracts.js";
import type { AgentPermissionRequest } from "./agentPermissionContracts.js";
import type { WorkerBrandingSpec } from "./resolveWorkerBranding.js";
import type { AgentExecutionReference } from "./agentExecutionContracts.js";
import type { AgentExtensionCapability, AgentInteractionEvent } from "./agentInteractionContracts.js";
import type { AgentExecutionView } from "./executionViewContracts.js";
import type { AgentTaskQueueView } from "./agentTaskQueue.js";
export type { AgentExecutionReference } from "./agentExecutionContracts.js";
export type { AgentExtensionCapability, AgentInteractionEvent } from "./agentInteractionContracts.js";
export type { AgentArtifactActionCommand, AgentArtifactDimensions, AgentArtifactGrant, AgentArtifactItem, AgentArtifactManifest, AgentArtifactProvenance, AgentArtifactRepresentation, AgentImageEditManifest, AgentNormalizedRegion, AgentPresentationManifest, AgentPresentationSlide, AgentPresentationVersion, AgentVideoManifest, } from "./agentArtifactContracts.js";
export type { AgentToolAction, AgentToolActionCommand, AgentToolApprovalReference, AgentToolActivityItem, AgentToolFailure, AgentToolProgress, AgentToolResult, AgentToolStatus, AgentToolTiming, } from "./agentToolContracts.js";
export type { AgentApprovalPermissionRequest, AgentPermissionAnswerContent, AgentPermissionQuestion, AgentPermissionQuestionOption, AgentPermissionRequest, AgentPermissionResolution, AgentQuestionPermissionRequest, } from "./agentPermissionContracts.js";
export type { AgentAttachmentReference, AgentSessionReference, AgentSessionRuntime, CreateAgentSessionInput, } from "./agentSessionRuntime.js";
export type { TerminalControlLease, TerminalRuntime } from "./terminalRuntimeContracts.js";
export type AgentSessionStatus = "idle" | "launching" | "running" | "waiting" | "failed" | "completed";
export type AgentConnectionStatus = "connecting" | "connected" | "reconnecting" | "disconnected";
export interface AgentWorkspaceCapabilities {
    sendMessage: boolean;
    interrupt: boolean;
    resolvePermission: boolean;
    updateConfiguration: boolean;
    terminal: boolean;
    executeToolAction: boolean;
    extensions: readonly AgentExtensionCapability[];
}
export interface AgentAttachmentItem {
    attachmentId: string;
    filename: string;
    id: string;
    kind: "attachment";
    mimeType: string | null;
    representationId?: string;
    execution?: AgentExecutionReference;
}
export interface AgentActivityItem {
    id: string;
    kind: "reasoning" | "error" | "system";
    title: string;
    detail?: string;
    status: "pending" | "running" | "completed" | "failed";
    execution?: AgentExecutionReference;
}
export type AgentTimelineItem = AgentMessageItem | AgentAttachmentItem | AgentActivityItem | AgentToolActivityItem | AgentArtifactItem;
export interface AgentPlanStep {
    execution?: AgentExecutionReference;
    id: string;
    title: string;
    status: "pending" | "running" | "completed" | "failed";
}
export interface TerminalResource {
    id: string;
    label: string;
    status: AgentConnectionStatus;
    writable: boolean;
    controlMode?: "surface" | "host";
}
export interface AgentCommand {
    name: string;
    label: string;
    description?: string;
    requiresArgument?: boolean;
}
export interface AgentConfigurationOption {
    value: string;
    label: string;
    description?: string;
}
export interface AgentConfigurationControl {
    id: string;
    label: string;
    value: string;
    options: AgentConfigurationOption[];
}
export interface AgentSessionMetadata {
    id: string;
    label: string;
    value: string;
}
export interface AgentPartnerSkill {
    slug: string;
    displayName: string;
}
export interface AgentPartnerIdentity {
    name: string;
    summary?: string;
    skills: AgentPartnerSkill[];
    branding?: WorkerBrandingSpec;
}
export interface AgentSessionSnapshot {
    sessionId: string;
    title: string;
    agentLabel: string;
    status: AgentSessionStatus;
    connection: AgentConnectionStatus;
    interactionMode: "acp" | "pty";
    latestUserCommandId?: string;
    capabilities: AgentWorkspaceCapabilities;
    interactions: readonly AgentInteractionEvent[];
    execution?: AgentExecutionView;
    queue?: AgentTaskQueueView;
    commands?: AgentCommand[];
    configuration?: AgentConfigurationControl[];
    metadata?: AgentSessionMetadata[];
    partner?: AgentPartnerIdentity;
    items: AgentTimelineItem[];
    plan: AgentPlanStep[];
    permissions: AgentPermissionRequest[];
    terminals: TerminalResource[];
    hasOlderItems: boolean;
    historyWindowSize?: number;
    error: string | null;
}
