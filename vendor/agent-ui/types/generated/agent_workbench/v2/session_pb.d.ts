import type { GenFile, GenMessage } from "@bufbuild/protobuf/codegenv2";
import type { ArtifactDescriptor, ArtifactReference } from "./artifact_pb.js";
import type { AgentError, CommandReceipt, PermissionDecision } from "./command_pb.js";
import type { SessionConfiguration } from "./configuration_pb.js";
import type { ContentBlock, ContentIdentity, StructuredPayload, UnsupportedValue } from "./content_pb.js";
import type { MessageRole, PermissionRequestState, PlanStepStatus, SessionResourceStatus, SessionStatus, TerminalControlMode, TerminalLeaseState, TimelineItemStatus } from "./session_state_pb.js";
import type { ToolExecution } from "./tool_pb.js";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file agent_workbench/v2/session.proto.
 */
export declare const file_agent_workbench_v2_session: GenFile;
/**
 * @generated from message proto.agent_workbench.v2.SessionCursor
 */
export type SessionCursor = Message<"proto.agent_workbench.v2.SessionCursor"> & {
    /**
     * @generated from field: string session_id = 1;
     */
    sessionId: string;
    /**
     * @generated from field: string stream_epoch = 2;
     */
    streamEpoch: string;
    /**
     * @generated from field: uint64 revision = 3;
     */
    revision: bigint;
    /**
     * @generated from field: uint64 sequence = 4;
     */
    sequence: bigint;
};
/**
 * Describes the message proto.agent_workbench.v2.SessionCursor.
 * Use `create(SessionCursorSchema)` to create a new message.
 */
export declare const SessionCursorSchema: GenMessage<SessionCursor>;
/**
 * @generated from message proto.agent_workbench.v2.EventEnvelope
 */
export type EventEnvelope = Message<"proto.agent_workbench.v2.EventEnvelope"> & {
    /**
     * @generated from field: string session_id = 1;
     */
    sessionId: string;
    /**
     * @generated from field: string stream_epoch = 2;
     */
    streamEpoch: string;
    /**
     * @generated from field: uint64 revision = 3;
     */
    revision: bigint;
    /**
     * @generated from field: uint64 sequence = 4;
     */
    sequence: bigint;
    /**
     * @generated from field: optional string turn_id = 5;
     */
    turnId?: string | undefined;
    /**
     * @generated from field: string item_id = 6;
     */
    itemId: string;
    /**
     * @generated from field: optional string parent_id = 7;
     */
    parentId?: string | undefined;
    /**
     * @generated from field: optional string causation_command_id = 8;
     */
    causationCommandId?: string | undefined;
    /**
     * @generated from field: string created_at = 9;
     */
    createdAt: string;
};
/**
 * Describes the message proto.agent_workbench.v2.EventEnvelope.
 * Use `create(EventEnvelopeSchema)` to create a new message.
 */
export declare const EventEnvelopeSchema: GenMessage<EventEnvelope>;
/**
 * @generated from message proto.agent_workbench.v2.MessageTimelineItem
 */
export type MessageTimelineItem = Message<"proto.agent_workbench.v2.MessageTimelineItem"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.MessageRole role = 1;
     */
    role: MessageRole;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ContentBlock content = 2;
     */
    content: ContentBlock[];
    /**
     * @generated from field: proto.agent_workbench.v2.TimelineItemStatus status = 3;
     */
    status: TimelineItemStatus;
};
/**
 * Describes the message proto.agent_workbench.v2.MessageTimelineItem.
 * Use `create(MessageTimelineItemSchema)` to create a new message.
 */
export declare const MessageTimelineItemSchema: GenMessage<MessageTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.ReasoningTimelineItem
 */
export type ReasoningTimelineItem = Message<"proto.agent_workbench.v2.ReasoningTimelineItem"> & {
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ContentBlock content = 1;
     */
    content: ContentBlock[];
    /**
     * @generated from field: proto.agent_workbench.v2.TimelineItemStatus status = 2;
     */
    status: TimelineItemStatus;
};
/**
 * Describes the message proto.agent_workbench.v2.ReasoningTimelineItem.
 * Use `create(ReasoningTimelineItemSchema)` to create a new message.
 */
