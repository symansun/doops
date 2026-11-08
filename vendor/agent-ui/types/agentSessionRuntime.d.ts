import type { AgentArtifactActionCommand, AgentArtifactItem } from "./agentArtifactContracts.js";
import type { AgentPermissionResolution } from "./agentPermissionContracts.js";
import type { AgentExtensionCommand } from "./agentFormSurface.js";
import type { AgentToolActionCommand } from "./agentToolContracts.js";
import type { AgentExtensionResult, AgentFormState } from "./agentFormResult.js";
import type { AgentSessionSnapshot } from "./contracts.js";
import type { AgentQueuePriority } from "./agentTaskQueue.js";
export interface CreateAgentSessionInput {
    agentId: string;
    title?: string;
    initialMessage?: string;
}
export interface AgentSessionReference {
    sessionId: string;
}
export interface AgentAttachmentReference {
    id: string;
    name: string;
    mediaType: string;
    bytes: number;
}
export interface AgentSessionRuntime {
    readonly formDraftScope?: object;
    create?(input: CreateAgentSessionInput): Promise<AgentSessionReference>;
    open(sessionId: string): Promise<void>;
    close(sessionId: string): void;
    getSnapshot(sessionId: string): AgentSessionSnapshot;
    subscribe(sessionId: string, listener: () => void): () => void;
    sendMessage(sessionId: string, commandId: string, input: {
        text: string;
        attachments?: AgentAttachmentReference[];
    }): Promise<void>;
    enqueueMessage?(sessionId: string, commandId: string, input: {
        text: string;
        attachments?: AgentAttachmentReference[];
    }, priority?: AgentQueuePriority): Promise<void>;
    enqueueSlashCommand?(sessionId: string, commandId: string, input: {
        name: string;
        arguments: string;
    }, priority?: AgentQueuePriority): Promise<void>;
    cancelQueuedMessage?(sessionId: string, commandId: string): boolean;
    updateQueuedMessage?(sessionId: string, commandId: string, input: {
        text?: string;
        attachments?: AgentAttachmentReference[];
    }): boolean;
    retryQueuedMessage?(sessionId: string, commandId: string): boolean;
    removeQueuedMessage?(sessionId: string, commandId: string): boolean;
    uploadAttachment?(sessionId: string, file: File): Promise<AgentAttachmentReference>;
    loadAttachment?(sessionId: string, attachmentId: string): Promise<Blob>;
    sendSlashCommand?(sessionId: string, commandId: string, input: {
        name: string;
        arguments: string;
    }): Promise<void>;
    interrupt(sessionId: string, commandId: string): Promise<void>;
    resolvePermission(sessionId: string, commandId: string, permissionId: string, result: AgentPermissionResolution): Promise<void>;
    updateConfiguration(sessionId: string, commandId: string, patch: Record<string, unknown>): Promise<void>;
    executeExtension?(sessionId: string, commandId: string, command: AgentExtensionCommand): Promise<void | AgentExtensionResult>;
    executeToolAction?(sessionId: string, action: AgentToolActionCommand): Promise<void>;
    getFormState?(sessionId: string, appSlug: string, formId: string, instanceId: string): AgentFormState | undefined;
    loadArtifact?(sessionId: string, artifactId: string, representationId?: string): Promise<Blob>;
    loadPreviewUrl?(item: AgentArtifactItem, sessionId: string): Promise<string>;
    executeArtifactAction?(sessionId: string, command: AgentArtifactActionCommand): Promise<void>;
    loadOlder(sessionId: string, beforeItemId?: string): Promise<void>;
}
