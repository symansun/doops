import type { GenEnum, GenFile, GenMessage } from "@bufbuild/protobuf/codegenv2";
import type { ContentBlock, StructuredPayload } from "./content_pb.js";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file agent_workbench/v2/command.proto.
 */
export declare const file_agent_workbench_v2_command: GenFile;
/**
 * @generated from message proto.agent_workbench.v2.SendPromptCommand
 */
export type SendPromptCommand = Message<"proto.agent_workbench.v2.SendPromptCommand"> & {
    /**
     * @generated from field: string text = 1;
     */
    text: string;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ContentBlock attachments = 2;
     */
    attachments: ContentBlock[];
};
/**
 * Describes the message proto.agent_workbench.v2.SendPromptCommand.
 * Use `create(SendPromptCommandSchema)` to create a new message.
 */
export declare const SendPromptCommandSchema: GenMessage<SendPromptCommand>;
/**
 * @generated from message proto.agent_workbench.v2.InterruptCommand
 */
export type InterruptCommand = Message<"proto.agent_workbench.v2.InterruptCommand"> & {
    /**
     * @generated from field: optional string turn_id = 1;
     */
    turnId?: string | undefined;
    /**
     * @generated from field: optional string reason = 2;
     */
    reason?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.InterruptCommand.
 * Use `create(InterruptCommandSchema)` to create a new message.
 */
export declare const InterruptCommandSchema: GenMessage<InterruptCommand>;
/**
 * @generated from message proto.agent_workbench.v2.ConfigurationValue
 */
export type ConfigurationValue = Message<"proto.agent_workbench.v2.ConfigurationValue"> & {
    /**
     * @generated from field: string key = 1;
     */
    key: string;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload value = 2;
     */
    value?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ConfigurationValue.
 * Use `create(ConfigurationValueSchema)` to create a new message.
 */
export declare const ConfigurationValueSchema: GenMessage<ConfigurationValue>;
/**
 * @generated from message proto.agent_workbench.v2.ChangeConfigurationCommand
 */
export type ChangeConfigurationCommand = Message<"proto.agent_workbench.v2.ChangeConfigurationCommand"> & {
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ConfigurationValue values = 1;
     */
    values: ConfigurationValue[];
};
/**
 * Describes the message proto.agent_workbench.v2.ChangeConfigurationCommand.
 * Use `create(ChangeConfigurationCommandSchema)` to create a new message.
 */
export declare const ChangeConfigurationCommandSchema: GenMessage<ChangeConfigurationCommand>;
/**
 * @generated from message proto.agent_workbench.v2.ResolvePermissionCommand
 */
export type ResolvePermissionCommand = Message<"proto.agent_workbench.v2.ResolvePermissionCommand"> & {
    /**
     * @generated from field: string permission_request_id = 1;
     */
    permissionRequestId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.PermissionDecision decision = 2;
     */
    decision: PermissionDecision;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload response = 3;
     */
    response?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ResolvePermissionCommand.
 * Use `create(ResolvePermissionCommandSchema)` to create a new message.
 */
export declare const ResolvePermissionCommandSchema: GenMessage<ResolvePermissionCommand>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactActionCommand
 */
export type ArtifactActionCommand = Message<"proto.agent_workbench.v2.ArtifactActionCommand"> & {
    /**
     * @generated from field: string artifact_id = 1;
     */
    artifactId: string;
    /**
     * @generated from field: string representation_id = 2;
     */
    representationId: string;
    /**
     * @generated from field: uint64 base_revision = 3;
     */
    baseRevision: bigint;
    /**
     * @generated from field: string client_action_id = 4;
     */
    clientActionId: string;
    /**
     * @generated from field: string action_type = 5;
     */
    actionType: string;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload payload = 6;
     */
    payload?: StructuredPayload | undefined;
    /**
     * @generated from field: string action_schema_version = 7;
     */
    actionSchemaVersion: string;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactActionCommand.
 * Use `create(ArtifactActionCommandSchema)` to create a new message.
 */
export declare const ArtifactActionCommandSchema: GenMessage<ArtifactActionCommand>;
/**
 * @generated from message proto.agent_workbench.v2.ToolActionCommand
 */
export type ToolActionCommand = Message<"proto.agent_workbench.v2.ToolActionCommand"> & {
    /**
     * @generated from field: string tool_execution_id = 1;
     */
    toolExecutionId: string;
    /**
     * @generated from field: string action_id = 2;
     */
    actionId: string;
    /**
     * @generated from field: string action_type = 3;
     */
    actionType: string;
    /**
     * @generated from field: string schema_version = 4;
     */
    schemaVersion: string;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload payload = 5;
     */
    payload?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolActionCommand.
 * Use `create(ToolActionCommandSchema)` to create a new message.
 */
export declare const ToolActionCommandSchema: GenMessage<ToolActionCommand>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalInput
 */
export type TerminalInput = Message<"proto.agent_workbench.v2.TerminalInput"> & {
    /**
     * @generated from field: bytes data = 1;
     */
    data: Uint8Array;
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalInput.
 * Use `create(TerminalInputSchema)` to create a new message.
 */
export declare const TerminalInputSchema: GenMessage<TerminalInput>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalResize
 */
export type TerminalResize = Message<"proto.agent_workbench.v2.TerminalResize"> & {
    /**
     * @generated from field: uint32 columns = 1;
     */
    columns: number;
    /**
     * @generated from field: uint32 rows = 2;
     */
    rows: number;
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalResize.
 * Use `create(TerminalResizeSchema)` to create a new message.
 */
export declare const TerminalResizeSchema: GenMessage<TerminalResize>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalSignal
 */
export type TerminalSignal = Message<"proto.agent_workbench.v2.TerminalSignal"> & {
    /**
     * @generated from field: string signal = 1;
     */
    signal: string;
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalSignal.
 * Use `create(TerminalSignalSchema)` to create a new message.
 */
export declare const TerminalSignalSchema: GenMessage<TerminalSignal>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalAcquireControl
 */
export type TerminalAcquireControl = Message<"proto.agent_workbench.v2.TerminalAcquireControl"> & {
    /**
     * @generated from field: string client_label = 1;
     */
    clientLabel: string;
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalAcquireControl.
 * Use `create(TerminalAcquireControlSchema)` to create a new message.
 */
export declare const TerminalAcquireControlSchema: GenMessage<TerminalAcquireControl>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalRenewControl
 */
export type TerminalRenewControl = Message<"proto.agent_workbench.v2.TerminalRenewControl"> & {};
/**
 * Describes the message proto.agent_workbench.v2.TerminalRenewControl.
 * Use `create(TerminalRenewControlSchema)` to create a new message.
 */
export declare const TerminalRenewControlSchema: GenMessage<TerminalRenewControl>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalReleaseControl
 */
export type TerminalReleaseControl = Message<"proto.agent_workbench.v2.TerminalReleaseControl"> & {};
/**
 * Describes the message proto.agent_workbench.v2.TerminalReleaseControl.
 * Use `create(TerminalReleaseControlSchema)` to create a new message.
 */
export declare const TerminalReleaseControlSchema: GenMessage<TerminalReleaseControl>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalOperationCommand
 */
export type TerminalOperationCommand = Message<"proto.agent_workbench.v2.TerminalOperationCommand"> & {
    /**
     * @generated from field: string resource_id = 1;
     */
    resourceId: string;
    /**
     * @generated from field: optional string lease_id = 2;
     */
    leaseId?: string | undefined;
    /**
     * @generated from field: optional uint64 fencing_epoch = 3;
     */
    fencingEpoch?: bigint | undefined;
    /**
     * @generated from oneof proto.agent_workbench.v2.TerminalOperationCommand.operation
     */
    operation: {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalInput input = 10;
         */
        value: TerminalInput;
        case: "input";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalResize resize = 11;
         */
        value: TerminalResize;
        case: "resize";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalSignal signal = 12;
         */
        value: TerminalSignal;
        case: "signal";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalAcquireControl acquire_control = 13;
         */
        value: TerminalAcquireControl;
        case: "acquireControl";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalRenewControl renew_control = 14;
         */
        value: TerminalRenewControl;
        case: "renewControl";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalReleaseControl release_control = 15;
         */
        value: TerminalReleaseControl;
        case: "releaseControl";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalOperationCommand.
 * Use `create(TerminalOperationCommandSchema)` to create a new message.
 */
export declare const TerminalOperationCommandSchema: GenMessage<TerminalOperationCommand>;
/**
 * @generated from message proto.agent_workbench.v2.ExtensionCommand
 */
export type ExtensionCommand = Message<"proto.agent_workbench.v2.ExtensionCommand"> & {
    /**
     * @generated from field: string namespace = 1;
     */
    namespace: string;
    /**
     * @generated from field: string semantic_type = 2;
     */
    semanticType: string;
    /**
     * @generated from field: string schema_version = 3;
     */
    schemaVersion: string;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload payload = 4;
     */
    payload?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ExtensionCommand.
 * Use `create(ExtensionCommandSchema)` to create a new message.
 */
export declare const ExtensionCommandSchema: GenMessage<ExtensionCommand>;
/**
 * @generated from message proto.agent_workbench.v2.UnsupportedCommand
 */
export type UnsupportedCommand = Message<"proto.agent_workbench.v2.UnsupportedCommand"> & {
    /**
     * @generated from field: string namespace = 1;
     */
    namespace: string;
    /**
     * @generated from field: string semantic_type = 2;
     */
    semanticType: string;
    /**
     * @generated from field: string schema_version = 3;
     */
    schemaVersion: string;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload payload = 4;
     */
    payload?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.UnsupportedCommand.
 * Use `create(UnsupportedCommandSchema)` to create a new message.
 */
export declare const UnsupportedCommandSchema: GenMessage<UnsupportedCommand>;
/**
 * @generated from message proto.agent_workbench.v2.CommandEnvelope
 */
export type CommandEnvelope = Message<"proto.agent_workbench.v2.CommandEnvelope"> & {
    /**
     * @generated from field: string session_id = 1;
     */
    sessionId: string;
    /**
     * @generated from field: string stream_epoch = 2;
     */
    streamEpoch: string;
    /**
     * @generated from field: string command_id = 3;
     */
    commandId: string;
    /**
     * @generated from field: string payload_digest = 4;
     */
    payloadDigest: string;
    /**
     * @generated from field: optional uint64 expected_revision = 5;
     */
    expectedRevision?: bigint | undefined;
    /**
     * @generated from field: string issued_at = 6;
     */
    issuedAt: string;
    /**
     * @generated from oneof proto.agent_workbench.v2.CommandEnvelope.command
     */
    command: {
        /**
         * @generated from field: proto.agent_workbench.v2.SendPromptCommand send_prompt = 10;
         */
        value: SendPromptCommand;
        case: "sendPrompt";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.InterruptCommand interrupt = 11;
         */
        value: InterruptCommand;
        case: "interrupt";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ChangeConfigurationCommand change_configuration = 12;
         */
        value: ChangeConfigurationCommand;
        case: "changeConfiguration";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ResolvePermissionCommand resolve_permission = 13;
         */
        value: ResolvePermissionCommand;
        case: "resolvePermission";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ArtifactActionCommand artifact_action = 14;
         */
        value: ArtifactActionCommand;
        case: "artifactAction";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalOperationCommand terminal_operation = 15;
         */
        value: TerminalOperationCommand;
        case: "terminalOperation";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ExtensionCommand extension = 16;
         */
        value: ExtensionCommand;
        case: "extension";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ToolActionCommand tool_action = 17;
         */
        value: ToolActionCommand;
        case: "toolAction";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.UnsupportedCommand unsupported = 100;
         */
        value: UnsupportedCommand;
        case: "unsupported";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.CommandEnvelope.
 * Use `create(CommandEnvelopeSchema)` to create a new message.
 */
export declare const CommandEnvelopeSchema: GenMessage<CommandEnvelope>;
/**
 * @generated from message proto.agent_workbench.v2.FieldViolation
 */
export type FieldViolation = Message<"proto.agent_workbench.v2.FieldViolation"> & {
    /**
     * @generated from field: string field = 1;
     */
    field: string;
    /**
     * @generated from field: string description = 2;
     */
    description: string;
};
/**
 * Describes the message proto.agent_workbench.v2.FieldViolation.
 * Use `create(FieldViolationSchema)` to create a new message.
 */
export declare const FieldViolationSchema: GenMessage<FieldViolation>;
/**
 * @generated from message proto.agent_workbench.v2.AgentError
 */
export type AgentError = Message<"proto.agent_workbench.v2.AgentError"> & {
    /**
     * @generated from field: string code = 1;
     */
    code: string;
    /**
     * @generated from field: string message = 2;
     */
    message: string;
    /**
     * @generated from field: bool retryable = 3;
     */
    retryable: boolean;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload details = 4;
     */
    details?: StructuredPayload | undefined;
    /**
     * @generated from field: optional uint64 current_revision = 5;
     */
    currentRevision?: bigint | undefined;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.FieldViolation field_violations = 6;
     */
    fieldViolations: FieldViolation[];
};
/**
 * Describes the message proto.agent_workbench.v2.AgentError.
 * Use `create(AgentErrorSchema)` to create a new message.
 */
export declare const AgentErrorSchema: GenMessage<AgentError>;
/**
 * @generated from message proto.agent_workbench.v2.CommandReceipt
 */
export type CommandReceipt = Message<"proto.agent_workbench.v2.CommandReceipt"> & {
    /**
     * @generated from field: string session_id = 1;
     */
    sessionId: string;
    /**
     * @generated from field: string command_id = 2;
     */
    commandId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.CommandReceiptState state = 3;
     */
    state: CommandReceiptState;
    /**
     * @generated from field: string payload_digest = 4;
     */
    payloadDigest: string;
    /**
     * @generated from field: optional uint64 resulting_revision = 5;
     */
    resultingRevision?: bigint | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.AgentError error = 6;
     */
    error?: AgentError | undefined;
    /**
     * @generated from field: optional string received_at = 7;
     */
    receivedAt?: string | undefined;
    /**
     * @generated from field: optional string updated_at = 8;
     */
    updatedAt?: string | undefined;
    /**
     * @generated from field: optional string interaction_kind = 9;
     */
    interactionKind?: string | undefined;
    /**
     * @generated from field: optional string interaction_source_id = 10;
     */
    interactionSourceId?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.CommandReceipt.
 * Use `create(CommandReceiptSchema)` to create a new message.
 */
export declare const CommandReceiptSchema: GenMessage<CommandReceipt>;
/**
 * @generated from enum proto.agent_workbench.v2.PermissionDecision
 */
export declare enum PermissionDecision {
    /**
     * @generated from enum value: PERMISSION_DECISION_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: PERMISSION_DECISION_ACCEPT = 1;
     */
    ACCEPT = 1,
    /**
     * @generated from enum value: PERMISSION_DECISION_DECLINE = 2;
     */
    DECLINE = 2
}
/**
 * Describes the enum proto.agent_workbench.v2.PermissionDecision.
 */
export declare const PermissionDecisionSchema: GenEnum<PermissionDecision>;
/**
 * @generated from enum proto.agent_workbench.v2.CommandReceiptState
 */
export declare enum CommandReceiptState {
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_RECEIVED = 1;
     */
    RECEIVED = 1,
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_ACCEPTED = 2;
     */
    ACCEPTED = 2,
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_RUNNING = 3;
     */
    RUNNING = 3,
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_SUCCEEDED = 4;
     */
    SUCCEEDED = 4,
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_FAILED = 5;
     */
    FAILED = 5,
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_REJECTED = 6;
     */
    REJECTED = 6,
    /**
     * @generated from enum value: COMMAND_RECEIPT_STATE_CANCELLED = 7;
     */
    CANCELLED = 7
}
/**
 * Describes the enum proto.agent_workbench.v2.CommandReceiptState.
 */
export declare const CommandReceiptStateSchema: GenEnum<CommandReceiptState>;