export declare const ReasoningTimelineItemSchema: GenMessage<ReasoningTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.PlanStep
 */
export type PlanStep = Message<"proto.agent_workbench.v2.PlanStep"> & {
    /**
     * @generated from field: string step_id = 1;
     */
    stepId: string;
    /**
     * @generated from field: string title = 2;
     */
    title: string;
    /**
     * @generated from field: proto.agent_workbench.v2.PlanStepStatus status = 3;
     */
    status: PlanStepStatus;
    /**
     * @generated from field: optional string detail = 4;
     */
    detail?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PlanStep.
 * Use `create(PlanStepSchema)` to create a new message.
 */
export declare const PlanStepSchema: GenMessage<PlanStep>;
/**
 * @generated from message proto.agent_workbench.v2.PlanTimelineItem
 */
export type PlanTimelineItem = Message<"proto.agent_workbench.v2.PlanTimelineItem"> & {
    /**
     * @generated from field: repeated proto.agent_workbench.v2.PlanStep steps = 1;
     */
    steps: PlanStep[];
};
/**
 * Describes the message proto.agent_workbench.v2.PlanTimelineItem.
 * Use `create(PlanTimelineItemSchema)` to create a new message.
 */
export declare const PlanTimelineItemSchema: GenMessage<PlanTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactReferenceTimelineItem
 */
export type ArtifactReferenceTimelineItem = Message<"proto.agent_workbench.v2.ArtifactReferenceTimelineItem"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.ArtifactReference artifact = 1;
     */
    artifact?: ArtifactReference | undefined;
    /**
     * @generated from field: optional string label = 2;
     */
    label?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactReferenceTimelineItem.
 * Use `create(ArtifactReferenceTimelineItemSchema)` to create a new message.
 */
export declare const ArtifactReferenceTimelineItemSchema: GenMessage<ArtifactReferenceTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.ApprovalTimelineItem
 */
export type ApprovalTimelineItem = Message<"proto.agent_workbench.v2.ApprovalTimelineItem"> & {
    /**
     * @generated from field: string permission_request_id = 1;
     */
    permissionRequestId: string;
};
/**
 * Describes the message proto.agent_workbench.v2.ApprovalTimelineItem.
 * Use `create(ApprovalTimelineItemSchema)` to create a new message.
 */
export declare const ApprovalTimelineItemSchema: GenMessage<ApprovalTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.StatusTimelineItem
 */
export type StatusTimelineItem = Message<"proto.agent_workbench.v2.StatusTimelineItem"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.SessionStatus status = 1;
     */
    status: SessionStatus;
    /**
     * @generated from field: optional string detail = 2;
     */
    detail?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.StatusTimelineItem.
 * Use `create(StatusTimelineItemSchema)` to create a new message.
 */
export declare const StatusTimelineItemSchema: GenMessage<StatusTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.ErrorTimelineItem
 */
export type ErrorTimelineItem = Message<"proto.agent_workbench.v2.ErrorTimelineItem"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.AgentError error = 1;
     */
    error?: AgentError | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ErrorTimelineItem.
 * Use `create(ErrorTimelineItemSchema)` to create a new message.
 */
export declare const ErrorTimelineItemSchema: GenMessage<ErrorTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.SystemTimelineItem
 */
export type SystemTimelineItem = Message<"proto.agent_workbench.v2.SystemTimelineItem"> & {
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ContentBlock content = 1;
     */
    content: ContentBlock[];
};
/**
 * Describes the message proto.agent_workbench.v2.SystemTimelineItem.
 * Use `create(SystemTimelineItemSchema)` to create a new message.
 */
export declare const SystemTimelineItemSchema: GenMessage<SystemTimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.TimelineItemContent
 */
export type TimelineItemContent = Message<"proto.agent_workbench.v2.TimelineItemContent"> & {
    /**
     * @generated from oneof proto.agent_workbench.v2.TimelineItemContent.content
     */
    content: {
        /**
         * @generated from field: proto.agent_workbench.v2.MessageTimelineItem message = 10;
         */
        value: MessageTimelineItem;
        case: "message";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ReasoningTimelineItem reasoning = 11;
         */
        value: ReasoningTimelineItem;
        case: "reasoning";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ToolExecution tool_execution = 12;
         */
        value: ToolExecution;
        case: "toolExecution";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.PlanTimelineItem plan = 13;
         */
        value: PlanTimelineItem;
        case: "plan";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ArtifactReferenceTimelineItem artifact_reference = 14;
         */
        value: ArtifactReferenceTimelineItem;
        case: "artifactReference";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ApprovalTimelineItem approval = 15;
         */
        value: ApprovalTimelineItem;
        case: "approval";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.StatusTimelineItem status = 16;
         */
        value: StatusTimelineItem;
        case: "status";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ErrorTimelineItem error = 17;
         */
        value: ErrorTimelineItem;
        case: "error";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.SystemTimelineItem system = 18;
         */
        value: SystemTimelineItem;
        case: "system";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.UnsupportedValue unsupported = 100;
         */
        value: UnsupportedValue;
        case: "unsupported";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.TimelineItemContent.
 * Use `create(TimelineItemContentSchema)` to create a new message.
 */
export declare const TimelineItemContentSchema: GenMessage<TimelineItemContent>;
/**
 * @generated from message proto.agent_workbench.v2.TimelineItem
 */
export type TimelineItem = Message<"proto.agent_workbench.v2.TimelineItem"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.EventEnvelope envelope = 1;
     */
    envelope?: EventEnvelope | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.TimelineItemContent content = 2;
     */
    content?: TimelineItemContent | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.TimelineItem.
 * Use `create(TimelineItemSchema)` to create a new message.
 */
export declare const TimelineItemSchema: GenMessage<TimelineItem>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionQuestionOption
 */
export type PermissionQuestionOption = Message<"proto.agent_workbench.v2.PermissionQuestionOption"> & {
    /**
     * @generated from field: string label = 1;
     */
    label: string;
    /**
     * @generated from field: optional string description = 2;
     */
    description?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload value = 3;
     */
    value?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionQuestionOption.
 * Use `create(PermissionQuestionOptionSchema)` to create a new message.
 */
export declare const PermissionQuestionOptionSchema: GenMessage<PermissionQuestionOption>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionQuestion
 */
export type PermissionQuestion = Message<"proto.agent_workbench.v2.PermissionQuestion"> & {
    /**
     * @generated from field: string question_id = 1;
     */
    questionId: string;
    /**
     * @generated from field: string prompt = 2;
     */
    prompt: string;
    /**
     * @generated from field: optional string header = 3;
     */
    header?: string | undefined;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.PermissionQuestionOption options = 4;
     */
    options: PermissionQuestionOption[];
    /**
     * @generated from field: bool multiple = 5;
     */
    multiple: boolean;
    /**
     * @generated from field: bool allow_custom = 6;
     */
    allowCustom: boolean;
    /**
     * @generated from field: bool secret = 7;
     */
    secret: boolean;
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionQuestion.
 * Use `create(PermissionQuestionSchema)` to create a new message.
 */
export declare const PermissionQuestionSchema: GenMessage<PermissionQuestion>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionApproval
 */
export type PermissionApproval = Message<"proto.agent_workbench.v2.PermissionApproval"> & {
    /**
     * @generated from field: string title = 1;
     */
    title: string;
    /**
     * @generated from field: optional string description = 2;
     */
    description?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionApproval.
 * Use `create(PermissionApprovalSchema)` to create a new message.
 */
export declare const PermissionApprovalSchema: GenMessage<PermissionApproval>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionQuestionnaire
 */
export type PermissionQuestionnaire = Message<"proto.agent_workbench.v2.PermissionQuestionnaire"> & {
    /**
     * @generated from field: string title = 1;
     */
    title: string;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.PermissionQuestion questions = 2;
     */
    questions: PermissionQuestion[];
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionQuestionnaire.
 * Use `create(PermissionQuestionnaireSchema)` to create a new message.
 */
export declare const PermissionQuestionnaireSchema: GenMessage<PermissionQuestionnaire>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionRequest
 */
export type PermissionRequest = Message<"proto.agent_workbench.v2.PermissionRequest"> & {
    /**
     * @generated from field: string permission_request_id = 1;
     */
    permissionRequestId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.PermissionRequestState state = 2;
     */
    state: PermissionRequestState;
    /**
     * @generated from field: optional string command_id = 3;
     */
    commandId?: string | undefined;
    /**
     * @generated from field: optional string artifact_id = 4;
     */
    artifactId?: string | undefined;
    /**
     * @generated from field: optional string representation_id = 5;
     */
    representationId?: string | undefined;
    /**
     * @generated from field: optional uint64 artifact_revision = 6;
     */
    artifactRevision?: bigint | undefined;
    /**
     * @generated from field: optional string action_type = 7;
     */
    actionType?: string | undefined;
    /**
     * @generated from field: repeated string required_grant_actions = 8;
     */
    requiredGrantActions: string[];
    /**
     * @generated from field: optional string created_at = 9;
     */
    createdAt?: string | undefined;
    /**
     * @generated from field: optional string expires_at = 10;
     */
    expiresAt?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.PermissionResolution resolution = 11;
     */
    resolution?: PermissionResolution | undefined;
    /**
     * @generated from oneof proto.agent_workbench.v2.PermissionRequest.request
     */
    request: {
        /**
         * @generated from field: proto.agent_workbench.v2.PermissionApproval approval = 20;
         */
        value: PermissionApproval;
        case: "approval";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.PermissionQuestionnaire questionnaire = 21;
         */
        value: PermissionQuestionnaire;
        case: "questionnaire";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.UnsupportedValue unsupported = 100;
         */
        value: UnsupportedValue;
        case: "unsupported";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionRequest.
 * Use `create(PermissionRequestSchema)` to create a new message.
 */
export declare const PermissionRequestSchema: GenMessage<PermissionRequest>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionResolution
 */
export type PermissionResolution = Message<"proto.agent_workbench.v2.PermissionResolution"> & {
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
    /**
     * @generated from field: optional string resolved_at = 4;
     */
    resolvedAt?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionResolution.
 * Use `create(PermissionResolutionSchema)` to create a new message.
 */
export declare const PermissionResolutionSchema: GenMessage<PermissionResolution>;
/**
 * @generated from message proto.agent_workbench.v2.AuthorizationGrant
 */
export type AuthorizationGrant = Message<"proto.agent_workbench.v2.AuthorizationGrant"> & {
    /**
     * @generated from field: string grant_id = 1;
     */
    grantId: string;
    /**
     * @generated from field: string issuer = 2;
     */
    issuer: string;
    /**
     * @generated from field: string subject = 3;
     */
    subject: string;
    /**
     * @generated from field: string session_id = 4;
     */
    sessionId: string;
    /**
     * @generated from field: optional string resource_id = 5;
     */
    resourceId?: string | undefined;
    /**
     * @generated from field: repeated string actions = 6;
     */
    actions: string[];
    /**
     * @generated from field: optional uint64 minimum_revision = 7;
     */
    minimumRevision?: bigint | undefined;
    /**
     * @generated from field: optional uint64 maximum_revision = 8;
     */
    maximumRevision?: bigint | undefined;
    /**
     * @generated from field: string issued_at = 9;
     */
    issuedAt: string;
    /**
     * @generated from field: optional string expires_at = 10;
     */
    expiresAt?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.AuthorizationGrant.
 * Use `create(AuthorizationGrantSchema)` to create a new message.
 */
export declare const AuthorizationGrantSchema: GenMessage<AuthorizationGrant>;
/**
 * @generated from message proto.agent_workbench.v2.CapabilityDescriptor
 */
export type CapabilityDescriptor = Message<"proto.agent_workbench.v2.CapabilityDescriptor"> & {
    /**
     * @generated from field: string namespace = 1;
     */
    namespace: string;
    /**
     * @generated from field: string semantic_key = 2;
     */
    semanticKey: string;
    /**
     * @generated from field: string schema_version = 3;
     */
    schemaVersion: string;
    /**
     * @generated from field: repeated string actions = 4;
     */
    actions: string[];
};
/**
 * Describes the message proto.agent_workbench.v2.CapabilityDescriptor.
 * Use `create(CapabilityDescriptorSchema)` to create a new message.
 */
export declare const CapabilityDescriptorSchema: GenMessage<CapabilityDescriptor>;
/**
 * @generated from message proto.agent_workbench.v2.SupportCapabilities
 */
export type SupportCapabilities = Message<"proto.agent_workbench.v2.SupportCapabilities"> & {
    /**
     * @generated from field: string protocol_version = 1;
     */
    protocolVersion: string;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.CapabilityDescriptor command_schemas = 2;
     */
    commandSchemas: CapabilityDescriptor[];
    /**
     * @generated from field: repeated string models = 3;
     */
    models: string[];
    /**
     * @generated from field: repeated string permission_modes = 4;
     */
    permissionModes: string[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ContentIdentity attachment_types = 5;
     */
    attachmentTypes: ContentIdentity[];
    /**
     * @generated from field: bool history = 6;
     */
    history: boolean;
    /**
     * @generated from field: repeated string terminal_operations = 7;
     */
    terminalOperations: string[];
    /**
     * @generated from field: repeated string artifact_operations = 8;
     */
    artifactOperations: string[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.CapabilityDescriptor extensions = 9;
     */
    extensions: CapabilityDescriptor[];
};
/**
 * Describes the message proto.agent_workbench.v2.SupportCapabilities.
 * Use `create(SupportCapabilitiesSchema)` to create a new message.
 */
export declare const SupportCapabilitiesSchema: GenMessage<SupportCapabilities>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalLease
 */
export type TerminalLease = Message<"proto.agent_workbench.v2.TerminalLease"> & {
    /**
     * @generated from field: string lease_id = 1;
     */
    leaseId: string;
    /**
     * @generated from field: string holder = 2;
     */
    holder: string;
    /**
     * @generated from field: proto.agent_workbench.v2.TerminalLeaseState state = 3;
     */
    state: TerminalLeaseState;
    /**
     * @generated from field: string expires_at = 4;
     */
    expiresAt: string;
    /**
     * @generated from field: uint64 fencing_epoch = 5;
     */
    fencingEpoch: bigint;
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalLease.
 * Use `create(TerminalLeaseSchema)` to create a new message.
 */
export declare const TerminalLeaseSchema: GenMessage<TerminalLease>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalResource
 */
export type TerminalResource = Message<"proto.agent_workbench.v2.TerminalResource"> & {
    /**
     * @generated from field: bool writable = 1;
     */
    writable: boolean;
    /**
     * @generated from field: proto.agent_workbench.v2.TerminalControlMode control_mode = 2;
     */
    controlMode: TerminalControlMode;
    /**
     * @generated from field: optional proto.agent_workbench.v2.TerminalLease lease = 3;
     */
    lease?: TerminalLease | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalResource.
 * Use `create(TerminalResourceSchema)` to create a new message.
 */
export declare const TerminalResourceSchema: GenMessage<TerminalResource>;
/**
 * @generated from message proto.agent_workbench.v2.FileResource
 */
export type FileResource = Message<"proto.agent_workbench.v2.FileResource"> & {
    /**
     * @generated from field: string path = 1;
     */
    path: string;
    /**
     * @generated from field: bool writable = 2;
     */
    writable: boolean;
};
/**
 * Describes the message proto.agent_workbench.v2.FileResource.
 * Use `create(FileResourceSchema)` to create a new message.
 */
export declare const FileResourceSchema: GenMessage<FileResource>;
/**
 * @generated from message proto.agent_workbench.v2.EnvironmentResource
 */
export type EnvironmentResource = Message<"proto.agent_workbench.v2.EnvironmentResource"> & {
    /**
     * @generated from field: optional string workspace_id = 1;
     */
    workspaceId?: string | undefined;
    /**
     * @generated from field: optional string runner_id = 2;
     */
    runnerId?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.EnvironmentResource.
 * Use `create(EnvironmentResourceSchema)` to create a new message.
 */
export declare const EnvironmentResourceSchema: GenMessage<EnvironmentResource>;
/**
 * @generated from message proto.agent_workbench.v2.LivePreviewResource
 */
export type LivePreviewResource = Message<"proto.agent_workbench.v2.LivePreviewResource"> & {
    /**
     * @generated from field: string security_profile = 1;
     */
    securityProfile: string;
    /**
     * @generated from field: optional string session_url = 2;
     */
    sessionUrl?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.LivePreviewResource.
 * Use `create(LivePreviewResourceSchema)` to create a new message.
 */
export declare const LivePreviewResourceSchema: GenMessage<LivePreviewResource>;
/**
 * @generated from message proto.agent_workbench.v2.SessionResource
 */
export type SessionResource = Message<"proto.agent_workbench.v2.SessionResource"> & {
    /**
     * @generated from field: string resource_id = 1;
     */
    resourceId: string;
    /**
     * @generated from field: string label = 2;
     */
    label: string;
    /**
     * @generated from field: proto.agent_workbench.v2.SessionResourceStatus status = 3;
     */
    status: SessionResourceStatus;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload metadata = 4;
     */
    metadata?: StructuredPayload | undefined;
    /**
     * @generated from oneof proto.agent_workbench.v2.SessionResource.resource
     */
    resource: {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalResource terminal = 10;
         */
        value: TerminalResource;
        case: "terminal";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.FileResource file = 11;
         */
        value: FileResource;
        case: "file";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.EnvironmentResource environment = 12;
         */
        value: EnvironmentResource;
        case: "environment";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.LivePreviewResource live_preview = 13;
         */
        value: LivePreviewResource;
        case: "livePreview";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.UnsupportedValue unsupported = 100;
         */
        value: UnsupportedValue;
        case: "unsupported";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.SessionResource.
 * Use `create(SessionResourceSchema)` to create a new message.
 */
export declare const SessionResourceSchema: GenMessage<SessionResource>;
/**
 * @generated from message proto.agent_workbench.v2.SessionSnapshot
 */
export type SessionSnapshot = Message<"proto.agent_workbench.v2.SessionSnapshot"> & {
    /**
     * @generated from field: string session_id = 1;
     */
    sessionId: string;
    /**
     * @generated from field: string stream_epoch = 2;
     */
    streamEpoch: string;
    /**
     * @generated from field: uint64 revision = 3;
     */
    revision: bigint;
    /**
     * @generated from field: uint64 latest_sequence = 4;
     */
    latestSequence: bigint;
    /**
     * @generated from field: optional string active_turn_id = 5;
     */
    activeTurnId?: string | undefined;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.TimelineItem history = 6;
     */
    history: TimelineItem[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.CommandReceipt command_receipts = 7;
     */
    commandReceipts: CommandReceipt[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.PermissionRequest permission_requests = 8;
     */
    permissionRequests: PermissionRequest[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.AuthorizationGrant grants = 9;
     */
    grants: AuthorizationGrant[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.SessionResource resources = 10;
     */
    resources: SessionResource[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ArtifactDescriptor artifacts = 11;
     */
    artifacts: ArtifactDescriptor[];
    /**
     * @generated from field: optional proto.agent_workbench.v2.SupportCapabilities capabilities = 12;
     */
    capabilities?: SupportCapabilities | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.SessionStatus status = 13;
     */
    status: SessionStatus;
    /**
     * @generated from field: optional string digest = 14;
     */
    digest?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.AgentError error = 15;
     */
    error?: AgentError | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.SessionConfiguration configuration = 16;
     */
    configuration?: SessionConfiguration | undefined;
    /**
     * @generated from field: bool has_older_history = 17;
     */
    hasOlderHistory: boolean;
    /**
     * @generated from field: uint32 history_window_size = 18;
     */
    historyWindowSize: number;
    /**
     * @generated from field: uint64 history_total_size = 19;
     */
    historyTotalSize: bigint;
    /**
     * @generated from field: repeated string hidden_artifact_ids = 20;
     */
    hiddenArtifactIds: string[];
};
/**
 * Describes the message proto.agent_workbench.v2.SessionSnapshot.
 * Use `create(SessionSnapshotSchema)` to create a new message.
 */
export declare const SessionSnapshotSchema: GenMessage<SessionSnapshot>;
/**
 * @generated from message proto.agent_workbench.v2.TimelineItemAppended
 */
export type TimelineItemAppended = Message<"proto.agent_workbench.v2.TimelineItemAppended"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.TimelineItemContent content = 1;
     */
    content?: TimelineItemContent | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.TimelineItemAppended.
 * Use `create(TimelineItemAppendedSchema)` to create a new message.
 */
export declare const TimelineItemAppendedSchema: GenMessage<TimelineItemAppended>;
/**
 * @generated from message proto.agent_workbench.v2.TimelineItemUpdated
 */
export type TimelineItemUpdated = Message<"proto.agent_workbench.v2.TimelineItemUpdated"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.TimelineItemContent content = 1;
     */
    content?: TimelineItemContent | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.TimelineItemUpdated.
 * Use `create(TimelineItemUpdatedSchema)` to create a new message.
 */
export declare const TimelineItemUpdatedSchema: GenMessage<TimelineItemUpdated>;
/**
 * @generated from message proto.agent_workbench.v2.CommandReceiptChanged
 */
export type CommandReceiptChanged = Message<"proto.agent_workbench.v2.CommandReceiptChanged"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.CommandReceipt receipt = 1;
     */
    receipt?: CommandReceipt | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.CommandReceiptChanged.
 * Use `create(CommandReceiptChangedSchema)` to create a new message.
 */
export declare const CommandReceiptChangedSchema: GenMessage<CommandReceiptChanged>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionRequested
 */
export type PermissionRequested = Message<"proto.agent_workbench.v2.PermissionRequested"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.PermissionRequest request = 1;
     */
    request?: PermissionRequest | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionRequested.
 * Use `create(PermissionRequestedSchema)` to create a new message.
 */
export declare const PermissionRequestedSchema: GenMessage<PermissionRequested>;
/**
 * @generated from message proto.agent_workbench.v2.PermissionResolved
 */
export type PermissionResolved = Message<"proto.agent_workbench.v2.PermissionResolved"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.PermissionResolution resolution = 1;
     */
    resolution?: PermissionResolution | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PermissionResolved.
 * Use `create(PermissionResolvedSchema)` to create a new message.
 */
export declare const PermissionResolvedSchema: GenMessage<PermissionResolved>;
/**
 * @generated from message proto.agent_workbench.v2.ResourceChanged
 */
export type ResourceChanged = Message<"proto.agent_workbench.v2.ResourceChanged"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.SessionResource resource = 1;
     */
    resource?: SessionResource | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ResourceChanged.
 * Use `create(ResourceChangedSchema)` to create a new message.
 */
export declare const ResourceChangedSchema: GenMessage<ResourceChanged>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactChanged
 */
export type ArtifactChanged = Message<"proto.agent_workbench.v2.ArtifactChanged"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.ArtifactDescriptor artifact = 1;
     */
    artifact?: ArtifactDescriptor | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactChanged.
 * Use `create(ArtifactChangedSchema)` to create a new message.
 */
export declare const ArtifactChangedSchema: GenMessage<ArtifactChanged>;
/**
 * @generated from message proto.agent_workbench.v2.TerminalLeaseChanged
 */
export type TerminalLeaseChanged = Message<"proto.agent_workbench.v2.TerminalLeaseChanged"> & {
    /**
     * @generated from field: string resource_id = 1;
     */
    resourceId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.TerminalLease lease = 2;
     */
    lease?: TerminalLease | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.TerminalLeaseChanged.
 * Use `create(TerminalLeaseChangedSchema)` to create a new message.
 */
export declare const TerminalLeaseChangedSchema: GenMessage<TerminalLeaseChanged>;
/**
 * @generated from message proto.agent_workbench.v2.CapabilitiesChanged
 */
export type CapabilitiesChanged = Message<"proto.agent_workbench.v2.CapabilitiesChanged"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.SupportCapabilities capabilities = 1;
     */
    capabilities?: SupportCapabilities | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.CapabilitiesChanged.
 * Use `create(CapabilitiesChangedSchema)` to create a new message.
 */
export declare const CapabilitiesChangedSchema: GenMessage<CapabilitiesChanged>;
/**
 * @generated from message proto.agent_workbench.v2.SessionStatusChanged
 */
export type SessionStatusChanged = Message<"proto.agent_workbench.v2.SessionStatusChanged"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.SessionStatus status = 1;
     */
    status: SessionStatus;
    /**
     * @generated from field: optional proto.agent_workbench.v2.AgentError error = 2;
     */
    error?: AgentError | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.SessionStatusChanged.
 * Use `create(SessionStatusChangedSchema)` to create a new message.
 */
export declare const SessionStatusChangedSchema: GenMessage<SessionStatusChanged>;
/**
 * @generated from message proto.agent_workbench.v2.ConfigurationChanged
 */
export type ConfigurationChanged = Message<"proto.agent_workbench.v2.ConfigurationChanged"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.SessionConfiguration configuration = 1;
     */
    configuration?: SessionConfiguration | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ConfigurationChanged.
 * Use `create(ConfigurationChangedSchema)` to create a new message.
 */
export declare const ConfigurationChangedSchema: GenMessage<ConfigurationChanged>;
/**
 * @generated from message proto.agent_workbench.v2.AgentEvent
 */
export type AgentEvent = Message<"proto.agent_workbench.v2.AgentEvent"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.EventEnvelope envelope = 1;
     */
    envelope?: EventEnvelope | undefined;
    /**
     * @generated from oneof proto.agent_workbench.v2.AgentEvent.event
     */
    event: {
        /**
         * @generated from field: proto.agent_workbench.v2.TimelineItemAppended timeline_item_appended = 10;
         */
        value: TimelineItemAppended;
        case: "timelineItemAppended";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TimelineItemUpdated timeline_item_updated = 11;
         */
        value: TimelineItemUpdated;
        case: "timelineItemUpdated";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.CommandReceiptChanged command_receipt_changed = 12;
         */
        value: CommandReceiptChanged;
        case: "commandReceiptChanged";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.PermissionRequested permission_requested = 13;
         */
        value: PermissionRequested;
        case: "permissionRequested";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.PermissionResolved permission_resolved = 14;
         */
        value: PermissionResolved;
        case: "permissionResolved";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ResourceChanged resource_changed = 15;
         */
        value: ResourceChanged;
        case: "resourceChanged";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ArtifactChanged artifact_changed = 16;
         */
        value: ArtifactChanged;
        case: "artifactChanged";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TerminalLeaseChanged terminal_lease_changed = 17;
         */
        value: TerminalLeaseChanged;
        case: "terminalLeaseChanged";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.CapabilitiesChanged capabilities_changed = 18;
         */
        value: CapabilitiesChanged;
        case: "capabilitiesChanged";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.SessionStatusChanged session_status_changed = 19;
         */
        value: SessionStatusChanged;
        case: "sessionStatusChanged";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ConfigurationChanged configuration_changed = 20;
         */
        value: ConfigurationChanged;
        case: "configurationChanged";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.UnsupportedValue unsupported = 100;
         */
        value: UnsupportedValue;
        case: "unsupported";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.AgentEvent.
 * Use `create(AgentEventSchema)` to create a new message.
 */
export declare const AgentEventSchema: GenMessage<AgentEvent>;
/**
 * @generated from message proto.agent_workbench.v2.SessionDeltaBatch
 */
export type SessionDeltaBatch = Message<"proto.agent_workbench.v2.SessionDeltaBatch"> & {
    /**
     * @generated from field: string session_id = 1;
     */
    sessionId: string;
    /**
     * @generated from field: string stream_epoch = 2;
     */
    streamEpoch: string;
    /**
     * @generated from field: uint64 base_revision = 3;
     */
    baseRevision: bigint;
    /**
     * @generated from field: uint64 revision = 4;
     */
    revision: bigint;
    /**
     * @generated from field: uint64 first_sequence = 5;
     */
    firstSequence: bigint;
    /**
     * @generated from field: uint64 last_sequence = 6;
     */
    lastSequence: bigint;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.AgentEvent events = 7;
     */
    events: AgentEvent[];
    /**
     * @generated from field: string digest = 8;
     */
    digest: string;
};
/**
 * Describes the message proto.agent_workbench.v2.SessionDeltaBatch.
 * Use `create(SessionDeltaBatchSchema)` to create a new message.
 */
export declare const SessionDeltaBatchSchema: GenMessage<SessionDeltaBatch>;
