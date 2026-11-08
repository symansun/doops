import type { AgentAttachmentReference, AgentPermissionResolution, AgentSessionRuntime, AgentSessionSnapshot } from "../contracts.js";
import type { AgentToolActionCommand } from "../agentToolContracts.js";
export declare class ReadOnlyAgentSessionRuntime implements AgentSessionRuntime {
    private readonly source;
    readonly formDraftScope: object;
    readonly loadArtifact?: AgentSessionRuntime["loadArtifact"];
    readonly loadAttachment?: AgentSessionRuntime["loadAttachment"];
    readonly loadPreviewUrl?: AgentSessionRuntime["loadPreviewUrl"];
    readonly getFormState?: AgentSessionRuntime["getFormState"];
    private sourceSnapshot;
    private projectedSnapshot;
    constructor(source: AgentSessionRuntime);
    open(sessionId: string): Promise<void>;
    close(sessionId: string): void;
    getSnapshot(sessionId: string): AgentSessionSnapshot;
    subscribe(sessionId: string, listener: () => void): () => void;
    sendMessage(_sessionId: string, _commandId: string, _input: {
        text: string;
        attachments?: AgentAttachmentReference[];
    }): Promise<void>;
    interrupt(_sessionId: string, _commandId: string): Promise<void>;
    resolvePermission(_sessionId: string, _commandId: string, _permissionId: string, _result: AgentPermissionResolution): Promise<void>;
    updateConfiguration(_sessionId: string, _commandId: string, _patch: Record<string, unknown>): Promise<void>;
    executeToolAction(_sessionId: string, _action: AgentToolActionCommand): Promise<void>;
    executeExtension(_sessionId: string, _commandId: string, _command: Parameters<NonNullable<AgentSessionRuntime["executeExtension"]>>[2]): Promise<void>;
    loadOlder(sessionId: string, beforeItemId?: string): Promise<void>;
}
